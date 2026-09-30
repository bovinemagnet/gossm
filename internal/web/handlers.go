package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
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
	HistorySVG      template.HTML
	TrafficSVG      template.HTML
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

	localPort, lerr := strconv.Atoi(r.FormValue("local_port"))
	remotePort, rerr := strconv.Atoi(r.FormValue("remote_port"))
	if sessionType == session.TypePortForward {
		if lerr != nil || rerr != nil || !validPort(localPort) || !validPort(remotePort) {
			http.Error(w, "local_port and remote_port must be integers in 1-65535", http.StatusBadRequest)
			return
		}
	}

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

// validPort reports whether p is a usable TCP port number.
func validPort(p int) bool {
	return p >= 1 && p <= 65535
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
	rc := http.NewResponseController(w)

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}

			// One snapshot per push so the lists and stats agree, and a
			// write deadline so a stalled browser cannot park this
			// goroutine forever.
			all := s.sm.ListSessions()
			active, stopped := splitSessions(all)
			_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))

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
			data := s.buildDashboardDataFrom(all)
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
	return s.buildDashboardDataFrom(s.sm.ListSessions())
}

// buildDashboardDataFrom assembles the template data from an existing
// session snapshot, so callers rendering several partials in one pass
// can keep them consistent.
func (s *Server) buildDashboardDataFrom(all []session.Session) DashboardData {
	active, stopped := splitSessions(all)
	history := s.sm.History()
	return DashboardData{
		ActiveSessions:  active,
		StoppedSessions: stopped,
		Stats:           buildDashboardStats(all),
		SessionCount:    len(active),
		Uptime:          uptimeSince(s.startedAt),
		Port:            s.cfg.DashboardPort,
		HistorySVG:      template.HTML(renderHistorySVG(history)),
		TrafficSVG:      template.HTML(renderTrafficSVG(history)),
		Presets:         s.cfg.Presets,
		LastUpdate:      time.Now().Format("15:04:05"),
		TerminalToken:   s.terminalToken,
	}
}

// History chart geometry, sized to fill the dashboard history panel.
const (
	chartWidth     = 880.0
	chartHeight    = 132.0
	chartPadTop    = 10.0
	chartPadBottom = 18.0
	chartPadLeft   = 6.0
	chartPadRight  = 6.0
	chartGridLines = 4
)

// chartScale maps sample indices and values onto the chart area.
type chartScale struct {
	n   int     // number of samples
	max float64 // value drawn at the top of the chart
}

func (c chartScale) x(i int) float64 {
	if c.n < 2 {
		return chartPadLeft
	}
	return chartPadLeft + float64(i)*(chartWidth-chartPadLeft-chartPadRight)/float64(c.n-1)
}

func (c chartScale) y(v float64) float64 {
	drawHeight := chartHeight - chartPadTop - chartPadBottom
	return chartPadTop + drawHeight - (v/c.max)*drawHeight
}

// line returns the polyline points for values.
func (c chartScale) line(values []float64) string {
	var b strings.Builder
	for i, v := range values {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%.1f,%.1f", c.x(i), c.y(v))
	}
	return b.String()
}

// band returns polygon points for the area between lower and upper.
func (c chartScale) band(lower, upper []float64) string {
	var b strings.Builder
	b.WriteString(c.line(upper))
	for i := len(lower) - 1; i >= 0; i-- {
		fmt.Fprintf(&b, " %.1f,%.1f", c.x(i), c.y(lower[i]))
	}
	return b.String()
}

// chartSVG wraps chart layers in an SVG with grid lines and 0/max labels.
func chartSVG(ariaLabel, maxLabel, layers string) string {
	drawHeight := chartHeight - chartPadTop - chartPadBottom
	var grid strings.Builder
	for i := 0; i <= chartGridLines; i++ {
		y := chartPadTop + (drawHeight/float64(chartGridLines))*float64(i)
		fmt.Fprintf(&grid,
			`<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" stroke="#2b3a55" stroke-width="0.6" stroke-dasharray="2 4"/>`,
			chartPadLeft, y, chartWidth-chartPadRight, y)
	}
	return fmt.Sprintf(
		`<svg viewBox="0 0 %.0f %.0f" preserveAspectRatio="none" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="%s">`+
			`%s%s`+
			`<text x="%.1f" y="%.1f" fill="#64748b" font-size="10" font-family="ui-monospace, Menlo, monospace">0</text>`+
			`<text x="%.1f" y="%.1f" fill="#64748b" font-size="10" font-family="ui-monospace, Menlo, monospace" text-anchor="end">%s</text>`+
			`</svg>`,
		chartWidth, chartHeight, template.HTMLEscapeString(ariaLabel),
		grid.String(), layers,
		chartPadLeft, chartHeight-chartPadBottom+12,
		chartWidth-chartPadRight, chartHeight-chartPadBottom+12,
		template.HTMLEscapeString(maxLabel),
	)
}

// renderHistorySVG draws active sessions over the last hour as a stacked
// area chart: tunnels on the bottom, shells stacked on top.
func renderHistorySVG(points []session.HistoryPoint) string {
	if len(points) == 0 {
		return ""
	}

	zero := make([]float64, len(points))
	tunnels := make([]float64, len(points))
	total := make([]float64, len(points))
	maxVal := 1
	for i, p := range points {
		tunnels[i] = float64(p.Tunnels)
		total[i] = float64(p.Tunnels + p.Shells)
		if p.Tunnels+p.Shells > maxVal {
			maxVal = p.Tunnels + p.Shells
		}
	}
	c := chartScale{n: len(points), max: float64(maxVal)}

	layers := fmt.Sprintf(
		`<g class="history-tunnels">`+
			`<polygon points="%s" fill="#34d399" fill-opacity="0.28" stroke="none"/>`+
			`<polyline points="%s" fill="none" stroke="#34d399" stroke-width="1.6" stroke-linejoin="round"/>`+
			`</g>`+
			`<g class="history-shells">`+
			`<polygon points="%s" fill="#818cf8" fill-opacity="0.28" stroke="none"/>`+
			`<polyline points="%s" fill="none" stroke="#818cf8" stroke-width="1.6" stroke-linejoin="round"/>`+
			`</g>`,
		c.band(zero, tunnels), c.line(tunnels),
		c.band(tunnels, total), c.line(total),
	)
	return chartSVG("Active sessions by type (last 60 minutes)", strconv.Itoa(maxVal), layers)
}

// renderTrafficSVG draws web-terminal bytes received and sent per sample
// over the last hour. It returns "" when there has been no traffic so the
// template can show a placeholder instead of a flat chart.
func renderTrafficSVG(points []session.HistoryPoint) string {
	in := make([]float64, len(points))
	out := make([]float64, len(points))
	var maxVal int64
	for i, p := range points {
		in[i] = float64(p.BytesIn)
		out[i] = float64(p.BytesOut)
		maxVal = max(maxVal, p.BytesIn, p.BytesOut)
	}
	if maxVal == 0 {
		return ""
	}
	c := chartScale{n: len(points), max: float64(maxVal)}

	zero := make([]float64, len(points))
	layers := fmt.Sprintf(
		`<g class="traffic-in">`+
			`<polygon points="%s" fill="#38bdf8" fill-opacity="0.25" stroke="none"/>`+
			`<polyline points="%s" fill="none" stroke="#38bdf8" stroke-width="1.6" stroke-linejoin="round"/>`+
			`</g>`+
			`<g class="traffic-out">`+
			`<polyline points="%s" fill="none" stroke="#f472b6" stroke-width="1.6" stroke-linejoin="round"/>`+
			`</g>`,
		c.band(zero, in), c.line(in), c.line(out),
	)
	return chartSVG("Web terminal traffic per minute (last 60 minutes)", formatBytes(maxVal), layers)
}

// formatBytes renders a byte count with a binary unit, e.g. "1.5 KB".
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 2; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMG"[exp])
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
	return state.IsActive()
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
