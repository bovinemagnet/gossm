package session

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// recordingBuilder counts builder calls and lets the test choose the
// command produced for each call.
type recordingBuilder struct {
	mu    sync.Mutex
	calls int32
	fns   []func(ctx context.Context) *exec.Cmd
}

func (b *recordingBuilder) Builder() CommandBuilder {
	return func(ctx context.Context, opts SessionOpts) *exec.Cmd {
		n := atomic.AddInt32(&b.calls, 1)
		b.mu.Lock()
		defer b.mu.Unlock()
		idx := int(n) - 1
		if idx < len(b.fns) && b.fns[idx] != nil {
			return b.fns[idx](ctx)
		}
		return exec.CommandContext(ctx, "sleep", "3600")
	}
}

func (b *recordingBuilder) Calls() int { return int(atomic.LoadInt32(&b.calls)) }

func waitForCalls(b *recordingBuilder, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b.Calls() >= want {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("timeout waiting for builder calls")
}

// quickExitBuilder returns a builder whose first call exits with error
// almost immediately, and subsequent calls produce long-running sleeps.
// This simulates an SSM subprocess that crashes shortly after launch.
func quickExitBuilder(b *recordingBuilder) {
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		// Force an error exit so monitor() takes the unexpected-exit path.
		return exec.CommandContext(ctx, "false")
	})
}

// TestReconnect_OnUnexpectedExit_RespawnsSubprocess verifies that when
// the SSM subprocess exits unexpectedly (non-zero, not user-initiated),
// the manager kicks off a reconnect cycle that builds a new command.
func TestReconnect_OnUnexpectedExit_RespawnsSubprocess(t *testing.T) {
	b := &recordingBuilder{}
	quickExitBuilder(b)
	// Subsequent calls fall through to the default sleep 3600.

	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 5, 5*time.Millisecond, 20*time.Millisecond)
	sm.SetSleeper(func(time.Duration) {})
	defer sm.Close()

	// Disable probes so the only reconnect signal is unexpected exit.
	sm.SetProbe(nil, 0, 0)

	if _, err := sm.StartSession(portForwardOpts("reconn-exit", 13302)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := waitForCalls(b, 2, 2*time.Second); err != nil {
		t.Fatalf("expected reconnect after unexpected exit: %v", err)
	}
}

// TestReconnect_OnUserStop_DoesNotRespawn verifies that StopSession
// (user-initiated) does not trigger a reconnect.
func TestReconnect_OnUserStop_DoesNotRespawn(t *testing.T) {
	b := &recordingBuilder{}
	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 5, 5*time.Millisecond, 20*time.Millisecond)
	sm.SetSleeper(func(time.Duration) {})
	defer sm.Close()
	sm.SetProbe(nil, 0, 0)

	id, err := sm.StartSession(portForwardOpts("reconn-stop", 13303))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := waitForCalls(b, 1, time.Second); err != nil {
		t.Fatalf("initial spawn: %v", err)
	}

	if err := sm.StopSession(id); err != nil {
		t.Fatalf("StopSession: %v", err)
	}

	// Wait for any rogue reconnect to (not) happen.
	time.Sleep(300 * time.Millisecond)

	if b.Calls() != 1 {
		t.Errorf("builder called %d times; expected exactly 1 (no reconnect on user stop)", b.Calls())
	}
}

// TestReconnect_RespectsMaxAttempts verifies that when every respawn
// fails to start, the loop gives up after maxAttempts and ends in
// StateErrored.
func TestReconnect_RespectsMaxAttempts(t *testing.T) {
	b := &recordingBuilder{}
	// Initial succeeds, every respawn afterwards fails to start.
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "3600")
	})
	for i := 0; i < 10; i++ {
		b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, "/this/binary/does/not/exist")
		})
	}

	var slept atomic.Int32
	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 3, 5*time.Millisecond, 20*time.Millisecond)
	sm.SetSleeper(func(time.Duration) { slept.Add(1) })
	defer sm.Close()
	sm.SetProbe(nil, 0, 0)

	id, err := sm.StartSession(portForwardOpts("reconn-max", 13304))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Trigger a reconnect manually since we disabled probing.
	if err := sm.ManualReconnect(id); err != nil {
		t.Fatalf("ManualReconnect: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := sm.GetSession(id)
		if s.State == StateErrored {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := sm.GetSession(id)
	if s.State != StateErrored {
		t.Fatalf("state = %d, want StateErrored after exhausting attempts", s.State)
	}
	// Initial + 3 retries = 4 builder calls.
	if got := b.Calls(); got != 4 {
		t.Errorf("builder calls = %d, want 4 (initial + 3 max attempts)", got)
	}
	if s.ReconnectAttempts != 3 {
		t.Errorf("ReconnectAttempts = %d, want 3", s.ReconnectAttempts)
	}
}

// TestReconnect_NotReconnectableForExternal verifies that externally
// registered sessions (CLI-launched, daemon does not own the process)
// are never reconnected even when their probe fails.
func TestReconnect_NotReconnectableForExternal(t *testing.T) {
	b := &recordingBuilder{}
	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 5, 5*time.Millisecond, 20*time.Millisecond)
	sm.SetSleeper(func(time.Duration) {})
	defer sm.Close()

	var fail atomic.Bool
	fail.Store(true)
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		return !fail.Load()
	}, 20*time.Millisecond, 50*time.Millisecond)

	id := sm.RegisterExternal(portForwardOpts("ext-noreconn", 13305), 99999)

	// Wait long enough for a few probes.
	time.Sleep(200 * time.Millisecond)

	s, ok := sm.GetSession(id)
	if !ok {
		t.Fatal("session not found")
	}
	if s.Reconnectable {
		t.Errorf("external session must not be Reconnectable")
	}
	if b.Calls() != 0 {
		t.Errorf("builder called %d times; external sessions must not be reconnected", b.Calls())
	}
}

// TestReconnectBackoff_Sequence verifies the doubling backoff sequence
// caps at the configured maximum.
func TestReconnectBackoff_Sequence(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 5 * time.Second},
		{2, 10 * time.Second},
		{3, 20 * time.Second},
		{4, 40 * time.Second},
		{5, 60 * time.Second}, // capped
		{6, 60 * time.Second},
	}
	for _, c := range cases {
		got := reconnectBackoff(c.attempt, 5*time.Second, 60*time.Second)
		if got != c.want {
			t.Errorf("reconnectBackoff(%d) = %v, want %v", c.attempt, got, c.want)
		}
	}
}

// TestManualReconnect_ResetsAttempts verifies that a manual reconnect
// after attempts have been exhausted resets the counter and tries again.
func TestManualReconnect_ResetsAttempts(t *testing.T) {
	b := &recordingBuilder{}
	// Initial spawn succeeds.
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "3600")
	})
	// First reconnect attempt fails.
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "/this/binary/does/not/exist")
	})
	// Manual retry succeeds.
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "3600")
	})

	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 1, 5*time.Millisecond, 20*time.Millisecond)
	sm.SetSleeper(func(time.Duration) {})
	defer sm.Close()
	sm.SetProbe(nil, 0, 0)

	id, err := sm.StartSession(portForwardOpts("reconn-manual", 13306))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := sm.ManualReconnect(id); err != nil {
		t.Fatalf("ManualReconnect: %v", err)
	}

	// Wait for the loop to give up (max=1 attempt, so it errors out fast).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := sm.GetSession(id)
		if s.State == StateErrored {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := sm.GetSession(id)
	if s.State != StateErrored {
		t.Fatalf("state = %d, want StateErrored after exhausting attempts", s.State)
	}

	// Manually reconnect again — counter should reset and the third
	// builder call should succeed.
	if err := sm.ManualReconnect(id); err != nil {
		t.Fatalf("second ManualReconnect: %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := sm.GetSession(id)
		if s.State == StateRunning && b.Calls() == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ = sm.GetSession(id)
	if s.State != StateRunning {
		t.Fatalf("after manual reconnect, state = %d, want StateRunning", s.State)
	}
	if got := b.Calls(); got != 3 {
		t.Errorf("builder calls = %d, want 3 (initial + failed + successful manual)", got)
	}
	if s.ReconnectAttempts != 1 {
		t.Errorf("ReconnectAttempts = %d, want 1 after one successful manual respawn", s.ReconnectAttempts)
	}
}

// TestSetSessionProbeInterval_AppliesOnNextTick verifies that updating
// a session's ProbeInterval is honoured by the running probe loop.
//
// Bypasses SetSessionProbeInterval's user-facing 1s minimum (which is a
// safety check for the dashboard, not the loop) by writing the field
// directly so the test can assert on millisecond timing.
func TestSetSessionProbeInterval_AppliesOnNextTick(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	var probes atomic.Int32
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		probes.Add(1)
		return true
	}, time.Hour, 50*time.Millisecond) // huge default so without override we'd see ~1 probe (immediate)

	id, err := sm.StartSession(portForwardOpts("probe-interval", 13307))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Override directly under the lock — same effect as
	// SetSessionProbeInterval but skipping the public-API range check.
	sm.mu.Lock()
	sm.sessions[id].ProbeInterval = 30 * time.Millisecond
	sm.mu.Unlock()

	// Wait for several probes to fire under the new interval.
	time.Sleep(250 * time.Millisecond)
	if got := probes.Load(); got < 3 {
		t.Errorf("probes = %d under 30ms interval; want >= 3 (fast override should be honoured)", got)
	}

	if got := sm.EffectiveProbeInterval(id); got != 30*time.Millisecond {
		t.Errorf("EffectiveProbeInterval = %v, want 30ms", got)
	}
}

// TestSetSessionProbeInterval_RejectsOutOfRange verifies validation.
func TestSetSessionProbeInterval_RejectsOutOfRange(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	id, err := sm.StartSession(portForwardOpts("probe-range", 13308))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if err := sm.SetSessionProbeInterval(id, 100*time.Millisecond); err == nil {
		t.Errorf("expected error for interval < 1s")
	}
	if err := sm.SetSessionProbeInterval(id, 11*time.Minute); err == nil {
		t.Errorf("expected error for interval > 10m")
	}
	if err := sm.SetSessionProbeInterval(id, 5*time.Second); err != nil {
		t.Errorf("5s interval should be accepted, got %v", err)
	}
}

// TestEffectiveProbeInterval_FallsBackToDefault verifies that when no
// per-session override is set, EffectiveProbeInterval returns the
// manager default.
func TestEffectiveProbeInterval_FallsBackToDefault(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()
	sm.SetProbe(nil, 7*time.Second, 0)

	id, err := sm.StartSession(portForwardOpts("probe-default", 13309))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	if got := sm.EffectiveProbeInterval(id); got != 7*time.Second {
		t.Errorf("EffectiveProbeInterval = %v, want 7s (manager default)", got)
	}
}

// TestStopSession_DuringReconnect_DoesNotResurrect verifies that a
// StopSession call landing while runReconnectLoop is mid-respawn does
// not get clobbered by the loop installing the new subprocess and
// setting the session back to StateRunning.
func TestStopSession_DuringReconnect_DoesNotResurrect(t *testing.T) {
	b := &recordingBuilder{}
	entered := make(chan struct{})
	release := make(chan struct{})
	// Initial subprocess crashes immediately to trigger a reconnect.
	quickExitBuilder(b)
	// The reconnect build blocks until the test has called StopSession,
	// putting the stop squarely inside the respawn window.
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		close(entered)
		<-release
		return exec.CommandContext(ctx, "sleep", "3600")
	})

	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 5, time.Millisecond, 5*time.Millisecond)
	sm.SetSleeper(func(time.Duration) {})
	defer sm.Close()
	sm.SetProbe(nil, 0, 0)

	id, err := sm.StartSession(portForwardOpts("stop-during-reconn", 13310))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	<-entered // reconnect loop is now mid-respawn
	if err := sm.StopSession(id); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	var state SessionState
	for time.Now().Before(deadline) {
		s, _ := sm.GetSession(id)
		state = s.State
		if state == StateStopped {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if state != StateStopped {
		t.Fatalf("state = %d, want StateStopped (stop during reconnect must not resurrect the session)", state)
	}
}

// TestManualReconnect_AfterErrored_ResumesProbing verifies that a
// session revived via manual reconnect after reaching StateErrored gets
// its liveness probe back. The probe goroutine exits permanently when
// it observes StateErrored, so the reconnect path must respawn it.
func TestManualReconnect_AfterErrored_ResumesProbing(t *testing.T) {
	b := &recordingBuilder{}
	// Initial spawn succeeds.
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "3600")
	})
	// First reconnect attempt fails to start → StateErrored (max=1).
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "/this/binary/does/not/exist")
	})
	// Second manual reconnect succeeds.
	b.fns = append(b.fns, func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "3600")
	})

	var probes atomic.Int32
	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 1, time.Millisecond, 5*time.Millisecond)
	sm.SetSleeper(func(time.Duration) {})
	defer sm.Close()
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		probes.Add(1)
		return true
	}, 20*time.Millisecond, 50*time.Millisecond)

	id, err := sm.StartSession(portForwardOpts("probe-resume", 13311))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// Exhaust attempts → StateErrored.
	if err := sm.ManualReconnect(id); err != nil {
		t.Fatalf("ManualReconnect: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := sm.GetSession(id)
		if s.State == StateErrored {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Let the probe goroutine observe StateErrored and exit.
	time.Sleep(100 * time.Millisecond)

	// Revive the session.
	if err := sm.ManualReconnect(id); err != nil {
		t.Fatalf("second ManualReconnect: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := sm.GetSession(id)
		if s.State == StateRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s, _ := sm.GetSession(id)
	if s.State != StateRunning {
		t.Fatalf("state = %d, want StateRunning after manual reconnect", s.State)
	}

	// Probing must resume on the revived session.
	baseline := probes.Load()
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if probes.Load() > baseline {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no probes after manual reconnect from StateErrored (probe goroutine was not respawned)")
}

// TestReconnect_OnStall_RespawnsSubprocess verifies that a stalled
// port-forward session triggers a reconnect that invokes the command
// builder again to spawn a fresh subprocess.
func TestReconnect_OnStall_RespawnsSubprocess(t *testing.T) {
	b := &recordingBuilder{}
	sm := New(b.Builder(), nil)
	sm.SetReconnectPolicy(1, 5, 5*time.Millisecond, 20*time.Millisecond)
	sm.SetSleeper(func(time.Duration) {})
	defer sm.Close()

	var fail atomic.Bool
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		return !fail.Load()
	}, 20*time.Millisecond, 50*time.Millisecond)

	id, err := sm.StartSession(portForwardOpts("reconn-stall", 13301))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// First builder call is the initial spawn.
	if err := waitForCalls(b, 1, time.Second); err != nil {
		t.Fatalf("initial spawn never built a command: %v", err)
	}

	// Daemon-spawned sessions must be marked Reconnectable.
	s, _ := sm.GetSession(id)
	if !s.Reconnectable {
		t.Fatalf("daemon-started session should have Reconnectable=true")
	}

	// Trigger probe failure → stall → reconnect.
	fail.Store(true)

	if err := waitForCalls(b, 2, 2*time.Second); err != nil {
		t.Fatalf("expected reconnect to invoke builder a second time: %v", err)
	}
}
