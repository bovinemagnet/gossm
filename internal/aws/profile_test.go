package aws

import (
	"os"
	"path/filepath"
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
