package aws

import (
	"bytes"
	"bufio"
	"reflect"
	"strings"
	"testing"
)

// --- parsePromptInput tests ---

func TestParsePromptInput(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantAction promptAction
		wantNum    int
	}{
		{"empty resets", "", actionReset, 0},
		{"whitespace resets", "   ", actionReset, 0},
		{"q quits", "q", actionQuit, 0},
		{"Q quits", "Q", actionQuit, 0},
		{"e quits", "e", actionQuit, 0},
		{"E quits", "E", actionQuit, 0},
		{"number selects", "3", actionSelect, 3},
		{"padded number selects", "  2  ", actionSelect, 2},
		{"negative number selects", "-1", actionSelect, -1},
		{"text filters", "prod", actionFilter, 0},
		{"mixed text filters", "web1", actionFilter, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, num := parsePromptInput(tc.input)
			if action != tc.wantAction {
				t.Errorf("parsePromptInput(%q) action = %v, want %v", tc.input, action, tc.wantAction)
			}
			if num != tc.wantNum {
				t.Errorf("parsePromptInput(%q) num = %d, want %d", tc.input, num, tc.wantNum)
			}
		})
	}
}

// --- filterIndices tests ---

func TestFilterIndices(t *testing.T) {
	names := []string{"alpha", "prod-web", "prod-db", "Staging"}
	tests := []struct {
		name   string
		filter string
		want   []int
	}{
		{"empty filter returns all", "", []int{0, 1, 2, 3}},
		{"substring match", "prod", []int{1, 2}},
		{"case-insensitive", "STAG", []int{3}},
		{"no match", "zzz", nil},
		{"single match", "alpha", []int{0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterIndices(names, tc.filter)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("filterIndices(%v, %q) = %v, want %v", names, tc.filter, got, tc.want)
			}
		})
	}
}

// --- selectFromList tests ---

// recordingRender returns a render func that records each visible slice it is
// called with.
func recordingRender(calls *[][]int) func([]int) {
	return func(visible []int) {
		snapshot := append([]int(nil), visible...)
		*calls = append(*calls, snapshot)
	}
}

func TestSelectFromList_DirectSelection(t *testing.T) {
	names := []string{"alpha", "beta", "gamma"}
	var calls [][]int
	scanner := bufio.NewScanner(strings.NewReader("2\n"))
	idx, err := selectFromList(scanner, "Select", names, recordingRender(&calls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 {
		t.Errorf("selectFromList = %d, want 1", idx)
	}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], []int{0, 1, 2}) {
		t.Errorf("render calls = %v, want [[0 1 2]]", calls)
	}
}

func TestSelectFromList_FilterThenSelect(t *testing.T) {
	names := []string{"alpha", "prod-web", "prod-db"}
	var calls [][]int
	scanner := bufio.NewScanner(strings.NewReader("prod\n1\n"))
	idx, err := selectFromList(scanner, "Select", names, recordingRender(&calls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 {
		t.Errorf("selectFromList = %d, want 1 (prod-web)", idx)
	}
	want := [][]int{{0, 1, 2}, {1, 2}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("render calls = %v, want %v", calls, want)
	}
}

func TestSelectFromList_FilterResetSelect(t *testing.T) {
	names := []string{"alpha", "prod-web", "prod-db"}
	var calls [][]int
	scanner := bufio.NewScanner(strings.NewReader("prod\n\n3\n"))
	idx, err := selectFromList(scanner, "Select", names, recordingRender(&calls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 2 {
		t.Errorf("selectFromList = %d, want 2 (prod-db from full list)", idx)
	}
	want := [][]int{{0, 1, 2}, {1, 2}, {0, 1, 2}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("render calls = %v, want %v", calls, want)
	}
}

func TestSelectFromList_NoMatchResets(t *testing.T) {
	names := []string{"alpha", "beta"}
	var calls [][]int
	scanner := bufio.NewScanner(strings.NewReader("zzz\n2\n"))
	idx, err := selectFromList(scanner, "Select", names, recordingRender(&calls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 1 {
		t.Errorf("selectFromList = %d, want 1", idx)
	}
	want := [][]int{{0, 1}, {0, 1}}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("render calls = %v, want %v", calls, want)
	}
}

func TestSelectFromList_OutOfRangeLoops(t *testing.T) {
	names := []string{"alpha", "beta"}
	var calls [][]int
	scanner := bufio.NewScanner(strings.NewReader("99\n1\n"))
	idx, err := selectFromList(scanner, "Select", names, recordingRender(&calls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 0 {
		t.Errorf("selectFromList = %d, want 0", idx)
	}
}

func TestSelectFromList_OutOfRangeAgainstFilteredLength(t *testing.T) {
	// Filter narrows the list to 2 entries; "3" must be out of range even
	// though the full list has 3 entries.
	names := []string{"alpha", "prod-web", "prod-db"}
	var calls [][]int
	scanner := bufio.NewScanner(strings.NewReader("prod\n3\n2\n"))
	idx, err := selectFromList(scanner, "Select", names, recordingRender(&calls))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 2 {
		t.Errorf("selectFromList = %d, want 2 (prod-db)", idx)
	}
}

func TestSelectFromList_EOFReturnsError(t *testing.T) {
	names := []string{"alpha"}
	var calls [][]int
	scanner := bufio.NewScanner(strings.NewReader(""))
	_, err := selectFromList(scanner, "Select", names, recordingRender(&calls))
	if err == nil {
		t.Error("expected error on EOF")
	}
}

// --- renderInstances tests ---

func testInstances() []InstanceInfo {
	return []InstanceInfo{
		{InstanceID: "i-web001", InstanceName: "prod-web", State: "running", InstanceType: "t3.micro", PrivateIP: "10.0.0.5", AZ: "eu-west-1a"},
		{InstanceID: "i-db001", InstanceName: "prod-db", State: "running", InstanceType: "t3.small", PrivateIP: "", AZ: "eu-west-1b"},
		{InstanceID: "i-app001", InstanceName: "stage-app", State: "running", InstanceType: "t3.medium", PrivateIP: "10.0.0.7", AZ: "eu-west-1c"},
	}
}

func TestRenderInstances_AllColumns(t *testing.T) {
	var buf bytes.Buffer
	opts := DisplayOptions{ShowInstanceType: true, ShowAvailabilityZone: true, ShowPrivateIP: true}
	renderInstances(&buf, testInstances(), opts)
	out := buf.String()

	for _, want := range []string{"[1]", "[2]", "[3]", "i-web001", "prod-web", "t3.micro", "eu-west-1a", "10.0.0.5"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestRenderInstances_EmptyIPShowsNA(t *testing.T) {
	var buf bytes.Buffer
	renderInstances(&buf, testInstances(), DisplayOptions{ShowPrivateIP: true})
	if !strings.Contains(buf.String(), "N/A") {
		t.Errorf("expected N/A for empty private IP:\n%s", buf.String())
	}
}

func TestRenderInstances_OptionalColumnsHidden(t *testing.T) {
	var buf bytes.Buffer
	renderInstances(&buf, testInstances(), DisplayOptions{})
	out := buf.String()
	for _, unwanted := range []string{"t3.micro", "eu-west-1a", "10.0.0.5"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output should not contain %q with options off:\n%s", unwanted, out)
		}
	}
}

func TestRenderInstances_SubsetRenumbersFromOne(t *testing.T) {
	var buf bytes.Buffer
	renderInstances(&buf, testInstances()[2:], DisplayOptions{})
	out := buf.String()
	if !strings.Contains(out, "[1]") || strings.Contains(out, "[3]") {
		t.Errorf("subset should be numbered from [1]:\n%s", out)
	}
}

// --- PromptInstance tests ---

func TestPromptInstance_DirectSelection(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("2\n"))
	selected, err := PromptInstance(scanner, testInstances(), DisplayOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected.InstanceID != "i-db001" {
		t.Errorf("InstanceID = %q, want i-db001", selected.InstanceID)
	}
}

func TestPromptInstance_FilterByName(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("stage\n1\n"))
	selected, err := PromptInstance(scanner, testInstances(), DisplayOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected.InstanceID != "i-app001" {
		t.Errorf("InstanceID = %q, want i-app001", selected.InstanceID)
	}
}

func TestPromptInstance_FilterByInstanceID(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("i-db\n1\n"))
	selected, err := PromptInstance(scanner, testInstances(), DisplayOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selected.InstanceID != "i-db001" {
		t.Errorf("InstanceID = %q, want i-db001", selected.InstanceID)
	}
}

func TestPromptInstance_EOFReturnsError(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	_, err := PromptInstance(scanner, testInstances(), DisplayOptions{})
	if err == nil {
		t.Error("expected error on EOF")
	}
}
