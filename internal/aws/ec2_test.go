package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// --- GetValue tests ---

func TestGetValue_FoundKey(t *testing.T) {
	tags := []types.Tag{
		{Key: aws.String("Name"), Value: aws.String("my-instance")},
		{Key: aws.String("Env"), Value: aws.String("prod")},
	}
	got := GetValue("Name", tags)
	if got != "my-instance" {
		t.Errorf("GetValue(\"Name\") = %q, want %q", got, "my-instance")
	}
}

func TestGetValue_CaseInsensitive(t *testing.T) {
	tags := []types.Tag{
		{Key: aws.String("Name"), Value: aws.String("my-instance")},
	}
	got := GetValue("name", tags)
	if got != "my-instance" {
		t.Errorf("GetValue(\"name\") = %q, want %q", got, "my-instance")
	}
}

func TestGetValue_MissingKey(t *testing.T) {
	tags := []types.Tag{
		{Key: aws.String("Env"), Value: aws.String("prod")},
	}
	got := GetValue("Name", tags)
	if got != "" {
		t.Errorf("GetValue(\"Name\") = %q, want empty string", got)
	}
}

func TestGetValue_EmptyTags(t *testing.T) {
	got := GetValue("Name", []types.Tag{})
	if got != "" {
		t.Errorf("GetValue with empty tags = %q, want empty string", got)
	}
}

func TestGetValue_NilTags(t *testing.T) {
	got := GetValue("Name", nil)
	if got != "" {
		t.Errorf("GetValue with nil tags = %q, want empty string", got)
	}
}

func TestGetValue_NilKeyOrValue(t *testing.T) {
	tags := []types.Tag{
		{Key: nil, Value: aws.String("orphan-value")},
		{Key: aws.String("Name"), Value: nil},
	}
	got := GetValue("Name", tags)
	if got != "" {
		t.Errorf("GetValue with nil Key/Value = %q, want empty string", got)
	}
}

// --- BuildFilters tests ---

func TestBuildFilters_NoFilters(t *testing.T) {
	filters := BuildFilters(nil, nil)
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter (instance-state-name), got %d", len(filters))
	}
	if *filters[0].Name != "instance-state-name" {
		t.Errorf("first filter name = %q, want %q", *filters[0].Name, "instance-state-name")
	}
	if filters[0].Values[0] != "running" {
		t.Errorf("first filter value = %q, want %q", filters[0].Values[0], "running")
	}
}

func TestBuildFilters_FlagFilters(t *testing.T) {
	filters := BuildFilters([]string{"web"}, nil)
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	if filters[1].Values[0] != "*web*" {
		t.Errorf("name filter value = %q, want %q", filters[1].Values[0], "*web*")
	}
}

func TestBuildFilters_PositionalArgs(t *testing.T) {
	filters := BuildFilters(nil, []string{"api"})
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	if filters[1].Values[0] != "*api*" {
		t.Errorf("name filter value = %q, want %q", filters[1].Values[0], "*api*")
	}
}

func TestBuildFilters_MergesFlagAndPositional(t *testing.T) {
	filters := BuildFilters([]string{"web"}, []string{"api"})
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	nameFilter := filters[1]
	if len(nameFilter.Values) != 2 {
		t.Fatalf("expected 2 name filter values, got %d", len(nameFilter.Values))
	}
	if nameFilter.Values[0] != "*web*" {
		t.Errorf("first name filter = %q, want %q", nameFilter.Values[0], "*web*")
	}
	if nameFilter.Values[1] != "*api*" {
		t.Errorf("second name filter = %q, want %q", nameFilter.Values[1], "*api*")
	}
}

// --- SafeString tests ---

func TestSafeString_NonNil(t *testing.T) {
	s := "hello"
	got := SafeString(&s, "fallback")
	if got != "hello" {
		t.Errorf("SafeString = %q, want %q", got, "hello")
	}
}

func TestSafeString_Nil(t *testing.T) {
	got := SafeString(nil, "fallback")
	if got != "fallback" {
		t.Errorf("SafeString(nil) = %q, want %q", got, "fallback")
	}
}

// --- Mock for EC2DescribeInstancesAPI ---

type mockEC2API struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockEC2API) DescribeInstances(_ context.Context, _ *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

func TestGetInstances_MockSuccess(t *testing.T) {
	expected := &ec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{InstanceId: aws.String("i-12345")},
				},
			},
		},
	}
	api := &mockEC2API{output: expected}
	result, err := GetInstances(context.Background(), api, &ec2.DescribeInstancesInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Reservations) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(result.Reservations))
	}
	if *result.Reservations[0].Instances[0].InstanceId != "i-12345" {
		t.Errorf("instance id = %q, want %q", *result.Reservations[0].Instances[0].InstanceId, "i-12345")
	}
}

func TestGetInstances_MockError(t *testing.T) {
	api := &mockEC2API{err: fmt.Errorf("api failure")}
	_, err := GetInstances(context.Background(), api, &ec2.DescribeInstancesInput{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "api failure" {
		t.Errorf("error = %q, want %q", err.Error(), "api failure")
	}
}

// --- ListInstances tests ---

func TestListInstances_ReturnsCorrectPositions(t *testing.T) {
	output := &ec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId: aws.String("i-aaa"),
						Tags:       []types.Tag{{Key: aws.String("Name"), Value: aws.String("alpha")}},
						State:      &types.InstanceState{Name: types.InstanceStateNameRunning},
					},
					{
						InstanceId: aws.String("i-bbb"),
						Tags:       []types.Tag{{Key: aws.String("Name"), Value: aws.String("bravo")}},
						State:      &types.InstanceState{Name: types.InstanceStateNameRunning},
					},
				},
			},
		},
	}
	positions := ListInstances(output, DisplayOptions{})
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(positions))
	}
	if positions[1].InstanceID != "i-aaa" {
		t.Errorf("position 1 instance id = %q, want %q", positions[1].InstanceID, "i-aaa")
	}
	if positions[1].InstanceName != "alpha" {
		t.Errorf("position 1 name = %q, want %q", positions[1].InstanceName, "alpha")
	}
	if positions[2].InstanceID != "i-bbb" {
		t.Errorf("position 2 instance id = %q, want %q", positions[2].InstanceID, "i-bbb")
	}
	if positions[2].ReservationCount != 1 {
		t.Errorf("position 2 reservation count = %d, want 1", positions[2].ReservationCount)
	}
}

func TestListInstances_NilFields(t *testing.T) {
	output := &ec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId: nil,
						Tags:       nil,
						State:      &types.InstanceState{Name: types.InstanceStateNameRunning},
					},
				},
			},
		},
	}
	positions := ListInstances(output, DisplayOptions{})
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[1].InstanceID != "N/A" {
		t.Errorf("nil instance id should show %q, got %q", "N/A", positions[1].InstanceID)
	}
	if positions[1].InstanceName != "" {
		t.Errorf("nil tags should yield empty name, got %q", positions[1].InstanceName)
	}
}
