package aws

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// PromptProfile displays available AWS profiles and lets the user pick one.
func PromptProfile(scanner *bufio.Scanner) (string, error) {
	profiles, err := ParseAWSProfiles()
	if err != nil {
		return "", err
	}
	if len(profiles) == 0 {
		return "", fmt.Errorf("no profiles found in ~/.aws/config")
	}

	fmt.Println("Available AWS Profiles:")
	for i, p := range profiles {
		fmt.Printf("[%d]   %s\n", i+1, p)
	}
	fmt.Print("Select profile: [Q/q:Quit] > ")
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading input: %w", err)
	}

	text := strings.TrimSpace(scanner.Text())
	switch strings.ToLower(text) {
	case "q", "e":
		fmt.Println("Exiting...")
		os.Exit(0)
	}

	num, err := strconv.Atoi(text)
	if err != nil {
		return "", fmt.Errorf("invalid selection: %s", text)
	}
	if num < 1 || num > len(profiles) {
		return "", fmt.Errorf("selection %d out of range (1-%d)", num, len(profiles))
	}
	return profiles[num-1], nil
}
