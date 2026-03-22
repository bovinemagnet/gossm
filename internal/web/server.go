// Package web provides the HTTP server and HTMX-powered dashboard for gossm.
package web

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"time"

	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

//go:embed templates/*
var templateFS embed.FS

// Server is the HTTP server that serves the gossm dashboard.
type Server struct {
	sm        *session.SessionManager
	cfg       *config.Config
	sse       *SSEBroker
	mux       *http.ServeMux
	tmpl      *template.Template
	startedAt time.Time
}

// NewServer creates a new web server, parses templates, sets up routes,
// and starts the SSE broker.
func NewServer(sm *session.SessionManager, cfg *config.Config, startedAt time.Time) *Server {
	funcMap := template.FuncMap{
		"sessionStateClass": sessionStateClass,
		"sessionStateName":  sessionStateName,
		"sessionTypeName":   sessionTypeName,
		"portDisplay":       portDisplay,
		"uptimeSince":       uptimeSince,
		"eq":                func(a, b int) bool { return a == b },
	}

	tmpl := template.Must(
		template.New("").Funcs(funcMap).ParseFS(templateFS,
			"templates/layout.html",
			"templates/dashboard.html",
			"templates/partials/session_list.html",
			"templates/partials/session_row.html",
			"templates/partials/stats.html",
		),
	)

	s := &Server{
		sm:        sm,
		cfg:       cfg,
		mux:       http.NewServeMux(),
		tmpl:      tmpl,
		startedAt: startedAt,
		sse:       NewSSEBroker(sm.OnChange),
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
	s.mux.HandleFunc("GET /api/sessions", s.handleSessionsList)
	s.mux.HandleFunc("POST /api/sessions", s.handleStartSession)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", s.handleStopSession)
	s.mux.HandleFunc("GET /api/instances", s.handleInstances)

	// SSE.
	s.mux.HandleFunc("GET /events", s.handleEvents)
}

// Stop shuts down the SSE broker.
func (s *Server) Stop() {
	s.sse.Stop()
}
