package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/protocgen/proto2mcp/pkg/mcpruntime"

	"github.com/divergedev/diverge/pkg/visualtest"
)

var visualTestSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"baseline_path": {"type": "string", "description": "Path to the baseline PNG/JPEG screenshot file"},
		"preview_path": {"type": "string", "description": "Path to the preview/candidate PNG/JPEG screenshot file"},
		"baseline_dir": {"type": "string", "description": "Directory containing baseline PNG screenshots for batch comparison"},
		"preview_dir": {"type": "string", "description": "Directory containing preview PNG screenshots for batch comparison"},
		"diff_output_path": {"type": "string", "description": "Optional file path (single file mode) or directory path (directory mode) to write diff PNG(s)"},
		"max_diff_percent": {"type": "number", "description": "Maximum allowable pixel drift percentage (0-100) before failing (default: 0.1)"},
		"color_tolerance": {"type": "number", "description": "Per-channel color difference tolerance between 0.0 and 1.0 (default: 0.05)"}
	}
}`)

type visualTestParams struct {
	BaselinePath   string  `json:"baseline_path"`
	PreviewPath    string  `json:"preview_path"`
	BaselineDir    string  `json:"baseline_dir"`
	PreviewDir     string  `json:"preview_dir"`
	DiffOutputPath string  `json:"diff_output_path"`
	MaxDiffPercent float64 `json:"max_diff_percent"`
	ColorTolerance float64 `json:"color_tolerance"`
}

type visualTestFileResult struct {
	File             string          `json:"file,omitempty"`
	Passed           bool            `json:"passed"`
	DiffPercent      float64         `json:"diff_percent"`
	MaxDiffPercent   float64         `json:"max_diff_percent"`
	MismatchedPixels int64           `json:"mismatched_pixels"`
	TotalPixels      int64           `json:"total_pixels"`
	DiffBounds       image.Rectangle `json:"diff_bounds"`
	DiffOutputPath   string          `json:"diff_output_path,omitempty"`
}

type visualTestDirResult struct {
	Passed         bool                   `json:"passed"`
	TotalFiles     int                    `json:"total_files"`
	FailedFiles    int                    `json:"failed_files"`
	MaxDiffPercent float64                `json:"max_diff_percent"`
	Results        []visualTestFileResult `json:"results"`
}

// registerVisualTest registers the diverge_visual_test MCP tool handler.
func registerVisualTest(registry mcpruntime.Registry) {
	registry.Register(mcpruntime.ToolDefinition{
		Name:        "diverge_visual_test",
		Description: "Run pixel-by-pixel visual regression comparison between baseline and preview screenshots (single file pair or directory batch). Highlights visual drift and optionally writes diff PNG artifacts.",
		InputSchema: visualTestSchema,
	}, handleVisualTest)
}

func handleVisualTest(_ context.Context, req mcpruntime.ToolRequest) (*mcpruntime.CallToolResult, error) {
	var params visualTestParams
	if err := json.Unmarshal(req.Arguments, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	maxDiffPercent := params.MaxDiffPercent
	if maxDiffPercent <= 0 {
		maxDiffPercent = 0.1
	}
	colorTolerance := params.ColorTolerance
	if colorTolerance <= 0 {
		colorTolerance = 0.05
	}

	if params.BaselineDir != "" || params.PreviewDir != "" {
		if params.BaselineDir == "" || params.PreviewDir == "" {
			return errorToolResult(fmt.Errorf("both baseline_dir and preview_dir are required for directory comparison")), nil
		}
		dirRes, err := compareImageDirectories(params.BaselineDir, params.PreviewDir, params.DiffOutputPath, colorTolerance, maxDiffPercent)
		if err != nil {
			return errorToolResult(err), nil
		}
		data, err := json.Marshal(dirRes)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal visual directory test result: %w", err)
		}
		return &mcpruntime.CallToolResult{
			Content: json.RawMessage(data),
			IsError: !dirRes.Passed,
		}, nil
	}

	if params.BaselinePath == "" || params.PreviewPath == "" {
		return errorToolResult(fmt.Errorf("provide either (baseline_path and preview_path) or (baseline_dir and preview_dir)")), nil
	}

	fileRes, err := compareImageFiles(params.BaselinePath, params.PreviewPath, params.DiffOutputPath, colorTolerance, maxDiffPercent)
	if err != nil {
		return errorToolResult(err), nil
	}

	data, err := json.Marshal(fileRes)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal visual test result: %w", err)
	}
	return &mcpruntime.CallToolResult{
		Content: json.RawMessage(data),
		IsError: !fileRes.Passed,
	}, nil
}

func compareImageFiles(baselinePath, previewPath, diffOutputPath string, colorTolerance, maxDiffPercent float64) (*visualTestFileResult, error) {
	baseImg, err := loadImageFile(baselinePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load baseline image %q: %w", baselinePath, err)
	}
	prevImg, err := loadImageFile(previewPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load preview image %q: %w", previewPath, err)
	}

	res, _, err := visualtest.Compare(baseImg, prevImg, colorTolerance, maxDiffPercent)
	if err != nil {
		return nil, err
	}

	if diffOutputPath != "" && len(res.DiffImagePNGBytes) > 0 {
		if dir := filepath.Dir(diffOutputPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("failed to create diff output directory %q: %w", dir, err)
			}
		}
		if err := os.WriteFile(diffOutputPath, res.DiffImagePNGBytes, 0o644); err != nil {
			return nil, fmt.Errorf("failed to write diff image %q: %w", diffOutputPath, err)
		}
	}

	return &visualTestFileResult{
		Passed:           res.Passed,
		DiffPercent:      res.DiffPercent,
		MaxDiffPercent:   res.MaxDiffPercent,
		MismatchedPixels: res.MismatchedPixels,
		TotalPixels:      res.TotalPixels,
		DiffBounds:       res.DiffBounds,
		DiffOutputPath:   diffOutputPath,
	}, nil
}

func compareImageDirectories(baselineDir, previewDir, diffOutputDir string, colorTolerance, maxDiffPercent float64) (*visualTestDirResult, error) {
	entries, err := os.ReadDir(baselineDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read baseline directory %q: %w", baselineDir, err)
	}

	var results []visualTestFileResult
	failedFiles := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
			continue
		}

		basePath := filepath.Join(baselineDir, entry.Name())
		prevPath := filepath.Join(previewDir, entry.Name())
		diffPath := ""
		if diffOutputDir != "" {
			diffPath = filepath.Join(diffOutputDir, entry.Name())
		}

		fileRes, err := compareImageFiles(basePath, prevPath, diffPath, colorTolerance, maxDiffPercent)
		if err != nil {
			return nil, err
		}
		fileRes.File = entry.Name()
		if !fileRes.Passed {
			failedFiles++
		}
		results = append(results, *fileRes)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no PNG or JPEG images found in baseline directory %q", baselineDir)
	}

	return &visualTestDirResult{
		Passed:         failedFiles == 0,
		TotalFiles:     len(results),
		FailedFiles:    failedFiles,
		MaxDiffPercent: maxDiffPercent,
		Results:        results,
	}, nil
}

func loadImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return img, nil
}
