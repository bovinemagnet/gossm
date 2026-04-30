package session

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// sleepBuilder returns a CommandBuilder that spawns a long-running sleep process.
func sleepBuilder() CommandBuilder {
	return func(ctx context.Context, opts SessionOpts) *exec.Cmd {
		return exec.CommandContext(ctx, "sleep", "3600")
	}
}

func testOpts(name string) SessionOpts {
	return SessionOpts{
		InstanceID:   "i-" + name,
		InstanceName: name,
		Profile:      "default",
		Type:         TypeShell,
	}
}

// --- defaultCommandBuilder tests ---

func TestDefaultCommandBuilder_Shell(t *testing.T) {
	opts := SessionOpts{
		InstanceID: "i-abc123",
		Profile:    "prod",
		Type:       TypeShell,
	}
	cmd := defaultCommandBuilder(context.Background(), opts)

	args := cmd.Args
	// Should be: aws ssm start-session --target i-abc123 --profile prod
	if args[0] != "aws" {
		t.Errorf("args[0] = %q, want %q", args[0], "aws")
	}
	if !containsArg(args, "--target") || argAfter(args, "--target") != "i-abc123" {
		t.Errorf("expected --target i-abc123 in args: %v", args)
	}
	if !containsArg(args, "--profile") || argAfter(args, "--profile") != "prod" {
		t.Errorf("expected --profile prod in args: %v", args)
	}
	// Should NOT contain port forwarding args.
	if containsArg(args, "--document-name") {
		t.Errorf("shell session should not have --document-name in args: %v", args)
	}
	if containsArg(args, "--parameters") {
		t.Errorf("shell session should not have --parameters in args: %v", args)
	}
}

func TestDefaultCommandBuilder_ShellNoProfile(t *testing.T) {
	opts := SessionOpts{
		InstanceID: "i-abc123",
		Type:       TypeShell,
	}
	cmd := defaultCommandBuilder(context.Background(), opts)

	if containsArg(cmd.Args, "--profile") {
		t.Errorf("empty profile should not produce --profile flag: %v", cmd.Args)
	}
}

func TestDefaultCommandBuilder_PortForward(t *testing.T) {
	opts := SessionOpts{
		InstanceID: "i-db999",
		Profile:    "staging",
		Type:       TypePortForward,
		LocalPort:  5432,
		RemotePort: 5432,
		RemoteHost: "db.internal",
	}
	cmd := defaultCommandBuilder(context.Background(), opts)

	args := cmd.Args
	if !containsArg(args, "--document-name") {
		t.Fatalf("expected --document-name in args: %v", args)
	}
	docName := argAfter(args, "--document-name")
	if docName != "AWS-StartPortForwardingSessionToRemoteHost" {
		t.Errorf("document name = %q, want %q", docName, "AWS-StartPortForwardingSessionToRemoteHost")
	}

	if !containsArg(args, "--parameters") {
		t.Fatalf("expected --parameters in args: %v", args)
	}
	params := argAfter(args, "--parameters")
	if !strings.Contains(params, `"portNumber":["5432"]`) {
		t.Errorf("parameters missing portNumber: %s", params)
	}
	if !strings.Contains(params, `"localPortNumber":["5432"]`) {
		t.Errorf("parameters missing localPortNumber: %s", params)
	}
	if !strings.Contains(params, `"host":["db.internal"]`) {
		t.Errorf("parameters missing host: %s", params)
	}
}

// containsArg checks if an argument exists in the args slice.
func containsArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// argAfter returns the argument immediately following the given flag.
func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestNewSessionManager(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	if sm == nil {
		t.Fatal("New returned nil")
	}
	if len(sm.ListSessions()) != 0 {
		t.Errorf("new manager should have 0 sessions, got %d", len(sm.ListSessions()))
	}
}

func TestStartSession(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	id, err := sm.StartSession(testOpts("alpha"))
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if id == "" {
		t.Fatal("StartSession returned empty id")
	}

	sessions := sm.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].InstanceID != "i-alpha" {
		t.Errorf("InstanceID = %q, want %q", sessions[0].InstanceID, "i-alpha")
	}
}

func TestStopSession(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	id, err := sm.StartSession(testOpts("beta"))
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	err = sm.StopSession(id)
	if err != nil {
		t.Fatalf("StopSession failed: %v", err)
	}

	// Allow the monitor goroutine a moment to update state after waitDone closes.
	time.Sleep(200 * time.Millisecond)

	s, ok := sm.GetSession(id)
	if !ok {
		t.Fatal("session not found after stop")
	}
	// Cancelled processes exit with an error, so StateErrored is expected.
	// StateStopped or StateStopping are also acceptable.
	if s.State != StateStopped && s.State != StateErrored && s.State != StateStopping {
		t.Errorf("state = %d, want StateStopped(%d), StateErrored(%d), or StateStopping(%d)",
			s.State, StateStopped, StateErrored, StateStopping)
	}
}

func TestListSessions(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	for _, name := range []string{"a", "b", "c"} {
		if _, err := sm.StartSession(testOpts(name)); err != nil {
			t.Fatalf("StartSession(%q) failed: %v", name, err)
		}
	}

	sessions := sm.ListSessions()
	if len(sessions) != 3 {
		t.Errorf("expected 3 sessions, got %d", len(sessions))
	}
}

func TestSessionCount(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	if sm.SessionCount() != 0 {
		t.Errorf("initial count = %d, want 0", sm.SessionCount())
	}

	if _, err := sm.StartSession(testOpts("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := sm.StartSession(testOpts("y")); err != nil {
		t.Fatal(err)
	}

	if sm.SessionCount() != 2 {
		t.Errorf("count = %d, want 2", sm.SessionCount())
	}
}

func TestRegisterExternal(t *testing.T) {
	sm := New(sleepBuilder(), nil)

	id := sm.RegisterExternal(testOpts("ext"), 99999)
	if id == "" {
		t.Fatal("RegisterExternal returned empty id")
	}

	s, ok := sm.GetSession(id)
	if !ok {
		t.Fatal("registered external session not found")
	}
	if s.PID != 99999 {
		t.Errorf("PID = %d, want 99999", s.PID)
	}
	if s.State != StateRunning {
		t.Errorf("State = %d, want StateRunning(%d)", s.State, StateRunning)
	}
}

func TestConcurrentAccess(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	var wg sync.WaitGroup
	const goroutines = 20

	// Start sessions concurrently, collect IDs.
	var mu sync.Mutex
	var ids []string
	for range goroutines {
		wg.Go(func() {
			id, err := sm.StartSession(testOpts("concurrent"))
			if err != nil {
				t.Errorf("StartSession failed: %v", err)
				return
			}
			mu.Lock()
			ids = append(ids, id)
			mu.Unlock()
		})
	}
	wg.Wait()

	// List and count concurrently.
	for range goroutines {
		wg.Go(func() {
			_ = sm.ListSessions()
			_ = sm.SessionCount()
		})
	}
	wg.Wait()

	// Stop sessions concurrently.
	for _, id := range ids {
		wg.Go(func() {
			_ = sm.StopSession(id)
		})
	}
	wg.Wait()
}

func TestSparkData(t *testing.T) {
	sm := New(sleepBuilder(), nil)

	// Record a few points with external sessions to get non-zero values.
	sm.RegisterExternal(testOpts("s1"), 1)
	sm.RecordSparkPoint()
	sm.RegisterExternal(testOpts("s2"), 2)
	sm.RecordSparkPoint()

	data := sm.SparkData()
	if len(data) != 60 {
		t.Fatalf("SparkData length = %d, want 60", len(data))
	}
	// First two entries should have 1 and 2 sessions respectively.
	if data[0] != 1 {
		t.Errorf("sparkData[0] = %d, want 1", data[0])
	}
	if data[1] != 2 {
		t.Errorf("sparkData[1] = %d, want 2 (but got %d)", data[1], data[1])
	}
}

func TestClose(t *testing.T) {
	sm := New(sleepBuilder(), nil)

	for _, name := range []string{"p", "q", "r"} {
		if _, err := sm.StartSession(testOpts(name)); err != nil {
			t.Fatalf("StartSession(%q) failed: %v", name, err)
		}
	}

	sm.Close()

	// Allow monitor goroutines time to update state.
	time.Sleep(200 * time.Millisecond)

	for _, s := range sm.ListSessions() {
		if s.State != StateStopped && s.State != StateErrored && s.State != StateStopping {
			t.Errorf("session %s state = %d after Close, want stopped/errored/stopping", s.ID, s.State)
		}
	}
}

// --- External session PID monitoring tests ---

func TestExternalSessionDetectedAsStopped(t *testing.T) {
	var alive atomic.Bool
	alive.Store(true)
	checker := func(pid int) bool { return alive.Load() }

	sm := New(sleepBuilder(), checker)
	defer sm.Close()

	id := sm.RegisterExternal(testOpts("ext-mon"), 12345)

	// Verify it starts as Running.
	s, ok := sm.GetSession(id)
	if !ok {
		t.Fatal("session not found")
	}
	if s.State != StateRunning {
		t.Fatalf("initial state = %d, want StateRunning(%d)", s.State, StateRunning)
	}

	// Simulate process death.
	alive.Store(false)

	// Wait for monitoring tick (5s) plus margin.
	time.Sleep(7 * time.Second)

	s, ok = sm.GetSession(id)
	if !ok {
		t.Fatal("session not found after monitoring")
	}
	if s.State != StateStopped {
		t.Errorf("state = %d, want StateStopped(%d)", s.State, StateStopped)
	}
}

func TestExternalSessionMonitoringStopsOnClose(t *testing.T) {
	checker := func(pid int) bool { return true } // always alive

	sm := New(sleepBuilder(), checker)

	sm.RegisterExternal(testOpts("ext-close"), 12345)

	// Close should not hang — monitoring goroutine exits via stopCh.
	done := make(chan struct{})
	go func() {
		sm.Close()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(3 * time.Second):
		t.Fatal("Close() did not return within 3s — monitoring goroutine may be leaked")
	}
}

func TestNoMonitoringWhenCheckerNil(t *testing.T) {
	sm := New(sleepBuilder(), nil) // nil checker

	id := sm.RegisterExternal(testOpts("ext-nil"), 12345)

	// Wait a bit — should not panic or change state.
	time.Sleep(500 * time.Millisecond)

	s, ok := sm.GetSession(id)
	if !ok {
		t.Fatal("session not found")
	}
	if s.State != StateRunning {
		t.Errorf("state = %d, want StateRunning(%d) — nil checker should not monitor", s.State, StateRunning)
	}
}

func TestSessionDefaultProbeFields(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	id, err := sm.StartSession(testOpts("probe-default"))
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	s, ok := sm.GetSession(id)
	if !ok {
		t.Fatal("session not found")
	}
	if !s.LastProbeAt.IsZero() {
		t.Errorf("LastProbeAt = %v, want zero value", s.LastProbeAt)
	}
	if s.LastProbeOK {
		t.Errorf("LastProbeOK = true, want false on a freshly started session")
	}
}

func TestStateStalledDistinct(t *testing.T) {
	if StateStalled == StateRunning {
		t.Fatal("StateStalled must not collide with StateRunning")
	}
	if StateStalled == StateStopped {
		t.Fatal("StateStalled must not collide with StateStopped")
	}
	if StateStalled == StateErrored {
		t.Fatal("StateStalled must not collide with StateErrored")
	}
}

func TestDefaultTCPProberSucceedsOnLiveListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	s := &Session{
		Type:      TypePortForward,
		LocalPort: port,
	}

	if !defaultTCPProber(context.Background(), s) {
		t.Errorf("defaultTCPProber returned false on a live listener at 127.0.0.1:%d", port)
	}
}

func TestDefaultTCPProberFailsOnDeadPort(t *testing.T) {
	// Bind a port to discover one that's free, then close the listener.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	s := &Session{
		Type:      TypePortForward,
		LocalPort: port,
	}

	if defaultTCPProber(context.Background(), s) {
		t.Errorf("defaultTCPProber returned true for a closed port %d", port)
	}
}

func TestSetProbeOverridesDefault(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	called := false
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		called = true
		return true
	}, 50*time.Millisecond, 100*time.Millisecond)

	if sm.probeInterval != 50*time.Millisecond {
		t.Errorf("probeInterval = %v, want 50ms", sm.probeInterval)
	}
	if sm.probeTimeout != 100*time.Millisecond {
		t.Errorf("probeTimeout = %v, want 100ms", sm.probeTimeout)
	}

	if !sm.prober(context.Background(), &Session{}) {
		t.Errorf("installed prober did not run")
	}
	if !called {
		t.Errorf("installed prober was not invoked")
	}
}

func TestNewInstallsDefaults(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	if sm.prober == nil {
		t.Fatal("New should install a default prober")
	}
	if sm.probeInterval == 0 {
		t.Fatal("New should install a non-zero probeInterval")
	}
	if sm.probeTimeout == 0 {
		t.Fatal("New should install a non-zero probeTimeout")
	}
}

// portForwardOpts builds a SessionOpts for a port-forward to a given local port.
func portForwardOpts(name string, localPort int) SessionOpts {
	return SessionOpts{
		InstanceID:   "i-" + name,
		InstanceName: name,
		Profile:      "default",
		Type:         TypePortForward,
		LocalPort:    localPort,
		RemotePort:   localPort,
		RemoteHost:   "remote.local",
	}
}

func TestProbeUpdatesLastProbeFields(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	var probes atomic.Int32
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		probes.Add(1)
		return true
	}, 30*time.Millisecond, 50*time.Millisecond)

	id, err := sm.StartSession(portForwardOpts("probe-up", 12345))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	// Mark Running so monitorProbe doesn't skip it.
	sm.markRunningForTest(id)

	// Wait long enough for at least 3 probes.
	time.Sleep(150 * time.Millisecond)

	s, ok := sm.GetSession(id)
	if !ok {
		t.Fatal("session not found")
	}
	if probes.Load() < 2 {
		t.Errorf("probe count = %d, want >= 2", probes.Load())
	}
	if s.LastProbeAt.IsZero() {
		t.Errorf("LastProbeAt should have been updated")
	}
	if !s.LastProbeOK {
		t.Errorf("LastProbeOK should be true after a successful probe")
	}
}

func TestProbeFailureTransitionsToStalled(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	var fail atomic.Bool
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		return !fail.Load()
	}, 30*time.Millisecond, 50*time.Millisecond)

	id, err := sm.StartSession(portForwardOpts("probe-stall", 12346))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	sm.markRunningForTest(id)

	// Drain the OnChange channel so we can wait for a fresh "updated" event.
	drainEvents(sm.OnChange)

	// Flip the prober to fail.
	fail.Store(true)

	if err := waitForState(sm, id, StateStalled, time.Second); err != nil {
		t.Fatalf("waiting for StateStalled: %v", err)
	}
}

func TestProbeRecoveryTransitionsBackToRunning(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	var fail atomic.Bool
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		return !fail.Load()
	}, 30*time.Millisecond, 50*time.Millisecond)

	id, err := sm.StartSession(portForwardOpts("probe-recover", 12347))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	sm.markRunningForTest(id)

	fail.Store(true)
	if err := waitForState(sm, id, StateStalled, time.Second); err != nil {
		t.Fatalf("waiting for StateStalled: %v", err)
	}

	fail.Store(false)
	if err := waitForState(sm, id, StateRunning, time.Second); err != nil {
		t.Fatalf("waiting for StateRunning recovery: %v", err)
	}
}

func TestProbeNotStartedForShellSessions(t *testing.T) {
	sm := New(sleepBuilder(), nil)
	defer sm.Close()

	var probes atomic.Int32
	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		probes.Add(1)
		return true
	}, 30*time.Millisecond, 50*time.Millisecond)

	id, err := sm.StartSession(testOpts("shell-no-probe"))
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	sm.markRunningForTest(id)

	time.Sleep(150 * time.Millisecond)

	if probes.Load() != 0 {
		t.Errorf("shell session probed %d times; expected 0", probes.Load())
	}
}

func TestProbeStopsOnClose(t *testing.T) {
	sm := New(sleepBuilder(), nil)

	sm.SetProbe(func(ctx context.Context, s *Session) bool {
		return true
	}, 30*time.Millisecond, 50*time.Millisecond)

	if _, err := sm.StartSession(portForwardOpts("close", 12348)); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	done := make(chan struct{})
	go func() {
		sm.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return — probe goroutine likely leaked")
	}
}

// --- test helpers used above ---

func drainEvents(ch <-chan SessionEvent) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func waitForState(sm *SessionManager, id string, want SessionState, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s, ok := sm.GetSession(id); ok && s.State == want {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if s, ok := sm.GetSession(id); ok {
		return fmt.Errorf("state = %d, want %d (after %v)", s.State, want, timeout)
	}
	return fmt.Errorf("session %s not found", id)
}
