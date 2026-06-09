package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"os/exec"
)

// terminalCmdBuilder produces the command used to drive a browser terminal
// session. Injecting it lets tests substitute a harmless echo process (e.g.
// `cat`) for the real `aws ssm start-session` invocation.
type terminalCmdBuilder func(ctx context.Context, instance, profile string) *exec.Cmd

// defaultTerminalCmdBuilder builds the real `aws ssm start-session` command for
// an interactive shell, mirroring session.defaultCommandBuilder.
func defaultTerminalCmdBuilder(ctx context.Context, instance, profile string) *exec.Cmd {
	args := []string{"ssm", "start-session", "--target", instance}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	return exec.CommandContext(ctx, "aws", args...)
}

// termControl is a control message sent by the browser over the text channel.
type termControl struct {
	T    string `json:"t"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// generateTerminalToken returns a cryptographically random hex token used to
// gate access to the terminal WebSocket endpoint.
func generateTerminalToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// rand.Read never returns an error on supported platforms; fall back
		// to an unusable token rather than a predictable one.
		return ""
	}
	return hex.EncodeToString(b)
}

// validToken reports whether the supplied token matches want in constant time.
func validToken(got, want string) bool {
	if want == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// originAllowed reports whether a WebSocket request carrying the given Origin
// header is permitted for a dashboard served at host. The terminal opens a
// shell, so we only accept same-origin upgrades to defend against cross-site
// WebSocket hijacking. An empty Origin is rejected (browsers always send one).
func originAllowed(origin, host string) bool {
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == host
}

// firstNonEmpty returns the first non-empty string in vals, or "".
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// handleTerminalPanel renders the xterm.js terminal panel partial for the
// requested instance. The booted panel opens the WebSocket itself.
func (s *Server) handleTerminalPanel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	// Accept both the launch form's field names (instance_id/instance_name)
	// and the short query names.
	instance := firstNonEmpty(q.Get("instance"), q.Get("instance_id"))
	if instance == "" {
		http.Error(w, "missing instance", http.StatusBadRequest)
		return
	}
	data := map[string]any{
		"InstanceID":   instance,
		"InstanceName": firstNonEmpty(q.Get("name"), q.Get("instance_name")),
		"Profile":      q.Get("profile"),
		"Token":        s.terminalToken,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "terminal.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
