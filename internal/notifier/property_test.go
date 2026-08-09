package notifier

import (
	"testing"

	"hegel.dev/go/hegel"
)

func TestSanitizeNoPipes(t *testing.T) {
	// Property: sanitizeMarkdown output never contains raw pipes
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result := sanitizeMarkdown(input)

		// Check that any | in the result is preceded by \
		for i, c := range result {
			if c == '|' {
				if i == 0 || result[i-1] != '\\' {
					ht.Fatalf("Found unescaped pipe at index %d in %q", i, result)
				}
			}
		}
	})
}

func TestSanitizeNoMentions(t *testing.T) {
	// Property: sanitizeMarkdown output never contains raw @mentions
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result := sanitizeMarkdown(input)

		for i, c := range result {
			if c == '@' {
				// We expect zero-width space after @
				if i+1 >= len(result) || result[i+1:i+4] != "\u200b" {
					ht.Fatalf("Found raw @ at index %d in %q", i, result)
				}
			}
		}
	})
}

func TestSanitizeIdempotent(t *testing.T) {
	// Property: sanitizeMarkdown is idempotent (sanitize(sanitize(x)) == sanitize(x))
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result1 := sanitizeMarkdown(input)
		result2 := sanitizeMarkdown(result1)

		if result1 != result2 {
			ht.Fatalf("sanitizeMarkdown is not idempotent: sanitize(%q) = %q, sanitize(%q) = %q", input, result1, result1, result2)
		}
	})
}
