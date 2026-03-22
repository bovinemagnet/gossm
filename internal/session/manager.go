package session

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/google/uuid"
)

// CommandBuilder is a factory that produces the exec.Cmd used to drive
// an SSM session.  Injecting this makes the manager testable.
type CommandBuilder func(ctx context.Context, opts SessionOpts) *exec.Cmd

// SessionManager is a goroutine-safe registry of active SSM sessions.
type SessionManager struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	OnChange     chan SessionEvent
	buildCommand CommandBuilder
	sparkData    []int // ring buffer of active session counts, last 60 entries
	sparkIndex   int
}

// New creates a SessionManager.  If builder is nil the default AWS CLI
// command builder is used.
func New(builder CommandBuilder) *SessionManager {
	if builder == nil {
		builder = defaultCommandBuilder
	}
	return &SessionManager{
		sessions:     make(map[string]*Session),
		OnChange:     make(chan SessionEvent, 64),
		buildCommand: builder,
		sparkData:    make([]int, 60),
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

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	m.emit(SessionEvent{Type: "added", SessionID: id, Timestamp: time.Now()})

	// Monitor the subprocess in the background.
	go m.monitor(s)

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
	s.cancel()

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

// Close stops every tracked session.
func (m *SessionManager) Close() {
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
