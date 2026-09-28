// Package providers defines the pluggable interface for fetching the
// "actual" side of a scan — live cloud state to compare against Terraform's
// expected state.
package providers

import "github.com/rubika/terraform-drift-detector-gcp/internal/model"

// CloudProvider fetches the live resources that correspond to a set of
// expected (Terraform state) resources. Implementations should only fetch
// resources relevant to `expected` — this tool diffs against state, it does
// not inventory an entire account/project.
type CloudProvider interface {
	// FetchActual returns the live model.Resource for every resource in
	// expected that still exists in the cloud. Resources with no live
	// counterpart are simply omitted (the drift engine treats that as
	// "missing in cloud"). Implementations may also return resources that
	// have no counterpart in expected in order to surface "extra in cloud"
	// drift.
	FetchActual(expected []model.Resource) ([]model.Resource, error)
}
