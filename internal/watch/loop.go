// Package watch runs the scan/log/alert loop shared by the CLI `driftgcp
// watch` command and the web dashboard's watch controls, so the two
// surfaces can't drift apart in behavior.
package watch

import (
	"context"
	"fmt"
	"time"

	"github.com/rubika/terraform-drift-detector-gcp/internal/alert"
	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
	"github.com/rubika/terraform-drift-detector-gcp/internal/scanner"
)

// Options configures one watch loop.
type Options struct {
	Scan       scanner.Options
	Interval   time.Duration
	WebhookURL string
}

// Run scans on Options.Interval until ctx is cancelled, calling onLine once
// per formatted, timestamped log line. Only findings not seen on a previous
// run within this call are logged as "NEW"; the first run is recorded as
// the baseline. Every run after the first is checked against WebhookURL via
// alert.NotifyIfCritical.
func Run(ctx context.Context, opts Options, onLine func(string)) {
	seen := map[string]bool{}
	first := true
	for {
		report, err := scanner.Run(ctx, opts.Scan)
		if err != nil {
			onLine(fmt.Sprintf("%s scan failed: %v", timestamp(), err))
		} else {
			logScan(report, seen, first, onLine)
			first = false
			if err := alert.NotifyIfCritical(ctx, opts.WebhookURL, report); err != nil {
				onLine(fmt.Sprintf("%s webhook notify failed: %v", timestamp(), err))
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(opts.Interval):
		}
	}
}

func timestamp() string {
	return time.Now().Format(time.RFC3339)
}

// logScan emits one summary line per run: on the first run it records
// every finding as the baseline; on later runs it only calls out findings
// whose (kind, resource, field) key hasn't been seen before.
func logScan(report model.DriftReport, seen map[string]bool, first bool, onLine func(string)) {
	now := timestamp()
	if report.Status == "clean" {
		onLine(fmt.Sprintf("%s scan clean (%d resources)", now, report.Summary.TotalResources))
		return
	}

	newCount := 0
	for _, f := range report.Findings {
		key := string(f.Kind) + "|" + f.ResourceID + "|" + f.Field
		if seen[key] {
			continue
		}
		seen[key] = true
		newCount++
		if !first {
			onLine(fmt.Sprintf("%s NEW [%s] %s %s (%s)", now, f.Severity, f.Kind, f.ResourceID, f.Field))
		}
	}

	switch {
	case first:
		onLine(fmt.Sprintf("%s scan found %d finding(s) across %d resources — baseline recorded", now, len(report.Findings), report.Summary.TotalResources))
	case newCount > 0:
		onLine(fmt.Sprintf("%s scan: %d new finding(s) since last run", now, newCount))
	default:
		onLine(fmt.Sprintf("%s scan: no new drift (%d findings total)", now, len(report.Findings)))
	}
}
