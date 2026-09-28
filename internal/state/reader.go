// Package state reads a Terraform state file — from local disk or, for a
// GCS-backed Terraform backend, directly from the bucket — and extracts
// the resources it manages into the shared model.Resource shape.
package state

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"cloud.google.com/go/storage"
)

// tfState mirrors the parts of the Terraform state v4 JSON schema we need.
// We parse the raw file directly rather than shelling out to `terraform show`.
type tfState struct {
	Version   int          `json:"version"`
	Resources []tfResource `json:"resources"`
}

type tfResource struct {
	Mode      string       `json:"mode"`
	Type      string       `json:"type"`
	Name      string       `json:"name"`
	Provider  string       `json:"provider"`
	Instances []tfInstance `json:"instances"`
}

type tfInstance struct {
	Attributes map[string]any `json:"attributes"`
}

// Read loads and parses a Terraform state file, either from local disk or,
// when path starts with "gs://", directly from Google Cloud Storage —
// exactly what a `backend "gcs" {}` Terraform config writes to, so driftgcp
// can read a live-updating remote state without redeploying.
func Read(ctx context.Context, path string) (*tfState, error) {
	var data []byte
	var err error
	if strings.HasPrefix(path, "gs://") {
		data, err = readGCS(ctx, path)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("reading state file %q: %w", path, err)
	}

	var s tfState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing state file %q: %w", path, err)
	}
	return &s, nil
}

// readGCS fetches the object at a gs://bucket/object/path URL.
func readGCS(ctx context.Context, gcsPath string) ([]byte, error) {
	bucket, object, ok := strings.Cut(strings.TrimPrefix(gcsPath, "gs://"), "/")
	if !ok || bucket == "" || object == "" {
		return nil, fmt.Errorf("invalid gs:// path %q (want gs://bucket/object)", gcsPath)
	}

	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating GCS client: %w", err)
	}
	defer client.Close()

	rc, err := client.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("opening gs://%s/%s: %w", bucket, object, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("reading gs://%s/%s: %w", bucket, object, err)
	}
	return data, nil
}
