package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

// IPCRequest is the JSON envelope sent by clients over the Unix socket.
type IPCRequest struct {
	Action string          `json:"action"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// IPCResponse is the JSON envelope returned to clients.
type IPCResponse struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// IPCServer listens on a Unix socket and dispatches JSON-encoded requests.
type IPCServer struct {
	listener net.Listener
	sm       *session.SessionManager
	cfg      *config.Config
	daemon   *Daemon
	done     chan struct{}
}

// registerRequest is the JSON payload for the "register" action.
type registerRequest struct {
	InstanceID   string `json:"instance_id"`
	InstanceName string `json:"instance_name"`
	Profile      string `json:"profile"`
	PID          int    `json:"pid"`
	Type         string `json:"type"`
}

// NewIPCServer creates a Unix socket listener at cfg.SocketPath(), removing
// any stale socket file first.
func NewIPCServer(cfg *config.Config, sm *session.SessionManager, d *Daemon) (*IPCServer, error) {
	sockPath := cfg.SocketPath()
	// Remove stale socket file if it exists.
	_ = os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", sockPath, err)
	}

	return &IPCServer{
		listener: listener,
		sm:       sm,
		cfg:      cfg,
		daemon:   d,
		done:     make(chan struct{}),
	}, nil
}

// Serve starts the accept loop in a goroutine.
func (s *IPCServer) Serve() {
	go func() {
		for {
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.done:
					return
				default:
					continue
				}
			}
			go s.handleConnection(conn)
		}
	}()
}

// handleConnection reads one JSON request, dispatches it, writes one JSON
// response, and closes the connection.
func (s *IPCServer) handleConnection(conn net.Conn) {
	defer conn.Close()

	var req IPCRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		resp := IPCResponse{OK: false, Error: "invalid request: " + err.Error()}
		_ = json.NewEncoder(conn).Encode(resp)
		return
	}

	resp := s.handleAction(req)
	_ = json.NewEncoder(conn).Encode(resp)
}

// handleAction dispatches a request to the appropriate handler.
func (s *IPCServer) handleAction(req IPCRequest) IPCResponse {
	switch req.Action {
	case "status":
		return s.handleStatus()
	case "list":
		return s.handleList()
	case "register":
		return s.handleRegister(req)
	case "shutdown":
		return s.handleShutdown()
	default:
		return IPCResponse{OK: false, Error: fmt.Sprintf("unknown action: %s", req.Action)}
	}
}

func (s *IPCServer) handleStatus() IPCResponse {
	status := StatusResponse{
		SessionCount: s.sm.SessionCount(),
		Uptime:       s.daemon.Uptime().String(),
		Port:         s.cfg.DashboardPort,
	}
	data, err := json.Marshal(status)
	if err != nil {
		return IPCResponse{OK: false, Error: err.Error()}
	}
	return IPCResponse{OK: true, Data: data}
}

func (s *IPCServer) handleList() IPCResponse {
	sessions := s.sm.ListSessions()
	data, err := json.Marshal(sessions)
	if err != nil {
		return IPCResponse{OK: false, Error: err.Error()}
	}
	return IPCResponse{OK: true, Data: data}
}

func (s *IPCServer) handleRegister(req IPCRequest) IPCResponse {
	var r registerRequest
	if err := json.Unmarshal(req.Data, &r); err != nil {
		return IPCResponse{OK: false, Error: "invalid register data: " + err.Error()}
	}

	sessionType := session.TypeShell
	if r.Type == "port-forward" {
		sessionType = session.TypePortForward
	}

	opts := session.SessionOpts{
		InstanceID:   r.InstanceID,
		InstanceName: r.InstanceName,
		Profile:      r.Profile,
		Type:         sessionType,
	}

	sessionID := s.sm.RegisterExternal(opts, r.PID)

	data, err := json.Marshal(map[string]string{"session_id": sessionID})
	if err != nil {
		return IPCResponse{OK: false, Error: err.Error()}
	}
	return IPCResponse{OK: true, Data: data}
}

func (s *IPCServer) handleShutdown() IPCResponse {
	go func() {
		_ = s.daemon.Stop()
	}()
	return IPCResponse{OK: true}
}

// Stop closes the listener and signals the accept loop to exit.
func (s *IPCServer) Stop() {
	select {
	case <-s.done:
		// Already closed.
	default:
		close(s.done)
	}
	_ = s.listener.Close()
}
