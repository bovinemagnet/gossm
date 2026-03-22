package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// mockEC2Client implements EC2DescribeInstancesAPI for testing.
type mockEC2Client struct {
	output *ec2.DescribeInstancesOutput
	err    error
}

func (m *mockEC2Client) DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return m.output, m.err
}

func TestGetValue_FoundKey(t *testing.T) {
	tags := []types.Tag{
		{Key: aws.String("Environment"), Value: aws.String("production")},
		{Key: aws.String("Name"), Value: aws.String("web-server-01")},
	}
	result := GetValue("Name", tags)
	if result != "web-server-01" {
		t.Errorf("expected 'web-server-01', got '%s'", result)
	}
}

func TestGetValue_CaseInsensitive(t *testing.T) {
	tags := []types.Tag{
		{Key: aws.String("name"), Value: aws.String("web-server-01")},
	}
	result := GetValue("Name", tags)
	if result != "web-server-01" {
		t.Errorf("expected 'web-server-01', got '%s'", result)
	}
}

func TestGetValue_MissingKey(t *testing.T) {
	tags := []types.Tag{
		{Key: aws.String("Environment"), Value: aws.String("production")},
	}
	result := GetValue("Name", tags)
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestGetValue_EmptyTags(t *testing.T) {
	result := GetValue("Name", []types.Tag{})
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestGetValue_NilTags(t *testing.T) {
	result := GetValue("Name", nil)
	if result != "" {
		t.Errorf("expected empty string, got '%s'", result)
	}
}

func TestGetValue_NilKeyOrValue(t *testing.T) {
	tags := []types.Tag{
		{Key: nil, Value: aws.String("val")},
		{Key: aws.String("Name"), Value: nil},
	}
	result := GetValue("Name", tags)
	if result != "" {
		t.Errorf("expected empty string for nil Value, got '%s'", result)
	}
}

func TestBuildFilters_NoFilters(t *testing.T) {
	filters := buildFilters([]string{}, []string{})
	if len(filters) != 1 {
		t.Fatalf("expected 1 filter (running state), got %d", len(filters))
	}
	if *filters[0].Name != "instance-state-name" {
		t.Errorf("expected 'instance-state-name' filter, got '%s'", *filters[0].Name)
	}
	if filters[0].Values[0] != "running" {
		t.Errorf("expected 'running' value, got '%s'", filters[0].Values[0])
	}
}

func TestBuildFilters_FlagFilters(t *testing.T) {
	filters := buildFilters([]string{"prd", "web"}, []string{})
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	nameFilter := filters[1]
	if *nameFilter.Name != "tag:Name" {
		t.Errorf("expected 'tag:Name' filter, got '%s'", *nameFilter.Name)
	}
	if len(nameFilter.Values) != 2 {
		t.Fatalf("expected 2 filter values, got %d", len(nameFilter.Values))
	}
	if nameFilter.Values[0] != "*prd*" {
		t.Errorf("expected '*prd*', got '%s'", nameFilter.Values[0])
	}
	if nameFilter.Values[1] != "*web*" {
		t.Errorf("expected '*web*', got '%s'", nameFilter.Values[1])
	}
}

func TestBuildFilters_PositionalArgs(t *testing.T) {
	filters := buildFilters([]string{}, []string{"staging"})
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	if filters[1].Values[0] != "*staging*" {
		t.Errorf("expected '*staging*', got '%s'", filters[1].Values[0])
	}
}

func TestBuildFilters_MergesFlagAndPositional(t *testing.T) {
	filters := buildFilters([]string{"prd"}, []string{"web"})
	if len(filters) != 2 {
		t.Fatalf("expected 2 filters, got %d", len(filters))
	}
	nameFilter := filters[1]
	if len(nameFilter.Values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(nameFilter.Values))
	}
	if nameFilter.Values[0] != "*prd*" || nameFilter.Values[1] != "*web*" {
		t.Errorf("expected '*prd*' and '*web*', got '%s' and '%s'", nameFilter.Values[0], nameFilter.Values[1])
	}
}

func TestSafeString_NonNil(t *testing.T) {
	s := "hello"
	if result := safeString(&s, "fallback"); result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
}

func TestSafeString_Nil(t *testing.T) {
	if result := safeString(nil, "fallback"); result != "fallback" {
		t.Errorf("expected 'fallback', got '%s'", result)
	}
}

func TestGetInstances_MockSuccess(t *testing.T) {
	mock := &mockEC2Client{
		output: &ec2.DescribeInstancesOutput{
			Reservations: []types.Reservation{
				{
					Instances: []types.Instance{
						{
							InstanceId: aws.String("i-1234567890abcdef0"),
							Tags: []types.Tag{
								{Key: aws.String("Name"), Value: aws.String("test-server")},
							},
							State: &types.InstanceState{Name: types.InstanceStateNameRunning},
						},
					},
				},
			},
		},
	}

	result, err := GetInstances(context.TODO(), mock, &ec2.DescribeInstancesInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Reservations) != 1 {
		t.Fatalf("expected 1 reservation, got %d", len(result.Reservations))
	}
	if *result.Reservations[0].Instances[0].InstanceId != "i-1234567890abcdef0" {
		t.Error("instance ID mismatch")
	}
}

func TestGetInstances_MockError(t *testing.T) {
	mock := &mockEC2Client{
		err: fmt.Errorf("access denied"),
	}

	_, err := GetInstances(context.TODO(), mock, &ec2.DescribeInstancesInput{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "access denied" {
		t.Errorf("expected 'access denied', got '%s'", err.Error())
	}
}

func TestListInstances_ReturnsCorrectPositions(t *testing.T) {
	result := &ec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId: aws.String("i-aaa"),
						Tags:       []types.Tag{{Key: aws.String("Name"), Value: aws.String("server-a")}},
						State:      &types.InstanceState{Name: types.InstanceStateNameRunning},
						InstanceType: types.InstanceTypeT2Micro,
					},
					{
						InstanceId: aws.String("i-bbb"),
						Tags:       []types.Tag{{Key: aws.String("Name"), Value: aws.String("server-b")}},
						State:      &types.InstanceState{Name: types.InstanceStateNameRunning},
						InstanceType: types.InstanceTypeT2Small,
					},
				},
			},
		},
	}

	positions := listInstances(result, displayOptions{})
	if len(positions) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(positions))
	}
	if positions[1].instanceId != "i-aaa" {
		t.Errorf("expected 'i-aaa', got '%s'", positions[1].instanceId)
	}
	if positions[2].instanceId != "i-bbb" {
		t.Errorf("expected 'i-bbb', got '%s'", positions[2].instanceId)
	}
	if positions[1].instanceName != "server-a" {
		t.Errorf("expected 'server-a', got '%s'", positions[1].instanceName)
	}
}

func TestListInstances_NilFields(t *testing.T) {
	result := &ec2.DescribeInstancesOutput{
		Reservations: []types.Reservation{
			{
				Instances: []types.Instance{
					{
						InstanceId:       nil,
						Tags:             nil,
						State:            &types.InstanceState{Name: types.InstanceStateNameRunning},
						PrivateIpAddress: nil,
						Placement:        nil,
					},
				},
			},
		},
	}

	// Should not panic with nil fields.
	positions := listInstances(result, displayOptions{
		showPrivateIp:       true,
		showAvailabilityZone: true,
		showInstanceType:    true,
	})
	if len(positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(positions))
	}
	if positions[1].instanceId != "N/A" {
		t.Errorf("expected 'N/A' for nil instanceId, got '%s'", positions[1].instanceId)
	}
}
