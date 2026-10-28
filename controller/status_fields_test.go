package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Product bonus fields moved to the console public-config endpoint. /api/status
// must not re-expose them, and it must not surface the retired freeze/affiliate
// fields or passkey origins either.
func TestGetStatusKeepsProductBonusAndAuthFieldsOut(t *testing.T) {
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() { common.OptionMap = originalOptionMap })

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

	GetStatus(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	for _, absent := range []string{
		"signup_bonus_enabled",
		"signup_bonus_quota",
		"referral_bonus_enabled",
		"referral_bonus_rate",
		"referral_bonus_freeze_days",
		"affiliate_reward_enabled",
		"affiliate_reward_rate",
		"affiliate_freeze_days",
		"affiliate_min_withdraw_quota",
		"passkey_origins",
	} {
		assert.NotContains(t, payload.Data, absent)
	}
	assert.Contains(t, payload.Data, "register_enabled")
	assert.Contains(t, payload.Data, "quota_per_unit")
}
