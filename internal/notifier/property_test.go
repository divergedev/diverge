package notifier

import (
	"strings"
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

func TestSanitizeNoRawBackticks(t *testing.T) {
	// Property: sanitizeMarkdown output never contains unescaped backticks
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result := sanitizeMarkdown(input)

		for i, c := range result {
			if c == '`' {
				if i == 0 || result[i-1] != '\\' {
					ht.Fatalf("Found unescaped backtick at index %d in %q", i, result)
				}
			}
		}
	})
}

// --- sanitizeHeaderKey properties ---

func TestHeaderKeyAlwaysMatchesRFC7230(t *testing.T) {
	// Property: for ANY input string, sanitizeHeaderKey always returns
	// a string matching ^[A-Za-z0-9][A-Za-z0-9-]*$
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result := sanitizeHeaderKey(input)

		if !validHeaderKey.MatchString(result) {
			ht.Fatalf("sanitizeHeaderKey(%q) = %q does not match RFC 7230 token format", input, result)
		}
	})
}

func TestHeaderKeyNeverContainsShellDangerousChars(t *testing.T) {
	// Property: output never contains characters that could cause
	// shell injection when embedded in a curl command
	dangerousChars := "`$\"'\\;|&()<>!\n\r\t"
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result := sanitizeHeaderKey(input)

		if strings.ContainsAny(result, dangerousChars) {
			ht.Fatalf("sanitizeHeaderKey(%q) = %q contains dangerous shell characters", input, result)
		}
	})
}

func TestHeaderKeyNeverEmpty(t *testing.T) {
	// Property: sanitizeHeaderKey always returns a non-empty string
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result := sanitizeHeaderKey(input)

		if result == "" {
			ht.Fatalf("sanitizeHeaderKey(%q) returned empty string", input)
		}
	})
}

func TestHeaderKeyIdempotent(t *testing.T) {
	// Property: sanitizeHeaderKey is idempotent
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text())
		result1 := sanitizeHeaderKey(input)
		result2 := sanitizeHeaderKey(result1)

		if result1 != result2 {
			ht.Fatalf("sanitizeHeaderKey is not idempotent: f(%q)=%q, f(%q)=%q",
				input, result1, result1, result2)
		}
	})
}

func TestHeaderKeyPreservesValidInput(t *testing.T) {
	// Property: valid header keys pass through unchanged
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(50))

		if validHeaderKey.MatchString(input) {
			result := sanitizeHeaderKey(input)
			if result != input {
				ht.Fatalf("sanitizeHeaderKey(%q) = %q but input was already valid", input, result)
			}
		}
	})
}

func TestHeaderKeyRejectsNewlines(t *testing.T) {
	// Property: any input containing a newline is rejected (falls back to default)
	hegel.Test(t, func(ht *hegel.T) {
		prefix := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(20))
		suffix := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(20))
		input := prefix + "\n" + suffix

		result := sanitizeHeaderKey(input)
		if result != "x-diverge-env" {
			ht.Fatalf("sanitizeHeaderKey(%q) = %q, expected fallback to default", input, result)
		}
	})
}

// --- escapeProjectPath properties ---

func TestEscapeProjectPathOutputHasExactlyOneSlash(t *testing.T) {
	// Property: when escapeProjectPath succeeds, the output contains
	// exactly one unencoded "/" separating owner and repo
	hegel.Test(t, func(ht *hegel.T) {
		owner := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))
		repo := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))

		// Skip inputs that would be rejected
		if owner == "." || owner == ".." || repo == "." || repo == ".." {
			return
		}

		project := owner + "/" + repo
		result, err := escapeProjectPath(project)
		if err != nil {
			return // some inputs are validly rejected
		}

		// Count unencoded slashes (encoded ones are %2F)
		slashCount := 0
		for i := 0; i < len(result); i++ {
			if result[i] == '/' {
				slashCount++
			}
		}
		if slashCount != 1 {
			ht.Fatalf("escapeProjectPath(%q) = %q has %d slashes, expected 1",
				project, result, slashCount)
		}
	})
}

func TestEscapeProjectPathNeverContainsRawDotDot(t *testing.T) {
	// Property: the output never contains an unencoded ".." path segment
	// that could cause path traversal
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(100))

		result, err := escapeProjectPath(input)
		if err != nil {
			return // rejected is safe
		}

		// Split on / and check each segment for raw ..
		parts := strings.SplitN(result, "/", 2)
		for _, part := range parts {
			if part == ".." {
				ht.Fatalf("escapeProjectPath(%q) = %q contains raw '..' path segment", input, result)
			}
		}
	})
}

func TestEscapeProjectPathValidInputRoundtrips(t *testing.T) {
	// Property: for simple alphanumeric+hyphen owner/repo strings,
	// the output matches the input (no encoding needed)
	hegel.Test(t, func(ht *hegel.T) {
		owner := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(30))
		repo := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(30))

		// Only test with alphanumeric+hyphen chars (GitHub-valid names)
		isSimple := func(s string) bool {
			for _, c := range s {
				isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
				isDigit := c >= '0' && c <= '9'
				if !isAlpha && !isDigit && c != '-' && c != '.' {
					return false
				}
			}
			return s != "." && s != ".." && len(s) > 0
		}

		if !isSimple(owner) || !isSimple(repo) {
			return
		}

		project := owner + "/" + repo
		result, err := escapeProjectPath(project)
		if err != nil {
			ht.Fatalf("escapeProjectPath(%q) returned unexpected error: %v", project, err)
		}
		if result != project {
			ht.Fatalf("escapeProjectPath(%q) = %q but input was simple enough to pass through", project, result)
		}
	})
}

func TestEscapeProjectPathRejectsNoSlash(t *testing.T) {
	// Property: any input without a "/" is always rejected
	hegel.Test(t, func(ht *hegel.T) {
		input := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(50))

		// Filter out inputs that contain /
		if strings.Contains(input, "/") {
			return
		}

		_, err := escapeProjectPath(input)
		if err == nil {
			ht.Fatalf("escapeProjectPath(%q) should have failed (no slash) but returned nil error", input)
		}
	})
}
