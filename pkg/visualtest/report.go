package visualtest

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
)

// GenerateHTMLReport produces an interactive, standalone HTML diff report.
func GenerateHTMLReport(w io.Writer, baseline, candidate, diff image.Image, res *DiffResult) error {
	baseB64, err := EncodeImageToBase64(baseline)
	if err != nil {
		return err
	}
	candB64, err := EncodeImageToBase64(candidate)
	if err != nil {
		return err
	}
	diffB64, err := EncodeImageToBase64(diff)
	if err != nil {
		return err
	}

	badgeColor := "#10b981" // green
	badgeText := "PASSED"
	if !res.Passed {
		badgeColor = "#ef4444" // red
		badgeText = "FAILED"
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<title>Diverge Visual Regression Report</title>
	<style>
		body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: #0f172a; color: #f8fafc; margin: 0; padding: 24px; }
		.container { max-width: 1200px; margin: 0 auto; }
		.header { display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid #334155; padding-bottom: 16px; margin-bottom: 24px; }
		.title { font-size: 24px; font-weight: bold; }
		.badge { background: %s; color: white; padding: 6px 16px; border-radius: 9999px; font-weight: bold; font-size: 14px; }
		.stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 24px; }
		.stat-card { background: #1e293b; border: 1px solid #334155; border-radius: 8px; padding: 16px; }
		.stat-label { color: #94a3b8; font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
		.stat-value { font-size: 20px; font-weight: bold; margin-top: 4px; color: #38bdf8; }
		.tabs { display: flex; gap: 8px; margin-bottom: 16px; }
		.tab-btn { background: #334155; border: none; color: #f8fafc; padding: 8px 16px; border-radius: 6px; cursor: pointer; font-weight: 500; }
		.tab-btn.active { background: #3b82f6; }
		.view-panel { display: none; background: #1e293b; border: 1px solid #334155; border-radius: 8px; padding: 16px; text-align: center; }
		.view-panel.active { display: block; }
		img { max-width: 100%%; height: auto; border-radius: 4px; box-shadow: 0 4px 6px -1px rgb(0 0 0 / 0.3); }
		.split-view { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
	</style>
</head>
<body>
	<div class="container">
		<div class="header">
			<div class="title">🔀 Diverge Visual Regression Report</div>
			<div class="badge">%s</div>
		</div>

		<div class="stats">
			<div class="stat-card">
				<div class="stat-label">Visual Drift</div>
				<div class="stat-value">%.3f%%%%</div>
			</div>
			<div class="stat-card">
				<div class="stat-label">Max Allowed Drift</div>
				<div class="stat-value">%.3f%%%%</div>
			</div>
			<div class="stat-card">
				<div class="stat-label">Mismatched Pixels</div>
				<div class="stat-value">%d / %d</div>
			</div>
		</div>

		<div class="tabs">
			<button class="tab-btn active" onclick="showTab('diff')">Visual Diff (Magenta Highlight)</button>
			<button class="tab-btn" onclick="showTab('side-by-side')">Side-by-Side</button>
			<button class="tab-btn" onclick="showTab('candidate')">Candidate Preview</button>
			<button class="tab-btn" onclick="showTab('baseline')">Baseline Staging</button>
		</div>

		<div id="diff" class="view-panel active">
			<img src="%s" alt="Visual Diff" />
		</div>
		<div id="side-by-side" class="view-panel">
			<div class="split-view">
				<div>
					<h3>Baseline</h3>
					<img src="%s" alt="Baseline" />
				</div>
				<div>
					<h3>Candidate Preview</h3>
					<img src="%s" alt="Candidate" />
				</div>
			</div>
		</div>
		<div id="candidate" class="view-panel">
			<img src="%s" alt="Candidate" />
		</div>
		<div id="baseline" class="view-panel">
			<img src="%s" alt="Baseline" />
		</div>
	</div>

	<script>
		function showTab(tabId) {
			document.querySelectorAll('.view-panel').forEach(el => el.classList.remove('active'));
			document.querySelectorAll('.tab-btn').forEach(el => el.classList.remove('active'));
			document.getElementById(tabId).classList.add('active');
			event.target.classList.add('active');
		}
	</script>
</body>
</html>`,
		badgeColor,
		badgeText,
		res.DiffPercent,
		res.MaxDiffPercent,
		res.MismatchedPixels,
		res.TotalPixels,
		diffB64,
		baseB64,
		candB64,
		candB64,
		baseB64,
	)

	_, err = io.WriteString(w, html)
	return err
}

// GenerateMarkdownSummary formats the result for CI and PR comments.
func GenerateMarkdownSummary(w io.Writer, res *DiffResult) error {
	statusEmoji := "✅"
	statusText := "PASSED"
	if !res.Passed {
		statusEmoji = "❌"
		statusText = "FAILED"
	}

	if _, err := fmt.Fprintf(w, "### %s Diverge Visual Regression: %s\n\n", statusEmoji, statusText); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| Metric | Value |\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| :--- | :--- |\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| **Visual Drift** | `%.3f%%` |\n", res.DiffPercent); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| **Allowed Threshold** | `%.3f%%` |\n", res.MaxDiffPercent); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| **Mismatched Pixels** | `%d / %d` |\n", res.MismatchedPixels, res.TotalPixels); err != nil {
		return err
	}
	if !res.Passed {
		if _, err := fmt.Fprintf(w, "\n> ⚠️ **Visual Regression Detected**: Candidate preview has exceeded the allowed threshold of %.3f%% drift.\n", res.MaxDiffPercent); err != nil {
			return err
		}
	}

	return nil
}

// FormatJSON outputs DiffResult as indented JSON.
func FormatJSON(w io.Writer, res *DiffResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}
