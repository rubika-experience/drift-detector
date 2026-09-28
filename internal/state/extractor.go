package state

import (
	"fmt"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
	"github.com/rubika/terraform-drift-detector-gcp/internal/resourcetypes"
)

// Extract converts a parsed Terraform state into the normalized "expected"
// resource model: managed resources of a supported GCP type only, with a
// stable cloud ID and only the attributes/tags this tool knows how to diff.
func Extract(s *tfState) []model.Resource {
	var out []model.Resource
	for _, r := range s.Resources {
		if r.Mode != "managed" {
			continue
		}
		spec, ok := resourcetypes.SpecFor(r.Type)
		if !ok {
			continue
		}
		for _, inst := range r.Instances {
			cloudID, _ := inst.Attributes[spec.IDField].(string)
			if cloudID == "" {
				continue
			}

			attrs := make(map[string]any, len(spec.CompareKeys))
			for _, key := range spec.CompareKeys {
				if v, ok := inst.Attributes[key]; ok {
					attrs[key] = v
				}
			}

			tags := extractTags(inst.Attributes)

			out = append(out, model.Resource{
				ID:         model.MakeID("gcp", r.Type, cloudID),
				Provider:   "gcp",
				Type:       r.Type,
				CloudID:    cloudID,
				Name:       r.Name,
				Attributes: attrs,
				Tags:       tags,
				Source:     model.SourceState,
			})
		}
	}
	return out
}

// extractTags pulls GCP "labels" (the GCP equivalent of tags) out of a
// resource's raw attribute map into a plain map[string]string.
func extractTags(attrs map[string]any) map[string]string {
	raw, ok := attrs["labels"].(map[string]any)
	if !ok {
		return map[string]string{}
	}
	tags := make(map[string]string, len(raw))
	for k, v := range raw {
		tags[k] = fmt.Sprintf("%v", v)
	}
	return tags
}
