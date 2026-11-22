package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
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

func TestGetStatusHidesInvoiceCenterWhenSettingsDatabaseIsUnavailable(t *testing.T) {
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "")
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "")

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)

	GetStatus(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.Equal(t, false, payload.Data["customer_invoice_center_visible"], "status must fail closed when invoice settings cannot be read")
	assert.Equal(t, false, payload.Data["customer_invoice_enabled"])
}

func TestGetStatusReflectsPersistedInvoiceCenterSwitchPostgres(t *testing.T) {
	db := setupNewUserBenefitControllerPostgres(t)
	require.NoError(t, db.AutoMigrate(model.CustomerInvoiceModels()...))
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "")
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "")
	root := createNewUserBenefitControllerUser(t, db, "invoice-center-root", false)
	require.NoError(t, db.Model(&root).Update("role", common.RoleRootUser).Error)

	readStatus := func() map[string]any {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		GetStatus(context)
		require.Equal(t, http.StatusOK, recorder.Code)
		var payload struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
		return payload.Data
	}
	settings, err := model.GetCustomerInvoiceSettings(t.Context())
	require.NoError(t, err)
	status := readStatus()
	assert.Equal(t, true, status["customer_invoice_center_visible"], "no merchant or payment rail is needed for the default navigation entry")
	assert.Equal(t, false, status["customer_invoice_enabled"], "unconfigured merchant must keep intake disabled")
	t.Setenv("CUSTOMER_INVOICE_EPAY_MERCHANT_SCOPE", "invoice-status-fixture")
	assert.Equal(t, true, readStatus()["customer_invoice_enabled"])
	settings.CenterEnabled = false
	settings, err = model.UpdateCustomerInvoiceSettings(t.Context(), root.Id, settings)
	require.NoError(t, err)
	status = readStatus()
	assert.Equal(t, false, status["customer_invoice_center_visible"])
	assert.Equal(t, false, status["customer_invoice_enabled"])
	settings.CenterEnabled = true
	_, err = model.UpdateCustomerInvoiceSettings(t.Context(), root.Id, settings)
	require.NoError(t, err)
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "false")
	status = readStatus()
	assert.Equal(t, false, status["customer_invoice_center_visible"], "the environment override remains a hard shutdown")
	assert.Equal(t, false, status["customer_invoice_enabled"])
}
