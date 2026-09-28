// Package model defines the canonical shapes shared by both the Terraform
// state side and the live GCP side of the pipeline, plus the drift report
// produced by comparing them.
package model

import "fmt"

// Source identifies which side of the comparison a Resource came from.
const (
	SourceState = "state"
	SourceCloud = "cloud"
)

// Resource is the normalized representation of an infrastructure object,
// whether it was extracted from Terraform state ("expected") or fetched
// live from GCP ("actual"). Both sides use this same struct so the drift
// engine can compare them by a shared ID.
type Resource struct {
	ID         string            `json:"id"`
	Provider   string            `json:"provider"`
	Type       string            `json:"type"`
	CloudID    string            `json:"cloud_id"`
	Name       string            `json:"name"`
	Attributes map[string]any    `json:"attributes"`
	Tags       map[string]string `json:"tags"`
	Region     string            `json:"region,omitempty"`
	Source     string            `json:"source"`
}

// MakeID builds the join key used to match expected and actual resources:
// "<provider>/<type>/<cloudID>".
func MakeID(provider, resourceType, cloudID string) string {
	return fmt.Sprintf("%s/%s/%s", provider, resourceType, cloudID)
}

// DriftKind classifies the nature of a single finding.
type DriftKind string

const (
	DriftMissingInCloud  DriftKind = "missing_in_cloud"
	DriftExtraInCloud    DriftKind = "extra_in_cloud"
	DriftAttributeChange DriftKind = "attribute_changed"
	DriftTagChange       DriftKind = "tag_changed"
)

// Severity ranks how urgent a finding is.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
	SeverityInfo     Severity = "info"
)

// DriftFinding is a single detected difference between expected and actual state.
type DriftFinding struct {
	Kind         DriftKind `json:"kind"`
	ResourceID   string    `json:"resource_id"`
	ResourceType string    `json:"resource_type"`
	ResourceName string    `json:"resource_name,omitempty"`
	Field        string    `json:"field,omitempty"`
	Expected     any       `json:"expected,omitempty"`
	Actual       any       `json:"actual,omitempty"`
	Severity     Severity  `json:"severity"`
}

// Summary tallies findings by kind for a quick at-a-glance report header.
type Summary struct {
	TotalResources   int `json:"total_resources"`
	MissingInCloud   int `json:"missing_in_cloud"`
	ExtraInCloud     int `json:"extra_in_cloud"`
	AttributeChanges int `json:"attribute_changes"`
	TagChanges       int `json:"tag_changes"`
	TotalFindings    int `json:"total_findings"`
}

// DriftReport is the complete output of a scan.
type DriftReport struct {
	Project   string         `json:"project,omitempty"`
	StatePath string         `json:"state_path"`
	Status    string         `json:"status"` // "clean" | "drift_detected"
	Summary   Summary        `json:"summary"`
	Findings  []DriftFinding `json:"findings"`
}

// BuildSummary computes a Summary from a resource count and a set of findings.
func BuildSummary(totalResources int, findings []DriftFinding) Summary {
	s := Summary{TotalResources: totalResources, TotalFindings: len(findings)}
	for _, f := range findings {
		switch f.Kind {
		case DriftMissingInCloud:
			s.MissingInCloud++
		case DriftExtraInCloud:
			s.ExtraInCloud++
		case DriftAttributeChange:
			s.AttributeChanges++
		case DriftTagChange:
			s.TagChanges++
		}
	}
	return s
}
