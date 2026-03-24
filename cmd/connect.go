package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	awsutil "github.com/bovinemagnet/gossm/internal/aws"
	goconfig "github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/daemon"
)

var (
	connectProfile          string
	connectFilters          []string
	connectTarget           string
	connectShowInstanceType bool
	connectShowAZ           bool
	connectShowIP           bool
	connectLocalPort        int
	connectRemotePort       int
	connectRemoteHost       string
)

var connectCmd = &cobra.Command{
	Use:   "connect [filter...]",
	Short: "Connect to an EC2 instance via SSM",
	Long:  "Lists running EC2 instances and launches an SSM session to the selected one. Positional arguments are used as tag name filters.",
	Run:   runConnect,
}

func init() {
	connectCmd.Flags().StringVarP(&connectProfile, "profile", "p", os.Getenv("AWS_PROFILE"), "AWS profile (defaults to $AWS_PROFILE)")
	connectCmd.Flags().StringSliceVarP(&connectFilters, "filter", "f", []string{}, "tag name filters (comma-separated)")
	connectCmd.Flags().StringVarP(&connectTarget, "target", "t", "", "connect directly to an instance ID")
	connectCmd.Flags().BoolVarP(&connectShowInstanceType, "type", "y", false, "display instance type")
	connectCmd.Flags().BoolVarP(&connectShowAZ, "az", "z", false, "display availability zone")
	connectCmd.Flags().BoolVarP(&connectShowIP, "ip", "i", false, "display private IP address")
	connectCmd.Flags().IntVar(&connectLocalPort, "local-port", 0, "local port for port forwarding")
	connectCmd.Flags().IntVar(&connectRemotePort, "remote-port", 0, "remote port for port forwarding")
	connectCmd.Flags().StringVar(&connectRemoteHost, "remote-host", "", "remote host for port forwarding (default: localhost)")

	rootCmd.AddCommand(connectCmd)
}

func runConnect(cmd *cobra.Command, args []string) {
	scanner := bufio.NewScanner(os.Stdin)

	// If no profile set, prompt the user to pick one.
	if connectProfile == "" {
		profile, err := awsutil.PromptProfile(scanner)
		if err != nil {
			log.Fatal().Msg(err.Error())
		}
		connectProfile = profile
	}

	os.Setenv("AWS_PROFILE", connectProfile)

	sessionType := "shell"
	if connectLocalPort != 0 || connectRemotePort != 0 {
		sessionType = "port-forward"
	}

	// Direct-connect mode.
	if connectTarget != "" {
		launchOpts := awsutil.LaunchOpts{
			Profile:    connectProfile,
			InstanceID: connectTarget,
			Type:       sessionType,
			LocalPort:  connectLocalPort,
			RemotePort: connectRemotePort,
			RemoteHost: connectRemoteHost,
		}
		if err := awsutil.LaunchSession(launchOpts); err != nil {
			log.Fatal().Msg(err.Error())
		}
		tryRegisterWithDaemon(daemon.RegisterOpts{
			InstanceID:  connectTarget,
			Profile:     connectProfile,
			SessionType: sessionType,
			LocalPort:   connectLocalPort,
			RemotePort:  connectRemotePort,
			RemoteHost:  connectRemoteHost,
		})
		return
	}

	// Build filters from both -f flag and positional arguments.
	filters := awsutil.BuildFilters(connectFilters, args)

	cfg, err := awsConfig.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal().Msg("configuration error: " + err.Error())
	}

	client := ec2.NewFromConfig(cfg)

	input := &ec2.DescribeInstancesInput{Filters: filters}
	paginator := ec2.NewDescribeInstancesPaginator(client, input)

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

	opts := awsutil.DisplayOptions{
		ShowInstanceType:     connectShowInstanceType,
		ShowAvailabilityZone: connectShowAZ,
		ShowPrivateIP:        connectShowIP,
	}

	instancePositions := awsutil.ListInstances(combinedResult, opts)

	if len(instancePositions) == 0 {
		fmt.Println("No running instances found.")
		return
	}

	launchNumber, err := awsutil.PromptUser(scanner, len(instancePositions))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	selected := instancePositions[launchNumber]
	log.Info().Msg("Selected Instance: " + selected.InstanceID)

	launchOpts := awsutil.LaunchOpts{
		Profile:    connectProfile,
		InstanceID: selected.InstanceID,
		Type:       sessionType,
		LocalPort:  connectLocalPort,
		RemotePort: connectRemotePort,
		RemoteHost: connectRemoteHost,
	}
	if err := awsutil.LaunchSession(launchOpts); err != nil {
		log.Fatal().Msg(err.Error())
	}

	tryRegisterWithDaemon(daemon.RegisterOpts{
		InstanceID:   selected.InstanceID,
		InstanceName: selected.InstanceName,
		Profile:      connectProfile,
		SessionType:  sessionType,
		LocalPort:    connectLocalPort,
		RemotePort:   connectRemotePort,
		RemoteHost:   connectRemoteHost,
	})
}

// tryRegisterWithDaemon attempts to register this session with a running daemon.
// Silently does nothing if the daemon is not running.
func tryRegisterWithDaemon(opts daemon.RegisterOpts) {
	cfg := goconfig.Load()
	opts.PID = os.Getpid()
	err := daemon.RegisterWithDaemon(cfg, opts)
	if err != nil {
		log.Debug().Err(err).Msg("could not register with daemon (not running?)")
	}
}
