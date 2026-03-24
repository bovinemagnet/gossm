package daemon

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/bovinemagnet/gossm/internal/config"
)

// StatusResponse holds the data returned by the "status" IPC action.
type StatusResponse struct {
	SessionCount int    `json:"session_count"`
	Uptime       string `json:"uptime"`
	Port         int    `json:"port"`
}

// IPCConnect connects to the daemon's Unix socket and returns the connection.
// The caller is responsible for closing the connection.
func IPCConnect(cfg *config.Config) (net.Conn, error) {
	conn, err := net.Dial("unix", cfg.SocketPath())
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	return conn, nil
}

// IPCSend connects to the daemon, sends a request, reads the response, and
// closes the connection.
func IPCSend(cfg *config.Config, req IPCRequest) (*IPCResponse, error) {
	conn, err := IPCConnect(cfg)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	var resp IPCResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &resp, nil
}

// RegisterOpts carries the parameters for daemon session registration.
type RegisterOpts struct {
	InstanceID   string
	InstanceName string
	Profile      string
	PID          int
	SessionType  string
	LocalPort    int
	RemotePort   int
	RemoteHost   string
}

// RegisterWithDaemon sends a "register" action to the running daemon,
// informing it of an externally started session.
func RegisterWithDaemon(cfg *config.Config, opts RegisterOpts) error {
	data, err := json.Marshal(map[string]any{
		"instance_id":   opts.InstanceID,
		"instance_name": opts.InstanceName,
		"profile":       opts.Profile,
		"pid":           opts.PID,
		"type":          opts.SessionType,
		"local_port":    opts.LocalPort,
		"remote_port":   opts.RemotePort,
		"remote_host":   opts.RemoteHost,
	})
	if err != nil {
		return fmt.Errorf("marshal register data: %w", err)
	}

	req := IPCRequest{
		Action: "register",
		Data:   data,
	}

	resp, err := IPCSend(cfg, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("register failed: %s", resp.Error)
	}
	return nil
}

// DaemonStatus queries the running daemon for its current status.
func DaemonStatus(cfg *config.Config) (*StatusResponse, error) {
	req := IPCRequest{Action: "status"}

	resp, err := IPCSend(cfg, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("status failed: %s", resp.Error)
	}

	var status StatusResponse
	if err := json.Unmarshal(resp.Data, &status); err != nil {
		return nil, fmt.Errorf("unmarshal status: %w", err)
	}
	return &status, nil
}
