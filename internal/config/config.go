// Package config provides configuration loading from multiple sources.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SessionPreset defines a saved session that can be started from the dashboard.
type SessionPreset struct {
	Name         string
	InstanceID   string
	InstanceName string
	Profile      string
	SessionType  string // "shell" or "port-forward"
	LocalPort    int
	RemotePort   int
	RemoteHost   string
}

// Config holds the application configuration.
type Config struct {
	DashboardPort int
	LogLevel      string
	PIDDir        string

	// Saved session presets loaded from config files.
	Presets []SessionPreset
}

// DefaultConfig returns a Config with compiled defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		DashboardPort: 8877,
		LogLevel:      "warn",
		PIDDir:        filepath.Join(home, ".gossm"),
	}
}

// Load returns a Config populated from all sources with proper precedence:
// compiled defaults → ~/.gossm/config → .env in CWD → env vars.
func Load() *Config {
	cfg := DefaultConfig()

	// Load from ~/.gossm/config if it exists.
	home, err := os.UserHomeDir()
	if err == nil {
		loadKeyValueFile(filepath.Join(home, ".gossm", "config"), cfg)
	}

	// Load from .env in current working directory if it exists.
	loadKeyValueFile(".env", cfg)

	// Environment variables override everything.
	applyEnv(cfg)

	return cfg
}

// loadKeyValueFile reads a key=value file and applies values to the config.
func loadKeyValueFile(path string, cfg *Config) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Remove surrounding quotes if present.
		value = strings.Trim(value, "\"'")
		applyKeyValue(key, value, cfg)
	}
}

// applyKeyValue sets a config field from a key-value pair.
func applyKeyValue(key, value string, cfg *Config) {
	switch strings.ToUpper(key) {
	case "GOSSM_PORT":
		if port, err := strconv.Atoi(value); err == nil {
			cfg.DashboardPort = port
		}
	case "GOSSM_LOG_LEVEL":
		cfg.LogLevel = value
	case "GOSSM_PID_DIR":
		cfg.PIDDir = value
	default:
		// Handle session presets: GOSSM_SESSION_<N>_<FIELD>=value
		applyPresetKey(key, value, cfg)
	}
}

// applyPresetKey handles GOSSM_SESSION_<N>_<FIELD> keys.
func applyPresetKey(key, value string, cfg *Config) {
	upper := strings.ToUpper(key)
	if !strings.HasPrefix(upper, "GOSSM_SESSION_") {
		return
	}
	// Strip prefix, leaving e.g. "1_NAME" or "2_INSTANCE_ID"
	rest := upper[len("GOSSM_SESSION_"):]
	// Split on first underscore to get the index.
	idxStr, field, ok := strings.Cut(rest, "_")
	if !ok {
		return
	}
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 1 {
		return
	}
	// Ensure the presets slice is large enough.
	for len(cfg.Presets) < idx {
		cfg.Presets = append(cfg.Presets, SessionPreset{})
	}
	p := &cfg.Presets[idx-1]
	switch field {
	case "NAME":
		p.Name = value
	case "INSTANCE_ID":
		p.InstanceID = value
	case "INSTANCE_NAME":
		p.InstanceName = value
	case "PROFILE":
		p.Profile = value
	case "TYPE":
		p.SessionType = value
	case "LOCAL_PORT":
		if port, err := strconv.Atoi(value); err == nil {
			p.LocalPort = port
		}
	case "REMOTE_PORT":
		if port, err := strconv.Atoi(value); err == nil {
			p.RemotePort = port
		}
	case "REMOTE_HOST":
		p.RemoteHost = value
	}
}

// applyEnv reads environment variables and applies them to the config.
func applyEnv(cfg *Config) {
	envMappings := map[string]func(string){
		"GOSSM_PORT": func(v string) {
			if port, err := strconv.Atoi(v); err == nil {
				cfg.DashboardPort = port
			}
		},
		"GOSSM_LOG_LEVEL": func(v string) {
			cfg.LogLevel = v
		},
		"GOSSM_PID_DIR": func(v string) {
			cfg.PIDDir = v
		},
	}

	// Also scan all env vars for GOSSM_SESSION_* presets.
	for _, env := range os.Environ() {
		k, v, ok := strings.Cut(env, "=")
		if ok && strings.HasPrefix(strings.ToUpper(k), "GOSSM_SESSION_") {
			applyPresetKey(k, v, cfg)
		}
	}

	for envKey, setter := range envMappings {
		if v := os.Getenv(envKey); v != "" {
			setter(v)
		}
	}
}

// EnsurePIDDir creates the PID directory if it does not exist.
func (c *Config) EnsurePIDDir() error {
	return os.MkdirAll(c.PIDDir, 0o700)
}

// PIDFilePath returns the path to the daemon PID file.
func (c *Config) PIDFilePath() string {
	return filepath.Join(c.PIDDir, "gossm.pid")
}

// SocketPath returns the path to the Unix socket for IPC.
func (c *Config) SocketPath() string {
	return filepath.Join(c.PIDDir, "gossm.sock")
}

// String returns a human-readable representation of the config.
func (c *Config) String() string {
	return fmt.Sprintf("DashboardPort=%d LogLevel=%s PIDDir=%s",
		c.DashboardPort, c.LogLevel, c.PIDDir)
}
