// Command driftgcp compares a Terraform state file against live GCP
// resources and reports drift.
package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rubika/terraform-drift-detector-gcp/internal/config"
	"github.com/rubika/terraform-drift-detector-gcp/internal/explain"
	"github.com/rubika/terraform-drift-detector-gcp/internal/output"
	"github.com/rubika/terraform-drift-detector-gcp/internal/scanner"
	"github.com/rubika/terraform-drift-detector-gcp/internal/watch"
	"github.com/rubika/terraform-drift-detector-gcp/internal/webserver"
)

func main() {
	exitCode := 0
	root := newRootCmd(&exitCode)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	os.Exit(exitCode)
}

func newRootCmd(exitCode *int) *cobra.Command {
	root := &cobra.Command{
		Use:   "driftgcp",
		Short: "Detect drift between Terraform state and live GCP resources",
	}
	root.AddCommand(newScanCmd(exitCode))
	root.AddCommand(newServeCmd())
	root.AddCommand(newWatchCmd())
	return root
}

func newServeCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start a local web dashboard for running scans in a browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			handler, err := webserver.NewHandler(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Printf("driftgcp dashboard listening on http://localhost%s\n", addr)
			return http.ListenAndServe(addr, handler)
		},
	}
	cmd.Flags().StringVar(&addr, "addr", ":8080", "address to listen on")
	return cmd
}

func newScanCmd(exitCode *int) *cobra.Command {
	var (
		statePath    string
		providerName string
		project      string
		cloudFixture string
		outputFormat string
		explainFlag  bool
		configPath   string
	)

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan a Terraform state file for drift against GCP",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath != "" {
				cfg, err := config.Load(configPath)
				if err != nil {
					return err
				}
				if !cmd.Flags().Changed("state") && cfg.State != "" {
					statePath = cfg.State
				}
				if !cmd.Flags().Changed("provider") && cfg.Provider != "" {
					providerName = cfg.Provider
				}
				if !cmd.Flags().Changed("project") && cfg.Project != "" {
					project = cfg.Project
				}
				if !cmd.Flags().Changed("cloud-fixture") && cfg.CloudFixture != "" {
					cloudFixture = cfg.CloudFixture
				}
				if !cmd.Flags().Changed("output") && cfg.Output != "" {
					outputFormat = cfg.Output
				}
				if !cmd.Flags().Changed("explain") && cfg.Explain {
					explainFlag = true
				}
			}

			if statePath == "" {
				return fmt.Errorf("--state (or config `state`) is required")
			}

			report, err := scanner.Run(cmd.Context(), scanner.Options{
				StatePath:    statePath,
				Provider:     providerName,
				Project:      project,
				CloudFixture: cloudFixture,
			})
			if err != nil {
				return err
			}

			if err := output.Format(os.Stdout, report, outputFormat); err != nil {
				return err
			}

			if explainFlag {
				summary, err := explain.Explain(cmd.Context(), report)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: --explain failed: %v\n", err)
				} else {
					fmt.Println("\nCLAUDE SUMMARY")
					fmt.Println(summary)
				}
			}

			if report.Status != "clean" {
				*exitCode = 1
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&statePath, "state", "", "path to Terraform state file")
	cmd.Flags().StringVar(&providerName, "provider", "gcp", "cloud provider to check against: gcp or file")
	cmd.Flags().StringVar(&project, "project", "", "GCP project ID (required for --provider=gcp)")
	cmd.Flags().StringVar(&cloudFixture, "cloud-fixture", "", "path to a JSON actual-resources fixture (required for --provider=file)")
	cmd.Flags().StringVar(&outputFormat, "output", "table", "output format: table or json")
	cmd.Flags().BoolVar(&explainFlag, "explain", false, "ask Claude for a plain-English summary and remediation steps")
	cmd.Flags().StringVar(&configPath, "config", "", "path to a driftgcp.yaml config file")

	return cmd
}

// newWatchCmd runs scans on a fixed interval instead of once: it logs only
// the drift that's new since the previous run, and posts to a webhook when
// a run finds critical drift. Stops cleanly on SIGINT/SIGTERM.
func newWatchCmd() *cobra.Command {
	var (
		statePath    string
		providerName string
		project      string
		cloudFixture string
		configPath   string
		webhookURL   string
		interval     time.Duration
	)

	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Re-scan on an interval, logging new drift and alerting a webhook on critical findings",
		RunE: func(cmd *cobra.Command, args []string) error {
			if configPath != "" {
				cfg, err := config.Load(configPath)
				if err != nil {
					return err
				}
				if !cmd.Flags().Changed("state") && cfg.State != "" {
					statePath = cfg.State
				}
				if !cmd.Flags().Changed("provider") && cfg.Provider != "" {
					providerName = cfg.Provider
				}
				if !cmd.Flags().Changed("project") && cfg.Project != "" {
					project = cfg.Project
				}
				if !cmd.Flags().Changed("cloud-fixture") && cfg.CloudFixture != "" {
					cloudFixture = cfg.CloudFixture
				}
			}
			if statePath == "" {
				return fmt.Errorf("--state (or config `state`) is required")
			}
			if webhookURL == "" {
				webhookURL = os.Getenv("DRIFTGCP_WEBHOOK_URL")
			}

			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			fmt.Printf("driftgcp watch: scanning %s every %s (ctrl-c to stop)\n", statePath, interval)

			watch.Run(ctx, watch.Options{
				Scan: scanner.Options{
					StatePath:    statePath,
					Provider:     providerName,
					Project:      project,
					CloudFixture: cloudFixture,
				},
				Interval:   interval,
				WebhookURL: webhookURL,
			}, func(line string) { fmt.Println(line) })

			fmt.Println("driftgcp watch: stopping")
			return nil
		},
	}

	cmd.Flags().StringVar(&statePath, "state", "", "path to Terraform state file")
	cmd.Flags().StringVar(&providerName, "provider", "gcp", "cloud provider to check against: gcp or file")
	cmd.Flags().StringVar(&project, "project", "", "GCP project ID (required for --provider=gcp)")
	cmd.Flags().StringVar(&cloudFixture, "cloud-fixture", "", "path to a JSON actual-resources fixture (required for --provider=file)")
	cmd.Flags().StringVar(&configPath, "config", "", "path to a driftgcp.yaml config file")
	cmd.Flags().StringVar(&webhookURL, "webhook", "", "webhook URL to notify on critical drift (default: DRIFTGCP_WEBHOOK_URL env var)")
	cmd.Flags().DurationVar(&interval, "interval", 15*time.Minute, "how often to re-scan")

	return cmd
}
