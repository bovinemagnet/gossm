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
	// DETACHED_PROCESS starts the child with no console at all, matching
	// the Unix Setsid behaviour. (CREATE_NEW_CONSOLE would pop up a
	// visible console window for the background daemon.)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008, // DETACHED_PROCESS
	}
	return cmd.Start()
}
