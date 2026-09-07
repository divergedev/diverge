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
	if _, err := fmt.Fprintf(w, "\n🩺 DIVERGE SYSTEM & ENVIRONMENT DOCTOR\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "=====================================================\n"); err != nil {
		return err
	}
	if report.EnvironmentName != "" {
		if _, err := fmt.Fprintf(w, "Environment: %s\n", report.EnvironmentName); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "Namespace:   %s\n", report.Namespace); err != nil {
		return err
	}

	if report.Healthy {
		if _, err := fmt.Fprintf(w, "\n✅ No issues detected. All workloads and environments healthy!\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "=====================================================\n\n"); err != nil {
			return err
		}
		return nil
	}

	if _, err := fmt.Fprintf(w, "\n⚠️  Detected Issues (%d):\n", len(report.Issues)); err != nil {
		return err
	}
	for i, issue := range report.Issues {
		badge := "🟡 WARN"
		if issue.Severity == SeverityCritical {
			badge = "🔴 CRIT"
		}
		if _, err := fmt.Fprintf(w, "\n  [%d] %s: %s\n", i+1, badge, issue.Component); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "      Problem:     %s\n", issue.Summary); err != nil {
			return err
		}
		if issue.Details != "" {
			if _, err := fmt.Fprintf(w, "      Details:     %s\n", issue.Details); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "      Remedy:      💡 %s\n", issue.Remediation); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(w, "\n=====================================================\n\n"); err != nil {
		return err
	}
	return nil
}
