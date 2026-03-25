package web

import (
	"strings"
	"testing"
	"time"

	"github.com/bovinemagnet/gossm/internal/session"
)

// --- sessionStateClass tests ---

func TestSessionStateClass(t *testing.T) {
	tests := []struct {
		state session.SessionState
		want  string
	}{
		{session.StateRunning, "bg-green-500"},
		{session.StateStarting, "bg-yellow-500"},
		{session.StateStopping, "bg-yellow-500"},
		{session.StateErrored, "bg-red-500"},
		{session.StateStopped, "bg-slate-500"},
		{session.SessionState(99), "bg-slate-500"}, // unknown
	}
	for _, tc := range tests {
		got := sessionStateClass(tc.state)
		if got != tc.want {
			t.Errorf("sessionStateClass(%v) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// --- sessionStateName tests ---

func TestSessionStateName(t *testing.T) {
	tests := []struct {
		state session.SessionState
		want  string
	}{
		{session.StateRunning, "Running"},
		{session.StateStarting, "Starting"},
		{session.StateStopping, "Stopping"},
		{session.StateErrored, "Errored"},
		{session.StateStopped, "Stopped"},
		{session.SessionState(99), "Unknown"},
	}
	for _, tc := range tests {
		got := sessionStateName(tc.state)
		if got != tc.want {
			t.Errorf("sessionStateName(%v) = %q, want %q", tc.state, got, tc.want)
		}
	}
}

// --- sessionTypeName tests ---

func TestSessionTypeName(t *testing.T) {
	tests := []struct {
		typ  session.SessionType
		want string
	}{
		{session.TypeShell, "Shell"},
		{session.TypePortForward, "Port Forward"},
		{session.SessionType(99), "Unknown"},
	}
	for _, tc := range tests {
		got := sessionTypeName(tc.typ)
		if got != tc.want {
			t.Errorf("sessionTypeName(%v) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}

// --- portDisplay tests ---

func TestPortDisplay_Shell(t *testing.T) {
	s := session.Session{Type: session.TypeShell}
	got := portDisplay(s)
	if got != "-" {
		t.Errorf("portDisplay(shell) = %q, want %q", got, "-")
	}
}

func TestPortDisplay_PortForwardNoHost(t *testing.T) {
	s := session.Session{
		Type:       session.TypePortForward,
		LocalPort:  5432,
		RemotePort: 5432,
	}
	got := portDisplay(s)
	want := "5432 → 5432"
	if got != want {
		t.Errorf("portDisplay = %q, want %q", got, want)
	}
}

func TestPortDisplay_PortForwardWithHost(t *testing.T) {
	s := session.Session{
		Type:       session.TypePortForward,
		LocalPort:  3306,
		RemotePort: 3306,
		RemoteHost: "db.internal",
	}
	got := portDisplay(s)
	want := "3306 → db.internal:3306"
	if got != want {
		t.Errorf("portDisplay = %q, want %q", got, want)
	}
}

// --- uptimeSince tests ---

func TestUptimeSince_Seconds(t *testing.T) {
	got := uptimeSince(time.Now().Add(-30 * time.Second))
	if !strings.HasSuffix(got, "s") {
		t.Errorf("uptimeSince(30s) = %q, expected seconds format", got)
	}
	if strings.Contains(got, "m") || strings.Contains(got, "h") || strings.Contains(got, "d") {
		t.Errorf("uptimeSince(30s) = %q, should not contain m/h/d", got)
	}
}

func TestUptimeSince_Minutes(t *testing.T) {
	got := uptimeSince(time.Now().Add(-5*time.Minute - 30*time.Second))
	if !strings.Contains(got, "m") {
		t.Errorf("uptimeSince(5m30s) = %q, expected minutes format", got)
	}
	if !strings.Contains(got, "s") {
		t.Errorf("uptimeSince(5m30s) = %q, expected seconds too", got)
	}
}

func TestUptimeSince_Hours(t *testing.T) {
	got := uptimeSince(time.Now().Add(-3*time.Hour - 15*time.Minute))
	if !strings.Contains(got, "h") {
		t.Errorf("uptimeSince(3h15m) = %q, expected hours format", got)
	}
	if !strings.Contains(got, "m") {
		t.Errorf("uptimeSince(3h15m) = %q, expected minutes too", got)
	}
}

func TestUptimeSince_Days(t *testing.T) {
	got := uptimeSince(time.Now().Add(-50 * time.Hour))
	if !strings.Contains(got, "d") {
		t.Errorf("uptimeSince(50h) = %q, expected days format", got)
	}
	if !strings.Contains(got, "h") {
		t.Errorf("uptimeSince(50h) = %q, expected hours too", got)
	}
}

// --- renderSparkSVG tests ---

func TestRenderSparkSVG_Empty(t *testing.T) {
	got := renderSparkSVG(nil)
	if got != "" {
		t.Errorf("renderSparkSVG(nil) = %q, want empty", got)
	}
}

func TestRenderSparkSVG_SinglePoint(t *testing.T) {
	got := renderSparkSVG([]int{5})
	if !strings.Contains(got, "<svg") {
		t.Error("expected SVG output for single point")
	}
	if !strings.Contains(got, "polyline") {
		t.Error("expected polyline element")
	}
}

func TestRenderSparkSVG_MultiplePoints(t *testing.T) {
	got := renderSparkSVG([]int{1, 3, 2, 5, 4})
	if !strings.Contains(got, "<svg") {
		t.Error("expected SVG output")
	}
	// Should have multiple coordinate pairs separated by spaces.
	if !strings.Contains(got, " ") {
		t.Error("expected multiple points in polyline")
	}
}

func TestRenderSparkSVG_AllZeros(t *testing.T) {
	got := renderSparkSVG([]int{0, 0, 0})
	if !strings.Contains(got, "<svg") {
		t.Error("expected SVG output for all-zero data")
	}
}
