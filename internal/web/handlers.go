package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"

	awsutil "github.com/bovinemagnet/gossm/internal/aws"
	"github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/session"
)

// DashboardData is the data passed to the dashboard template.
type DashboardData struct {
	Sessions     []session.Session
	SessionCount int
	Uptime       string
	Port         int
	SparkSVG     template.HTML
	Presets      []config.SessionPreset
}

// handleDashboard renders the full dashboard page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := s.buildDashboardData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "layout.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSessionsList renders the session list partial.
func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	sessions := s.sm.ListSessions()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "session_list.html", sessions); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleStartSession parses the form, starts a new session, and returns the
// updated session list.
func (s *Server) handleStartSession(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	sessionType := session.TypeShell
	if r.FormValue("type") == "port-forward" {
		sessionType = session.TypePortForward
	}

	localPort, _ := strconv.Atoi(r.FormValue("local_port"))
	remotePort, _ := strconv.Atoi(r.FormValue("remote_port"))

	opts := session.SessionOpts{
		InstanceID:   r.FormValue("instance_id"),
		InstanceName: r.FormValue("instance_name"),
		Profile:      r.FormValue("profile"),
		Type:         sessionType,
		LocalPort:    localPort,
		RemotePort:   remotePort,
		RemoteHost:   r.FormValue("remote_host"),
	}

	if _, err := s.sm.StartSession(opts); err != nil {
		http.Error(w, fmt.Sprintf("failed to start session: %v", err), http.StatusInternalServerError)
		return
	}

	// Return the updated session list.
	s.handleSessionsList(w, r)
}

// handleStopSession extracts the session ID from the path, stops the session,
// and returns the updated session list.
func (s *Server) handleStopSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	if err := s.sm.StopSession(id); err != nil {
		http.Error(w, fmt.Sprintf("failed to stop session: %v", err), http.StatusInternalServerError)
		return
	}

	// Return the updated session list.
	s.handleSessionsList(w, r)
}

// handleInstances queries EC2 for running instances and returns the instance
// picker partial. Query params: profile (required), filter (optional).
func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="text-slate-500 p-4 text-sm">Enter an AWS profile above to browse instances.</div>`)
		return
	}

	if s.ec2Factory == nil {
		http.Error(w, "AWS not configured", http.StatusServiceUnavailable)
		return
	}

	ctx := r.Context()
	client, err := s.ec2Factory(ctx, profile)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create AWS client: %v", err), http.StatusInternalServerError)
		return
	}

	// Build filters: always running instances, optionally filtered by name.
	filter := r.URL.Query().Get("filter")
	var filterArgs []string
	if filter != "" {
		filterArgs = strings.Split(filter, ",")
	}
	filters := awsutil.BuildFilters(nil, filterArgs)
	input := &ec2.DescribeInstancesInput{Filters: filters}

	result, err := awsutil.GetInstances(ctx, client, input)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list instances: %v", err), http.StatusInternalServerError)
		return
	}

	instances := awsutil.ExtractInstances(result)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "instance_picker.html", instances); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleProfiles returns <option> elements for AWS profiles found in ~/.aws/config.
func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := awsutil.ParseAWSProfiles()
	if err != nil {
		// Not fatal — just return an empty list.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	for _, p := range profiles {
		fmt.Fprintf(w, "<option value=\"%s\">\n", template.HTMLEscapeString(p))
	}
}

// handlePreset returns a preset's data as a JSON object for client-side form filling.
func (s *Server) handlePreset(w http.ResponseWriter, r *http.Request) {
	idxStr := r.PathValue("index")
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(s.cfg.Presets) {
		http.Error(w, "invalid preset index", http.StatusBadRequest)
		return
	}
	p := s.cfg.Presets[idx]
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

// handleStartPreset starts a session directly from a preset by index.
func (s *Server) handleStartPreset(w http.ResponseWriter, r *http.Request) {
	idxStr := r.PathValue("index")
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 || idx >= len(s.cfg.Presets) {
		http.Error(w, "invalid preset index", http.StatusBadRequest)
		return
	}
	p := s.cfg.Presets[idx]

	sessionType := session.TypeShell
	if p.SessionType == "port-forward" {
		sessionType = session.TypePortForward
	}

	opts := session.SessionOpts{
		InstanceID:   p.InstanceID,
		InstanceName: p.InstanceName,
		Profile:      p.Profile,
		Type:         sessionType,
		LocalPort:    p.LocalPort,
		RemotePort:   p.RemotePort,
		RemoteHost:   p.RemoteHost,
	}

	if _, err := s.sm.StartSession(opts); err != nil {
		http.Error(w, fmt.Sprintf("failed to start session: %v", err), http.StatusInternalServerError)
		return
	}

	s.handleSessionsList(w, r)
}

// handleEvents is the SSE endpoint. It registers with the broker and streams
// events until the client disconnects.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch := s.sse.Subscribe()
	defer s.sse.Unsubscribe(ch)

	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}

			// Render the session list partial.
			var sessionsBuf bytes.Buffer
			sessions := s.sm.ListSessions()
			if err := s.tmpl.ExecuteTemplate(&sessionsBuf, "session_list.html", sessions); err == nil {
				fmt.Fprintf(w, "event: sessions\ndata: %s\n\n",
					strings.ReplaceAll(sessionsBuf.String(), "\n", "\ndata: "))
			}

			// Render the stats partial.
			var statsBuf bytes.Buffer
			data := s.buildDashboardData()
			if err := s.tmpl.ExecuteTemplate(&statsBuf, "stats.html", data); err == nil {
				fmt.Fprintf(w, "event: stats\ndata: %s\n\n",
					strings.ReplaceAll(statsBuf.String(), "\n", "\ndata: "))
			}

			flusher.Flush()
		}
	}
}

// buildDashboardData assembles the template data for the dashboard.
func (s *Server) buildDashboardData() DashboardData {
	return DashboardData{
		Sessions:     s.sm.ListSessions(),
		SessionCount: s.sm.SessionCount(),
		Uptime:       uptimeSince(s.startedAt),
		Port:         s.cfg.DashboardPort,
		SparkSVG:     template.HTML(renderSparkSVG(s.sm.SparkData())),
		Presets:      s.cfg.Presets,
	}
}

// renderSparkSVG generates an inline SVG polyline from sparkline data.
// The SVG is 200px wide and 40px tall.
func renderSparkSVG(data []int) string {
	if len(data) == 0 {
		return ""
	}

	maxVal := 0
	for _, v := range data {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		maxVal = 1
	}

	width := 200.0
	height := 40.0
	padding := 2.0
	drawHeight := height - 2*padding

	step := width / float64(len(data)-1)
	if len(data) == 1 {
		step = 0
	}

	var points strings.Builder
	for i, v := range data {
		x := float64(i) * step
		y := padding + drawHeight - (float64(v)/float64(maxVal))*drawHeight
		if math.IsNaN(y) {
			y = padding + drawHeight
		}
		if i > 0 {
			points.WriteString(" ")
		}
		fmt.Fprintf(&points, "%.1f,%.1f", x, y)
	}

	return fmt.Sprintf(
		`<svg width="200" height="40" viewBox="0 0 200 40" xmlns="http://www.w3.org/2000/svg">`+
			`<polyline fill="none" stroke="#38bdf8" stroke-width="1.5" points="%s"/>`+
			`</svg>`,
		points.String(),
	)
}

// sessionStateClass returns the Tailwind CSS colour class for a session state.
func sessionStateClass(state session.SessionState) string {
	switch state {
	case session.StateRunning:
		return "bg-green-500"
	case session.StateStarting:
		return "bg-yellow-500"
	case session.StateStopping:
		return "bg-yellow-500"
	case session.StateErrored:
		return "bg-red-500"
	case session.StateStopped:
		return "bg-slate-500"
	default:
		return "bg-slate-500"
	}
}

// sessionStateName returns a human-readable label for a session state.
func sessionStateName(state session.SessionState) string {
	switch state {
	case session.StateRunning:
		return "Running"
	case session.StateStarting:
		return "Starting"
	case session.StateStopping:
		return "Stopping"
	case session.StateErrored:
		return "Errored"
	case session.StateStopped:
		return "Stopped"
	default:
		return "Unknown"
	}
}

// sessionTypeName returns a human-readable label for a session type.
func sessionTypeName(t session.SessionType) string {
	switch t {
	case session.TypeShell:
		return "Shell"
	case session.TypePortForward:
		return "Port Forward"
	default:
		return "Unknown"
	}
}

// portDisplay formats port information for a session.
func portDisplay(s session.Session) string {
	if s.Type != session.TypePortForward {
		return "-"
	}
	if s.RemoteHost != "" {
		return fmt.Sprintf("%d → %s:%d", s.LocalPort, s.RemoteHost, s.RemotePort)
	}
	return fmt.Sprintf("%d → %d", s.LocalPort, s.RemotePort)
}

// uptimeSince returns a human-readable duration since the given time.
func uptimeSince(t time.Time) string {
	d := time.Since(t)

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if hours < 24 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	days := hours / 24
	hours = hours % 24
	return fmt.Sprintf("%dd %dh", days, hours)
}
