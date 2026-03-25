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
		DashboardPort: 8877,
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
	if status.Port != 8877 {
		t.Errorf("Port = %d, want 8877", status.Port)
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

func TestIPCRegisterShell(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

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

	regData, _ := json.Marshal(registerRequest{
		InstanceID:   "i-shell001",
		InstanceName: "shell-instance",
		Profile:      "prod",
		PID:          99999,
		Type:         "shell",
	})
	req := IPCRequest{Action: "register", Data: regData}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp IPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK {
		t.Fatalf("register not OK: %s", resp.Error)
	}

	sessions := sm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.InstanceID != "i-shell001" {
		t.Errorf("InstanceID = %q, want %q", s.InstanceID, "i-shell001")
	}
	if s.Type != session.TypeShell {
		t.Errorf("Type = %v, want TypeShell", s.Type)
	}
}

func TestIPCRegisterPortForward(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

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

	regData, _ := json.Marshal(registerRequest{
		InstanceID:   "i-db001",
		InstanceName: "db-instance",
		Profile:      "prod",
		PID:          88888,
		Type:         "port-forward",
		LocalPort:    5432,
		RemotePort:   5432,
		RemoteHost:   "db.internal",
	})
	req := IPCRequest{Action: "register", Data: regData}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp IPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.OK {
		t.Fatalf("register not OK: %s", resp.Error)
	}

	sessions := sm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.InstanceID != "i-db001" {
		t.Errorf("InstanceID = %q, want %q", s.InstanceID, "i-db001")
	}
	if s.Type != session.TypePortForward {
		t.Errorf("Type = %v, want TypePortForward", s.Type)
	}
	if s.LocalPort != 5432 {
		t.Errorf("LocalPort = %d, want 5432", s.LocalPort)
	}
	if s.RemotePort != 5432 {
		t.Errorf("RemotePort = %d, want 5432", s.RemotePort)
	}
	if s.RemoteHost != "db.internal" {
		t.Errorf("RemoteHost = %q, want %q", s.RemoteHost, "db.internal")
	}
	if s.PID != 88888 {
		t.Errorf("PID = %d, want 88888", s.PID)
	}
}

func TestIPCConnectNotRunning(t *testing.T) {
	cfg := &config.Config{
		DashboardPort: 8877,
		LogLevel:      "warn",
		PIDDir:        filepath.Join(t.TempDir(), "nonexistent"),
	}

	_, err := IPCConnect(cfg)
	if err == nil {
		t.Fatal("expected error connecting to non-existent socket, got nil")
	}
}
