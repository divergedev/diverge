package controller

import (
	"testing"

	"hegel.dev/go/hegel"
)

func TestDerivePhaseProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		val := hegel.Draw(ht, hegel.Integers(1, 100))
		if val <= 0 {
			t.Errorf("Expected positive int")
		}
	})
}
