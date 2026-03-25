package cmd

import (
	"testing"
)

func TestRootCommandHasSubcommands(t *testing.T) {
	names := make(map[string]bool)
	for _, c := range rootCmd.Commands() {
		names[c.Name()] = true
	}

	required := []string{"connect", "daemon", "version"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("root command missing subcommand %q", name)
		}
	}
}

func TestDaemonHasSubcommands(t *testing.T) {
	names := make(map[string]bool)
	for _, c := range daemonCmd.Commands() {
		names[c.Name()] = true
	}

	required := []string{"start", "stop", "status"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("daemon command missing subcommand %q", name)
		}
	}
}

func TestDaemonStartHasForegroundFlag(t *testing.T) {
	f := daemonStartCmd.Flags().Lookup("foreground")
	if f == nil {
		t.Fatal("daemon start missing --foreground flag")
	}
	if f.DefValue != "false" {
		t.Errorf("expected --foreground default %q, got %q", "false", f.DefValue)
	}
}

func TestConnectHasPortForwardingFlags(t *testing.T) {
	flags := []struct {
		name     string
		defValue string
	}{
		{"local-port", "0"},
		{"remote-port", "0"},
		{"remote-host", ""},
	}
	for _, tt := range flags {
		f := connectCmd.Flags().Lookup(tt.name)
		if f == nil {
			t.Errorf("connect command missing --%s flag", tt.name)
			continue
		}
		if f.DefValue != tt.defValue {
			t.Errorf("--%s default: got %q, want %q", tt.name, f.DefValue, tt.defValue)
		}
	}
}
