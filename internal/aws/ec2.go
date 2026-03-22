// Package aws provides AWS EC2 instance discovery and display functions.
package aws

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rs/zerolog/log"
)

var nameString = "tag:Name"

// InstancePosition holds the display position and identity of an EC2 instance.
type InstancePosition struct {
	Num              int
	ReservationCount int
	InstanceCount    int
	InstanceID       string
	InstanceName     string
}

// DisplayOptions controls which optional columns are shown in the instance listing.
type DisplayOptions struct {
	ShowInstanceType     bool
	ShowAvailabilityZone bool
	ShowPrivateIP        bool
}

// EC2DescribeInstancesAPI defines the interface for the DescribeInstances function.
// We use this interface to test the function using a mocked service.
type EC2DescribeInstancesAPI interface {
	DescribeInstances(ctx context.Context,
		params *ec2.DescribeInstancesInput,
		optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// GetInstances retrieves information about Amazon EC2 instances.
func GetInstances(c context.Context, api EC2DescribeInstancesAPI, input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	return api.DescribeInstances(c, input)
}

// GetValue returns the value of a tag by key (case-insensitive).
// Returns an empty string if the key is not found or the tags slice is empty.
func GetValue(key string, tags []types.Tag) string {
	for _, tag := range tags {
		if tag.Key != nil && tag.Value != nil && strings.EqualFold(*tag.Key, key) {
			log.Info().Msg("Found tag: " + *tag.Key + ":" + *tag.Value)
			return *tag.Value
		}
	}
	return ""
}

// BuildFilters creates the EC2 filter list from flag filters and positional arguments.
// It always includes a filter for running instances.
func BuildFilters(flagFilters []string, positionalArgs []string) []types.Filter {
	stateFilterName := "instance-state-name"
	filters := []types.Filter{
		{Name: &stateFilterName, Values: []string{"running"}},
	}

	allFilters := append([]string{}, flagFilters...)
	allFilters = append(allFilters, positionalArgs...)

	if len(allFilters) > 0 {
		fmt.Println("Applying Filters:", allFilters)
		wildcarded := make([]string, len(allFilters))
		for i, v := range allFilters {
			wildcarded[i] = "*" + v + "*"
		}
		filters = append(filters, types.Filter{Name: &nameString, Values: wildcarded})
	}

	return filters
}

// SafeString dereferences a string pointer, returning a fallback if nil.
func SafeString(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// ListInstances prints the instance list using tabwriter and returns the position map.
func ListInstances(result *ec2.DescribeInstancesOutput, opts DisplayOptions) map[int]InstancePosition {
	instancePositions := make(map[int]InstancePosition)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	counter := 0
	reservationCount := 0
	for _, r := range result.Reservations {
		instanceCount := 0
		reservationCount++
		for _, inst := range r.Instances {
			instanceCount++
			counter++

			instanceID := SafeString(inst.InstanceId, "N/A")
			instanceName := GetValue("name", inst.Tags)
			state := string(inst.State.Name)

			fmt.Fprintf(w, "[%d]\t%s\t%s\t%s", counter, instanceID, instanceName, state)

			if opts.ShowInstanceType {
				fmt.Fprintf(w, "\t%s", string(inst.InstanceType))
			}
			if opts.ShowAvailabilityZone {
				az := ""
				if inst.Placement != nil {
					az = SafeString(inst.Placement.AvailabilityZone, "")
				}
				fmt.Fprintf(w, "\t%s", az)
			}
			if opts.ShowPrivateIP {
				fmt.Fprintf(w, "\t%s", SafeString(inst.PrivateIpAddress, "N/A"))
			}
			fmt.Fprintln(w)

			instancePositions[counter] = InstancePosition{
				Num:              counter,
				ReservationCount: reservationCount,
				InstanceCount:    instanceCount,
				InstanceID:       instanceID,
				InstanceName:     instanceName,
			}
		}
	}
	w.Flush()

	return instancePositions
}

// PromptUser asks the user to select an instance number. Returns the chosen number.
func PromptUser(scanner *bufio.Scanner, max int) (int, error) {
	fmt.Print("Launch number: [Q/q:Quit] > ")
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading input: %w", err)
	}

	text := strings.TrimSpace(scanner.Text())
	log.Info().Msg("Selected: [" + text + "]")

	switch strings.ToLower(text) {
	case "q", "e":
		fmt.Println("Exiting...")
		os.Exit(0)
	}

	num, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("invalid launch number: %s", text)
	}
	if num < 1 || num > max {
		return 0, fmt.Errorf("launch number %d out of range (1-%d)", num, max)
	}
	return num, nil
}

// LaunchSession executes the aws ssm start-session command for the given instance.
func LaunchSession(profile, instanceID string) error {
	fmt.Printf("running command:> aws --profile %s ssm start-session --target %s\n", profile, instanceID)
	cmd := exec.Command("aws", "--profile", profile, "ssm", "start-session", "--target", instanceID)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
