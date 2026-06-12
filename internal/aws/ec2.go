// Package aws provides AWS EC2 instance discovery and display functions.
package aws

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

// GetInstances retrieves information about Amazon EC2 instances,
// following NextToken pagination so accounts with more instances than
// one DescribeInstances page are not silently truncated.
func GetInstances(c context.Context, api EC2DescribeInstancesAPI, input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	out, err := api.DescribeInstances(c, input)
	if err != nil || out == nil {
		return out, err
	}

	combined := *out
	for out.NextToken != nil && *out.NextToken != "" {
		next := *input
		next.NextToken = out.NextToken
		out, err = api.DescribeInstances(c, &next)
		if err != nil {
			return nil, err
		}
		combined.Reservations = append(combined.Reservations, out.Reservations...)
	}
	combined.NextToken = nil
	return &combined, nil
}

// instanceState safely extracts the state name; State is a pointer in
// the SDK and is not guaranteed non-nil.
func instanceState(inst types.Instance) string {
	if inst.State == nil {
		return ""
	}
	return string(inst.State.Name)
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
		log.Debug().Strs("filters", allFilters).Msg("applying filters")
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
			state := instanceState(inst)

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

// InstanceInfo holds structured EC2 instance data for the web dashboard.
type InstanceInfo struct {
	InstanceID   string
	InstanceName string
	State        string
	InstanceType string
	PrivateIP    string
	AZ           string
}

// ExtractInstances parses DescribeInstancesOutput into a slice of InstanceInfo.
func ExtractInstances(output *ec2.DescribeInstancesOutput) []InstanceInfo {
	if output == nil {
		return nil
	}
	var instances []InstanceInfo
	for _, r := range output.Reservations {
		for _, inst := range r.Instances {
			info := InstanceInfo{
				InstanceID:   SafeString(inst.InstanceId, "N/A"),
				InstanceName: GetValue("name", inst.Tags),
				State:        instanceState(inst),
				InstanceType: string(inst.InstanceType),
				PrivateIP:    SafeString(inst.PrivateIpAddress, ""),
			}
			if inst.Placement != nil {
				info.AZ = SafeString(inst.Placement.AvailabilityZone, "")
			}
			instances = append(instances, info)
		}
	}
	return instances
}

// LaunchOpts carries the parameters for launching an SSM session from the CLI.
type LaunchOpts struct {
	Profile    string
	InstanceID string
	Type       string // "shell" or "port-forward"
	LocalPort  int
	RemotePort int
	RemoteHost string
	// OnStart, if set, is invoked with the aws subprocess PID once the
	// session has started, before LaunchSession blocks waiting for it.
	OnStart func(pid int)
}

// execCommand builds the session subprocess; a seam for tests.
var execCommand = exec.Command

// LaunchSession executes the aws ssm start-session command for the given instance.
// It supports both shell and port-forward session types.
func LaunchSession(opts LaunchOpts) error {
	args := []string{"--profile", opts.Profile, "ssm", "start-session", "--target", opts.InstanceID}

	if opts.Type == "port-forward" {
		args = append(args, "--document-name", "AWS-StartPortForwardingSessionToRemoteHost")
		host := opts.RemoteHost
		if host == "" {
			host = "localhost"
		}
		args = append(args, "--parameters", fmt.Sprintf(
			`{"portNumber":["%d"],"localPortNumber":["%d"],"host":["%s"]}`,
			opts.RemotePort, opts.LocalPort, host,
		))
	}

	log.Debug().Str("command", "aws "+strings.Join(args, " ")).Msg("launching session")
	cmd := execCommand("aws", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// While the session runs, the AWS session-manager-plugin takes over the
	// terminal (raw mode) and forwards keystrokes such as Ctrl-C to the
	// remote shell. Divert those terminal signals away from gossm so that,
	// for example, a Ctrl-C interrupts the command running on the remote
	// machine rather than killing gossm and tearing down the whole session.
	restore := ignoreSignals(terminalSignals())
	defer restore()

	if err := cmd.Start(); err != nil {
		return err
	}
	if opts.OnStart != nil && cmd.Process != nil {
		opts.OnStart(cmd.Process.Pid)
	}
	return cmd.Wait()
}

// ignoreSignals diverts the given signals away from gossm for the duration of
// a child SSM session and returns a function that restores their default
// disposition. Diverted signals are drained and discarded so the AWS plugin,
// which owns the terminal while the session is live, can handle them. The
// returned restore function is safe to call exactly once.
func ignoreSignals(sigs []os.Signal) func() {
	if len(sigs) == 0 {
		return func() {}
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ch:
				// Swallow: leave terminal handling to the plugin.
			case <-done:
				return
			}
		}
	}()

	return func() {
		signal.Stop(ch)
		close(done)
	}
}
