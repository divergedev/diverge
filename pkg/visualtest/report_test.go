package visualtest

import (
	"bytes"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReport_HTMLAndMarkdown(t *testing.T) {
	white := color.RGBA{255, 255, 255, 255}
	base := CreateSolidImage(20, 20, white)
	cand := CreateSolidImage(20, 20, white)

	res, diffImg, err := Compare(base, cand, 0.05, 0.1)
	require.NoError(t, err)

	var htmlBuf bytes.Buffer
	err = GenerateHTMLReport(&htmlBuf, base, cand, diffImg, res)
	require.NoError(t, err)
	assert.Contains(t, htmlBuf.String(), "<!DOCTYPE html>")

	var mdBuf bytes.Buffer
	err = GenerateMarkdownSummary(&mdBuf, res)
	require.NoError(t, err)
	assert.Contains(t, mdBuf.String(), "Diverge Visual Regression")

	var jsonBuf bytes.Buffer
	err = FormatJSON(&jsonBuf, res)
	require.NoError(t, err)
	assert.Contains(t, jsonBuf.String(), `"diff_percent": 0`)
}
