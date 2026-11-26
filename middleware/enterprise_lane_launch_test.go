package middleware

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestEnterpriseLanePartialManifestCannotStart(t *testing.T) {
	for _, k := range EnterpriseLaneGatewayLaunchKeys() {
		t.Setenv(k, "")
	}
	t.Setenv(laneEnabledEnv, "false")
	t.Setenv(laneStopEnv, "false")
	require.NoError(t, PrepareEnterpriseLaneGateway(false))
	require.False(t, EnterpriseLaneGatewayEnabled())
	require.Contains(t, []string{"ordinary", "enterprise_lane_lab"}, EnterpriseLaneBuildProfile())
	for _, key := range EnterpriseLaneGatewayLaunchKeys() {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "1")
			require.Error(t, PrepareEnterpriseLaneGateway(false))
			require.False(t, EnterpriseLaneGatewayEnabled())
		})
	}
	t.Setenv(laneCountEnv, "false")
	require.Error(t, PrepareEnterpriseLaneGateway(false), "false may disable flags, not numeric or model settings")
}
