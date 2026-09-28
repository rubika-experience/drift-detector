// Package file implements a mock providers.CloudProvider that reads the
// "actual" resource set from a local JSON fixture instead of calling a real
// cloud API. It exists so the full scan → diff → report pipeline can be
// demonstrated and tested without live GCP credentials.
package file

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
)

// Provider reads a fixed set of actual resources from a JSON file.
type Provider struct {
	Path string
}

// New creates a file-backed provider reading resources from path.
func New(path string) *Provider {
	return &Provider{Path: path}
}

// FetchActual ignores `expected` and returns every resource in the fixture
// file verbatim — the fixture is expected to already be authored as the
// "actual" (cloud-side) resource set, including any intentional drift.
func (p *Provider) FetchActual(_ []model.Resource) ([]model.Resource, error) {
	data, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, fmt.Errorf("reading cloud fixture %q: %w", p.Path, err)
	}
	var resources []model.Resource
	if err := json.Unmarshal(data, &resources); err != nil {
		return nil, fmt.Errorf("parsing cloud fixture %q: %w", p.Path, err)
	}
	for i := range resources {
		resources[i].Source = model.SourceCloud
	}
	return resources, nil
}
