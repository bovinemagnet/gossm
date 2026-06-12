package session

import (
	"context"
	"os/exec"
	"time"
)

// SessionType identifies the kind of SSM session.
type SessionType int

const (
	TypeShell       SessionType = iota
	TypePortForward
)

// SessionState tracks the lifecycle of a session.
type SessionState int

const (
	StateStarting SessionState = iota
	StateRunning
	StateStopping
	StateStalled
	StateStopped
	StateErrored
	StateReconnecting
)

// Session represents a single SSM session, including the underlying
// subprocess that drives it.
type Session struct {
	ID                  string
	InstanceID          string
	InstanceName        string
	Profile             string
	Type                SessionType
	State               SessionState
	LocalPort           int    // port forwarding only
	RemotePort          int    // port forwarding only
	RemoteHost          string // port forwarding only
	StartedAt           time.Time
	LastError           string
	PID                 int           // PID of the aws ssm subprocess
	LastProbeAt         time.Time     // last time the tunnel probe ran (port-forward only)
	LastProbeOK         bool          // outcome of the last probe (port-forward only)
	ProbeInterval       time.Duration // per-session probe interval; zero falls back to manager default
	Reconnectable       bool          // true if the manager owns the subprocess and may respawn it
	ReconnectAttempts   int           // attempts in the current failure cycle; resets on probe success
	LastReconnectAt     time.Time     // timestamp of the most recent reconnect attempt
	consecutiveFailures int  // consecutive failed probes since the last success
	reconnectInFlight   bool // guarded by SessionManager.mu; true while a reconnect cycle is running
	probeInFlight       bool // guarded by SessionManager.mu; true while a monitorProbe goroutine is running
	cmd                 *exec.Cmd
	cancel              context.CancelFunc
	waitDone            chan struct{} // closed when cmd.Wait() completes in monitor
}

// SessionEvent is emitted whenever the session registry changes.
type SessionEvent struct {
	Type      string // "added", "removed", "updated"
	SessionID string
	Timestamp time.Time
}

// SessionOpts carries the parameters needed to start a new session.
type SessionOpts struct {
	InstanceID   string
	InstanceName string
	Profile      string
	Type         SessionType
	LocalPort    int
	RemotePort   int
	RemoteHost   string
}
