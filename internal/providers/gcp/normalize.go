package gcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	adminpb "cloud.google.com/go/iam/admin/apiv1/adminpb"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/storage"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
)

// lastURLSegment extracts the trailing path component from a GCP
// self-link-style value, e.g. "zones/us-central1-a/machineTypes/e2-medium"
// -> "e2-medium". GCP API responses return full resource URLs/paths for
// fields whose Terraform attribute is just the short name.
func lastURLSegment(s string) string {
	parts := strings.Split(s, "/")
	return parts[len(parts)-1]
}

func newResource(resourceType, cloudID, name string, attrs map[string]any, tags map[string]string) *model.Resource {
	if tags == nil {
		tags = map[string]string{}
	}
	return &model.Resource{
		ID:         model.MakeID("gcp", resourceType, cloudID),
		Provider:   "gcp",
		Type:       resourceType,
		CloudID:    cloudID,
		Name:       name,
		Attributes: attrs,
		Tags:       tags,
		Source:     model.SourceCloud,
	}
}

func (p *Provider) fetchInstance(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	zone, _ := exp.Attributes["zone"].(string)
	if zone == "" {
		return nil, fmt.Errorf("google_compute_instance %q: state has no zone attribute", exp.CloudID)
	}
	resp, err := p.instances.Get(ctx, &computepb.GetInstanceRequest{
		Project:  p.ProjectID,
		Zone:     zone,
		Instance: exp.CloudID,
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching instance %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"machine_type":        lastURLSegment(resp.GetMachineType()),
		"zone":                lastURLSegment(resp.GetZone()),
		"deletion_protection": resp.GetDeletionProtection(),
		"can_ip_forward":      resp.GetCanIpForward(),
		"description":         resp.GetDescription(),
	}
	return newResource(exp.Type, exp.CloudID, resp.GetName(), attrs, resp.GetLabels()), nil
}

func (p *Provider) fetchBucket(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	attrs, err := p.storage.Bucket(exp.CloudID).Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrBucketNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching bucket %q: %w", exp.CloudID, err)
	}
	out := map[string]any{
		"location":                    attrs.Location,
		"storage_class":               attrs.StorageClass,
		"versioning_enabled":          attrs.VersioningEnabled,
		"uniform_bucket_level_access": attrs.UniformBucketLevelAccess.Enabled,
	}
	return newResource(exp.Type, exp.CloudID, attrs.Name, out, attrs.Labels), nil
}

func (p *Provider) fetchNetwork(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	resp, err := p.networks.Get(ctx, &computepb.GetNetworkRequest{
		Project: p.ProjectID,
		Network: exp.CloudID,
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching network %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"auto_create_subnetworks": resp.GetAutoCreateSubnetworks(),
		"routing_mode":            resp.GetRoutingConfig().GetRoutingMode(),
		"description":             resp.GetDescription(),
	}
	return newResource(exp.Type, exp.CloudID, resp.GetName(), attrs, nil), nil
}

func (p *Provider) fetchSubnetwork(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	region, _ := exp.Attributes["region"].(string)
	if region == "" {
		return nil, fmt.Errorf("google_compute_subnetwork %q: state has no region attribute", exp.CloudID)
	}
	resp, err := p.subnetworks.Get(ctx, &computepb.GetSubnetworkRequest{
		Project:    p.ProjectID,
		Region:     lastURLSegment(region),
		Subnetwork: exp.CloudID,
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching subnetwork %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"ip_cidr_range":            resp.GetIpCidrRange(),
		"region":                   lastURLSegment(resp.GetRegion()),
		"private_ip_google_access": resp.GetPrivateIpGoogleAccess(),
	}
	return newResource(exp.Type, exp.CloudID, resp.GetName(), attrs, nil), nil
}

func (p *Provider) fetchFirewall(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	resp, err := p.firewalls.Get(ctx, &computepb.GetFirewallRequest{
		Project:  p.ProjectID,
		Firewall: exp.CloudID,
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching firewall %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"direction": resp.GetDirection(),
		"priority":  int64(resp.GetPriority()),
		"allowed":   formatAllowed(resp.GetAllowed()),
		"disabled":  resp.GetDisabled(),
	}
	return newResource(exp.Type, exp.CloudID, resp.GetName(), attrs, nil), nil
}

// formatAllowed renders firewall allow-rules into a comparable, order-stable
// representation for diffing.
func formatAllowed(rules []*computepb.Allowed) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, fmt.Sprintf("%s:%s", r.GetIPProtocol(), strings.Join(r.GetPorts(), ",")))
	}
	return out
}

func (p *Provider) fetchDisk(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	zone, _ := exp.Attributes["zone"].(string)
	if zone == "" {
		return nil, fmt.Errorf("google_compute_disk %q: state has no zone attribute", exp.CloudID)
	}
	resp, err := p.disks.Get(ctx, &computepb.GetDiskRequest{
		Project: p.ProjectID,
		Zone:    zone,
		Disk:    exp.CloudID,
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching disk %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"size_gb": resp.GetSizeGb(),
		"type":    lastURLSegment(resp.GetType()),
		"zone":    lastURLSegment(resp.GetZone()),
	}
	return newResource(exp.Type, exp.CloudID, resp.GetName(), attrs, resp.GetLabels()), nil
}

func (p *Provider) fetchAddress(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	region, _ := exp.Attributes["region"].(string)
	if region == "" {
		return nil, fmt.Errorf("google_compute_address %q: state has no region attribute", exp.CloudID)
	}
	resp, err := p.addresses.Get(ctx, &computepb.GetAddressRequest{
		Project: p.ProjectID,
		Region:  lastURLSegment(region),
		Address: exp.CloudID,
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching address %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"address_type": resp.GetAddressType(),
		"region":       lastURLSegment(resp.GetRegion()),
	}
	return newResource(exp.Type, exp.CloudID, resp.GetName(), attrs, resp.GetLabels()), nil
}

func (p *Provider) fetchServiceAccount(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	email := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", exp.CloudID, p.ProjectID)
	resp, err := p.iam.GetServiceAccount(ctx, &adminpb.GetServiceAccountRequest{
		Name: fmt.Sprintf("projects/%s/serviceAccounts/%s", p.ProjectID, email),
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching service account %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"display_name": resp.GetDisplayName(),
		"description":  resp.GetDescription(),
		"disabled":     resp.GetDisabled(),
	}
	return newResource(exp.Type, exp.CloudID, exp.CloudID, attrs, nil), nil
}

func (p *Provider) fetchTopic(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	topic := p.pubsub.Topic(exp.CloudID)
	exists, err := topic.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("checking topic %q: %w", exp.CloudID, err)
	}
	if !exists {
		return nil, nil
	}
	cfg, err := topic.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching topic %q: %w", exp.CloudID, err)
	}
	// RetentionDuration is an optional.Duration (an untyped nil when unset);
	// only a time.Duration assertion succeeds when it's actually set. Format
	// as Terraform's "<seconds>s" string so it lines up with the tfstate value.
	retention, _ := cfg.RetentionDuration.(time.Duration)
	attrs := map[string]any{
		"message_retention_duration": fmt.Sprintf("%ds", int64(retention.Seconds())),
	}
	return newResource(exp.Type, exp.CloudID, exp.CloudID, attrs, cfg.Labels), nil
}

func (p *Provider) fetchDataset(ctx context.Context, exp model.Resource) (*model.Resource, error) {
	md, err := p.bigquery.Dataset(exp.CloudID).Metadata(ctx)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("fetching dataset %q: %w", exp.CloudID, err)
	}
	attrs := map[string]any{
		"location":    md.Location,
		"description": md.Description,
	}
	return newResource(exp.Type, exp.CloudID, exp.CloudID, attrs, md.Labels), nil
}
