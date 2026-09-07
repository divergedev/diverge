package cli

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeTestPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()
	err = png.Encode(f, img)
	require.NoError(t, err)
}

func TestTestVisualCmd(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "base.png")
	candPath := filepath.Join(tempDir, "cand.png")
	outDir := filepath.Join(tempDir, "out")

	white := color.RGBA{255, 255, 255, 255}
	red := color.RGBA{255, 0, 0, 255}

	baseImg := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(baseImg, baseImg.Bounds(), &image.Uniform{C: white}, image.Point{}, draw.Src)
	writeTestPNG(t, basePath, baseImg)

	candImg := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(candImg, candImg.Bounds(), &image.Uniform{C: white}, image.Point{}, draw.Src)
	// 10x10 red patch = 1% diff
	draw.Draw(candImg, image.Rect(10, 10, 20, 20), &image.Uniform{C: red}, image.Point{}, draw.Src)
	writeTestPNG(t, candPath, candImg)

	app := &App{}

	// 1. Threshold 2% -> PASS
	rootPass := NewRootCmd(app)
	var stdoutPass bytes.Buffer
	rootPass.SetOut(&stdoutPass)
	rootPass.SetArgs([]string{
		"test", "visual",
		"--baseline", basePath,
		"--candidate", candPath,
		"--max-diff-percent", "2.0",
		"-o", outDir,
	})
	err := rootPass.Execute()
	require.NoError(t, err)
	assert.Contains(t, stdoutPass.String(), "PASSED")
	assert.FileExists(t, filepath.Join(outDir, "diff.png"))
	assert.FileExists(t, filepath.Join(outDir, "visual-report.html"))
	assert.FileExists(t, filepath.Join(outDir, "summary.md"))

	// 2. Threshold 0.5% -> FAIL
	rootFail := NewRootCmd(app)
	var stdoutFail bytes.Buffer
	rootFail.SetOut(&stdoutFail)
	rootFail.SetErr(&stdoutFail)
	rootFail.SetArgs([]string{
		"test", "visual",
		"--baseline", basePath,
		"--candidate", candPath,
		"--max-diff-percent", "0.5",
	})
	err = rootFail.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "visual regression detected")

	// 3. JSON Output
	rootJSON := NewRootCmd(app)
	var stdoutJSON bytes.Buffer
	rootJSON.SetOut(&stdoutJSON)
	rootJSON.SetArgs([]string{
		"test", "visual",
		"--baseline", basePath,
		"--candidate", candPath,
		"--max-diff-percent", "2.0",
		"--json",
	})
	err = rootJSON.Execute()
	require.NoError(t, err)
	assert.Contains(t, stdoutJSON.String(), `"total_pixels": 10000`)
	assert.Contains(t, stdoutJSON.String(), `"passed": true`)
}
