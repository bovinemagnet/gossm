//go:build !windows

package cmd

import (
	"os"
	"os/signal"
	"syscall"
)

// notifyShutdownSignals registers for SIGTERM and SIGINT.
func notifyShutdownSignals(ch chan<- os.Signal) {
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
}

// signalTerminate sends SIGTERM to the given process.
func signalTerminate(proc *os.Process) {
	proc.Signal(syscall.SIGTERM)
}
