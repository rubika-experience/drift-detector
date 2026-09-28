// Package explain turns a drift report into a plain-English summary and
// remediation suggestions using the Claude API. Claude is given a tool to
// look up the exact terraform import ID format per GCP resource type,
// rather than being asked to infer it from memory.
package explain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
)

const (
	defaultModel     = "claude-opus-5"
	maxToolLoopTurns = 6
	lookupToolName   = "lookup_import_id_format"
)

// importIDFormats gives the exact terraform import ID shape for every GCP
// resource type this tool supports (see internal/resourcetypes), taken from
// the terraform-provider-google import docs. Claude calls the
// lookup_import_id_format tool against this table instead of guessing.
var importIDFormats = map[string]string{
	"google_compute_instance":   "{project}/{zone}/{name}",
	"google_storage_bucket":     "{name}",
	"google_compute_network":    "{name} (or the full form projects/{project}/global/networks/{name})",
	"google_compute_subnetwork": "{project}/{region}/{name}",
	"google_compute_firewall":   "{project}/{name}",
	"google_compute_disk":       "{project}/{zone}/{name}",
	"google_compute_address":    "{project}/{region}/{name}",
	"google_service_account":    "projects/{project}/serviceAccounts/{email}",
	"google_pubsub_topic":       "{project}/{name}",
	"google_bigquery_dataset":   "projects/{project}/datasets/{dataset_id}",
}

// lookupImportIDFormat is the tool's implementation: a plain local map
// lookup, run on our side and handed back to Claude as a tool_result.
func lookupImportIDFormat(resourceType string) string {
	if format, ok := importIDFormats[resourceType]; ok {
		return format
	}
	return "unknown resource type — not one of driftgcp's supported GCP resource types; do not guess an import ID for it"
}

var importFormatTool = anthropic.ToolUnionParam{
	OfTool: &anthropic.ToolParam{
		Name: lookupToolName,
		Description: param.NewOpt(
			"Look up the exact terraform import ID format for a supported GCP " +
				"resource type. Call this once for every extra_in_cloud finding " +
				"before writing its `terraform import` command — never guess the " +
				"ID shape from general knowledge.",
		),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"resource_type": map[string]any{
					"type":        "string",
					"description": "The Terraform resource type, e.g. google_storage_bucket",
				},
			},
			Required: []string{"resource_type"},
		},
	},
}

// Explain sends report to Claude and returns a plain-English summary with
// prioritized remediation steps. Requires ANTHROPIC_API_KEY to be set.
func Explain(ctx context.Context, report model.DriftReport) (string, error) {
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY is not set; export it to use --explain")
	}

	reportJSON, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling report for Claude: %w", err)
	}

	prompt := fmt.Sprintf(
		"You are helping a platform engineer understand Terraform drift against GCP. "+
			"Given this JSON drift report, respond with these sections, in order, using "+
			"markdown headers. Omit a section only if it truly doesn't apply.\n\n"+
			"## What Drifted\n"+
			"Short plain-English summary of what drifted and why it matters.\n\n"+
			"## Import Commands\n"+
			"For every finding with kind \"extra_in_cloud\" (a real resource with no "+
			"matching Terraform resource), give the exact `terraform import "+
			"<resource_type>.<suggested_local_name> <id>` command. Before writing each "+
			"one, call the %s tool with that finding's resource_type to get the exact "+
			"ID format, then fill it in from the finding's resource/cloud identifiers. "+
			"Skip this section if there are no extra_in_cloud findings.\n\n"+
			"## How to Edit the .tf Files\n"+
			"For every attribute_changed or tag_changed finding, and for every "+
			"extra_in_cloud finding that needs a new resource block, show the exact "+
			"HCL to add or change — a minimal before/after snippet or a ready-to-paste "+
			"resource block, not just prose. Name the argument/attribute precisely.\n\n"+
			"## Remediation Steps\n"+
			"A prioritized list of next actions (e.g. re-apply Terraform, run the "+
			"import then apply, update state, or accept the change). Be concise "+
			"throughout.\n\n%s",
		lookupToolName, string(reportJSON),
	)

	client := anthropic.NewClient()
	messages := []anthropic.MessageParam{
		anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
	}

	for turn := 0; turn < maxToolLoopTurns; turn++ {
		resp, err := client.Messages.New(ctx, anthropic.MessageNewParams{
			Model:     defaultModel,
			MaxTokens: 2048,
			Tools:     []anthropic.ToolUnionParam{importFormatTool},
			Messages:  messages,
		})
		if err != nil {
			return "", fmt.Errorf("calling Claude: %w", err)
		}
		if resp.StopReason == anthropic.StopReasonRefusal {
			return "", fmt.Errorf("Claude declined to respond to this request")
		}

		if resp.StopReason != anthropic.StopReasonToolUse {
			for _, block := range resp.Content {
				if text, ok := block.AsAny().(anthropic.TextBlock); ok {
					return text.Text, nil
				}
			}
			return "", fmt.Errorf("Claude returned no text content")
		}

		messages = append(messages, resp.ToParam())

		var toolResults []anthropic.ContentBlockParamUnion
		for _, block := range resp.Content {
			toolUse, ok := block.AsAny().(anthropic.ToolUseBlock)
			if !ok {
				continue
			}
			var input struct {
				ResourceType string `json:"resource_type"`
			}
			if err := json.Unmarshal(toolUse.Input, &input); err != nil {
				toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUse.ID, "invalid tool input: "+err.Error(), true))
				continue
			}
			result := lookupImportIDFormat(input.ResourceType)
			toolResults = append(toolResults, anthropic.NewToolResultBlock(toolUse.ID, result, false))
		}
		messages = append(messages, anthropic.NewUserMessage(toolResults...))
	}

	return "", fmt.Errorf("Claude did not finish within %d tool-calling turns", maxToolLoopTurns)
}
