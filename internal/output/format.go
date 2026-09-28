// Package output renders a model.DriftReport as either a JSON document or a
// human-readable console table.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/rubika/terraform-drift-detector-gcp/internal/model"
)

// Format writes report to w in the requested format ("json" or "table").
func Format(w io.Writer, report model.DriftReport, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case "table", "":
		return writeTable(w, report)
	default:
		return fmt.Errorf("unknown output format %q (want %q or %q)", format, "table", "json")
	}
}

func writeTable(w io.Writer, report model.DriftReport) error {
	fmt.Fprintf(w, "State:    %s\n", report.StatePath)
	if report.Project != "" {
		fmt.Fprintf(w, "Project:  %s\n", report.Project)
	}
	fmt.Fprintf(w, "Status:   %s\n\n", report.Status)

	fmt.Fprintln(w, "SUMMARY")
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintf(tw, "  Total resources:\t%d\n", report.Summary.TotalResources)
	fmt.Fprintf(tw, "  Missing in cloud:\t%d\n", report.Summary.MissingInCloud)
	fmt.Fprintf(tw, "  Extra in cloud:\t%d\n", report.Summary.ExtraInCloud)
	fmt.Fprintf(tw, "  Attribute changes:\t%d\n", report.Summary.AttributeChanges)
	fmt.Fprintf(tw, "  Tag changes:\t%d\n", report.Summary.TagChanges)
	fmt.Fprintf(tw, "  Total findings:\t%d\n", report.Summary.TotalFindings)
	if err := tw.Flush(); err != nil {
		return err
	}

	if len(report.Findings) == 0 {
		fmt.Fprintln(w, "\nNo drift detected.")
		return nil
	}

	fmt.Fprintln(w, "\nFINDINGS")
	ftw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(ftw, "  KIND\tSEVERITY\tRESOURCE\tFIELD\tEXPECTED\tACTUAL")
	for _, f := range report.Findings {
		fmt.Fprintf(ftw, "  %s\t%s\t%s\t%s\t%v\t%v\n",
			f.Kind, f.Severity, resourceLabel(f), f.Field, valueOrDash(f.Expected), valueOrDash(f.Actual))
	}
	return ftw.Flush()
}

func resourceLabel(f model.DriftFinding) string {
	if f.ResourceName != "" {
		return fmt.Sprintf("%s (%s)", f.ResourceName, f.ResourceType)
	}
	return f.ResourceType
}

func valueOrDash(v any) any {
	if v == nil {
		return "-"
	}
	return v
}
