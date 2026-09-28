// Package alert posts drift reports to an external webhook (e.g. a Slack
// incoming webhook): NotifyIfCritical for a running `driftgcp watch` (only
// when a run finds critical drift), and NotifyScan for a one-off scan from
// the dashboard's "Run scan" button (every time, so it doubles as a Slack
// audit trail of manual scans).
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
)

const requestTimeout = 10 * time.Second

// payload is Slack-compatible (a top-level "text" field renders directly in
// an incoming webhook message) while still carrying the full report for any
// other webhook consumer.
type payload struct {
	Text          string               `json:"text"`
	Project       string               `json:"project,omitempty"`
	StatePath     string               `json:"state_path"`
	Status        string               `json:"status"`
	CriticalCount int                  `json:"critical_count"`
	Findings      []model.DriftFinding `json:"critical_findings"`
}

// NotifyIfCritical POSTs report to url when it contains at least one
// critical finding. It is a no-op (returns nil) when there are none, so
// callers can call it unconditionally after every scan. On a failed POST it
// retries once after a short backoff before returning the error.
func NotifyIfCritical(ctx context.Context, url string, report model.DriftReport) error {
	if url == "" {
		return nil
	}

	var critical []model.DriftFinding
	for _, f := range report.Findings {
		if f.Severity == model.SeverityCritical {
			critical = append(critical, f)
		}
	}
	if len(critical) == 0 {
		return nil
	}

	body, err := json.Marshal(payload{
		Text:          fmt.Sprintf("driftgcp: %d critical finding(s) in %s (%s)", len(critical), report.StatePath, report.Status),
		Project:       report.Project,
		StatePath:     report.StatePath,
		Status:        report.Status,
		CriticalCount: len(critical),
		Findings:      critical,
	})
	if err != nil {
		return fmt.Errorf("marshaling webhook payload: %w", err)
	}

	return postWithRetry(ctx, url, body)
}

// maxExplanationChars keeps the Slack message body well under Slack's
// per-request size limit even for a long Claude explanation.
const maxExplanationChars = 6000

// scanPayload mirrors payload but carries the full report and, when
// requested, Claude's explanation — used for a manual "Run scan" from the
// dashboard rather than the critical-only watch-mode alert.
type scanPayload struct {
	Text        string               `json:"text"`
	Project     string               `json:"project,omitempty"`
	StatePath   string               `json:"state_path"`
	Status      string               `json:"status"`
	Summary     model.Summary        `json:"summary"`
	Findings    []model.DriftFinding `json:"findings,omitempty"`
	Explanation string               `json:"explanation,omitempty"`
}

// NotifyScan POSTs report (and, if non-empty, Claude's explanation) to url
// unconditionally — every manual scan, clean or not. It is a no-op when url
// is empty. On a failed POST it retries once after a short backoff.
func NotifyScan(ctx context.Context, url string, report model.DriftReport, explanation string) error {
	if url == "" {
		return nil
	}

	text := fmt.Sprintf("driftgcp scan: %s — %d finding(s) in %s", report.Status, report.Summary.TotalFindings, report.StatePath)
	if explanation != "" {
		truncated := explanation
		if len(truncated) > maxExplanationChars {
			truncated = truncated[:maxExplanationChars] + "\n… (truncated)"
		}
		text += "\n\n" + truncated
	}

	body, err := json.Marshal(scanPayload{
		Text:        text,
		Project:     report.Project,
		StatePath:   report.StatePath,
		Status:      report.Status,
		Summary:     report.Summary,
		Findings:    report.Findings,
		Explanation: explanation,
	})
	if err != nil {
		return fmt.Errorf("marshaling webhook payload: %w", err)
	}

	return postWithRetry(ctx, url, body)
}

func postWithRetry(ctx context.Context, url string, body []byte) error {
	err := post(ctx, url, body)
	if err != nil {
		time.Sleep(2 * time.Second)
		err = post(ctx, url, body)
	}
	return err
}

func post(ctx context.Context, url string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting to webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}
