package console

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPublicConfigExposesOnlyPublicProductFields(t *testing.T) {
	previousRegister, previousSystemName, previousVersion := common.RegisterEnabled, common.SystemName, common.Version
	previousQuotaPerUnit := common.QuotaPerUnit
	previousSignupEnabled, previousSignupQuota := common.SignupBonusEnabled, common.QuotaForSignupBonus
	previousReferralEnabled, previousReferralRate := common.AffiliateRewardEnabled, common.AffiliateRewardRate
	passkeySettings := system_setting.GetPasskeySettings()
	previousPasskeySettings := *passkeySettings
	t.Cleanup(func() {
		common.RegisterEnabled, common.SystemName, common.Version = previousRegister, previousSystemName, previousVersion
		common.QuotaPerUnit = previousQuotaPerUnit
		common.SignupBonusEnabled, common.QuotaForSignupBonus = previousSignupEnabled, previousSignupQuota
		common.AffiliateRewardEnabled, common.AffiliateRewardRate = previousReferralEnabled, previousReferralRate
		*passkeySettings = previousPasskeySettings
	})
	common.RegisterEnabled = true
	common.SystemName = "Fixture API"
	common.Version = "v9.9.9"
	common.QuotaPerUnit = 123456
	common.SignupBonusEnabled = true
	common.QuotaForSignupBonus = 789
	common.AffiliateRewardEnabled = true
	common.AffiliateRewardRate = 0.2
	// A configured origin must never leak into the anonymous payload.
	passkeySettings.Origins = "https://sentinel-passkey-origin.example"

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/chuanapi/public-config", nil)

	GetPublicConfig(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	require.True(t, payload.Success)
	assert.Equal(t, map[string]any{
		"register_enabled":       true,
		"system_name":            "Fixture API",
		"version":                "v9.9.9",
		"quota_per_unit":         float64(123456),
		"quota_display_type":     operation_setting.GetQuotaDisplayType(),
		"signup_bonus_enabled":   true,
		"signup_bonus_quota":     float64(370368),
		"signup_bonus_provider":  "linuxdo",
		"referral_bonus_enabled": true,
		"referral_bonus_rate":    0.2,
	}, payload.Data)
	assert.NotContains(t, recorder.Body.String(), "sentinel-passkey-origin.example")
}
