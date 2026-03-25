//go:build windows

package cmd

import (
	"os"
	"os/signal"
)

// notifyShutdownSignals registers for os.Interrupt on Windows.
func notifyShutdownSignals(ch chan<- os.Signal) {
	signal.Notify(ch, os.Interrupt)
}

// signalTerminate sends os.Kill to the given process on Windows,
// as SIGTERM is not supported.
func signalTerminate(proc *os.Process) {
	proc.Kill()
}
