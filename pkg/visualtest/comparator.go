package visualtest

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

// DiffResult holds the pixel-by-pixel comparison result between two images.
type DiffResult struct {
	TotalPixels       int64           `json:"total_pixels"`
	MismatchedPixels  int64           `json:"mismatched_pixels"`
	DiffPercent       float64         `json:"diff_percent"`
	Passed            bool            `json:"passed"`
	MaxDiffPercent    float64         `json:"max_diff_percent"`
	DiffBounds        image.Rectangle `json:"diff_bounds"`
	DiffImagePNGBytes []byte          `json:"-"`
}

// DiffHighlightColor is the magenta color used to highlight pixel differences.
var DiffHighlightColor = color.RGBA{R: 255, G: 0, B: 128, A: 255}

// Compare compares two images pixel-by-pixel, calculates the mismatch percentage,
// and produces a diff image highlighting altered regions.
// colorTolerance defines the per-channel threshold (0.0 to 1.0) before a pixel is considered different.
func Compare(baseline, candidate image.Image, colorTolerance float64, maxDiffPercent float64) (*DiffResult, image.Image, error) {
	if math.IsNaN(colorTolerance) || math.IsInf(colorTolerance, 0) || colorTolerance < 0 || colorTolerance > 1 {
		return nil, nil, fmt.Errorf("color tolerance must be between 0.0 and 1.0")
	}
	if math.IsNaN(maxDiffPercent) || math.IsInf(maxDiffPercent, 0) || maxDiffPercent < 0 || maxDiffPercent > 100 {
		return nil, nil, fmt.Errorf("max diff percent must be between 0 and 100")
	}

	bBounds := baseline.Bounds()
	cBounds := candidate.Bounds()

	// Normalize dimensions to the maximum bounding box
	w := max(bBounds.Dx(), cBounds.Dx())
	h := max(bBounds.Dy(), cBounds.Dy())

	if w == 0 || h == 0 {
		return nil, nil, fmt.Errorf("images have zero dimensions")
	}

	diffImg := image.NewRGBA(image.Rect(0, 0, w, h))

	var mismatched int64
	totalPixels := int64(w * h)

	minX, minY := w, h
	maxX, maxY := 0, 0

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			bIn := x < bBounds.Dx() && y < bBounds.Dy()
			cIn := x < cBounds.Dx() && y < cBounds.Dy()

			if !bIn && !cIn {
				continue
			}

			if !bIn || !cIn {
				// Pixel exists in one image but not the other (dimension mismatch)
				mismatched++
				diffImg.Set(x, y, DiffHighlightColor)
				updateBounds(&minX, &minY, &maxX, &maxY, x, y)
				continue
			}

			bColor := baseline.At(bBounds.Min.X+x, bBounds.Min.Y+y)
			cColor := candidate.At(cBounds.Min.X+x, cBounds.Min.Y+y)

			if isColorDifferent(bColor, cColor, colorTolerance) {
				mismatched++
				diffImg.Set(x, y, DiffHighlightColor)
				updateBounds(&minX, &minY, &maxX, &maxY, x, y)
			} else {
				// Matching pixel: draw dimmed candidate pixel for context
				orig := color.RGBAModel.Convert(cColor).(color.RGBA)
				// Dim to 35% alpha blended onto dark background
				dimmed := color.RGBA{
					R: uint8(float64(orig.R) * 0.4),
					G: uint8(float64(orig.G) * 0.4),
					B: uint8(float64(orig.B) * 0.4),
					A: 255,
				}
				diffImg.Set(x, y, dimmed)
			}
		}
	}

	diffPercent := float64(mismatched) / float64(totalPixels) * 100.0
	passed := diffPercent <= maxDiffPercent

	var diffBounds image.Rectangle
	if mismatched > 0 {
		diffBounds = image.Rect(minX, minY, maxX+1, maxY+1)
	}

	// Encode diff image to PNG bytes
	var buf bytes.Buffer
	if err := png.Encode(&buf, diffImg); err != nil {
		return nil, nil, fmt.Errorf("failed to encode diff image: %w", err)
	}

	res := &DiffResult{
		TotalPixels:       totalPixels,
		MismatchedPixels:  mismatched,
		DiffPercent:       diffPercent,
		Passed:            passed,
		MaxDiffPercent:    maxDiffPercent,
		DiffBounds:        diffBounds,
		DiffImagePNGBytes: buf.Bytes(),
	}

	return res, diffImg, nil
}

func updateBounds(minX, minY, maxX, maxY *int, x, y int) {
	if x < *minX {
		*minX = x
	}
	if x > *maxX {
		*maxX = x
	}
	if y < *minY {
		*minY = y
	}
	if y > *maxY {
		*maxY = y
	}
}

func isColorDifferent(c1, c2 color.Color, tolerance float64) bool {
	r1, g1, b1, a1 := c1.RGBA()
	r2, g2, b2, a2 := c2.RGBA()

	// Colors are uint32 in range [0, 0xffff]
	maxDiff := tolerance * 65535.0

	dr := math.Abs(float64(r1) - float64(r2))
	dg := math.Abs(float64(g1) - float64(g2))
	db := math.Abs(float64(b1) - float64(b2))
	da := math.Abs(float64(a1) - float64(a2))

	return dr > maxDiff || dg > maxDiff || db > maxDiff || da > maxDiff
}

// EncodeImageToBase64 converts an image.Image into a data URI base64 string.
func EncodeImageToBase64(img image.Image) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// RenderSolidRect helper to create mock test images.
func CreateSolidImage(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return img
}
