package aws

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ParseAWSProfiles reads ~/.aws/config and returns a list of profile names.
func ParseAWSProfiles() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	configPath := filepath.Join(home, ".aws", "config")
	f, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", configPath, err)
	}
	defer f.Close()

	var profiles []string
	profileRegex := regexp.MustCompile(`^\[profile\s+(.+)\]$`)
	defaultRegex := regexp.MustCompile(`^\[default\]$`)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if matches := profileRegex.FindStringSubmatch(line); matches != nil {
			profiles = append(profiles, matches[1])
		} else if defaultRegex.MatchString(line) {
			profiles = append(profiles, "default")
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading %s: %w", configPath, err)
	}
	return profiles, nil
}

// PromptProfile displays available AWS profiles and lets the user pick one,
// filtering the list as they type.
func PromptProfile(scanner *bufio.Scanner) (string, error) {
	profiles, err := ParseAWSProfiles()
	if err != nil {
		return "", err
	}
	if len(profiles) == 0 {
		return "", fmt.Errorf("no profiles found in ~/.aws/config")
	}
	return promptProfileFrom(scanner, profiles)
}

// promptProfileFrom runs the interactive profile pick loop over the given
// profile names.
func promptProfileFrom(scanner *bufio.Scanner, profiles []string) (string, error) {
	render := func(visible []int) {
		fmt.Println("Available AWS Profiles:")
		for i, idx := range visible {
			fmt.Printf("[%d]   %s\n", i+1, profiles[idx])
		}
	}

	idx, err := selectFromList(scanner, "Select profile", profiles, render)
	if err != nil {
		return "", err
	}
	return profiles[idx], nil
}
