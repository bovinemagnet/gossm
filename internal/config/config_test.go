package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.DashboardPort != 8443 {
		t.Errorf("DashboardPort = %d, want 8443", cfg.DashboardPort)
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

func TestApplyEnv(t *testing.T) {
	t.Setenv("GOSSM_PORT", "5555")

	cfg := DefaultConfig()
	applyEnv(cfg)

	if cfg.DashboardPort != 5555 {
		t.Errorf("DashboardPort = %d, want 5555", cfg.DashboardPort)
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
