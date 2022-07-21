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
	"strconv"
	"strings"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/spf13/pflag"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var cmdProfile *string = pflag.StringP("profile", "p", os.Getenv("AWS_PROFILE"), "set the flag for the aws profile, otherwise it defaults to env.<AWS_PROFILE>:[env."+os.Getenv("AWS_PROFILE")+"]")
var cmdFilters *[]string = pflag.StringSliceP("filter", "f", []string{}, "set the flag for the filter, as a comma separated list eg: `-f value1,value2`")
var nameString string = "tag:Name"
var cmdLogger *bool = pflag.BoolP("logging", "l", false, "turn on logging")
var cmdAvailabilityZone *bool = pflag.BoolP("az", "z", false, "display availability zone, default false")
var cmdPrivateIp *bool = pflag.BoolP("ip", "i", false, "display internal ip address, default false")
var cmdInstanceType *bool = pflag.BoolP("type", "y", false, "display instance type, default false")

type instancePosition struct {
	num              int
	reservationCount int
	instanceCount    int
	instanceId       string
	instanceName     string
}

// EC2DescribeInstancesAPI defines the interface for the DescribeInstances function.
// We use this interface to test the function using a mocked service.
type EC2DescribeInstancesAPI interface {
	DescribeInstances(ctx context.Context,
		params *ec2.DescribeInstancesInput,
		optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// GetInstances retrieves information about your Amazon Elastic Compute Cloud (Amazon EC2) instances.
// Inputs:
//     c is the context of the method call, which includes the AWS Region.
//     api is the interface that defines the method call.
//     input defines the input arguments to the service call.
// Output:
//     If success, a DescribeInstancesOutput object containing the result of the service call and nil.
//     Otherwise, nil and an error from the call to DescribeInstances.
func GetInstances(c context.Context, api EC2DescribeInstancesAPI, input *ec2.DescribeInstancesInput) (*ec2.DescribeInstancesOutput, error) {
	return api.DescribeInstances(c, input)
}

// function to take an array of Tag and return the value for the key
func GetValue(key string, tags []types.Tag) string {

	for _, tag := range tags {
		if strings.ToUpper(*tag.Key) == strings.ToUpper(key) {
			log.Info().Msg("Found tag: " + *tag.Key + ":" + *tag.Value)
			return *tag.Value
		}
	}
	// if the length is < 1 then return an empty string. else return the first value
	if len(tags) < 1 {
		log.Info().Msg("no tags found for key: " + key)
		return ""
	} else {
		log.Info().Msg("return first tag: " + *tags[0].Key + ":" + *tags[0].Value)
		return *tags[0].Value
	}

}

func main() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	if *cmdLogger {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	}

	// Set the environment variable for the AWS profile.
	if *cmdProfile != "" {
		os.Setenv("AWS_PROFILE", *cmdProfile)
	}

	// Check for filters.
	if len(*cmdFilters) > 0 {
		fmt.Println("Applying Filters:", *cmdFilters)
		// loop through array of strings, add * to each string.
		// as this allows the AWS filter to match the string.
		for i, v := range *cmdFilters {
			(*cmdFilters)[i] = "*" + v + "*"
		}
	}

	cfg, err := awsConfig.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal().Msg("configuration error" + err.Error())
		panic("configuration error, " + err.Error())
	}

	client := ec2.NewFromConfig(cfg)

	var input *ec2.DescribeInstancesInput

	// Create the input object for the DescribeInstances call.
	if len(*cmdFilters) > 0 {
		// if the filters slice is not empty, then create an ec2 filter list
		ec2Filters := make([]types.Filter, 1)
		ec2Filters[0] = types.Filter{Name: &nameString, Values: *cmdFilters}
		input = &ec2.DescribeInstancesInput{Filters: ec2Filters}
	} else {
		input = &ec2.DescribeInstancesInput{}
	}
	result, err := GetInstances(context.TODO(), client, input)
	if err != nil {
		fmt.Println("ERROR: Got an error retrieving information about your Amazon EC2 instances:")
		fmt.Println(err)
		return
	}

	// create a map to store the of instance information
	instancePositions := make(map[int]instancePosition)

	// Instance counter
	counter := 0
	// Reservation counter, as instances sit within reservations.
	reservationCount := 0
	// Loop through the reservations looking for instances.
	for _, r := range result.Reservations {
		//fmt.Println("Reservation ID: " + *r.ReservationId)
		//fmt.Println("Instance IDs:")
		instanceCount := 0
		reservationCount++
		for _, i := range r.Instances {
			instanceCount++
			counter++
			fmt.Printf("[%d]", counter)
			fmt.Print("   " + *i.InstanceId)
			fmt.Print("   " + GetValue("name", i.Tags))
			if *cmdInstanceType {
				fmt.Print("   " + i.InstanceType.Values()[0])
			}
			if *cmdAvailabilityZone {
				fmt.Print("   " + *i.Placement.AvailabilityZone)
			}
			// display internal IP address
			if *cmdPrivateIp {
				fmt.Print("   " + *i.PrivateIpAddress)
			}
			// create a new instancePosition
			instancePosition := instancePosition{num: counter, reservationCount: reservationCount, instanceCount: instanceCount, instanceId: *i.InstanceId, instanceName: GetValue("name", i.Tags)}
			// add instancePostion to the map
			instancePositions[counter] = instancePosition
		}
		fmt.Println("")
	}
	// Wait for user input before continuing
	fmt.Print("Launch number: [Q/q:Quit] > ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	err = scanner.Err()
	// check for error in input.
	if err != nil {
		log.Fatal().Msg(err.Error())
	}
	// Get the user input.
	log.Info().Msg("Selected: [" + scanner.Text() + "]")

	// get the integer from the user input.
	launchNumber := scanner.Text()
	//	if launch number is Q or q then exit.
	if launchNumber == "Q" || launchNumber == "q" || launchNumber == "e" || launchNumber == "E" {
		fmt.Println("Exiting...")
		os.Exit(0)
	}

	// validate that launchNumber is an integer
	// convert the string to an integer.
	launchNumberValue, err := strconv.Atoi(launchNumber)
	if err != nil {
		fmt.Printf("Invalid launch number: %s\n", launchNumber)
		// this catches cases of entering the wrong value
		log.Fatal().Msg(err.Error())
	}
	// validate that the integer is in the range of 1 to the number of instances.
	if launchNumberValue < 1 || launchNumberValue > counter {
		fmt.Printf("Invalid launch number: %d\n", launchNumberValue)
	}

	// get the instance ID from the result.
	log.Info().Msg("Selected Instance: " + instancePositions[launchNumberValue].instanceId)

	// get the environmental variable for AWS_PROFILE
	profile := os.Getenv("AWS_PROFILE")
	// launch ssm-agent to connect to instance
	fmt.Printf("running command:> aws --profile %s ssm start-session --target %s", profile, instancePositions[launchNumberValue].instanceId)
	fmt.Println()
	// run go.exec of the command line
	cmd := exec.Command("aws", "--profile", profile, "ssm", "start-session", "--target", instancePositions[launchNumberValue].instanceId)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// run the command
	err = cmd.Run()
	if err != nil {
		log.Fatal().Msg(err.Error())
	}

}
