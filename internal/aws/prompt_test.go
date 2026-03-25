package aws

import (
	"bufio"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// --- PromptUser tests ---

func TestPromptUser_ValidSelection(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("2\n"))
	num, err := PromptUser(scanner, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != 2 {
		t.Errorf("PromptUser = %d, want 2", num)
	}
}

func TestPromptUser_FirstItem(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("1\n"))
	num, err := PromptUser(scanner, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != 1 {
		t.Errorf("PromptUser = %d, want 1", num)
	}
}

func TestPromptUser_LastItem(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("5\n"))
	num, err := PromptUser(scanner, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != 5 {
		t.Errorf("PromptUser = %d, want 5", num)
	}
}

func TestPromptUser_OutOfRange(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
	}{
		{"zero", "0\n", 5},
		{"negative", "-1\n", 5},
		{"too high", "6\n", 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			scanner := bufio.NewScanner(strings.NewReader(tc.input))
			_, err := PromptUser(scanner, tc.max)
			if err == nil {
				t.Error("expected error for out-of-range input")
			}
		})
	}
}

func TestPromptUser_InvalidInput(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("abc\n"))
	_, err := PromptUser(scanner, 5)
	if err == nil {
		t.Error("expected error for non-numeric input")
	}
}

func TestPromptUser_WhitespaceInput(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("  3  \n"))
	num, err := PromptUser(scanner, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != 3 {
		t.Errorf("PromptUser = %d, want 3", num)
	}
}

// --- ListInstances with DisplayOptions ---

func TestListInstances_WithDisplayOptions(t *testing.T) {
	output := &ec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId:       aws.String("i-test001"),
						Tags:             []types.Tag{{Key: aws.String("Name"), Value: aws.String("test-server")}},
						State:            &types.InstanceState{Name: types.InstanceStateNameRunning},
						InstanceType:     types.InstanceTypeT3Micro,
						PrivateIpAddress: aws.String("10.0.0.5"),
						Placement:        &types.Placement{AvailabilityZone: aws.String("eu-west-1a")},
					},
				},
			},
		},
	}
	opts := DisplayOptions{
		ShowInstanceType:     true,
		ShowAvailabilityZone: true,
		ShowPrivateIP:        true,
	}
	positions := ListInstances(output, opts)
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[1].InstanceID != "i-test001" {
		t.Errorf("InstanceID = %q, want %q", positions[1].InstanceID, "i-test001")
	}
}

func TestListInstances_EmptyReservations(t *testing.T) {
	output := &ec2.DescribeInstancesOutput{}
	positions := ListInstances(output, DisplayOptions{})
	if len(positions) != 0 {
		t.Errorf("expected 0 positions, got %d", len(positions))
	}
}
