//     GNU GENERAL PUBLIC LICENSE
//     Version 3, 29 June 2007
//
// Copyright (C) 2007 Free Software Foundation, Inc. <https://fsf.org/>
// Everyone is permitted to copy and distribute verbatim copies
// of this license document, but changing it is not allowed.
//
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/spf13/pflag"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

var cmdProfile *string = pflag.StringP("profile", "p", os.Getenv("AWS_PROFILE"), "set the flag for the aws profile, otherwise it defaults to env.<AWS_PROFILE>:[env."+os.Getenv("AWS_PROFILE")+"]")
var cmdFilters *[]string = pflag.StringSliceP("filter", "f", []string{}, "set the flag for the filter, as a comma separated list eg: `-f value1,value2`")
var nameString string = "tag:Name"
var cmdLogger *bool = pflag.BoolP("logging", "l", false, "turn on logging")
var cmdAvailabilityZone *bool = pflag.BoolP("az", "z", false, "display availability zone, default false")
var cmdPrivateIp *bool = pflag.BoolP("ip", "i", false, "display internal ip address, default false")
var cmdInstanceType *bool = pflag.BoolP("type", "y", false, "display instance type, default false")
var cmdVersion *bool = pflag.BoolP("version", "v", false, "display version and exit")
var cmdTarget *string = pflag.StringP("target", "t", "", "connect directly to an instance ID, skipping the instance list")

type instancePosition struct {
	num              int
	reservationCount int
	instanceCount    int
	instanceId       string
	instanceName     string
}

// displayOptions controls which optional columns are shown in the instance listing.
type displayOptions struct {
	showInstanceType    bool
	showAvailabilityZone bool
	showPrivateIp       bool
}

// EC2DescribeInstancesAPI defines the interface for the DescribeInstances function.
// We use this interface to test the function using a mocked service.
type EC2DescribeInstancesAPI interface {
	DescribeInstances(ctx context.Context,
		params *ec2.DescribeInstancesInput,
		optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// GetInstances retrieves information about your Amazon Elastic Compute Cloud (Amazon EC2) instances.
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

// buildFilters creates the EC2 filter list from flag filters and positional arguments.
// It always includes a filter for running instances.
func buildFilters(flagFilters []string, positionalArgs []string) []types.Filter {
	// Always filter to running instances only.
	stateFilterName := "instance-state-name"
	filters := []types.Filter{
		{Name: &stateFilterName, Values: []string{"running"}},
	}

	// Merge flag filters and positional args.
	allFilters := append([]string{}, flagFilters...)
	allFilters = append(allFilters, positionalArgs...)

	if len(allFilters) > 0 {
		fmt.Println("Applying Filters:", allFilters)
		// Wrap each value with wildcards for substring matching.
		wildcarded := make([]string, len(allFilters))
		for i, v := range allFilters {
			wildcarded[i] = "*" + v + "*"
		}
		filters = append(filters, types.Filter{Name: &nameString, Values: wildcarded})
	}

	return filters
}

// safeString dereferences a string pointer, returning a fallback if nil.
func safeString(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// listInstances prints the instance list using tabwriter and returns the position map.
func listInstances(result *ec2.DescribeInstancesOutput, opts displayOptions) map[int]instancePosition {
	instancePositions := make(map[int]instancePosition)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	counter := 0
	reservationCount := 0
	for _, r := range result.Reservations {
		instanceCount := 0
		reservationCount++
		for _, inst := range r.Instances {
			instanceCount++
			counter++

			instanceId := safeString(inst.InstanceId, "N/A")
			instanceName := GetValue("name", inst.Tags)
			state := string(inst.State.Name)

			fmt.Fprintf(w, "[%d]\t%s\t%s\t%s", counter, instanceId, instanceName, state)

			if opts.showInstanceType {
				fmt.Fprintf(w, "\t%s", string(inst.InstanceType))
			}
			if opts.showAvailabilityZone {
				az := ""
				if inst.Placement != nil {
					az = safeString(inst.Placement.AvailabilityZone, "")
				}
				fmt.Fprintf(w, "\t%s", az)
			}
			if opts.showPrivateIp {
				fmt.Fprintf(w, "\t%s", safeString(inst.PrivateIpAddress, "N/A"))
			}
			fmt.Fprintln(w)

			instancePositions[counter] = instancePosition{
				num:              counter,
				reservationCount: reservationCount,
				instanceCount:    instanceCount,
				instanceId:       instanceId,
				instanceName:     instanceName,
			}
		}
	}
	w.Flush()

	return instancePositions
}

// promptUser asks the user to select an instance number. Returns the chosen number.
func promptUser(scanner *bufio.Scanner, max int) (int, error) {
	fmt.Print("Launch number: [Q/q:Quit] > ")
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading input: %w", err)
	}

	text := strings.TrimSpace(scanner.Text())
	log.Info().Msg("Selected: [" + text + "]")

	// Check for quit.
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

// launchSession executes the aws ssm start-session command for the given instance.
func launchSession(profile, instanceId string) error {
	fmt.Printf("running command:> aws --profile %s ssm start-session --target %s\n", profile, instanceId)
	cmd := exec.Command("aws", "--profile", profile, "ssm", "start-session", "--target", instanceId)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// parseAWSProfiles reads ~/.aws/config and returns a list of profile names.
func parseAWSProfiles() ([]string, error) {
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

// promptProfile displays available AWS profiles and lets the user pick one.
func promptProfile(scanner *bufio.Scanner) (string, error) {
	profiles, err := parseAWSProfiles()
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

func main() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	// Handle --version.
	if *cmdVersion {
		fmt.Printf("gossm version %s\n", version)
		os.Exit(0)
	}

	if *cmdLogger {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	}

	scanner := bufio.NewScanner(os.Stdin)

	// If no profile set, prompt the user to pick one.
	if *cmdProfile == "" {
		profile, err := promptProfile(scanner)
		if err != nil {
			log.Fatal().Msg(err.Error())
		}
		*cmdProfile = profile
	}

	// Set the environment variable for the AWS profile.
	os.Setenv("AWS_PROFILE", *cmdProfile)

	// Direct-connect mode: skip listing, connect immediately.
	if *cmdTarget != "" {
		profile := os.Getenv("AWS_PROFILE")
		if err := launchSession(profile, *cmdTarget); err != nil {
			log.Fatal().Msg(err.Error())
		}
		return
	}

	// Build filters from both -f flag and positional arguments.
	filters := buildFilters(*cmdFilters, pflag.Args())

	cfg, err := awsConfig.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal().Msg("configuration error: " + err.Error())
	}

	client := ec2.NewFromConfig(cfg)

	// Use paginator to handle large result sets.
	input := &ec2.DescribeInstancesInput{Filters: filters}
	paginator := ec2.NewDescribeInstancesPaginator(client, input)

	// Collect all reservations across pages.
	var allReservations []types.Reservation
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.TODO())
		if err != nil {
			fmt.Println("ERROR: Got an error retrieving information about your Amazon EC2 instances:")
			fmt.Println(err)
			return
		}
		allReservations = append(allReservations, page.Reservations...)
	}

	combinedResult := &ec2.DescribeInstancesOutput{Reservations: allReservations}

	opts := displayOptions{
		showInstanceType:    *cmdInstanceType,
		showAvailabilityZone: *cmdAvailabilityZone,
		showPrivateIp:       *cmdPrivateIp,
	}

	instancePositions := listInstances(combinedResult, opts)

	if len(instancePositions) == 0 {
		fmt.Println("No running instances found.")
		return
	}

	launchNumber, err := promptUser(scanner, len(instancePositions))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	log.Info().Msg("Selected Instance: " + instancePositions[launchNumber].instanceId)

	profile := os.Getenv("AWS_PROFILE")
	if err := launchSession(profile, instancePositions[launchNumber].instanceId); err != nil {
		log.Fatal().Msg(err.Error())
	}
}
