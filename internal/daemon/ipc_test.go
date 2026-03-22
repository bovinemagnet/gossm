package daemon

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

// testConfig returns a Config that uses a temp directory for the socket.
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		DashboardPort: 8443,
		LogLevel:      "warn",
		PIDDir:        dir,
	}
}

// testDaemon creates a minimal Daemon value for IPC tests.
func testDaemon(cfg *config.Config, sm *session.SessionManager) *Daemon {
	return &Daemon{
		cfg:       cfg,
		sm:        sm,
		startedAt: time.Now(),
		stopCh:    make(chan struct{}),
	}
}

func TestIPCRoundTrip(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

	srv, err := NewIPCServer(cfg, sm, d)
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer srv.Stop()
	srv.Serve()

	// Connect and send a "status" request.
	conn, err := net.Dial("unix", cfg.SocketPath())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := IPCRequest{Action: "status"}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode request: %v", err)
	}

	var resp IPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not OK: %s", resp.Error)
	}

	var status StatusResponse
	if err := json.Unmarshal(resp.Data, &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.SessionCount != 0 {
		t.Errorf("SessionCount = %d, want 0", status.SessionCount)
	}
	if status.Port != 8443 {
		t.Errorf("Port = %d, want 8443", status.Port)
	}
}

func TestIPCList(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

	// Register a session so the list is non-empty.
	sm.RegisterExternal(session.SessionOpts{
		InstanceID:   "i-test",
		InstanceName: "test-instance",
		Profile:      "default",
		Type:         session.TypeShell,
	}, 12345)

	srv, err := NewIPCServer(cfg, sm, d)
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer srv.Stop()
	srv.Serve()

	conn, err := net.Dial("unix", cfg.SocketPath())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := IPCRequest{Action: "list"}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp IPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not OK: %s", resp.Error)
	}

	var sessions []session.Session
	if err := json.Unmarshal(resp.Data, &sessions); err != nil {
		t.Fatalf("unmarshal sessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].InstanceID != "i-test" {
		t.Errorf("InstanceID = %q, want %q", sessions[0].InstanceID, "i-test")
	}
}

func TestIPCConnectNotRunning(t *testing.T) {
	cfg := &config.Config{
		DashboardPort: 8443,
		LogLevel:      "warn",
		PIDDir:        filepath.Join(t.TempDir(), "nonexistent"),
	}

	_, err := IPCConnect(cfg)
	if err == nil {
		t.Fatal("expected error connecting to non-existent socket, got nil")
	}
}
