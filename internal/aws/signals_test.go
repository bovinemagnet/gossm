//go:build !windows

package aws

import (
	"syscall"
	"testing"
	"time"
)

// TestTerminalSignalsIncludesInterrupt verifies the signal list we divert
// away from gossm contains SIGINT, the Ctrl-C signal that must reach the
// remote shell rather than killing gossm.
func TestTerminalSignalsIncludesInterrupt(t *testing.T) {
	sigs := terminalSignals()
	for _, s := range sigs {
		if s == syscall.SIGINT {
			return
		}
	}
	t.Fatalf("terminalSignals() should include SIGINT, got %v", sigs)
}

// TestIgnoreSignalsSwallowsSIGINT delivers SIGINT to the test process while
// signals are diverted. With the default disposition this would terminate
// the test binary; if execution reaches the end of the test, the signal was
// successfully swallowed.
func TestIgnoreSignalsSwallowsSIGINT(t *testing.T) {
	restore := ignoreSignals(terminalSignals())
	defer restore()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("kill: %v", err)
	}

	// Give the signal time to be delivered and swallowed.
	time.Sleep(50 * time.Millisecond)
}

// TestIgnoreSignalsEmptyList ensures the helper is a no-op when given no
// signals, returning a restore function that is safe to call.
func TestIgnoreSignalsEmptyList(t *testing.T) {
	restore := ignoreSignals(nil)
	restore()
}
