//go:build windows

package aws

import "os"

// terminalSignals lists the terminal-generated signals that gossm must divert
// away from itself while an SSM session is running. Windows only supports
// os.Interrupt (Ctrl-C); SIGQUIT and SIGTSTP do not exist there.
func terminalSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
