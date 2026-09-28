// Package drift compares the expected (Terraform state) and actual (live
// GCP) resource sets and produces a list of findings.
package drift

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
)

// Compare diffs expected against actual and returns every drift finding.
// Resources are matched by their shared ID ("<provider>/<type>/<cloudID>").
func Compare(expected, actual []model.Resource) []model.DriftFinding {
	actualByID := make(map[string]model.Resource, len(actual))
	for _, a := range actual {
		actualByID[a.ID] = a
	}
	expectedByID := make(map[string]model.Resource, len(expected))
	for _, e := range expected {
		expectedByID[e.ID] = e
	}

	var findings []model.DriftFinding

	for _, exp := range expected {
		act, ok := actualByID[exp.ID]
		if !ok {
			findings = append(findings, model.DriftFinding{
				Kind:         model.DriftMissingInCloud,
				ResourceID:   exp.ID,
				ResourceType: exp.Type,
				ResourceName: exp.Name,
				Severity:     model.SeverityCritical,
			})
			continue
		}
		findings = append(findings, diffAttributes(exp, act)...)
		findings = append(findings, diffTags(exp, act)...)
	}

	for _, act := range actual {
		if _, ok := expectedByID[act.ID]; !ok {
			findings = append(findings, model.DriftFinding{
				Kind:         model.DriftExtraInCloud,
				ResourceID:   act.ID,
				ResourceType: act.Type,
				ResourceName: act.Name,
				Severity:     model.SeverityWarning,
			})
		}
	}

	sortFindings(findings)
	return findings
}

// diffAttributes compares every attribute key present on either side of a
// matched resource pair, one finding per changed field.
func diffAttributes(exp, act model.Resource) []model.DriftFinding {
	keys := unionKeys(exp.Attributes, act.Attributes)
	var findings []model.DriftFinding
	for _, key := range keys {
		expVal, actVal := exp.Attributes[key], act.Attributes[key]
		if !equalNormalized(expVal, actVal) {
			findings = append(findings, model.DriftFinding{
				Kind:         model.DriftAttributeChange,
				ResourceID:   exp.ID,
				ResourceType: exp.Type,
				ResourceName: exp.Name,
				Field:        key,
				Expected:     expVal,
				Actual:       actVal,
				Severity:     model.SeverityWarning,
			})
		}
	}
	return findings
}

// diffTags compares GCP labels between a matched resource pair, one finding
// per added, removed, or changed key.
func diffTags(exp, act model.Resource) []model.DriftFinding {
	keys := unionStringKeys(exp.Tags, act.Tags)
	var findings []model.DriftFinding
	for _, key := range keys {
		expVal, expOK := exp.Tags[key]
		actVal, actOK := act.Tags[key]
		if expOK == actOK && expVal == actVal {
			continue
		}
		field := "tags." + key
		var expOut, actOut any
		if expOK {
			expOut = expVal
		}
		if actOK {
			actOut = actVal
		}
		findings = append(findings, model.DriftFinding{
			Kind:         model.DriftTagChange,
			ResourceID:   exp.ID,
			ResourceType: exp.Type,
			ResourceName: exp.Name,
			Field:        field,
			Expected:     expOut,
			Actual:       actOut,
			Severity:     model.SeverityInfo,
		})
	}
	return findings
}

// equalNormalized compares two values for semantic equality by round-
// tripping both through JSON, which irons out differences like int vs
// float64 that would otherwise show up as false drift.
func equalNormalized(a, b any) bool {
	aj, errA := json.Marshal(a)
	bj, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}
	return string(aj) == string(bj)
}

func unionKeys(a, b map[string]any) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func unionStringKeys(a, b map[string]string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortFindings orders findings deterministically for stable output.
func sortFindings(findings []model.DriftFinding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].ResourceID != findings[j].ResourceID {
			return findings[i].ResourceID < findings[j].ResourceID
		}
		return findings[i].Field < findings[j].Field
	})
}
