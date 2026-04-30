package session

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CommandBuilder is a factory that produces the exec.Cmd used to drive
// an SSM session.  Injecting this makes the manager testable.
type CommandBuilder func(ctx context.Context, opts SessionOpts) *exec.Cmd

// ProcessChecker tests whether a process with the given PID is still alive.
// Injecting this allows the manager to monitor externally registered sessions.
type ProcessChecker func(pid int) bool

// Prober tests whether a port-forward tunnel is still functional.
// It should return true when the tunnel passes its liveness check.
// The context carries the probe deadline.
type Prober func(ctx context.Context, s *Session) bool

// defaultTCPProber dials 127.0.0.1:LocalPort with the deadline carried
// by ctx. A successful connect indicates the local plugin listener is
// up; over time, dial failures correlate strongly with a torn-down
// tunnel because the plugin closes its listener when the SSM control
// channel is lost.
func defaultTCPProber(ctx context.Context, s *Session) bool {
	if s.Type != TypePortForward || s.LocalPort == 0 {
		return false
	}
	d := net.Dialer{}
	addr := fmt.Sprintf("127.0.0.1:%d", s.LocalPort)
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// SessionManager is a goroutine-safe registry of active SSM sessions.
type SessionManager struct {
	mu            sync.RWMutex
	sessions      map[string]*Session
	OnChange      chan SessionEvent
	buildCommand  CommandBuilder
	checkProcess  ProcessChecker
	prober        Prober
	probeInterval time.Duration
	probeTimeout  time.Duration
	sparkData     []int // ring buffer of active session counts, last 60 entries
	sparkIndex    int
	stopCh        chan struct{}
}

// New creates a SessionManager.  If builder is nil the default AWS CLI
// command builder is used.  If checker is nil, externally registered
// sessions will not be monitored for process exit.
func New(builder CommandBuilder, checker ProcessChecker) *SessionManager {
	if builder == nil {
		builder = defaultCommandBuilder
	}
	return &SessionManager{
		sessions:      make(map[string]*Session),
		OnChange:      make(chan SessionEvent, 64),
		buildCommand:  builder,
		checkProcess:  checker,
		prober:        defaultTCPProber,
		probeInterval: 30 * time.Second,
		probeTimeout:  2 * time.Second,
		sparkData:     make([]int, 60),
		stopCh:        make(chan struct{}),
	}
}

// SetProbe overrides the prober, probe interval, and per-probe timeout.
// Pass interval=0 or timeout=0 to keep the existing values for those
// individual knobs. Pass prober=nil to disable probing entirely.
// Intended for tests and for higher layers wanting custom liveness checks.
func (m *SessionManager) SetProbe(p Prober, interval, timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prober = p
	if interval > 0 {
		m.probeInterval = interval
	}
	if timeout > 0 {
		m.probeTimeout = timeout
	}
}

// defaultCommandBuilder produces the real `aws ssm start-session` command.
func defaultCommandBuilder(ctx context.Context, opts SessionOpts) *exec.Cmd {
	args := []string{"ssm", "start-session", "--target", opts.InstanceID}

	if opts.Profile != "" {
		args = append(args, "--profile", opts.Profile)
	}

	if opts.Type == TypePortForward {
		args = append(args, "--document-name", "AWS-StartPortForwardingSessionToRemoteHost")
		args = append(args, "--parameters", fmt.Sprintf(
			`{"portNumber":["%d"],"localPortNumber":["%d"],"host":["%s"]}`,
			opts.RemotePort, opts.LocalPort, opts.RemoteHost,
		))
	}

	return exec.CommandContext(ctx, "aws", args...)
}

// StartSession creates a new session, spawns its subprocess in a
// goroutine, and returns the session ID.
func (m *SessionManager) StartSession(opts SessionOpts) (string, error) {
	id := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := m.buildCommand(ctx, opts)

	s := &Session{
		ID:           id,
		InstanceID:   opts.InstanceID,
		InstanceName: opts.InstanceName,
		Profile:      opts.Profile,
		Type:         opts.Type,
		State:        StateStarting,
		LocalPort:    opts.LocalPort,
		RemotePort:   opts.RemotePort,
		RemoteHost:   opts.RemoteHost,
		StartedAt:    time.Now(),
		cmd:          cmd,
		cancel:       cancel,
		waitDone:     make(chan struct{}),
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("failed to start session: %w", err)
	}

	s.PID = cmd.Process.Pid
	s.State = StateRunning

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	m.emit(SessionEvent{Type: "added", SessionID: id, Timestamp: time.Now()})

	// Monitor the subprocess in the background.
	go m.monitor(s)

	// Probe the tunnel for liveness if this is a port-forward session.
	if s.Type == TypePortForward {
		go m.monitorProbe(s)
	}

	return id, nil
}

// monitor waits for the subprocess to finish and updates state accordingly.
func (m *SessionManager) monitor(s *Session) {
	err := s.cmd.Wait()
	close(s.waitDone)

	m.mu.Lock()
	if err != nil {
		s.State = StateErrored
		s.LastError = err.Error()
	} else {
		s.State = StateStopped
	}
	m.mu.Unlock()

	m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
}

// monitorExternalPID polls the PID of an externally registered session
// and marks it stopped when the process is no longer alive.
func (m *SessionManager) monitorExternalPID(s *Session) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !m.checkProcess(s.PID) {
				m.mu.Lock()
				if s.State == StateRunning || s.State == StateStarting {
					s.State = StateStopped
				}
				m.mu.Unlock()
				m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
				return
			}
		case <-m.stopCh:
			return
		}
	}
}

// StopSession cancels the session context, waits briefly, then kills the
// process if it is still running.
func (m *SessionManager) StopSession(id string) error {
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("session %s not found", id)
	}
	s.State = StateStopping
	m.mu.Unlock()

	// Signal the subprocess via context cancellation.
	if s.cancel != nil {
		s.cancel()
	}

	// Wait for the monitor goroutine to observe the exit, or kill after timeout.
	if s.waitDone != nil {
		select {
		case <-s.waitDone:
			// exited cleanly
		case <-time.After(5 * time.Second):
			if s.cmd != nil && s.cmd.Process != nil {
				_ = s.cmd.Process.Kill()
			}
		}
	}

	return nil
}

// GetSession returns a copy of the session with the given ID.
func (m *SessionManager) GetSession(id string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.sessions[id]
	if !ok {
		return nil, false
	}
	cp := *s
	return &cp, true
}

// ListSessions returns a copy of every session in the registry.
func (m *SessionManager) ListSessions() []Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	list := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, *s)
	}
	return list
}

// RegisterExternal registers a session that was started outside of the
// manager (e.g. by the CLI) so the daemon can track it.
func (m *SessionManager) RegisterExternal(opts SessionOpts, pid int) string {
	id := uuid.New().String()

	s := &Session{
		ID:           id,
		InstanceID:   opts.InstanceID,
		InstanceName: opts.InstanceName,
		Profile:      opts.Profile,
		Type:         opts.Type,
		State:        StateRunning,
		LocalPort:    opts.LocalPort,
		RemotePort:   opts.RemotePort,
		RemoteHost:   opts.RemoteHost,
		StartedAt:    time.Now(),
		PID:          pid,
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	m.emit(SessionEvent{Type: "added", SessionID: id, Timestamp: time.Now()})

	// Monitor the external process so we detect when it exits.
	if m.checkProcess != nil {
		go m.monitorExternalPID(s)
	}

	if s.Type == TypePortForward {
		go m.monitorProbe(s)
	}

	return id
}

// RemoveSession removes a session from the registry.
func (m *SessionManager) RemoveSession(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()

	m.emit(SessionEvent{Type: "removed", SessionID: id, Timestamp: time.Now()})
}

// SessionCount returns the number of sessions currently tracked.
func (m *SessionManager) SessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}

// SparkData returns a copy of the sparkline ring buffer.
func (m *SessionManager) SparkData() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cp := make([]int, len(m.sparkData))
	copy(cp, m.sparkData)
	return cp
}

// RecordSparkPoint records the current session count into the ring buffer.
func (m *SessionManager) RecordSparkPoint() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sparkData[m.sparkIndex] = len(m.sessions)
	m.sparkIndex = (m.sparkIndex + 1) % len(m.sparkData)
}

// Close stops every tracked session and shuts down monitoring goroutines.
func (m *SessionManager) Close() {
	// Signal all monitorExternalPID goroutines to exit.
	select {
	case <-m.stopCh:
		// Already closed.
	default:
		close(m.stopCh)
	}

	m.mu.RLock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		_ = m.StopSession(id)
	}
}

// emit sends a SessionEvent on the OnChange channel without blocking.
func (m *SessionManager) emit(e SessionEvent) {
	select {
	case m.OnChange <- e:
	default:
	}
}

// monitorProbe periodically runs the prober for a port-forward session.
// On a Running → probe-fails edge it transitions to StateStalled; on a
// Stalled → probe-succeeds edge it transitions back to Running. The
// goroutine exits when the session leaves an active state, when stopCh
// is closed, or when the prober is nil.
func (m *SessionManager) monitorProbe(s *Session) {
	m.mu.RLock()
	interval := m.probeInterval
	timeout := m.probeTimeout
	prober := m.prober
	m.mu.RUnlock()

	if prober == nil || interval <= 0 {
		return
	}

	// Run an initial probe immediately so the dashboard does not show
	// "pending" for a full interval after the session starts.
	if !m.runProbe(s, prober, timeout) {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			if !m.runProbe(s, prober, timeout) {
				return
			}
		}
	}
}

// runProbe executes a single probe against s and applies the result to
// the session record. It returns false to signal that monitorProbe
// should exit (because the session has been removed from the registry
// or has reached a terminal state).
func (m *SessionManager) runProbe(s *Session, prober Prober, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	ok := prober(ctx, s)
	cancel()

	m.mu.Lock()
	cur, exists := m.sessions[s.ID]
	if !exists {
		m.mu.Unlock()
		return false
	}
	if cur.State == StateStopping || cur.State == StateStopped || cur.State == StateErrored {
		m.mu.Unlock()
		return false
	}

	cur.LastProbeAt = time.Now()
	cur.LastProbeOK = ok

	stateChanged := false
	switch {
	case !ok && cur.State == StateRunning:
		cur.State = StateStalled
		stateChanged = true
	case ok && cur.State == StateStalled:
		cur.State = StateRunning
		stateChanged = true
	}
	m.mu.Unlock()

	if stateChanged {
		m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
	}
	return true
}

