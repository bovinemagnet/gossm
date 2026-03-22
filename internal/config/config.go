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

// Config holds the application configuration.
type Config struct {
	DashboardPort int
	LogLevel      string
	PIDDir        string
}

// DefaultConfig returns a Config with compiled defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		DashboardPort: 8443,
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
