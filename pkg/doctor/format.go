package doctor

import (
	"encoding/json"
	"fmt"
	"io"
)

// FormatJSON writes the doctor report as formatted JSON.
func FormatJSON(w io.Writer, report *Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// FormatTable outputs a readable summary of the diagnostic report.
func FormatTable(w io.Writer, report *Report) error {
	_, _ = fmt.Fprintf(w, "\n🩺 DIVERGE SYSTEM & ENVIRONMENT DOCTOR\n")
	_, _ = fmt.Fprintf(w, "=====================================================\n")
	if report.EnvironmentName != "" {
		_, _ = fmt.Fprintf(w, "Environment: %s\n", report.EnvironmentName)
	}
	_, _ = fmt.Fprintf(w, "Namespace:   %s\n", report.Namespace)

	if report.Healthy {
		_, _ = fmt.Fprintf(w, "\n✅ No issues detected. All workloads and environments healthy!\n")
		_, _ = fmt.Fprintf(w, "=====================================================\n\n")
		return nil
	}

	_, _ = fmt.Fprintf(w, "\n⚠️  Detected Issues (%d):\n", len(report.Issues))
	for i, issue := range report.Issues {
		badge := "🟡 WARN"
		if issue.Severity == SeverityCritical {
			badge = "🔴 CRIT"
		}
		_, _ = fmt.Fprintf(w, "\n  [%d] %s: %s\n", i+1, badge, issue.Component)
		_, _ = fmt.Fprintf(w, "      Problem:     %s\n", issue.Summary)
		if issue.Details != "" {
			_, _ = fmt.Fprintf(w, "      Details:     %s\n", issue.Details)
		}
		_, _ = fmt.Fprintf(w, "      Remedy:      💡 %s\n", issue.Remediation)
	}

	_, _ = fmt.Fprintf(w, "\n=====================================================\n\n")
	return nil
}
