// Package aws provides AWS EC2 instance discovery and display functions.
package aws

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"text/tabwriter"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rs/zerolog/log"
)

var nameString = "tag:Name"

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

// renderInstances prints a numbered instance list using tabwriter. The list
// is numbered from 1, so a filtered subset renumbers from [1].
func renderInstances(w io.Writer, instances []InstanceInfo, opts DisplayOptions) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	for i, inst := range instances {
		fmt.Fprintf(tw, "[%d]\t%s\t%s\t%s", i+1, inst.InstanceID, inst.InstanceName, inst.State)

		if opts.ShowInstanceType {
			fmt.Fprintf(tw, "\t%s", inst.InstanceType)
		}
		if opts.ShowAvailabilityZone {
			fmt.Fprintf(tw, "\t%s", inst.AZ)
		}
		if opts.ShowPrivateIP {
			ip := inst.PrivateIP
			if ip == "" {
				ip = "N/A"
			}
			fmt.Fprintf(tw, "\t%s", ip)
		}
		fmt.Fprintln(tw)
	}
	tw.Flush()
}

// PromptInstance asks the user to select an instance, filtering the list by
// name or instance ID as they type. Returns the chosen instance.
func PromptInstance(scanner *bufio.Scanner, instances []InstanceInfo, opts DisplayOptions) (InstanceInfo, error) {
	names := make([]string, len(instances))
	for i, inst := range instances {
		names[i] = inst.InstanceName + " " + inst.InstanceID
	}

	render := func(visible []int) {
		subset := make([]InstanceInfo, len(visible))
		for i, idx := range visible {
			subset[i] = instances[idx]
		}
		renderInstances(os.Stdout, subset, opts)
	}

	idx, err := selectFromList(scanner, "Launch number", names, render)
	if err != nil {
		return InstanceInfo{}, err
	}
	log.Info().Msg("Selected Instance: " + instances[idx].InstanceID)
	return instances[idx], nil
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
				State:        string(inst.State.Name),
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
}

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

	fmt.Printf("running command:> aws %s\n", strings.Join(args, " "))
	cmd := exec.Command("aws", args...)
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

	return cmd.Run()
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
