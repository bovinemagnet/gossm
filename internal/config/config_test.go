package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfig_ProbeAndReconnectDefaults(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ProbeInterval != 30*time.Second {
		t.Errorf("ProbeInterval = %v, want 30s", cfg.ProbeInterval)
	}
	if cfg.ProbeTimeout != 2*time.Second {
		t.Errorf("ProbeTimeout = %v, want 2s", cfg.ProbeTimeout)
	}
	if cfg.ReconnectFailureThreshold != 1 {
		t.Errorf("ReconnectFailureThreshold = %d, want 1", cfg.ReconnectFailureThreshold)
	}
	if cfg.ReconnectMaxAttempts != 5 {
		t.Errorf("ReconnectMaxAttempts = %d, want 5", cfg.ReconnectMaxAttempts)
	}
	if cfg.ReconnectBackoffInitial != 5*time.Second {
		t.Errorf("ReconnectBackoffInitial = %v, want 5s", cfg.ReconnectBackoffInitial)
	}
	if cfg.ReconnectBackoffMax != 60*time.Second {
		t.Errorf("ReconnectBackoffMax = %v, want 60s", cfg.ReconnectBackoffMax)
	}
}

func TestDefaultConfig_BindAddress(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.BindAddress != "127.0.0.1" {
		t.Errorf("BindAddress = %q, want 127.0.0.1", cfg.BindAddress)
	}
}

func TestLoadKeyValueFile_BindAddress(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	if err := os.WriteFile(cfgFile, []byte("GOSSM_BIND=0.0.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)

	if cfg.BindAddress != "0.0.0.0" {
		t.Errorf("BindAddress = %q, want 0.0.0.0", cfg.BindAddress)
	}
}

func TestLoadKeyValueFile_ProbeAndReconnect(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")

	content := `GOSSM_PROBE_INTERVAL=15s
GOSSM_PROBE_TIMEOUT=500ms
GOSSM_RECONNECT_FAILURE_THRESHOLD=3
GOSSM_RECONNECT_MAX_ATTEMPTS=8
GOSSM_RECONNECT_BACKOFF_INITIAL=2s
GOSSM_RECONNECT_BACKOFF_MAX=2m
`
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)

	if cfg.ProbeInterval != 15*time.Second {
		t.Errorf("ProbeInterval = %v, want 15s", cfg.ProbeInterval)
	}
	if cfg.ProbeTimeout != 500*time.Millisecond {
		t.Errorf("ProbeTimeout = %v, want 500ms", cfg.ProbeTimeout)
	}
	if cfg.ReconnectFailureThreshold != 3 {
		t.Errorf("ReconnectFailureThreshold = %d, want 3", cfg.ReconnectFailureThreshold)
	}
	if cfg.ReconnectMaxAttempts != 8 {
		t.Errorf("ReconnectMaxAttempts = %d, want 8", cfg.ReconnectMaxAttempts)
	}
	if cfg.ReconnectBackoffInitial != 2*time.Second {
		t.Errorf("ReconnectBackoffInitial = %v, want 2s", cfg.ReconnectBackoffInitial)
	}
	if cfg.ReconnectBackoffMax != 2*time.Minute {
		t.Errorf("ReconnectBackoffMax = %v, want 2m", cfg.ReconnectBackoffMax)
	}
}

func TestLoadKeyValueFile_InvalidDurationIgnored(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")

	content := "GOSSM_PROBE_INTERVAL=not-a-duration\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)

	if cfg.ProbeInterval != 30*time.Second {
		t.Errorf("ProbeInterval = %v, want 30s (default preserved on invalid input)", cfg.ProbeInterval)
	}
}

func TestApplyEnv_ProbeAndReconnect(t *testing.T) {
	t.Setenv("GOSSM_PROBE_INTERVAL", "10s")
	t.Setenv("GOSSM_RECONNECT_MAX_ATTEMPTS", "7")

	cfg := DefaultConfig()
	applyEnv(cfg)

	if cfg.ProbeInterval != 10*time.Second {
		t.Errorf("ProbeInterval = %v, want 10s", cfg.ProbeInterval)
	}
	if cfg.ReconnectMaxAttempts != 7 {
		t.Errorf("ReconnectMaxAttempts = %d, want 7", cfg.ReconnectMaxAttempts)
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DashboardPort != 8877 {
		t.Errorf("DashboardPort = %d, want 8877", cfg.DashboardPort)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "warn")
	}
	if !strings.Contains(cfg.PIDDir, ".gossm") {
		t.Errorf("PIDDir = %q, want it to contain %q", cfg.PIDDir, ".gossm")
	}
}

func TestLoadKeyValueFile(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")

	content := "GOSSM_PORT=9999\nGOSSM_LOG_LEVEL=debug\nGOSSM_PID_DIR=/tmp/test-gossm\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)

	if cfg.DashboardPort != 9999 {
		t.Errorf("DashboardPort = %d, want 9999", cfg.DashboardPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.PIDDir != "/tmp/test-gossm" {
		t.Errorf("PIDDir = %q, want %q", cfg.PIDDir, "/tmp/test-gossm")
	}
}

func TestLoadKeyValueFile_Comments(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")

	content := "# This is a comment\n\nGOSSM_PORT=7777\n# Another comment\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)

	if cfg.DashboardPort != 7777 {
		t.Errorf("DashboardPort = %d, want 7777", cfg.DashboardPort)
	}
	// LogLevel should remain default since it was not in the file.
	if cfg.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want %q (default)", cfg.LogLevel, "warn")
	}
}

func TestLoadKeyValueFile_Quotes(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")

	content := "GOSSM_LOG_LEVEL=\"info\"\nGOSSM_PID_DIR='/var/run/gossm'\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)

	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q (quotes stripped)", cfg.LogLevel, "info")
	}
	if cfg.PIDDir != "/var/run/gossm" {
		t.Errorf("PIDDir = %q, want %q (quotes stripped)", cfg.PIDDir, "/var/run/gossm")
	}
}

// TestLoadKeyValueFile_QuoteEdgeCases verifies only a single matching
// surrounding quote pair is stripped — mismatched or embedded quotes are
// preserved rather than silently mangled.
func TestLoadKeyValueFile_QuoteEdgeCases(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"matched double", `"info"`, "info"},
		{"matched single", `'info'`, "info"},
		{"unquoted", `info`, "info"},
		{"apostrophe inside double quotes", `"it's"`, "it's"},
		{"mismatched pair preserved", `"info'`, `"info'`},
		{"trailing quote only preserved", `info'`, `info'`},
		{"nested quotes strip one pair", `'"info"'`, `"info"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgFile := filepath.Join(dir, "config")
			content := "GOSSM_LOG_LEVEL=" + c.value + "\n"
			if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg := DefaultConfig()
			loadKeyValueFile(cfgFile, cfg)

			if cfg.LogLevel != c.want {
				t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, c.want)
			}
		})
	}
}

func TestApplyEnv(t *testing.T) {
	t.Setenv("GOSSM_PORT", "5555")

	cfg := DefaultConfig()
	applyEnv(cfg)

	if cfg.DashboardPort != 5555 {
		t.Errorf("DashboardPort = %d, want 5555", cfg.DashboardPort)
	}
}

func TestApplyPresetKey_SinglePreset(t *testing.T) {
	cfg := DefaultConfig()

	applyPresetKey("GOSSM_SESSION_1_NAME", "prod-web", cfg)
	applyPresetKey("GOSSM_SESSION_1_INSTANCE_ID", "i-abc123", cfg)
	applyPresetKey("GOSSM_SESSION_1_INSTANCE_NAME", "web-server", cfg)
	applyPresetKey("GOSSM_SESSION_1_PROFILE", "production", cfg)
	applyPresetKey("GOSSM_SESSION_1_TYPE", "shell", cfg)

	if len(cfg.Presets) != 1 {
		t.Fatalf("Presets length = %d, want 1", len(cfg.Presets))
	}
	p := cfg.Presets[0]
	if p.Name != "prod-web" {
		t.Errorf("Name = %q, want %q", p.Name, "prod-web")
	}
	if p.InstanceID != "i-abc123" {
		t.Errorf("InstanceID = %q, want %q", p.InstanceID, "i-abc123")
	}
	if p.InstanceName != "web-server" {
		t.Errorf("InstanceName = %q, want %q", p.InstanceName, "web-server")
	}
	if p.Profile != "production" {
		t.Errorf("Profile = %q, want %q", p.Profile, "production")
	}
	if p.SessionType != "shell" {
		t.Errorf("SessionType = %q, want %q", p.SessionType, "shell")
	}
}

func TestApplyPresetKey_PortForward(t *testing.T) {
	cfg := DefaultConfig()

	applyPresetKey("GOSSM_SESSION_1_NAME", "db-tunnel", cfg)
	applyPresetKey("GOSSM_SESSION_1_INSTANCE_ID", "i-db999", cfg)
	applyPresetKey("GOSSM_SESSION_1_TYPE", "port-forward", cfg)
	applyPresetKey("GOSSM_SESSION_1_LOCAL_PORT", "5432", cfg)
	applyPresetKey("GOSSM_SESSION_1_REMOTE_PORT", "5432", cfg)
	applyPresetKey("GOSSM_SESSION_1_REMOTE_HOST", "db.internal", cfg)

	if len(cfg.Presets) != 1 {
		t.Fatalf("Presets length = %d, want 1", len(cfg.Presets))
	}
	p := cfg.Presets[0]
	if p.SessionType != "port-forward" {
		t.Errorf("SessionType = %q, want %q", p.SessionType, "port-forward")
	}
	if p.LocalPort != 5432 {
		t.Errorf("LocalPort = %d, want 5432", p.LocalPort)
	}
	if p.RemotePort != 5432 {
		t.Errorf("RemotePort = %d, want 5432", p.RemotePort)
	}
	if p.RemoteHost != "db.internal" {
		t.Errorf("RemoteHost = %q, want %q", p.RemoteHost, "db.internal")
	}
}

func TestApplyPresetKey_MultiplePresets(t *testing.T) {
	cfg := DefaultConfig()

	applyPresetKey("GOSSM_SESSION_1_NAME", "first", cfg)
	applyPresetKey("GOSSM_SESSION_3_NAME", "third", cfg)
	applyPresetKey("GOSSM_SESSION_2_NAME", "second", cfg)

	if len(cfg.Presets) != 3 {
		t.Fatalf("Presets length = %d, want 3", len(cfg.Presets))
	}
	if cfg.Presets[0].Name != "first" {
		t.Errorf("Presets[0].Name = %q, want %q", cfg.Presets[0].Name, "first")
	}
	if cfg.Presets[1].Name != "second" {
		t.Errorf("Presets[1].Name = %q, want %q", cfg.Presets[1].Name, "second")
	}
	if cfg.Presets[2].Name != "third" {
		t.Errorf("Presets[2].Name = %q, want %q", cfg.Presets[2].Name, "third")
	}
}

func TestApplyPresetKey_CaseInsensitive(t *testing.T) {
	cfg := DefaultConfig()

	applyPresetKey("gossm_session_1_name", "lower", cfg)

	if len(cfg.Presets) != 1 {
		t.Fatalf("Presets length = %d, want 1", len(cfg.Presets))
	}
	if cfg.Presets[0].Name != "lower" {
		t.Errorf("Name = %q, want %q", cfg.Presets[0].Name, "lower")
	}
}

func TestApplyPresetKey_InvalidIndex(t *testing.T) {
	cfg := DefaultConfig()

	// Index 0 is invalid (1-based).
	applyPresetKey("GOSSM_SESSION_0_NAME", "zero", cfg)
	if len(cfg.Presets) != 0 {
		t.Errorf("Presets length = %d, want 0 for index 0", len(cfg.Presets))
	}

	// Negative index.
	applyPresetKey("GOSSM_SESSION_-1_NAME", "negative", cfg)
	if len(cfg.Presets) != 0 {
		t.Errorf("Presets length = %d, want 0 for negative index", len(cfg.Presets))
	}

	// Non-numeric index.
	applyPresetKey("GOSSM_SESSION_ABC_NAME", "alpha", cfg)
	if len(cfg.Presets) != 0 {
		t.Errorf("Presets length = %d, want 0 for non-numeric index", len(cfg.Presets))
	}
}

func TestApplyPresetKey_NonPresetKeyIgnored(t *testing.T) {
	cfg := DefaultConfig()

	applyPresetKey("GOSSM_PORT", "9999", cfg)
	if len(cfg.Presets) != 0 {
		t.Errorf("Presets length = %d, want 0 for non-preset key", len(cfg.Presets))
	}
}

func TestApplyPresetKey_InvalidPortIgnored(t *testing.T) {
	cfg := DefaultConfig()

	applyPresetKey("GOSSM_SESSION_1_NAME", "test", cfg)
	applyPresetKey("GOSSM_SESSION_1_LOCAL_PORT", "not-a-number", cfg)

	if cfg.Presets[0].LocalPort != 0 {
		t.Errorf("LocalPort = %d, want 0 for invalid port", cfg.Presets[0].LocalPort)
	}
}

func TestApplyPresetKey_NoField(t *testing.T) {
	cfg := DefaultConfig()

	// Key with index but no field (no underscore after index).
	applyPresetKey("GOSSM_SESSION_1", "value", cfg)
	if len(cfg.Presets) != 0 {
		t.Errorf("Presets length = %d, want 0 for key without field", len(cfg.Presets))
	}
}

func TestLoadKeyValueFile_Presets(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")

	content := `GOSSM_SESSION_1_NAME=web-shell
GOSSM_SESSION_1_INSTANCE_ID=i-web001
GOSSM_SESSION_1_PROFILE=prod
GOSSM_SESSION_1_TYPE=shell
GOSSM_SESSION_2_NAME=db-tunnel
GOSSM_SESSION_2_INSTANCE_ID=i-db001
GOSSM_SESSION_2_TYPE=port-forward
GOSSM_SESSION_2_LOCAL_PORT=3306
GOSSM_SESSION_2_REMOTE_PORT=3306
`
	if err := os.WriteFile(cfgFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)

	if len(cfg.Presets) != 2 {
		t.Fatalf("Presets length = %d, want 2", len(cfg.Presets))
	}
	if cfg.Presets[0].Name != "web-shell" {
		t.Errorf("Presets[0].Name = %q, want %q", cfg.Presets[0].Name, "web-shell")
	}
	if cfg.Presets[1].Name != "db-tunnel" {
		t.Errorf("Presets[1].Name = %q, want %q", cfg.Presets[1].Name, "db-tunnel")
	}
	if cfg.Presets[1].LocalPort != 3306 {
		t.Errorf("Presets[1].LocalPort = %d, want 3306", cfg.Presets[1].LocalPort)
	}
}

func TestPrecedence(t *testing.T) {
	// Config file sets port to 1111.
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config")
	if err := os.WriteFile(cfgFile, []byte("GOSSM_PORT=1111\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Environment variable sets port to 2222.
	t.Setenv("GOSSM_PORT", "2222")

	cfg := DefaultConfig()
	loadKeyValueFile(cfgFile, cfg)
	applyEnv(cfg)

	// Environment variable should win.
	if cfg.DashboardPort != 2222 {
		t.Errorf("DashboardPort = %d, want 2222 (env var should take precedence)", cfg.DashboardPort)
	}
}

func TestApplyEnv_Presets(t *testing.T) {
	t.Setenv("GOSSM_SESSION_1_NAME", "env-preset")
	t.Setenv("GOSSM_SESSION_1_INSTANCE_ID", "i-env001")
	t.Setenv("GOSSM_SESSION_1_PROFILE", "staging")
	t.Setenv("GOSSM_SESSION_1_TYPE", "port-forward")
	t.Setenv("GOSSM_SESSION_1_LOCAL_PORT", "8080")
	t.Setenv("GOSSM_SESSION_1_REMOTE_PORT", "80")
	t.Setenv("GOSSM_SESSION_1_REMOTE_HOST", "app.internal")

	cfg := DefaultConfig()
	applyEnv(cfg)

	if len(cfg.Presets) != 1 {
		t.Fatalf("Presets length = %d, want 1", len(cfg.Presets))
	}
	p := cfg.Presets[0]
	if p.Name != "env-preset" {
		t.Errorf("Name = %q, want %q", p.Name, "env-preset")
	}
	if p.InstanceID != "i-env001" {
		t.Errorf("InstanceID = %q, want %q", p.InstanceID, "i-env001")
	}
	if p.Profile != "staging" {
		t.Errorf("Profile = %q, want %q", p.Profile, "staging")
	}
	if p.SessionType != "port-forward" {
		t.Errorf("SessionType = %q, want %q", p.SessionType, "port-forward")
	}
	if p.LocalPort != 8080 {
		t.Errorf("LocalPort = %d, want 8080", p.LocalPort)
	}
	if p.RemotePort != 80 {
		t.Errorf("RemotePort = %d, want 80", p.RemotePort)
	}
	if p.RemoteHost != "app.internal" {
		t.Errorf("RemoteHost = %q, want %q", p.RemoteHost, "app.internal")
	}
}
