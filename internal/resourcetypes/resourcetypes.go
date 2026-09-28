// Package resourcetypes is the single source of truth for which Terraform
// GCP resource types this tool understands, how to derive a stable cloud ID
// for each, and which attributes are meaningful to diff. Both the state
// extractor (expected side) and the GCP/file providers (actual side) key off
// this table so the two sides always compare the same fields.
package resourcetypes

// Spec describes how to handle one Terraform resource type.
type Spec struct {
	// IDField is the tfstate attribute (and, by convention, the normalized
	// Attributes key) that uniquely identifies the resource within its
	// project/region/zone scope.
	IDField string
	// CompareKeys are the attribute keys compared between expected and actual.
	CompareKeys []string
}

var specs = map[string]Spec{
	"google_compute_instance": {
		IDField:     "name",
		CompareKeys: []string{"machine_type", "zone", "deletion_protection", "can_ip_forward", "description"},
	},
	"google_storage_bucket": {
		IDField:     "name",
		CompareKeys: []string{"location", "storage_class", "versioning_enabled", "uniform_bucket_level_access"},
	},
	"google_compute_network": {
		IDField:     "name",
		CompareKeys: []string{"auto_create_subnetworks", "routing_mode", "description"},
	},
	"google_compute_subnetwork": {
		IDField:     "name",
		CompareKeys: []string{"ip_cidr_range", "region", "private_ip_google_access"},
	},
	"google_compute_firewall": {
		IDField:     "name",
		CompareKeys: []string{"direction", "priority", "allowed", "disabled"},
	},
	"google_compute_disk": {
		IDField:     "name",
		CompareKeys: []string{"size_gb", "type", "zone"},
	},
	"google_compute_address": {
		IDField:     "name",
		CompareKeys: []string{"address_type", "region"},
	},
	"google_service_account": {
		IDField:     "account_id",
		CompareKeys: []string{"display_name", "description", "disabled"},
	},
	"google_pubsub_topic": {
		IDField:     "name",
		CompareKeys: []string{"message_retention_duration"},
	},
	"google_bigquery_dataset": {
		IDField:     "dataset_id",
		CompareKeys: []string{"location", "description"},
	},
}

// Supported reports whether a Terraform resource type is handled by this tool.
func Supported(resourceType string) bool {
	_, ok := specs[resourceType]
	return ok
}

// Spec returns the Spec for a resource type and whether it is supported.
func SpecFor(resourceType string) (Spec, bool) {
	s, ok := specs[resourceType]
	return s, ok
}

// Types returns the list of all supported Terraform resource types.
func Types() []string {
	out := make([]string, 0, len(specs))
	for t := range specs {
		out = append(out, t)
	}
	return out
}
