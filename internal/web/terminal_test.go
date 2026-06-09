package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidToken(t *testing.T) {
	cases := []struct {
		name       string
		got, want  string
		wantResult bool
	}{
		{"match", "abc123", "abc123", true},
		{"mismatch", "abc123", "xyz789", false},
		{"empty got", "", "abc123", false},
		{"empty want", "abc123", "", false},
		{"both empty", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := validToken(c.got, c.want); got != c.wantResult {
				t.Errorf("validToken(%q,%q) = %v, want %v", c.got, c.want, got, c.wantResult)
			}
		})
	}
}

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name         string
		origin, host string
		want         bool
	}{
		{"same host", "http://127.0.0.1:8877", "127.0.0.1:8877", true},
		{"https same host", "https://localhost:8877", "localhost:8877", true},
		{"different host", "http://evil.example", "127.0.0.1:8877", false},
		{"different port", "http://127.0.0.1:9999", "127.0.0.1:8877", false},
		{"empty origin", "", "127.0.0.1:8877", false},
		{"garbage origin", "://nonsense", "127.0.0.1:8877", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := originAllowed(c.origin, c.host); got != c.want {
				t.Errorf("originAllowed(%q,%q) = %v, want %v", c.origin, c.host, got, c.want)
			}
		})
	}
}

func TestGenerateTerminalToken(t *testing.T) {
	a := generateTerminalToken()
	b := generateTerminalToken()
	if a == "" || b == "" {
		t.Fatal("token should not be empty")
	}
	if len(a) != 64 {
		t.Errorf("token length = %d, want 64 hex chars", len(a))
	}
	if a == b {
		t.Error("consecutive tokens should differ")
	}
}

func TestHandleTerminalPanel(t *testing.T) {
	s := testServer(t)
	s.terminalToken = "tok-123"

	req := httptest.NewRequest("GET", "/api/terminal?instance_id=i-abc&instance_name=web&profile=prod", nil)
	rec := httptest.NewRecorder()
	s.handleTerminalPanel(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"i-abc", "tok-123", "prod", "/ws/terminal", "new Terminal("} {
		if !strings.Contains(body, want) {
			t.Errorf("panel body missing %q", want)
		}
	}
}

func TestHandleTerminalPanel_MissingInstance(t *testing.T) {
	s := testServer(t)
	req := httptest.NewRequest("GET", "/api/terminal", nil)
	rec := httptest.NewRecorder()
	s.handleTerminalPanel(rec, req)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
