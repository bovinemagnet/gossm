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
		{session.StateRunning, "running"},
		{session.StateStarting, "starting"},
		{session.StateStopping, "stopping"},
		{session.StateReconnecting, "reconnecting"},
		{session.StateErrored, "errored"},
		{session.StateStopped, "stopped"},
		{session.SessionState(99), "unknown"},
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

// --- renderHistorySVG tests ---

func TestRenderHistorySVG_Empty(t *testing.T) {
	if got := renderHistorySVG(nil); got != "" {
		t.Errorf("renderHistorySVG(nil) = %q, want empty", got)
	}
}

func TestRenderHistorySVG_SinglePoint(t *testing.T) {
	got := renderHistorySVG([]session.HistoryPoint{{Shells: 1, Tunnels: 2}})
	if !strings.Contains(got, "<svg") {
		t.Error("expected SVG output for single point")
	}
	if strings.Contains(got, "NaN") {
		t.Error("single point produced NaN coordinates")
	}
}

func TestRenderHistorySVG_StacksShellsOnTunnels(t *testing.T) {
	got := renderHistorySVG([]session.HistoryPoint{
		{Shells: 1, Tunnels: 0},
		{Shells: 2, Tunnels: 3},
		{Shells: 0, Tunnels: 1},
	})
	if !strings.Contains(got, `class="history-tunnels"`) || !strings.Contains(got, `class="history-shells"`) {
		t.Errorf("expected a tunnel layer and a shell layer; got %s", got)
	}
	// The scale covers the stacked total (2+3), not either series alone.
	if !strings.Contains(got, ">5</text>") {
		t.Errorf("expected y-axis max label 5; got %s", got)
	}
	if !strings.Contains(got, "last 60 minutes") {
		t.Error("expected aria label to describe a 60-minute window")
	}
}

func TestRenderHistorySVG_AllZeros(t *testing.T) {
	got := renderHistorySVG(make([]session.HistoryPoint, 3))
	if !strings.Contains(got, "<svg") {
		t.Error("expected SVG output for all-zero data")
	}
}

// --- renderTrafficSVG tests ---

func TestRenderTrafficSVG_NoTraffic(t *testing.T) {
	got := renderTrafficSVG(make([]session.HistoryPoint, 3))
	if got != "" {
		t.Errorf("renderTrafficSVG(no traffic) = %q, want empty", got)
	}
}

func TestRenderTrafficSVG_InAndOut(t *testing.T) {
	got := renderTrafficSVG([]session.HistoryPoint{
		{BytesIn: 0, BytesOut: 0},
		{BytesIn: 2048, BytesOut: 512},
		{BytesIn: 1024, BytesOut: 0},
	})
	if !strings.Contains(got, `class="traffic-in"`) || !strings.Contains(got, `class="traffic-out"`) {
		t.Errorf("expected received and sent series; got %s", got)
	}
	if !strings.Contains(got, ">2.0 KB</text>") {
		t.Errorf("expected y-axis max label 2.0 KB; got %s", got)
	}
}

// --- formatBytes tests ---

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
		{3 * 1024 * 1024 * 1024, "3.0 GB"},
	}
	for _, tt := range tests {
		if got := formatBytes(tt.in); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSessionStateClassStalled(t *testing.T) {
	got := sessionStateClass(session.StateStalled)
	if got != "stalled" {
		t.Errorf("sessionStateClass(StateStalled) = %q, want %q", got, "stalled")
	}
}

func TestSessionTypeClass(t *testing.T) {
	tests := []struct {
		typ  session.SessionType
		want string
	}{
		{session.TypeShell, "shell"},
		{session.TypePortForward, "port-forward"},
		{session.SessionType(99), "unknown"},
	}
	for _, tc := range tests {
		got := sessionTypeClass(tc.typ)
		if got != tc.want {
			t.Errorf("sessionTypeClass(%v) = %q, want %q", tc.typ, got, tc.want)
		}
	}
}

// --- buildDashboardStats tests ---

func TestBuildDashboardStats_Empty(t *testing.T) {
	got := buildDashboardStats(nil)
	if got.Total != 0 || got.Active != 0 || got.Running != 0 ||
		got.Shells != 0 || got.PortForwards != 0 {
		t.Errorf("expected zero stats, got %+v", got)
	}
}

func TestBuildDashboardStats_CountsByStateAndType(t *testing.T) {
	sessions := []session.Session{
		{State: session.StateRunning, Type: session.TypeShell},
		{State: session.StateRunning, Type: session.TypePortForward},
		{State: session.StateStarting, Type: session.TypeShell},
		{State: session.StateStalled, Type: session.TypePortForward},
		{State: session.StateReconnecting, Type: session.TypePortForward},
		{State: session.StateStopping, Type: session.TypeShell},
		{State: session.StateErrored, Type: session.TypePortForward},
		{State: session.StateStopped, Type: session.TypeShell},
	}

	got := buildDashboardStats(sessions)

	if got.Total != 8 {
		t.Errorf("Total = %d, want 8", got.Total)
	}
	if got.Running != 2 {
		t.Errorf("Running = %d, want 2", got.Running)
	}
	if got.Starting != 1 {
		t.Errorf("Starting = %d, want 1", got.Starting)
	}
	if got.Stopping != 1 {
		t.Errorf("Stopping = %d, want 1", got.Stopping)
	}
	if got.Stalled != 1 {
		t.Errorf("Stalled = %d, want 1", got.Stalled)
	}
	if got.Reconnecting != 1 {
		t.Errorf("Reconnecting = %d, want 1", got.Reconnecting)
	}
	if got.Errored != 1 {
		t.Errorf("Errored = %d, want 1", got.Errored)
	}
	if got.Stopped != 1 {
		t.Errorf("Stopped = %d, want 1", got.Stopped)
	}
	// Active = Running + Starting + Stopping + Stalled + Reconnecting = 6
	if got.Active != 6 {
		t.Errorf("Active = %d, want 6", got.Active)
	}
	if got.Shells != 4 {
		t.Errorf("Shells = %d, want 4", got.Shells)
	}
	if got.PortForwards != 4 {
		t.Errorf("PortForwards = %d, want 4", got.PortForwards)
	}
}

// --- templateDict tests ---

func TestTemplateDict_Pairs(t *testing.T) {
	got := templateDict("Index", 3, "Preset", "alpha")
	if got["Index"] != 3 || got["Preset"] != "alpha" {
		t.Errorf("dict = %+v, want Index=3 Preset=alpha", got)
	}
}

func TestTemplateDict_OddArgsReturnsNil(t *testing.T) {
	if got := templateDict("a", 1, "b"); got != nil {
		t.Errorf("dict with odd args = %+v, want nil", got)
	}
}

func TestTemplateDict_NonStringKeyReturnsNil(t *testing.T) {
	if got := templateDict(1, "x"); got != nil {
		t.Errorf("dict with non-string key = %+v, want nil", got)
	}
}

func TestSessionStateNameStalled(t *testing.T) {
	got := sessionStateName(session.StateStalled)
	if got != "Stalled" {
		t.Errorf("sessionStateName(StateStalled) = %q, want %q", got, "Stalled")
	}
}

func TestSessionProbeDisplayShellShowsDash(t *testing.T) {
	s := session.Session{Type: session.TypeShell}
	if got := sessionProbeDisplay(s); got != "—" {
		t.Errorf("sessionProbeDisplay(shell) = %q, want %q", got, "—")
	}
}

func TestSessionProbeDisplayUnprobed(t *testing.T) {
	s := session.Session{Type: session.TypePortForward}
	if got := sessionProbeDisplay(s); got != "pending" {
		t.Errorf("sessionProbeDisplay(unprobed) = %q, want %q", got, "pending")
	}
}

func TestSessionProbeDisplayOK(t *testing.T) {
	s := session.Session{
		Type:        session.TypePortForward,
		LastProbeAt: time.Now().Add(-12 * time.Second),
		LastProbeOK: true,
	}
	got := sessionProbeDisplay(s)
	if !strings.HasPrefix(got, "ok ") {
		t.Errorf("sessionProbeDisplay(ok) = %q, want prefix %q", got, "ok ")
	}
}

func TestSessionProbeDisplayFail(t *testing.T) {
	s := session.Session{
		Type:        session.TypePortForward,
		LastProbeAt: time.Now().Add(-3 * time.Second),
		LastProbeOK: false,
	}
	got := sessionProbeDisplay(s)
	if !strings.HasPrefix(got, "fail ") {
		t.Errorf("sessionProbeDisplay(fail) = %q, want prefix %q", got, "fail ")
	}
}
