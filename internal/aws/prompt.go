package aws

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// promptAction classifies a line of prompt input.
type promptAction int

const (
	actionReset  promptAction = iota // empty input: show the full list again
	actionQuit                       // q/Q/e/E: exit gossm
	actionSelect                     // a number: pick from the displayed list
	actionFilter                     // any other text: filter the list
)

// parsePromptInput classifies trimmed prompt input. For actionSelect the
// second return value is the parsed number; otherwise it is 0.
func parsePromptInput(text string) (promptAction, int) {
	text = strings.TrimSpace(text)
	if text == "" {
		return actionReset, 0
	}
	switch strings.ToLower(text) {
	case "q", "e":
		return actionQuit, 0
	}
	if num, err := strconv.Atoi(text); err == nil {
		return actionSelect, num
	}
	return actionFilter, 0
}

// filterIndices returns the indices of names containing filter
// (case-insensitive substring). An empty filter matches everything.
func filterIndices(names []string, filter string) []int {
	if filter == "" {
		indices := make([]int, len(names))
		for i := range names {
			indices[i] = i
		}
		return indices
	}
	var indices []int
	filter = strings.ToLower(filter)
	for i, name := range names {
		if strings.Contains(strings.ToLower(name), filter) {
			indices = append(indices, i)
		}
	}
	return indices
}

// selectFromList runs an interactive pick loop over names. Each iteration it
// calls render with the indices of the currently visible entries (the caller
// numbers them 1..len(visible)), then prompts. Typing a number selects from
// the visible entries and returns the original index into names; other text
// filters the full list; an empty line resets the filter; q/Q quits gossm.
// Returns an error if input cannot be read (e.g. EOF).
func selectFromList(scanner *bufio.Scanner, label string, names []string, render func(visible []int)) (int, error) {
	visible := filterIndices(names, "")
	for {
		render(visible)
		fmt.Printf("%s: [number, text:filter, Q/q:Quit] > ", label)
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return 0, fmt.Errorf("error reading input: %w", err)
			}
			return 0, fmt.Errorf("input closed")
		}

		text := strings.TrimSpace(scanner.Text())
		action, num := parsePromptInput(text)
		switch action {
		case actionQuit:
			fmt.Println("Exiting...")
			os.Exit(0)
		case actionReset:
			visible = filterIndices(names, "")
		case actionSelect:
			if num < 1 || num > len(visible) {
				fmt.Printf("selection %d out of range (1-%d)\n", num, len(visible))
				continue
			}
			return visible[num-1], nil
		case actionFilter:
			matched := filterIndices(names, text)
			if len(matched) == 0 {
				fmt.Printf("No matches for %q — showing all.\n", text)
				matched = filterIndices(names, "")
			}
			visible = matched
		}
	}
}
