package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeSHA_Normal(t *testing.T) {
	sha := "1234567890abcdef1234567890abcdef12345678"
	assert.Equal(t, "1234567890ab", safeSHA(sha, 12))
}

func TestSafeSHA_Short(t *testing.T) {
	sha := "short"
	assert.Equal(t, "short", safeSHA(sha, 12))
}

func TestSafeSHA_Empty(t *testing.T) {
	assert.Equal(t, "", safeSHA("", 12))
}

func TestSafeSHA_Exact(t *testing.T) {
	sha := "1234567890ab"
	assert.Equal(t, "1234567890ab", safeSHA(sha, 12))
}
