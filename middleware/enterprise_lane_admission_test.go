//go:build enterprise_lane_lab

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseLaneAdmissionLimits(t *testing.T) {
	gate := &enterpriseLaneAdmission{globalLimit: 2, laneLimit: 1}
	a, ok := gate.tryAcquire(0)
	require.True(t, ok)
	b, ok := gate.tryAcquire(0)
	require.False(t, ok)
	require.Nil(t, b)
	b, ok = gate.tryAcquire(1)
	require.True(t, ok, "a full lane must not reserve the remaining global slot")
	c, ok := gate.tryAcquire(2)
	require.False(t, ok)
	require.Nil(t, c)
	a.Release()
	a.Release()
	c, ok = gate.tryAcquire(2)
	require.True(t, ok)
	defer c.Release()
	defer b.Release()
	other, ok := gate.tryAcquire(0)
	require.False(t, ok, "double release must not create extra global capacity")
	require.Nil(t, other)
}

func TestEnterpriseLaneAdmissionManifest(t *testing.T) {
	for _, tc := range []struct{ global, lane string }{
		{"0", "1"}, {"1", "0"}, {"", "1"}, {"1", ""}, {"1", "2"}, {"3", "1"}, {"-1", "1"}, {"2000000", "1"},
	} {
		_, _, err := parseEnterpriseLaneAdmission(2, tc.global, tc.lane)
		require.Error(t, err, "%+v", tc)
	}
	global, lane, err := parseEnterpriseLaneAdmission(2, "2", "1")
	require.NoError(t, err)
	require.Equal(t, 2, global)
	require.Equal(t, 1, lane)
	global, lane, err = parseEnterpriseLaneAdmission(2, "", "")
	require.NoError(t, err)
	require.Zero(t, global)
	require.Zero(t, lane)
}

func TestEnterpriseLaneAdmissionDefaultAndCanceledContext(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	permit, denial := AcquireEnterpriseLaneAdmission(c)
	require.Equal(t, admissionBusy, denial)
	require.Nil(t, permit)

	c.Set(enterpriseLaneGatewayContextKey, enterpriseLaneGatewayAuthorization{})
	permit, denial = AcquireEnterpriseLaneAdmission(c)
	require.Empty(t, denial)
	require.Nil(t, permit)
	permit.Release()

	ctx, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	gate := &enterpriseLaneAdmission{globalLimit: 1, laneLimit: 1}
	c.Set(enterpriseLaneGatewayContextKey, enterpriseLaneGatewayAuthorization{admission: gate})
	permit, denial = AcquireEnterpriseLaneAdmission(c)
	require.Equal(t, admissionBusy, denial)
	require.Nil(t, permit)
	require.Zero(t, gate.active, "a canceled context must not consume intake capacity")
}
