package deployer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManifestSizeLimit(t *testing.T) {
	// Generate payload > 5MB
	data := []byte("apiVersion: diverge.io/v1alpha1\nkind: ServiceConfig\nmetadata:\n  name: x\nspec:\n  serviceName: " + strings.Repeat("a", (5<<20)+10))

	_, err := ParseDotDivergeConfig(data)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum size")
}
