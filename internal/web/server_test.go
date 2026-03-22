package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	sm := session.New(nil)
	cfg := &config.Config{
		DashboardPort: 8443,
		LogLevel:      "warn",
		PIDDir:        t.TempDir(),
	}
	s := NewServer(sm, cfg, time.Now())
	t.Cleanup(func() { s.Stop() })
	return s
}

func TestDashboardHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET / status = %d, want %d", w.Code, http.StatusOK)
	}
	body := w.Body.String()
	if len(body) == 0 {
		t.Error("GET / returned empty body")
	}
	// The dashboard HTML should mention "gossm" somewhere.
	if !containsString(body, "gossm") {
		t.Error("GET / body does not contain \"gossm\"")
	}
}

func TestSessionsListHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /api/sessions status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestStopSessionHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/sessions/nonexistent-id", nil)
	w := httptest.NewRecorder()

	// Should not panic; will return an error status since the session does not exist.
	srv.Handler().ServeHTTP(w, req)

	// We expect a 500 because StopSession will return "session not found".
	if w.Code != http.StatusInternalServerError {
		t.Errorf("DELETE /api/sessions/nonexistent status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}

func TestStaticHandler(t *testing.T) {
	srv := testServer(t)
	req := httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("GET /static/htmx.min.js status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.Len() == 0 {
		t.Error("GET /static/htmx.min.js returned empty body")
	}
}

// containsString is a small helper to check substring presence.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
