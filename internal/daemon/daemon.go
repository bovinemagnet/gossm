// Package daemon manages the gossm background process lifecycle.
package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

// Daemon is the long-running background process that owns sessions and IPC.
type Daemon struct {
	cfg       *config.Config
	sm        *session.SessionManager
	ipc       *IPCServer
	startedAt time.Time
	stopCh    chan struct{}
}

// IsRunning checks whether a daemon process is already alive by reading the
// PID file and probing the process. Returns (alive, pid).
func IsRunning(cfg *config.Config) (bool, int) {
	pid, err := ReadPID(cfg)
	if err != nil {
		return false, 0
	}
	return isProcessAlive(pid), pid
}

// WritePID writes the current process ID to the PID file.
func WritePID(cfg *config.Config) error {
	if err := cfg.EnsurePIDDir(); err != nil {
		return fmt.Errorf("ensure pid dir: %w", err)
	}
	data := []byte(strconv.Itoa(os.Getpid()))
	return os.WriteFile(cfg.PIDFilePath(), data, 0o644)
}

// RemovePID removes the PID file, ignoring errors.
func RemovePID(cfg *config.Config) {
	_ = os.Remove(cfg.PIDFilePath())
}

// ReadPID reads the PID from the PID file.
func ReadPID(cfg *config.Config) (int, error) {
	data, err := os.ReadFile(cfg.PIDFilePath())
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid pid file contents: %w", err)
	}
	return pid, nil
}

// Start creates a new Daemon, starts the SessionManager and IPC server, writes
// the PID file, and begins periodic spark-point recording.
func Start(cfg *config.Config) (*Daemon, error) {
	alive, existingPID := IsRunning(cfg)
	if alive {
		return nil, fmt.Errorf("daemon already running (pid %d)", existingPID)
	}

	sm := session.New(nil, isProcessAlive)

	d := &Daemon{
		cfg:       cfg,
		sm:        sm,
		startedAt: time.Now(),
		stopCh:    make(chan struct{}),
	}

	ipc, err := NewIPCServer(cfg, sm, d)
	if err != nil {
		return nil, fmt.Errorf("start ipc server: %w", err)
	}
	d.ipc = ipc
	ipc.Serve()

	if err := WritePID(cfg); err != nil {
		ipc.Stop()
		return nil, fmt.Errorf("write pid: %w", err)
	}

	// Periodically record spark data points.
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sm.RecordSparkPoint()
			case <-d.stopCh:
				return
			}
		}
	}()

	return d, nil
}

// Stop shuts down the daemon: closes sessions, stops IPC, and cleans up files.
func (d *Daemon) Stop() error {
	// Signal the spark-point goroutine to exit.
	select {
	case <-d.stopCh:
		// Already closed.
	default:
		close(d.stopCh)
	}

	d.sm.Close()

	if d.ipc != nil {
		d.ipc.Stop()
	}

	RemovePID(d.cfg)
	_ = os.Remove(d.cfg.SocketPath())

	return nil
}

// SessionManager returns the daemon's session manager.
func (d *Daemon) SessionManager() *session.SessionManager {
	return d.sm
}

// Config returns the daemon's configuration.
func (d *Daemon) Config() *config.Config {
	return d.cfg
}

// Uptime returns the duration since the daemon was started.
func (d *Daemon) Uptime() time.Duration {
	return time.Since(d.startedAt)
}

// StartedAt returns the time the daemon was started.
func (d *Daemon) StartedAt() time.Time {
	return d.startedAt
}

