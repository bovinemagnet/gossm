package aws

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParseAWSProfiles_HonoursAWSConfigFile verifies the AWS_CONFIG_FILE
// environment variable overrides the default ~/.aws/config location, as
// it does for the AWS CLI and SDK.
func TestParseAWSProfiles_HonoursAWSConfigFile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "custom-config")
	content := "[default]\nregion = eu-west-1\n\n[profile staging]\nregion = eu-west-2\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)

	profiles, err := ParseAWSProfiles()
	if err != nil {
		t.Fatalf("ParseAWSProfiles: %v", err)
	}
	want := map[string]bool{"default": false, "staging": false}
	for _, p := range profiles {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("profile %q not found in %v", name, profiles)
		}
	}
	if len(profiles) != 2 {
		t.Errorf("got %d profiles %v, want 2 from the overridden config file", len(profiles), profiles)
	}
}

// TestParseAWSProfiles_DedupesCaseInsensitiveAliases verifies that profiles
// differing only by case (e.g. ACU and acu, which are commonly defined as
// aliases pointing at the same account) collapse to a single entry, keeping
// the casing of the first occurrence in the config file.
func TestParseAWSProfiles_DedupesCaseInsensitiveAliases(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "custom-config")
	content := "[default]\n\n[profile ACU]\nregion = ap-southeast-2\n\n[profile acu]\nregion = ap-southeast-2\n\n[profile UQ]\n\n[profile uq]\n\n[profile Unique]\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("AWS_CONFIG_FILE", configPath)

	profiles, err := ParseAWSProfiles()
	if err != nil {
		t.Fatalf("ParseAWSProfiles: %v", err)
	}

	want := []string{"default", "ACU", "UQ", "Unique"}
	if len(profiles) != len(want) {
		t.Fatalf("got %d profiles %v, want %d %v", len(profiles), profiles, len(want), want)
	}
	for i, name := range want {
		if profiles[i] != name {
			t.Errorf("profiles[%d] = %q, want %q (full list %v)", i, profiles[i], name, profiles)
		}
	}
}

func TestPromptProfileFrom_DirectSelection(t *testing.T) {
	profiles := []string{"default", "prd-web", "dev-web"}
	scanner := bufio.NewScanner(strings.NewReader("2\n"))
	profile, err := promptProfileFrom(scanner, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "prd-web" {
		t.Errorf("promptProfileFrom = %q, want prd-web", profile)
	}
}

func TestPromptProfileFrom_FilterThenSelect(t *testing.T) {
	profiles := []string{"default", "prd-web", "prd-db", "dev-web"}
	scanner := bufio.NewScanner(strings.NewReader("prd\n2\n"))
	profile, err := promptProfileFrom(scanner, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "prd-db" {
		t.Errorf("promptProfileFrom = %q, want prd-db", profile)
	}
}

func TestPromptProfileFrom_ResetThenSelect(t *testing.T) {
	profiles := []string{"default", "prd-web", "dev-web"}
	scanner := bufio.NewScanner(strings.NewReader("prd\n\n3\n"))
	profile, err := promptProfileFrom(scanner, profiles)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile != "dev-web" {
		t.Errorf("promptProfileFrom = %q, want dev-web", profile)
	}
}

func TestPromptProfileFrom_EOFReturnsError(t *testing.T) {
	profiles := []string{"default"}
	scanner := bufio.NewScanner(strings.NewReader(""))
	_, err := promptProfileFrom(scanner, profiles)
	if err == nil {
		t.Error("expected error on EOF")
	}
}
