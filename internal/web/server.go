// Package web provides the HTTP server and HTMX-powered dashboard for gossm.
package web

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	awsutil "github.com/bovinemagnet/gossm/internal/aws"
	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
	"github.com/gorilla/websocket"
)

// EC2ClientFactory creates an EC2DescribeInstancesAPI for the given AWS profile.
// Returning nil from NewServer makes the instance picker gracefully unavailable.
type EC2ClientFactory func(ctx context.Context, profile string) (awsutil.EC2DescribeInstancesAPI, error)

//go:embed templates/*
var templateFS embed.FS

// Server is the HTTP server that serves the gossm dashboard.
type Server struct {
	sm         *session.SessionManager
	cfg        *config.Config
	ec2Factory EC2ClientFactory
	sse        *SSEBroker
	mux        *http.ServeMux
	tmpl       *template.Template
	startedAt  time.Time

	// Terminal support.
	terminalToken  string
	termCmdBuilder terminalCmdBuilder
	upgrader       websocket.Upgrader
}

// NewServer creates a new web server, parses templates, sets up routes,
// and starts the SSE broker.
func NewServer(sm *session.SessionManager, cfg *config.Config, startedAt time.Time, ec2Factory EC2ClientFactory) *Server {
	funcMap := template.FuncMap{
		"sessionStateClass":   sessionStateClass,
		"sessionStateName":    sessionStateName,
		"sessionTypeName":     sessionTypeName,
		"sessionTypeClass":    sessionTypeClass,
		"portDisplay":         portDisplay,
		"sessionProbeDisplay": sessionProbeDisplay,
		"uptimeSince":         uptimeSince,
		"isActiveState":       isActiveState,
		"dict":                templateDict,
		"effectiveProbeSecs": func(s session.Session) int {
			d := s.ProbeInterval
			if d <= 0 {
				d = sm.DefaultProbeInterval()
			}
			if d <= 0 {
				return 30
			}
			return int(d / time.Second)
		},
		"reconnectMaxAttempts": func() int { return cfg.ReconnectMaxAttempts },
	}

	tmpl := template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/layout.html",
			"templates/dashboard.html",
			"templates/partials/session_list.html",
			"templates/partials/session_row.html",
			"templates/partials/session_card.html",
			"templates/partials/stats.html",
			"templates/partials/instance_picker.html",
			"templates/partials/preset_card.html",
			"templates/partials/manual_launch.html",
			"templates/partials/terminal.html",
		),
	)

	s := &Server{
		sm:             sm,
		cfg:            cfg,
		ec2Factory:     ec2Factory,
		mux:            http.NewServeMux(),
		tmpl:           tmpl,
		startedAt:      startedAt,
		sse:            NewSSEBroker(sm.OnChange),
		terminalToken:  generateTerminalToken(),
		termCmdBuilder: defaultTerminalCmdBuilder,
	}
	s.upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return originAllowed(r.Header.Get("Origin"), r.Host)
		},
	}

	s.setupRoutes()
	return s
}

// Handler returns the http.Handler for this server.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// setupRoutes registers all routes on the mux using Go 1.22+ patterns.
func (s *Server) setupRoutes() {
	// Static files served from embedded FS. The embed root is "templates/",
	// so we create a sub-filesystem rooted at "templates/static".
	staticFiles, _ := fs.Sub(templateFS, "templates/static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/",
		http.FileServerFS(staticFiles)),
	)

	// Pages.
	s.mux.HandleFunc("GET /", s.handleDashboard)

	// API endpoints.
	s.mux.HandleFunc("GET /api/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/sessions", s.handleSessionsList)
	s.mux.HandleFunc("POST /api/sessions", s.handleStartSession)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.handleStopSession)
	s.mux.HandleFunc("POST /api/sessions/{id}/reconnect", s.handleReconnectSession)
	s.mux.HandleFunc("POST /api/sessions/{id}/probe-interval", s.handleSetProbeInterval)
	s.mux.HandleFunc("GET /api/instances", s.handleInstances)
	s.mux.HandleFunc("GET /api/profiles", s.handleProfiles)

	// Terminal.
	s.mux.HandleFunc("GET /api/terminal", s.handleTerminalPanel)
	s.mux.HandleFunc("GET /ws/terminal", s.handleTerminalWS)

	// Presets.
	s.mux.HandleFunc("GET /api/presets/{index}", s.handlePreset)
	s.mux.HandleFunc("POST /api/presets/{index}/start", s.handleStartPreset)

	// SSE.
	s.mux.HandleFunc("GET /events", s.handleEvents)
}

// Stop shuts down the SSE broker.
func (s *Server) Stop() {
	s.sse.Stop()
}
