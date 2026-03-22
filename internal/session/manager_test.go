package session

import (
	"context"
	"os/exec"
	"sync"
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

func TestNewSessionManager(t *testing.T) {
	sm := New(sleepBuilder())
	if sm == nil {
		t.Fatal("New returned nil")
	}
	if len(sm.ListSessions()) != 0 {
		t.Errorf("new manager should have 0 sessions, got %d", len(sm.ListSessions()))
	}
}

func TestStartSession(t *testing.T) {
	sm := New(sleepBuilder())
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
	sm := New(sleepBuilder())
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
	sm := New(sleepBuilder())
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
	sm := New(sleepBuilder())
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
	sm := New(sleepBuilder())

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
	sm := New(sleepBuilder())
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
	sm := New(sleepBuilder())

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
	sm := New(sleepBuilder())

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
