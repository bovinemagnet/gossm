//go:build !windows

package aws

import (
	"os"
	"syscall"
)

// terminalSignals lists the terminal-generated signals that gossm must divert
// away from itself while an SSM session is running. While the session is live
// the AWS session-manager-plugin owns the terminal (in raw mode) and forwards
// these keystrokes to the remote shell, so gossm acting on them would tear the
// session down instead. Covers SIGINT (Ctrl-C), SIGQUIT (Ctrl-\), and SIGTSTP
// (Ctrl-Z).
func terminalSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP}
}
