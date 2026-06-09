//go:build !windows

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dialTerminal opens a WebSocket to the terminal endpoint on the test server.
func dialTerminal(t *testing.T, srv *httptest.Server, query, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/terminal?" + query
	hdr := http.Header{}
	if origin != "" {
		hdr.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(wsURL, hdr)
}

// catTerminalServer returns a test server whose terminal sessions run `cat`
// under a PTY (so input echoes back) with a known token.
func catTerminalServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := testServer(t)
	s.terminalToken = "secret-token"
	s.termCmdBuilder = func(ctx context.Context, instance, profile string) *exec.Cmd {
		return exec.CommandContext(ctx, "cat")
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func TestTerminalWS_RejectsBadToken(t *testing.T) {
	srv := catTerminalServer(t)
	origin := "http://" + strings.TrimPrefix(srv.URL, "http://")

	conn, resp, err := dialTerminal(t, srv, "instance=i-x&token=wrong", origin)
	if err == nil {
		conn.Close()
		t.Fatal("expected handshake failure for bad token")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

func TestTerminalWS_RejectsBadOrigin(t *testing.T) {
	srv := catTerminalServer(t)

	conn, resp, err := dialTerminal(t, srv, "instance=i-x&token=secret-token", "http://evil.example")
	if err == nil {
		conn.Close()
		t.Fatal("expected handshake failure for bad origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %v, want 403", resp)
	}
}

func TestTerminalWS_EchoRoundtrip(t *testing.T) {
	srv := catTerminalServer(t)
	origin := "http://" + strings.TrimPrefix(srv.URL, "http://")

	conn, _, err := dialTerminal(t, srv, "instance=i-echo&token=secret-token", origin)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// Send a resize control message (text frame) — should be absorbed silently.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"t":"resize","cols":100,"rows":30}`)); err != nil {
		t.Fatalf("write resize failed: %v", err)
	}

	// Send input as a binary frame; cat (under the PTY) echoes it back.
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("hello\n")); err != nil {
		t.Fatalf("write input failed: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var got strings.Builder
	for got.Len() < 5 {
		_, data, rerr := conn.ReadMessage()
		if rerr != nil {
			t.Fatalf("read failed before echo (got %q): %v", got.String(), rerr)
		}
		got.Write(data)
		if strings.Contains(got.String(), "hello") {
			return
		}
	}
	if !strings.Contains(got.String(), "hello") {
		t.Fatalf("echo not received; got %q", got.String())
	}
}

func TestTerminalWS_RegistersSession(t *testing.T) {
	s := testServer(t)
	s.terminalToken = "secret-token"
	s.termCmdBuilder = func(ctx context.Context, instance, profile string) *exec.Cmd {
		return exec.CommandContext(ctx, "cat")
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	origin := "http://" + strings.TrimPrefix(srv.URL, "http://")

	conn, _, err := dialTerminal(t, srv, "instance=i-reg&token=secret-token&profile=prod", origin)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	// The session should appear in the manager as a running shell.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, sess := range s.sm.ListSessions() {
			if sess.InstanceID == "i-reg" && sess.Type == 0 /* TypeShell */ {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("terminal session was not registered with the manager")
}
