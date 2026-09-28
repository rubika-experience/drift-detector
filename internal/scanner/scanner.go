// Package scanner runs the state-read -> cloud-fetch -> diff pipeline shared
// by the CLI `scan` command and the web dashboard.
package scanner

import (
	"context"
	"fmt"

	"github.com/rubika/terraform-drift-detector-gcp/internal/drift"
	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
	"github.com/rubika/terraform-drift-detector-gcp/internal/providers"
	"github.com/rubika/terraform-drift-detector-gcp/internal/providers/file"
	"github.com/rubika/terraform-drift-detector-gcp/internal/providers/gcp"
	"github.com/rubika/terraform-drift-detector-gcp/internal/state"
)

// Options describes one scan request, independent of how it was triggered
// (CLI flags/config or a web request).
type Options struct {
	StatePath    string
	Provider     string // "gcp" or "file"
	Project      string // required when Provider == "gcp"
	CloudFixture string // required when Provider == "file"
}

// Run executes a full scan and returns the resulting drift report.
func Run(ctx context.Context, opts Options) (model.DriftReport, error) {
	if opts.StatePath == "" {
		return model.DriftReport{}, fmt.Errorf("state path is required")
	}

	tfState, err := state.Read(ctx, opts.StatePath)
	if err != nil {
		return model.DriftReport{}, err
	}
	expected := state.Extract(tfState)

	provider, closeProvider, err := newProvider(ctx, opts)
	if err != nil {
		return model.DriftReport{}, err
	}
	if closeProvider != nil {
		defer closeProvider()
	}

	actual, err := provider.FetchActual(expected)
	if err != nil {
		return model.DriftReport{}, err
	}

	findings := drift.Compare(expected, actual)
	status := "clean"
	if len(findings) > 0 {
		status = "drift_detected"
	}

	return model.DriftReport{
		Project:   opts.Project,
		StatePath: opts.StatePath,
		Status:    status,
		Summary:   model.BuildSummary(len(expected), findings),
		Findings:  findings,
	}, nil
}

func newProvider(ctx context.Context, opts Options) (providers.CloudProvider, func(), error) {
	switch opts.Provider {
	case "gcp":
		if opts.Project == "" {
			return nil, nil, fmt.Errorf("project is required when provider is gcp")
		}
		p, err := gcp.New(ctx, opts.Project)
		if err != nil {
			return nil, nil, err
		}
		return p, func() { _ = p.Close() }, nil
	case "file":
		if opts.CloudFixture == "" {
			return nil, nil, fmt.Errorf("cloud fixture path is required when provider is file")
		}
		return file.New(opts.CloudFixture), nil, nil
	default:
		return nil, nil, fmt.Errorf("unknown provider %q (want %q or %q)", opts.Provider, "gcp", "file")
	}
}
