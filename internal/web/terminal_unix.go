//go:build !windows

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/bovinemagnet/gossm/internal/session"
	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

// handleTerminalWS upgrades the request to a WebSocket and bridges it to a
// PTY-backed `aws ssm start-session` shell. Output bytes are streamed to the
// browser as binary frames; browser input arrives as binary frames written
// straight to the PTY, while text frames carry resize control messages.
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	instance := r.URL.Query().Get("instance")
	profile := r.URL.Query().Get("profile")

	if instance == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already wrote the error response (e.g. 403 on bad origin).
		return
	}
	defer conn.Close()

	// The first frame must be an auth control message. The token rides in
	// the WebSocket payload rather than the URL so it never appears in
	// request logs or browser history.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	mt, data, rerr := conn.ReadMessage()
	if rerr != nil {
		return
	}
	var auth termControl
	if mt != websocket.TextMessage || json.Unmarshal(data, &auth) != nil ||
		auth.T != "auth" || !validToken(auth.Token, s.terminalToken) {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("forbidden"))
		return
	}
	_ = conn.SetReadDeadline(time.Time{})

	ctx, cancel := context.WithCancel(context.Background())
	cmd := s.termCmdBuilder(ctx, instance, profile)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		cancel()
		_ = conn.WriteMessage(websocket.TextMessage, []byte("failed to start terminal: "+err.Error()))
		return
	}

	// The manager owns cmd.Wait() and tears the process down on StopSession.
	id := s.sm.AdoptSession(session.SessionOpts{
		InstanceID: instance,
		Profile:    profile,
		Type:       session.TypeShell,
	}, cmd, cancel)
	defer func() {
		_ = s.sm.StopSession(id)
		_ = ptmx.Close()
	}()

	// PTY output → WebSocket.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if rerr != nil {
				// PTY closed (shell exited); drop the WebSocket so the read
				// loop below unblocks and cleanup runs.
				_ = conn.Close()
				return
			}
		}
	}()

	// WebSocket input → PTY (text frames are resize control messages).
	for {
		mt, data, rerr := conn.ReadMessage()
		if rerr != nil {
			return
		}
		if mt == websocket.TextMessage {
			var ctl termControl
			if json.Unmarshal(data, &ctl) == nil && ctl.T == "resize" {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: ctl.Cols, Rows: ctl.Rows})
				continue
			}
		}
		if _, werr := ptmx.Write(data); werr != nil {
			return
		}
	}
}
