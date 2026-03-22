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
	StateStopped
	StateErrored
)

// Session represents a single SSM session, including the underlying
// subprocess that drives it.
type Session struct {
	ID           string
	InstanceID   string
	InstanceName string
	Profile      string
	Type         SessionType
	State        SessionState
	LocalPort    int    // port forwarding only
	RemotePort   int    // port forwarding only
	RemoteHost   string // port forwarding only
	StartedAt    time.Time
	LastError    string
	PID          int // PID of the aws ssm subprocess
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	waitDone     chan struct{} // closed when cmd.Wait() completes in monitor
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
