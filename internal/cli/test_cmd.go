package cli

import (
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/divergedev/diverge/pkg/visualtest"
)

func newTestCmd(app *App) *cobra.Command {
	testCmd := &cobra.Command{
		Use:   "test",
		Short: "Run verification and quality tests against preview environments",
		Long:  `Suite of testing tools for Diverge preview environments, including visual regression and load benchmarks.`,
	}

	testCmd.AddCommand(newTestVisualCmd(app))
	return testCmd
}

func newTestVisualCmd(app *App) *cobra.Command {
	var (
		baselineImgPath  string
		candidateImgPath string
		outputDir        string
		maxDiffPercent   float64
		colorTolerance   float64
		outputJSON       bool
	)

	cmd := &cobra.Command{
		Use:   "visual",
		Short: "Run pixel-level visual regression comparison between baseline and candidate images",
		Long: `Compares baseline and candidate screenshots, detects UI drift, and generates an
interactive HTML visual diff report (with magenta highlight overlays) and Markdown summary for PRs.`,
		Example: `	# Compare two screenshots
	diverge test visual --baseline baseline.png --candidate preview.png

	# Enforce CI quality gate: fail if visual drift exceeds 0.05%
	diverge test visual --baseline base.png --candidate cand.png --max-diff-percent 0.05 -o ./reports`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if baselineImgPath == "" || candidateImgPath == "" {
				return fmt.Errorf("both --baseline and --candidate image paths are required")
			}

			// Load baseline
			bFile, err := os.Open(baselineImgPath)
			if err != nil {
				return fmt.Errorf("failed to open baseline image %q: %w", baselineImgPath, err)
			}
			defer func() { _ = bFile.Close() }()
			bImg, _, err := image.Decode(bFile)
			if err != nil {
				return fmt.Errorf("failed to decode baseline image: %w", err)
			}

			// Load candidate
			cFile, err := os.Open(candidateImgPath)
			if err != nil {
				return fmt.Errorf("failed to open candidate image %q: %w", candidateImgPath, err)
			}
			defer func() { _ = cFile.Close() }()
			cImg, _, err := image.Decode(cFile)
			if err != nil {
				return fmt.Errorf("failed to decode candidate image: %w", err)
			}

			res, diffImg, err := visualtest.Compare(bImg, cImg, colorTolerance, maxDiffPercent)
			if err != nil {
				return fmt.Errorf("comparison failed: %w", err)
			}

			// Save artifacts if output directory is provided
			if outputDir != "" {
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					return fmt.Errorf("failed to create output directory %q: %w", outputDir, err)
				}

				// Diff image
				diffPath := filepath.Join(outputDir, "diff.png")
				df, err := os.Create(diffPath)
				if err != nil {
					return fmt.Errorf("failed to create diff image file: %w", err)
				}
				if err := png.Encode(df, diffImg); err != nil {
					_ = df.Close()
					return fmt.Errorf("failed to encode diff image: %w", err)
				}
				_ = df.Close()

				// HTML report
				reportPath := filepath.Join(outputDir, "visual-report.html")
				rf, err := os.Create(reportPath)
				if err != nil {
					return fmt.Errorf("failed to create visual report file: %w", err)
				}
				if err := visualtest.GenerateHTMLReport(rf, bImg, cImg, diffImg, res); err != nil {
					_ = rf.Close()
					return fmt.Errorf("failed to generate HTML report: %w", err)
				}
				_ = rf.Close()

				// Markdown summary
				mdPath := filepath.Join(outputDir, "summary.md")
				mf, err := os.Create(mdPath)
				if err != nil {
					return fmt.Errorf("failed to create markdown summary file: %w", err)
				}
				if err := visualtest.GenerateMarkdownSummary(mf, res); err != nil {
					_ = mf.Close()
					return fmt.Errorf("failed to generate markdown summary: %w", err)
				}
				_ = mf.Close()
			}

			if outputJSON {
				if err := visualtest.FormatJSON(cmd.OutOrStdout(), res); err != nil {
					return err
				}
			} else {
				if err := visualtest.GenerateMarkdownSummary(cmd.OutOrStdout(), res); err != nil {
					return err
				}
				if outputDir != "" {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "📁 Artifacts written to: %s\n", outputDir)
				}
			}

			if !res.Passed {
				return fmt.Errorf("visual regression detected: %.3f%% drift exceeds allowed threshold %.3f%%", res.DiffPercent, res.MaxDiffPercent)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&baselineImgPath, "baseline", "", "Path to baseline screenshot PNG/JPEG")
	cmd.Flags().StringVar(&candidateImgPath, "candidate", "", "Path to candidate screenshot PNG/JPEG")
	cmd.Flags().StringVarP(&outputDir, "output", "o", ".diverge/visual", "Directory to write diff.png and visual-report.html")
	cmd.Flags().Float64VarP(&maxDiffPercent, "max-diff-percent", "t", 0.1, "Maximum allowable pixel drift percentage before failing")
	cmd.Flags().Float64Var(&colorTolerance, "color-tolerance", 0.05, "Color channel difference tolerance (0.0 to 1.0)")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output results as JSON")

	return cmd
}
