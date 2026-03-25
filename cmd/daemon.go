package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"

	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"

	awsutil "github.com/bovinemagnet/gossm/internal/aws"
	goconfig "github.com/bovinemagnet/gossm/internal/config"
	"github.com/bovinemagnet/gossm/internal/daemon"
	"github.com/bovinemagnet/gossm/internal/web"
)

var daemonForeground bool

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the gossm daemon",
	Long:  "Start, stop, or check status of the gossm background daemon that serves the HTMX dashboard.",
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the gossm daemon",
	Run:   runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the gossm daemon",
	Run:   runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	Run:   runDaemonStatus,
}

func init() {
	daemonStartCmd.Flags().BoolVar(&daemonForeground, "foreground", false, "run in foreground (do not fork)")

	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	rootCmd.AddCommand(daemonCmd)
}

func runDaemonStart(cmd *cobra.Command, args []string) {
	cfg := goconfig.Load()

	// Check if daemon is already running.
	if running, pid := daemon.IsRunning(cfg); running {
		fmt.Printf("Daemon is already running (PID %d)\n", pid)
		os.Exit(1)
	}

	if !daemonForeground {
		// Re-exec self with --foreground flag in the background.
		if err := daemon.ForkDaemon(os.Args[0], cfg); err != nil {
			log.Fatal().Err(err).Msg("failed to fork daemon")
		}
		fmt.Printf("Daemon started. Dashboard at http://localhost:%d\n", cfg.DashboardPort)
		return
	}

	// Foreground mode — run the daemon directly.
	d, err := daemon.Start(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to start daemon")
	}

	// Set up web server with EC2 client factory for instance picker.
	ec2Factory := func(ctx context.Context, profile string) (awsutil.EC2DescribeInstancesAPI, error) {
		awsCfg, err := awsConfig.LoadDefaultConfig(ctx, awsConfig.WithSharedConfigProfile(profile))
		if err != nil {
			return nil, err
		}
		return ec2.NewFromConfig(awsCfg), nil
	}
	srv := web.NewServer(d.SessionManager(), cfg, d.StartedAt(), ec2Factory)
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.DashboardPort),
		Handler: srv.Handler(),
	}

	fmt.Printf("gossm daemon running. Dashboard at http://localhost:%d\n", cfg.DashboardPort)

	// Handle shutdown signals.
	sigCh := make(chan os.Signal, 1)
	notifyShutdownSignals(sigCh)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	<-sigCh
	fmt.Println("\nShutting down...")
	httpServer.Close()
	d.Stop()
}

func runDaemonStop(cmd *cobra.Command, args []string) {
	cfg := goconfig.Load()

	running, _ := daemon.IsRunning(cfg)
	if !running {
		fmt.Println("Daemon is not running.")
		return
	}

	// Try graceful shutdown via IPC first.
	resp, err := daemon.IPCSend(cfg, daemon.IPCRequest{Action: "shutdown"})
	if err != nil {
		// IPC failed, try killing by PID.
		pid, pidErr := daemon.ReadPID(cfg)
		if pidErr != nil {
			fmt.Println("Cannot determine daemon PID:", pidErr)
			os.Exit(1)
		}
		proc, findErr := os.FindProcess(pid)
		if findErr != nil {
			fmt.Println("Cannot find daemon process:", findErr)
			os.Exit(1)
		}
		signalTerminate(proc)
		fmt.Println("Sent termination signal to daemon.")
		return
	}

	if resp.OK {
		fmt.Println("Daemon is shutting down.")
	} else {
		fmt.Println("Shutdown request failed:", resp.Error)
	}
}

func runDaemonStatus(cmd *cobra.Command, args []string) {
	cfg := goconfig.Load()

	running, pid := daemon.IsRunning(cfg)
	if !running {
		fmt.Println("Daemon is not running.")
		return
	}

	status, err := daemon.DaemonStatus(cfg)
	if err != nil {
		fmt.Printf("Daemon is running (PID %d) but could not query status: %v\n", pid, err)
		return
	}

	fmt.Printf("Daemon running (PID %d)\n", pid)
	fmt.Printf("  Dashboard: http://localhost:%d\n", status.Port)
	fmt.Printf("  Sessions:  %d active\n", status.SessionCount)
	fmt.Printf("  Uptime:    %s\n", status.Uptime)
}
