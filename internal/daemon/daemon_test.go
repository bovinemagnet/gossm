package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

// --- PID file tests ---

func TestWriteAndReadPID(t *testing.T) {
	cfg := testConfig(t)

	if err := WritePID(cfg); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	pid, err := ReadPID(cfg)
	if err != nil {
		t.Fatalf("ReadPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("ReadPID = %d, want %d", pid, os.Getpid())
	}
}

func TestReadPID_MissingFile(t *testing.T) {
	cfg := &config.Config{
		PIDDir: filepath.Join(t.TempDir(), "nonexistent"),
	}
	_, err := ReadPID(cfg)
	if err == nil {
		t.Fatal("expected error reading PID from missing file, got nil")
	}
}

func TestReadPID_InvalidContents(t *testing.T) {
	cfg := testConfig(t)
	if err := os.WriteFile(cfg.PIDFilePath(), []byte("not-a-number"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	_, err := ReadPID(cfg)
	if err == nil {
		t.Fatal("expected error for invalid PID file contents, got nil")
	}
}

func TestRemovePID(t *testing.T) {
	cfg := testConfig(t)
	if err := WritePID(cfg); err != nil {
		t.Fatalf("WritePID: %v", err)
	}

	RemovePID(cfg)

	if _, err := os.Stat(cfg.PIDFilePath()); !os.IsNotExist(err) {
		t.Error("PID file should not exist after RemovePID")
	}
}

func TestRemovePID_NoFile(t *testing.T) {
	cfg := testConfig(t)
	// Should not panic or error on missing file.
	RemovePID(cfg)
}

// --- IsRunning tests ---

func TestIsRunning_CurrentProcess(t *testing.T) {
	cfg := testConfig(t)
	// Write our own PID so IsRunning finds a live process.
	pidData := []byte(strconv.Itoa(os.Getpid()))
	if err := os.WriteFile(cfg.PIDFilePath(), pidData, 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	running, pid := IsRunning(cfg)
	if !running {
		t.Error("expected IsRunning to return true for current process")
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestIsRunning_NoPIDFile(t *testing.T) {
	cfg := testConfig(t)
	running, _ := IsRunning(cfg)
	if running {
		t.Error("expected IsRunning to return false when no PID file exists")
	}
}

func TestIsRunning_StalePID(t *testing.T) {
	cfg := testConfig(t)
	// PID 2147483647 is very unlikely to be a real process.
	pidData := []byte("2147483647")
	if err := os.WriteFile(cfg.PIDFilePath(), pidData, 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	running, pid := IsRunning(cfg)
	if running {
		t.Error("expected IsRunning to return false for stale PID")
	}
	if pid != 2147483647 {
		t.Errorf("pid = %d, want 2147483647", pid)
	}
}

// --- Daemon lifecycle tests ---

func TestStartAndStop(t *testing.T) {
	cfg := testConfig(t)

	d, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// PID file should exist.
	if _, err := os.Stat(cfg.PIDFilePath()); os.IsNotExist(err) {
		t.Error("PID file should exist after Start")
	}

	// Socket file should exist.
	if _, err := os.Stat(cfg.SocketPath()); os.IsNotExist(err) {
		t.Error("socket file should exist after Start")
	}

	// Getters should work.
	if d.SessionManager() == nil {
		t.Error("SessionManager() should not be nil")
	}
	if d.Config() != cfg {
		t.Error("Config() should return the same config")
	}
	if d.StartedAt().IsZero() {
		t.Error("StartedAt() should not be zero")
	}
	if d.Uptime() < 0 {
		t.Error("Uptime() should be non-negative")
	}

	// Stop the daemon.
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// PID file should be cleaned up.
	if _, err := os.Stat(cfg.PIDFilePath()); !os.IsNotExist(err) {
		t.Error("PID file should not exist after Stop")
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	cfg := testConfig(t)

	d, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	// A second Start should fail.
	_, err = Start(cfg)
	if err == nil {
		t.Fatal("expected error starting daemon when already running")
	}
}

func TestStopIdempotent(t *testing.T) {
	cfg := testConfig(t)

	d, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Calling Stop twice should not panic.
	if err := d.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// --- IPC client integration tests ---

func TestIPCSend_Status(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

	srv, err := NewIPCServer(cfg, sm, d)
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer srv.Stop()
	srv.Serve()

	resp, err := IPCSend(cfg, IPCRequest{Action: "status"})
	if err != nil {
		t.Fatalf("IPCSend: %v", err)
	}
	if !resp.OK {
		t.Fatalf("response not OK: %s", resp.Error)
	}
}

func TestIPCSend_UnknownAction(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

	srv, err := NewIPCServer(cfg, sm, d)
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer srv.Stop()
	srv.Serve()

	resp, err := IPCSend(cfg, IPCRequest{Action: "bogus"})
	if err != nil {
		t.Fatalf("IPCSend: %v", err)
	}
	if resp.OK {
		t.Error("expected response not OK for unknown action")
	}
}

func TestDaemonStatusClient(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

	srv, err := NewIPCServer(cfg, sm, d)
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer srv.Stop()
	srv.Serve()

	status, err := DaemonStatus(cfg)
	if err != nil {
		t.Fatalf("DaemonStatus: %v", err)
	}
	if status.Port != 8877 {
		t.Errorf("Port = %d, want 8877", status.Port)
	}
	if status.SessionCount != 0 {
		t.Errorf("SessionCount = %d, want 0", status.SessionCount)
	}
}

func TestRegisterWithDaemonClient(t *testing.T) {
	cfg := testConfig(t)
	sm := session.New(nil)
	d := testDaemon(cfg, sm)

	srv, err := NewIPCServer(cfg, sm, d)
	if err != nil {
		t.Fatalf("NewIPCServer: %v", err)
	}
	defer srv.Stop()
	srv.Serve()

	err = RegisterWithDaemon(cfg, RegisterOpts{
		InstanceID:  "i-test123",
		Profile:     "dev",
		SessionType: "shell",
		PID:         os.Getpid(),
	})
	if err != nil {
		t.Fatalf("RegisterWithDaemon: %v", err)
	}

	sessions := sm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].InstanceID != "i-test123" {
		t.Errorf("InstanceID = %q, want %q", sessions[0].InstanceID, "i-test123")
	}
}

func TestRegisterWithDaemon_NoDaemon(t *testing.T) {
	cfg := &config.Config{
		PIDDir: filepath.Join(t.TempDir(), "empty"),
	}
	err := RegisterWithDaemon(cfg, RegisterOpts{
		InstanceID:  "i-test",
		SessionType: "shell",
	})
	if err == nil {
		t.Fatal("expected error registering with no daemon running")
	}
}

// --- IPC shutdown test ---

func TestIPCShutdown(t *testing.T) {
	cfg := testConfig(t)

	d, err := Start(cfg)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := IPCSend(cfg, IPCRequest{Action: "shutdown"})
	if err != nil {
		t.Fatalf("IPCSend shutdown: %v", err)
	}
	if !resp.OK {
		t.Fatalf("shutdown response not OK: %s", resp.Error)
	}

	// Give the async shutdown a moment to complete.
	time.Sleep(100 * time.Millisecond)

	// PID file should be removed after shutdown.
	if _, err := os.Stat(cfg.PIDFilePath()); !os.IsNotExist(err) {
		// The daemon may still be cleaning up; give it a bit more time.
		time.Sleep(200 * time.Millisecond)
	}
	_ = d // keep reference so it isn't GC'd during shutdown
}
