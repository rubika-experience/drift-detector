// Package gcp implements providers.CloudProvider against live Google Cloud
// resources, using Application Default Credentials (gcloud auth
// application-default login, or GOOGLE_APPLICATION_CREDENTIALS).
//
// Fetches are targeted: for each expected resource type present in the scan,
// only the specific resources whose cloud ID appears in Terraform state are
// described — this tool diffs against state, it does not inventory the
// whole project.
package gcp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	admin "cloud.google.com/go/iam/admin/apiv1"

	"cloud.google.com/go/bigquery"
	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
)

// Provider fetches live GCP resources via the Compute Engine, Cloud Storage,
// IAM, Pub/Sub, and BigQuery APIs.
type Provider struct {
	ProjectID string

	instances   *compute.InstancesClient
	networks    *compute.NetworksClient
	subnetworks *compute.SubnetworksClient
	firewalls   *compute.FirewallsClient
	disks       *compute.DisksClient
	addresses   *compute.AddressesClient
	storage     *storage.Client
	iam         *admin.IamClient
	pubsub      *pubsub.Client
	bigquery    *bigquery.Client
}

// New creates a GCP provider for the given project, authenticating via
// Application Default Credentials.
func New(ctx context.Context, projectID string) (*Provider, error) {
	instances, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating instances client: %w", err)
	}
	networks, err := compute.NewNetworksRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating networks client: %w", err)
	}
	subnetworks, err := compute.NewSubnetworksRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating subnetworks client: %w", err)
	}
	firewalls, err := compute.NewFirewallsRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating firewalls client: %w", err)
	}
	disks, err := compute.NewDisksRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating disks client: %w", err)
	}
	addresses, err := compute.NewAddressesRESTClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating addresses client: %w", err)
	}
	storageClient, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating storage client: %w", err)
	}
	iamClient, err := admin.NewIamClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating IAM client: %w", err)
	}
	pubsubClient, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("creating Pub/Sub client: %w", err)
	}
	bigqueryClient, err := bigquery.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("creating BigQuery client: %w", err)
	}

	return &Provider{
		ProjectID:   projectID,
		instances:   instances,
		networks:    networks,
		subnetworks: subnetworks,
		firewalls:   firewalls,
		disks:       disks,
		addresses:   addresses,
		storage:     storageClient,
		iam:         iamClient,
		pubsub:      pubsubClient,
		bigquery:    bigqueryClient,
	}, nil
}

// Close releases the underlying API clients.
func (p *Provider) Close() error {
	var errs []error
	if err := p.instances.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.networks.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.subnetworks.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.firewalls.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.disks.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.addresses.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.storage.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.iam.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.pubsub.Close(); err != nil {
		errs = append(errs, err)
	}
	if err := p.bigquery.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// fetchResult carries one resource fetch outcome back to the fan-in loop.
type fetchResult struct {
	resource *model.Resource // nil if not found in the cloud
	err      error
}

// FetchActual describes, concurrently and by ID, the live counterpart of
// every expected resource of a supported type.
func (p *Provider) FetchActual(expected []model.Resource) ([]model.Resource, error) {
	ctx := context.Background()

	results := make(chan fetchResult, len(expected))
	var wg sync.WaitGroup

	for _, exp := range expected {
		exp := exp
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := p.fetchOne(ctx, exp)
			results <- fetchResult{resource: res, err: err}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var actual []model.Resource
	var errs []error
	for r := range results {
		if r.err != nil {
			errs = append(errs, r.err)
			continue
		}
		if r.resource != nil {
			actual = append(actual, *r.resource)
		}
	}
	if len(errs) > 0 {
		return actual, fmt.Errorf("fetching live GCP resources: %w", errors.Join(errs...))
	}
	return actual, nil
}

// fetchOne describes a single expected resource's live counterpart.
// Returns (nil, nil) if the resource no longer exists in the cloud.
func (p *Provider) fetchOne(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	switch exp.Type {
	case "google_compute_instance":
		return p.fetchInstance(ctx, exp)
	case "google_storage_bucket":
		return p.fetchBucket(ctx, exp)
	case "google_compute_network":
		return p.fetchNetwork(ctx, exp)
	case "google_compute_subnetwork":
		return p.fetchSubnetwork(ctx, exp)
	case "google_compute_firewall":
		return p.fetchFirewall(ctx, exp)
	case "google_compute_disk":
		return p.fetchDisk(ctx, exp)
	case "google_compute_address":
		return p.fetchAddress(ctx, exp)
	case "google_service_account":
		return p.fetchServiceAccount(ctx, exp)
	case "google_pubsub_topic":
		return p.fetchTopic(ctx, exp)
	case "google_bigquery_dataset":
		return p.fetchDataset(ctx, exp)
	default:
		return nil, nil
	}
}

// isNotFound reports whether err represents a GCP 404 (resource no longer
// exists) rather than a real fetch failure.
func isNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == 404
	}
	return false
}
