package argocd

import (
	"testing"

	"hegel.dev/go/hegel"
)

func TestSanitizeNameAlwaysWithinMaxLen(t *testing.T) {
	// Property: for any input string and maxLen >= 10, the result
	// never exceeds maxLen
	hegel.Test(t, func(ht *hegel.T) {
		name := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(300))
		maxLen := 63 // K8s name limit

		result := sanitizeName(name, maxLen)
		if len(result) > maxLen {
			ht.Fatalf("sanitizeName(%q, %d) = %q (len %d) exceeds maxLen",
				name, maxLen, result, len(result))
		}
	})
}

func TestSanitizeNameDeterministic(t *testing.T) {
	// Property: same input always produces same output (deterministic hash)
	hegel.Test(t, func(ht *hegel.T) {
		name := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(200))
		maxLen := 63

		result1 := sanitizeName(name, maxLen)
		result2 := sanitizeName(name, maxLen)
		if result1 != result2 {
			ht.Fatalf("sanitizeName is not deterministic: %q vs %q for input %q",
				result1, result2, name)
		}
	})
}

func TestSanitizeNamePreservesShortNames(t *testing.T) {
	// Property: names shorter than maxLen are returned unchanged
	hegel.Test(t, func(ht *hegel.T) {
		name := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(62))
		maxLen := 63

		if len(name) <= maxLen {
			result := sanitizeName(name, maxLen)
			if result != name {
				ht.Fatalf("sanitizeName(%q, %d) = %q but input was short enough to pass through",
					name, maxLen, result)
			}
		}
	})
}

func TestSanitizeNameNonEmpty(t *testing.T) {
	// Property: output is never empty for non-empty input
	hegel.Test(t, func(ht *hegel.T) {
		name := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(300))
		maxLen := 63

		result := sanitizeName(name, maxLen)
		if result == "" {
			ht.Fatalf("sanitizeName(%q, %d) returned empty string", name, maxLen)
		}
	})
}

func TestSanitizeNameDistinctInputsProduceDistinctOutputs(t *testing.T) {
	// Property: different long inputs produce different truncated outputs
	// (hash suffix should differentiate them)
	hegel.Test(t, func(ht *hegel.T) {
		name1 := hegel.Draw(ht, hegel.Text().MinSize(64).MaxSize(200))
		name2 := hegel.Draw(ht, hegel.Text().MinSize(64).MaxSize(200))
		maxLen := 63

		if name1 == name2 {
			return // skip trivial case
		}

		result1 := sanitizeName(name1, maxLen)
		result2 := sanitizeName(name2, maxLen)
		if result1 == result2 {
			ht.Fatalf("Hash collision: sanitizeName(%q) == sanitizeName(%q) == %q",
				name1, name2, result1)
		}
	})
}
