//go:build windows

package daemon

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"github.com/bovinemagnet/gossm/internal/config"
)

// isProcessAlive checks whether a process with the given PID exists.
// On Windows, we shell out to tasklist to query the process.
func isProcessAlive(pid int) bool {
	cmd := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	// tasklist prints "INFO: No tasks are running..." when no match is found.
	return !strings.Contains(string(output), "No tasks")
}

// ForkDaemon re-executes the current binary with "daemon start --foreground"
// detached from the current terminal.
func ForkDaemon(executable string, cfg *config.Config) error {
	cmd := exec.Command(executable, "daemon", "start", "--foreground")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	// On Windows, CREATE_NEW_CONSOLE detaches from the parent console.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000010, // CREATE_NEW_CONSOLE
	}
	return cmd.Start()
}
