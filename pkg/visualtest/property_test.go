package visualtest

import (
	"image"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// TestProperty_VisualSelfIdentity verifies that comparing any generated image
// to itself always yields exactly 0% difference and passed = true.
func TestProperty_VisualSelfIdentity(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := rapid.IntRange(2, 30).Draw(t, "width")
		h := rapid.IntRange(2, 30).Draw(t, "height")
		img := image.NewRGBA(image.Rect(0, 0, w, h))

		r := uint8(rapid.IntRange(0, 255).Draw(t, "r"))
		g := uint8(rapid.IntRange(0, 255).Draw(t, "g"))
		b := uint8(rapid.IntRange(0, 255).Draw(t, "b"))

		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
			}
		}

		res, diffImg, err := Compare(img, img, 0.0, 0.0)
		require.NoError(t, err)
		assert.NotNil(t, diffImg)
		assert.Equal(t, int64(0), res.MismatchedPixels)
		assert.Equal(t, 0.0, res.DiffPercent)
		assert.True(t, res.Passed)
	})
}

// TestProperty_VisualSymmetry verifies that Compare(A, B) and Compare(B, A)
// produce identical mismatched pixel counts and diff percentages.
func TestProperty_VisualSymmetry(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := rapid.IntRange(2, 25).Draw(t, "width")
		h := rapid.IntRange(2, 25).Draw(t, "height")

		imgA := image.NewRGBA(image.Rect(0, 0, w, h))
		imgB := image.NewRGBA(image.Rect(0, 0, w, h))

		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				rA := uint8(rapid.IntRange(0, 255).Draw(t, "rA"))
				gA := uint8(rapid.IntRange(0, 255).Draw(t, "gA"))
				bA := uint8(rapid.IntRange(0, 255).Draw(t, "bA"))
				imgA.Set(x, y, color.RGBA{R: rA, G: gA, B: bA, A: 255})

				rB := uint8(rapid.IntRange(0, 255).Draw(t, "rB"))
				gB := uint8(rapid.IntRange(0, 255).Draw(t, "gB"))
				bB := uint8(rapid.IntRange(0, 255).Draw(t, "bB"))
				imgB.Set(x, y, color.RGBA{R: rB, G: gB, B: bB, A: 255})
			}
		}

		resAB, _, err := Compare(imgA, imgB, 0.05, 5.0)
		require.NoError(t, err)

		resBA, _, err := Compare(imgB, imgA, 0.05, 5.0)
		require.NoError(t, err)

		assert.Equal(t, resAB.MismatchedPixels, resBA.MismatchedPixels)
		assert.Equal(t, resAB.DiffPercent, resBA.DiffPercent)
		assert.Equal(t, resAB.Passed, resBA.Passed)
	})
}

// TestProperty_VisualInversion verifies that completely inverting an image
// results in 100% mismatched pixels when tolerance is zero.
func TestProperty_VisualInversion(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		w := rapid.IntRange(2, 20).Draw(t, "width")
		h := rapid.IntRange(2, 20).Draw(t, "height")

		imgA := image.NewRGBA(image.Rect(0, 0, w, h))
		imgB := image.NewRGBA(image.Rect(0, 0, w, h))

		// Fill imgA with black (0,0,0) and imgB with white (255,255,255)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				imgA.Set(x, y, color.RGBA{R: 0, G: 0, B: 0, A: 255})
				imgB.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
			}
		}

		res, _, err := Compare(imgA, imgB, 0.0, 0.0)
		require.NoError(t, err)
		assert.Equal(t, int64(w*h), res.MismatchedPixels)
		assert.Equal(t, 100.0, res.DiffPercent)
		assert.False(t, res.Passed)
	})
}
