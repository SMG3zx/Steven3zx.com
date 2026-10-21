package storetest

import "testing"

func TestMemoryChecksAndDeploymentsFollowTheirRules(t *testing.T) {
	testChecksAndDeployments(t, NewMemory())
}
