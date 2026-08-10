package deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestArgoDeployer_ImplementsDeployer(t *testing.T) {
	// Compile-time interface compliance check
	var _ Deployer = (*ArgoDeployer)(nil)
	assert.True(t, true)
}

func TestDirectDeployer_ImplementsDeployer(t *testing.T) {
	var _ Deployer = (*DirectDeployer)(nil)
	assert.True(t, true)
}

func TestNoopDeployer_ImplementsDeployer(t *testing.T) {
	var _ Deployer = (*NoopDeployer)(nil)
	assert.True(t, true)
}

func TestInstrumentedDeployer_ImplementsDeployer(t *testing.T) {
	var _ Deployer = (*InstrumentedDeployer)(nil)
	assert.True(t, true)
}
