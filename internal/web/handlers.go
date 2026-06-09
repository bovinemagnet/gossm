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

// DashboardStats summarises the active session population for the
// metric cards on the dashboard.
type DashboardStats struct {
	Total        int
	Active       int
	Running      int
	Starting     int
	Stopping     int
	Stalled      int
	Reconnecting int
	Errored      int
	Stopped      int
	Shells       int
	PortForwards int
}

// DashboardData is the data passed to the dashboard template.
type DashboardData struct {
	ActiveSessions  []session.Session
	StoppedSessions []session.Session
	Stats           DashboardStats
	SessionCount    int
	Uptime          string
	Port            int
	SparkSVG        template.HTML
	Presets         []config.SessionPreset
	LastUpdate      string
	TerminalToken   string
}

// splitSessions separates a session slice into active (live or
// recovering) and stopped (terminal) lists.
func splitSessions(all []session.Session) (active, stopped []session.Session) {
	for _, s := range all {
		if isActiveState(s.State) {
			active = append(active, s)
		} else {
			stopped = append(stopped, s)
		}
	}
	return
}

// buildDashboardStats summarises a slice of sessions into the metric
// counters used by the dashboard.
func buildDashboardStats(sessions []session.Session) DashboardStats {
	var stats DashboardStats
	stats.Total = len(sessions)
	for _, sess := range sessions {
		if isActiveState(sess.State) {
			stats.Active++
		}
		switch sess.State {
		case session.StateRunning:
			stats.Running++
		case session.StateStarting:
			stats.Starting++
		case session.StateStopping:
			stats.Stopping++
		case session.StateStalled:
			stats.Stalled++
		case session.StateReconnecting:
			stats.Reconnecting++
		case session.StateErrored:
			stats.Errored++
		case session.StateStopped:
			stats.Stopped++
		}
		switch sess.Type {
		case session.TypeShell:
			stats.Shells++
		case session.TypePortForward:
			stats.PortForwards++
		}
	}
	return stats
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

// handleStats renders the stats bar partial for HTMX polling.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	data := s.buildDashboardData()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "stats.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleSessionsList renders the session list partial. By default it
// returns active sessions. Pass ?stopped=1 for the stopped/errored list.
func (s *Server) handleSessionsList(w http.ResponseWriter, r *http.Request) {
	active, stopped := splitSessions(s.sm.ListSessions())
	list := active
	if r != nil && r.URL.Query().Get("stopped") == "1" {
		list = stopped
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "session_list.html", list); err != nil {
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

// handleReconnectSession kicks off a manual reconnect cycle for a
// daemon-managed port-forward session.
func (s *Server) handleReconnectSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	if _, ok := s.sm.GetSession(id); !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if err := s.sm.ManualReconnect(id); err != nil {
		// Most often: session is not reconnectable (externally registered).
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.handleSessionsList(w, r)
}

// handleSetProbeInterval updates the per-session probe interval. The
// interval form value is in seconds (1-600).
func (s *Server) handleSetProbeInterval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "missing session id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form data", http.StatusBadRequest)
		return
	}

	intervalSec, err := strconv.Atoi(r.FormValue("interval"))
	if err != nil {
		http.Error(w, "invalid interval", http.StatusBadRequest)
		return
	}

	if _, ok := s.sm.GetSession(id); !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	if err := s.sm.SetSessionProbeInterval(id, time.Duration(intervalSec)*time.Second); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Return the updated session row.
	cur, ok := s.sm.GetSession(id)
	if !ok {
		// Disappeared between calls — just return the full list.
		s.handleSessionsList(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "session_row.html", *cur); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleInstances queries EC2 for running instances and returns the instance
// picker partial. Query params: profile (required), filter (optional).
func (s *Server) handleInstances(w http.ResponseWriter, r *http.Request) {
	profile := r.URL.Query().Get("profile")
	if profile == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<div class="instance-picker-empty">Enter an AWS profile above to browse instances.</div>`)
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

			active, stopped := splitSessions(s.sm.ListSessions())

			// Render the active session list partial.
			var activeBuf bytes.Buffer
			if err := s.tmpl.ExecuteTemplate(&activeBuf, "session_list.html", active); err == nil {
				fmt.Fprintf(w, "event: active-sessions\ndata: %s\n\n",
					strings.ReplaceAll(activeBuf.String(), "\n", "\ndata: "))
			}

			// Render the stopped session list partial.
			var stoppedBuf bytes.Buffer
			if err := s.tmpl.ExecuteTemplate(&stoppedBuf, "session_list.html", stopped); err == nil {
				fmt.Fprintf(w, "event: stopped-sessions\ndata: %s\n\n",
					strings.ReplaceAll(stoppedBuf.String(), "\n", "\ndata: "))
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
// ListSessions is called once and the resulting slice is reused for
// stats and split lists.
func (s *Server) buildDashboardData() DashboardData {
	all := s.sm.ListSessions()
	active, stopped := splitSessions(all)
	return DashboardData{
		ActiveSessions:  active,
		StoppedSessions: stopped,
		Stats:           buildDashboardStats(all),
		SessionCount:    len(active),
		Uptime:          uptimeSince(s.startedAt),
		Port:            s.cfg.DashboardPort,
		SparkSVG:        template.HTML(renderSparkSVG(s.sm.SparkData())),
		Presets:         s.cfg.Presets,
		LastUpdate:      time.Now().Format("15:04:05"),
		TerminalToken:   s.terminalToken,
	}
}

// renderSparkSVG generates an inline SVG chart from sparkline data,
// sized to fill the dashboard history panel. The SVG renders horizontal
// grid lines, a soft area fill, and a polyline.
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

	const (
		width        = 880.0
		height       = 132.0
		padTop       = 10.0
		padBottom    = 18.0
		padLeft      = 6.0
		padRight     = 6.0
		gridLines    = 4
	)
	drawHeight := height - padTop - padBottom
	drawWidth := width - padLeft - padRight

	step := drawWidth / float64(len(data)-1)
	if len(data) == 1 {
		step = 0
	}

	var points strings.Builder
	var area strings.Builder
	fmt.Fprintf(&area, "%.1f,%.1f ", padLeft, padTop+drawHeight)
	for i, v := range data {
		x := padLeft + float64(i)*step
		y := padTop + drawHeight - (float64(v)/float64(maxVal))*drawHeight
		if math.IsNaN(y) {
			y = padTop + drawHeight
		}
		if i > 0 {
			points.WriteString(" ")
		}
		fmt.Fprintf(&points, "%.1f,%.1f", x, y)
		fmt.Fprintf(&area, "%.1f,%.1f ", x, y)
	}
	fmt.Fprintf(&area, "%.1f,%.1f", padLeft+float64(len(data)-1)*step, padTop+drawHeight)

	var grid strings.Builder
	for i := 0; i <= gridLines; i++ {
		y := padTop + (drawHeight/float64(gridLines))*float64(i)
		fmt.Fprintf(&grid,
			`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#2b3a55" stroke-width="0.6" stroke-dasharray="2 4"/>`,
			padLeft, y, padLeft+drawWidth, y)
	}

	return fmt.Sprintf(
		`<svg viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Session history (last 60 minutes)">`+
			`<defs><linearGradient id="spark-fill" x1="0" y1="0" x2="0" y2="1">`+
			`<stop offset="0%%" stop-color="#38bdf8" stop-opacity="0.45"/>`+
			`<stop offset="100%%" stop-color="#38bdf8" stop-opacity="0"/>`+
			`</linearGradient></defs>`+
			`%s`+
			`<polygon points="%s" fill="url(#spark-fill)" stroke="none"/>`+
			`<polyline fill="none" stroke="#38bdf8" stroke-width="1.8" stroke-linejoin="round" stroke-linecap="round" points="%s"/>`+
			`<text x="%.1f" y="%.1f" fill="#64748b" font-size="10" font-family="ui-monospace, Menlo, monospace">0</text>`+
			`<text x="%.1f" y="%.1f" fill="#64748b" font-size="10" font-family="ui-monospace, Menlo, monospace" text-anchor="end">%d</text>`+
			`</svg>`,
		width, height,
		grid.String(),
		area.String(),
		points.String(),
		padLeft, padTop+drawHeight+12,
		padLeft+drawWidth, padTop+drawHeight+12,
		maxVal,
	)
}

// sessionStateClass returns the lowercase state slug used as a CSS
// modifier on `.session-card--{slug}` and `.state-pill--{slug}`.
func sessionStateClass(state session.SessionState) string {
	switch state {
	case session.StateRunning:
		return "running"
	case session.StateStarting:
		return "starting"
	case session.StateStopping:
		return "stopping"
	case session.StateStalled:
		return "stalled"
	case session.StateReconnecting:
		return "reconnecting"
	case session.StateErrored:
		return "errored"
	case session.StateStopped:
		return "stopped"
	default:
		return "unknown"
	}
}

// templateDict builds a map[string]any from key/value pairs so templates
// can pass multiple named values to a partial via `{{template "x" (dict
// "Key" value ...)}}`. An odd number of args, or a non-string key,
// returns nil so the template is rendered without the partial blowing
// up.
func templateDict(values ...any) map[string]any {
	if len(values)%2 != 0 {
		return nil
	}
	d := make(map[string]any, len(values)/2)
	for i := 0; i < len(values); i += 2 {
		k, ok := values[i].(string)
		if !ok {
			return nil
		}
		d[k] = values[i+1]
	}
	return d
}

// sessionTypeClass returns the lowercase type slug used as a CSS
// modifier (e.g. `.type-pill--port-forward`).
func sessionTypeClass(t session.SessionType) string {
	switch t {
	case session.TypeShell:
		return "shell"
	case session.TypePortForward:
		return "port-forward"
	default:
		return "unknown"
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
	case session.StateStalled:
		return "Stalled"
	case session.StateReconnecting:
		return "Reconnecting"
	case session.StateErrored:
		return "Errored"
	case session.StateStopped:
		return "Stopped"
	default:
		return "Unknown"
	}
}

// isActiveState returns true for states that represent a live or
// recovering session (i.e. not Stopped/Errored).
func isActiveState(state session.SessionState) bool {
	switch state {
	case session.StateStarting, session.StateRunning, session.StateStopping,
		session.StateStalled, session.StateReconnecting:
		return true
	}
	return false
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

// sessionProbeDisplay formats the probe outcome for the dashboard.
// Shell sessions return "—" since they aren't probed. Port-forward
// sessions return "pending" until the first probe fires, then either
// "ok <Ns ago>" or "fail <Ns ago>".
func sessionProbeDisplay(s session.Session) string {
	if s.Type != session.TypePortForward {
		return "—"
	}
	if s.LastProbeAt.IsZero() {
		return "pending"
	}
	age := time.Since(s.LastProbeAt)
	prefix := "ok"
	if !s.LastProbeOK {
		prefix = "fail"
	}
	return fmt.Sprintf("%s %s ago", prefix, shortDuration(age))
}

// shortDuration formats a duration as e.g. "4s", "2m", "1h".
func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
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
