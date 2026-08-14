//go:build e2e_dual

package e2e

import (
	"testing"
)

func TestE2E_DualCluster_PreviewRouting(t *testing.T) {
	// 1. Create mgmt and prod clusters
	createK3dCluster(t, "diverge-e2e-mgmt")
	createK3dCluster(t, "diverge-e2e-prod")

	// 2. Install CRDs on mgmt cluster
	installCRDs(t, "k3d-diverge-e2e-mgmt")

	// 3. Deploy echo-server on prod cluster
	deployEchoServer(t, "k3d-diverge-e2e-prod")

	// 4. Create PreviewGroup on mgmt
	// 5. Verify routing works on prod
}

func TestE2E_DualCluster_Teardown(t *testing.T) {
	// Create PG, delete it, verify cleanup on prod
}
