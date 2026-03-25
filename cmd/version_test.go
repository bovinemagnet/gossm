package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionDefaults(t *testing.T) {
	if Version != "dev" {
		t.Errorf("expected default Version to be %q, got %q", "dev", Version)
	}
	if Commit != "unknown" {
		t.Errorf("expected default Commit to be %q, got %q", "unknown", Commit)
	}
	if Date != "unknown" {
		t.Errorf("expected default Date to be %q, got %q", "unknown", Date)
	}
}

func TestVersionCommandOutput(t *testing.T) {
	// Temporarily set build vars so the output is deterministic.
	origVersion, origCommit, origDate := Version, Commit, Date
	Version, Commit, Date = "1.2.3", "abc1234", "2026-01-01T00:00:00Z"
	t.Cleanup(func() {
		Version, Commit, Date = origVersion, origCommit, origDate
	})

	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()

	expected := []string{
		"gossm version 1.2.3",
		"commit:  abc1234",
		"built:   2026-01-01T00:00:00Z",
		"go:      go",
		"os/arch:",
	}
	for _, want := range expected {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, out)
		}
	}
}
