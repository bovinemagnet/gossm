package aws

import (
	"bufio"
	"strings"
	"testing"
)

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
