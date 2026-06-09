//go:build windows

package web

import "net/http"

// handleTerminalWS is unsupported on Windows: the terminal relies on a Unix
// PTY to drive the interactive SSM shell.
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "terminal not supported on this platform", http.StatusNotImplemented)
}
