//go:build !windows

package daemon

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/bovinemagnet/gossm/internal/config"
)

// isProcessAlive checks whether a process with the given PID exists by
// sending signal 0.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// ForkDaemon re-executes the current binary with "daemon start --foreground"
// detached from the current terminal.
func ForkDaemon(executable string, cfg *config.Config) error {
	cmd := exec.Command(executable, "daemon", "start", "--foreground")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// Start the process in a new session, detached from the terminal.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	return cmd.Start()
}
