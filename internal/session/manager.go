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

	// Reconnect policy. Configured via SetReconnectPolicy.
	reconnectFailureThreshold int           // consecutive probe failures before triggering reconnect
	reconnectMaxAttempts      int           // give up after this many failed respawn attempts
	reconnectBackoffInitial   time.Duration // first inter-attempt sleep
	reconnectBackoffMax       time.Duration // upper bound on backoff sleep
	sleep                     func(time.Duration)

	sparkData  []int // ring buffer of active session counts, last 60 entries
	sparkIndex int
	stopCh     chan struct{}
}

// New creates a SessionManager.  If builder is nil the default AWS CLI
// command builder is used.  If checker is nil, externally registered
// sessions will not be monitored for process exit.
func New(builder CommandBuilder, checker ProcessChecker) *SessionManager {
	if builder == nil {
		builder = defaultCommandBuilder
	}
	return &SessionManager{
		sessions:                  make(map[string]*Session),
		OnChange:                  make(chan SessionEvent, 64),
		buildCommand:              builder,
		checkProcess:              checker,
		prober:                    defaultTCPProber,
		probeInterval:             30 * time.Second,
		probeTimeout:              2 * time.Second,
		reconnectFailureThreshold: 1,
		reconnectMaxAttempts:      5,
		reconnectBackoffInitial:   5 * time.Second,
		reconnectBackoffMax:       60 * time.Second,
		sleep:                     time.Sleep,
		sparkData:                 make([]int, 60),
		stopCh:                    make(chan struct{}),
	}
}

// SetReconnectPolicy overrides the reconnect parameters. Pass zero for any
// value to keep the existing default for that knob.
func (m *SessionManager) SetReconnectPolicy(failureThreshold, maxAttempts int, backoffInitial, backoffMax time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if failureThreshold > 0 {
		m.reconnectFailureThreshold = failureThreshold
	}
	if maxAttempts > 0 {
		m.reconnectMaxAttempts = maxAttempts
	}
	if backoffInitial > 0 {
		m.reconnectBackoffInitial = backoffInitial
	}
	if backoffMax > 0 {
		m.reconnectBackoffMax = backoffMax
	}
}

// SetSleeper replaces the manager's sleep function. Intended for tests
// so reconnect backoff sequences can be exercised deterministically.
func (m *SessionManager) SetSleeper(s func(time.Duration)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s != nil {
		m.sleep = s
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

// SetProbeTimings updates the probe interval and timeout without
// touching the installed prober. Pass zero for either value to keep
// the existing default for that knob.
func (m *SessionManager) SetProbeTimings(interval, timeout time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
		ID:            id,
		InstanceID:    opts.InstanceID,
		InstanceName:  opts.InstanceName,
		Profile:       opts.Profile,
		Type:          opts.Type,
		State:         StateStarting,
		LocalPort:     opts.LocalPort,
		RemotePort:    opts.RemotePort,
		RemoteHost:    opts.RemoteHost,
		StartedAt:     time.Now(),
		cmd:           cmd,
		cancel:        cancel,
		waitDone:      make(chan struct{}),
		Reconnectable: opts.Type == TypePortForward,
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
	go m.monitor(s, cmd, s.waitDone)

	// Probe the tunnel for liveness if this is a port-forward session.
	if s.Type == TypePortForward {
		go m.monitorProbe(s)
	}

	return id, nil
}

// AdoptSession registers an already-started subprocess (for example a
// PTY-backed shell owned by the web terminal handler) so it appears in the
// dashboard and StopSession can cancel and kill it. The caller owns the
// subprocess I/O; the manager owns cmd.Wait() via the monitor goroutine.
//
// Adopted sessions are non-reconnectable shells: when the subprocess exits,
// monitor transitions them to StateStopped or StateErrored and emits an
// updated event.
func (m *SessionManager) AdoptSession(opts SessionOpts, cmd *exec.Cmd, cancel context.CancelFunc) string {
	id := uuid.New().String()
	waitDone := make(chan struct{})

	s := &Session{
		ID:            id,
		InstanceID:    opts.InstanceID,
		InstanceName:  opts.InstanceName,
		Profile:       opts.Profile,
		Type:          TypeShell,
		State:         StateRunning,
		StartedAt:     time.Now(),
		cmd:           cmd,
		cancel:        cancel,
		waitDone:      waitDone,
		Reconnectable: false,
	}
	if cmd != nil && cmd.Process != nil {
		s.PID = cmd.Process.Pid
	}

	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()

	m.emit(SessionEvent{Type: "added", SessionID: id, Timestamp: time.Now()})

	go m.monitor(s, cmd, waitDone)

	return id
}

// monitor waits for the subprocess to finish and updates state accordingly.
// cmd and waitDone are passed in so each monitor goroutine owns its own
// subprocess: after a reconnect, the previous monitor closes its OWN
// waitDone (not the new one) and exits without disturbing the new state.
func (m *SessionManager) monitor(s *Session, cmd *exec.Cmd, waitDone chan struct{}) {
	err := cmd.Wait()
	close(waitDone)

	m.mu.Lock()
	// If a reconnect cycle has already taken charge of this session, do
	// not override the state it has set.
	if s.State == StateReconnecting {
		m.mu.Unlock()
		return
	}
	// User asked us to stop — record that and we're done.
	if s.State == StateStopping {
		s.State = StateStopped
		m.mu.Unlock()
		m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
		return
	}
	// For non-reconnectable sessions (shells, externally registered),
	// preserve the original behaviour: terminal state on exit.
	if !s.Reconnectable {
		if err != nil {
			s.State = StateErrored
			s.LastError = err.Error()
		} else {
			s.State = StateStopped
		}
		m.mu.Unlock()
		m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
		return
	}
	// Reconnectable session, unexpected exit — flag stalled and ask the
	// reconnect loop to take over.
	s.State = StateStalled
	if err != nil {
		s.LastError = err.Error()
	}
	m.mu.Unlock()
	m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})

	m.triggerReconnect(s, false)
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
	// Snapshot subprocess handles under the lock — these fields can be
	// rewritten by runReconnectLoop during a reconnect cycle.
	cancel := s.cancel
	waitDone := s.waitDone
	cmd := s.cmd
	m.mu.Unlock()

	// Signal the subprocess via context cancellation.
	if cancel != nil {
		cancel()
	}

	// Wait for the monitor goroutine to observe the exit, or kill after timeout.
	if waitDone != nil {
		select {
		case <-waitDone:
			// exited cleanly
		case <-time.After(5 * time.Second):
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Kill()
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
//
// The effective interval between probes is read fresh on every tick so
// runtime per-session updates via SetSessionProbeInterval take effect
// without restarting the goroutine.
func (m *SessionManager) monitorProbe(s *Session) {
	m.mu.Lock()
	if s.probeInFlight {
		m.mu.Unlock()
		return
	}
	s.probeInFlight = true
	prober := m.prober
	timeout := m.probeTimeout
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		s.probeInFlight = false
		m.mu.Unlock()
	}()

	if prober == nil {
		return
	}

	// Run an initial probe immediately so the dashboard does not show
	// "pending" for a full interval after the session starts.
	if !m.runProbe(s, prober, timeout) {
		return
	}

	for {
		interval := m.effectiveProbeInterval(s)
		if interval <= 0 {
			return
		}
		t := time.NewTimer(interval)
		select {
		case <-m.stopCh:
			t.Stop()
			return
		case <-t.C:
			m.mu.RLock()
			prober = m.prober
			timeout = m.probeTimeout
			m.mu.RUnlock()
			if prober == nil {
				return
			}
			if !m.runProbe(s, prober, timeout) {
				return
			}
		}
	}
}

// effectiveProbeInterval returns the per-session ProbeInterval if it's
// set, otherwise the manager-wide default.
func (m *SessionManager) effectiveProbeInterval(s *Session) time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cur, ok := m.sessions[s.ID]; ok && cur.ProbeInterval > 0 {
		return cur.ProbeInterval
	}
	return m.probeInterval
}

// EffectiveProbeInterval is the public view of effectiveProbeInterval.
// The web layer uses it to render the current interval per row.
func (m *SessionManager) EffectiveProbeInterval(id string) time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if cur, ok := m.sessions[id]; ok && cur.ProbeInterval > 0 {
		return cur.ProbeInterval
	}
	return m.probeInterval
}

// DefaultProbeInterval returns the manager-wide probe interval default.
func (m *SessionManager) DefaultProbeInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.probeInterval
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
	shouldReconnect := false
	switch {
	case !ok && cur.State == StateRunning:
		cur.State = StateStalled
		cur.consecutiveFailures++
		stateChanged = true
		if cur.Reconnectable && cur.consecutiveFailures >= m.reconnectFailureThreshold {
			shouldReconnect = true
		}
	case !ok && cur.State == StateStalled:
		cur.consecutiveFailures++
		if cur.Reconnectable && cur.consecutiveFailures >= m.reconnectFailureThreshold {
			shouldReconnect = true
		}
	case ok && cur.State == StateStalled:
		cur.State = StateRunning
		cur.consecutiveFailures = 0
		cur.ReconnectAttempts = 0
		stateChanged = true
	case ok && cur.State == StateRunning:
		cur.consecutiveFailures = 0
		cur.ReconnectAttempts = 0
	}
	m.mu.Unlock()

	if stateChanged {
		m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
	}
	if shouldReconnect {
		m.triggerReconnect(s, false)
	}
	return true
}

// triggerReconnect schedules a reconnect cycle for s. Only one cycle
// runs at a time per session; concurrent triggers while a cycle is
// already in flight are dropped (manual triggers reset the attempt
// counter so they still take effect on the in-flight cycle).
func (m *SessionManager) triggerReconnect(s *Session, manual bool) {
	m.mu.Lock()
	if !s.Reconnectable {
		m.mu.Unlock()
		return
	}
	if manual {
		s.ReconnectAttempts = 0
	}
	if s.reconnectInFlight {
		m.mu.Unlock()
		return
	}
	s.reconnectInFlight = true
	m.mu.Unlock()

	go m.runReconnectLoop(s)
}

// runReconnectLoop keeps respawning the session's subprocess until it
// either succeeds (state returns to StateRunning) or the configured
// maximum number of attempts is exhausted (state becomes StateErrored).
// Backoff between attempts doubles from the initial value to the
// configured max.
func (m *SessionManager) runReconnectLoop(s *Session) {
	defer func() {
		m.mu.Lock()
		s.reconnectInFlight = false
		m.mu.Unlock()
	}()

	for {
		m.mu.Lock()
		if s.State == StateStopping {
			// The old subprocess was already cancelled and reaped before
			// this iteration, so nothing else will record the stop.
			s.State = StateStopped
			m.mu.Unlock()
			m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
			return
		}
		if s.State == StateStopped {
			m.mu.Unlock()
			return
		}
		if _, exists := m.sessions[s.ID]; !exists {
			m.mu.Unlock()
			return
		}
		attempt := s.ReconnectAttempts + 1
		if attempt > m.reconnectMaxAttempts {
			s.State = StateErrored
			if s.LastError == "" {
				s.LastError = fmt.Sprintf("reconnect exhausted after %d attempts", m.reconnectMaxAttempts)
			}
			m.mu.Unlock()
			m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
			return
		}
		opts := optsFromSession(s)
		oldCmd := s.cmd
		oldCancel := s.cancel
		oldWaitDone := s.waitDone
		backoffInitial := m.reconnectBackoffInitial
		backoffMax := m.reconnectBackoffMax
		s.State = StateReconnecting
		s.ReconnectAttempts = attempt
		s.LastReconnectAt = time.Now()
		m.mu.Unlock()
		m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})

		// Stop the old subprocess if any. Wait briefly for the old
		// monitor to clean up, but do not block forever.
		if oldCancel != nil {
			oldCancel()
		}
		if oldWaitDone != nil {
			select {
			case <-oldWaitDone:
			case <-time.After(5 * time.Second):
				if oldCmd != nil && oldCmd.Process != nil {
					_ = oldCmd.Process.Kill()
				}
			}
		}

		// Build and start a fresh subprocess with the same opts.
		ctx, cancel := context.WithCancel(context.Background())
		newCmd := m.buildCommand(ctx, opts)
		startErr := newCmd.Start()
		if startErr != nil {
			cancel()
			m.mu.Lock()
			s.LastError = fmt.Sprintf("reconnect attempt %d: %v", attempt, startErr)
			m.mu.Unlock()
			m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})

			// Sleep with backoff, then loop.
			m.sleep(reconnectBackoff(attempt, backoffInitial, backoffMax))
			continue
		}

		// Success — install the new subprocess on the session, unless a
		// stop or removal arrived while we were respawning. In that case
		// kill the fresh subprocess rather than resurrecting the session.
		newWaitDone := make(chan struct{})
		m.mu.Lock()
		_, exists := m.sessions[s.ID]
		if s.State == StateStopping || s.State == StateStopped || !exists {
			if s.State == StateStopping {
				s.State = StateStopped
			}
			m.mu.Unlock()
			cancel()
			if newCmd.Process != nil {
				_ = newCmd.Process.Kill()
			}
			_ = newCmd.Wait()
			m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})
			return
		}
		s.cmd = newCmd
		s.cancel = cancel
		s.waitDone = newWaitDone
		s.PID = newCmd.Process.Pid
		s.State = StateRunning
		s.consecutiveFailures = 0
		// ReconnectAttempts is left non-zero until a successful probe
		// confirms the tunnel is healthy; that lets us track repeated
		// flapping and eventually give up if it keeps failing.
		m.mu.Unlock()
		m.emit(SessionEvent{Type: "updated", SessionID: s.ID, Timestamp: time.Now()})

		go m.monitor(s, newCmd, newWaitDone)
		// Restore liveness probing. The probe goroutine exits permanently
		// when it observes a terminal state (e.g. StateErrored before a
		// manual reconnect); probeInFlight makes this a no-op when the
		// original goroutine is still running.
		if s.Type == TypePortForward {
			go m.monitorProbe(s)
		}
		return
	}
}

// reconnectBackoff returns the delay for the n-th reconnect attempt
// (1-indexed). The sequence doubles from initial up to max.
func reconnectBackoff(attempt int, initial, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := initial
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}

// optsFromSession reconstructs the SessionOpts that drive the
// command builder for a respawn. Caller must hold m.mu.
func optsFromSession(s *Session) SessionOpts {
	return SessionOpts{
		InstanceID:   s.InstanceID,
		InstanceName: s.InstanceName,
		Profile:      s.Profile,
		Type:         s.Type,
		LocalPort:    s.LocalPort,
		RemotePort:   s.RemotePort,
		RemoteHost:   s.RemoteHost,
	}
}

// ManualReconnect resets the attempt counter and triggers a reconnect
// cycle for the named session. Returns an error if the session is
// missing or not reconnectable.
func (m *SessionManager) ManualReconnect(id string) error {
	m.mu.RLock()
	s, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("session %s not found", id)
	}
	if !s.Reconnectable {
		return fmt.Errorf("session %s is not reconnectable", id)
	}
	m.triggerReconnect(s, true)
	return nil
}

// SetSessionProbeInterval updates the per-session probe interval. The
// new interval must be between 1 second and 10 minutes.
func (m *SessionManager) SetSessionProbeInterval(id string, d time.Duration) error {
	if d < time.Second || d > 10*time.Minute {
		return fmt.Errorf("probe interval %s out of range (must be between 1s and 10m)", d)
	}
	m.mu.Lock()
	s, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("session %s not found", id)
	}
	s.ProbeInterval = d
	m.mu.Unlock()
	m.emit(SessionEvent{Type: "updated", SessionID: id, Timestamp: time.Now()})
	return nil
}

