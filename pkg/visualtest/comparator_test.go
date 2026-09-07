package visualtest

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompare_IdenticalImages(t *testing.T) {
	blue := color.RGBA{0, 0, 255, 255}
	img1 := CreateSolidImage(100, 100, blue)
	img2 := CreateSolidImage(100, 100, blue)

	res, diffImg, err := Compare(img1, img2, 0.05, 0.1)
	require.NoError(t, err)
	require.NotNil(t, diffImg)

	assert.Equal(t, int64(10000), res.TotalPixels)
	assert.Equal(t, int64(0), res.MismatchedPixels)
	assert.Equal(t, 0.0, res.DiffPercent)
	assert.True(t, res.Passed)
}

func TestCompare_AlteredImage(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	red := color.RGBA{255, 0, 0, 255}

	base := CreateSolidImage(100, 100, white)

	// Candidate has a 10x10 red square (100 pixels out of 10,000 = 1%)
	candRGBA := image.NewRGBA(image.Rect(0, 0, 100, 100))
	draw.Draw(candRGBA, candRGBA.Bounds(), &image.Uniform{C: white}, image.Point{}, draw.Src)
	draw.Draw(candRGBA, image.Rect(10, 10, 20, 20), &image.Uniform{C: red}, image.Point{}, draw.Src)

	// Threshold 0.5% max drift -> should FAIL
	res, diffImg, err := Compare(base, candRGBA, 0.05, 0.5)
	require.NoError(t, err)
	require.NotNil(t, diffImg)

	assert.Equal(t, int64(10000), res.TotalPixels)
	assert.Equal(t, int64(100), res.MismatchedPixels)
	assert.InDelta(t, 1.0, res.DiffPercent, 0.001)
	assert.False(t, res.Passed)
	assert.Equal(t, image.Rect(10, 10, 20, 20), res.DiffBounds)

	// Threshold 2.0% max drift -> should PASS
	resPass, _, err := Compare(base, candRGBA, 0.05, 2.0)
	require.NoError(t, err)
	assert.True(t, resPass.Passed)
}

func TestReports(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	base := CreateSolidImage(50, 50, white)
	cand := CreateSolidImage(50, 50, white)

	res, diffImg, err := Compare(base, cand, 0.05, 0.1)
	require.NoError(t, err)

	// HTML Report
	var htmlBuf bytes.Buffer
	err = GenerateHTMLReport(&htmlBuf, base, cand, diffImg, res)
	require.NoError(t, err)
	assert.Contains(t, htmlBuf.String(), "<!DOCTYPE html>")
	assert.Contains(t, htmlBuf.String(), "Diverge Visual Regression Report")
	assert.Contains(t, htmlBuf.String(), "data:image/png;base64,")

	// Markdown Summary
	var mdBuf bytes.Buffer
	err = GenerateMarkdownSummary(&mdBuf, res)
	require.NoError(t, err)
	assert.Contains(t, mdBuf.String(), "Diverge Visual Regression: PASSED")
}

func TestCompare_DimensionMismatch(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	// base is 50x50 = 2,500 pixels
	base := CreateSolidImage(50, 50, white)
	// cand is 100x100 = 10,000 pixels
	cand := CreateSolidImage(100, 100, white)

	// In the 100x100 union, the top-left 50x50 matches (2500 pixels),
	// and the remaining 7500 pixels exist only in cand => mismatched.
	res, diffImg, err := Compare(base, cand, 0.05, 5.0)
	require.NoError(t, err)
	require.NotNil(t, diffImg)

	assert.Equal(t, int64(10000), res.TotalPixels)
	assert.Equal(t, int64(7500), res.MismatchedPixels)
	assert.InDelta(t, 75.0, res.DiffPercent, 0.01)
	assert.False(t, res.Passed)
}

func TestCompare_ZeroDimensions(t *testing.T) {
	empty1 := image.NewRGBA(image.Rect(0, 0, 0, 0))
	empty2 := image.NewRGBA(image.Rect(0, 0, 0, 0))

	_, _, err := Compare(empty1, empty2, 0.05, 1.0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero dimensions")
}

func TestCompare_FailingMarkdownReport(t *testing.T) {
	res := &DiffResult{
		TotalPixels:      10000,
		MismatchedPixels: 200,
		DiffPercent:      2.0,
		Passed:           false,
		MaxDiffPercent:   0.5,
		DiffBounds:       image.Rect(10, 10, 30, 30),
	}

	var mdBuf bytes.Buffer
	err := GenerateMarkdownSummary(&mdBuf, res)
	require.NoError(t, err)
	assert.Contains(t, mdBuf.String(), "Diverge Visual Regression: FAILED")
	assert.Contains(t, mdBuf.String(), "0.500%")
	assert.Contains(t, mdBuf.String(), "200 / 10000")

	var jsonBuf bytes.Buffer
	err = FormatJSON(&jsonBuf, res)
	require.NoError(t, err)
	assert.Contains(t, jsonBuf.String(), "\"passed\": false")
	assert.Contains(t, jsonBuf.String(), "\"diff_percent\": 2")
}
