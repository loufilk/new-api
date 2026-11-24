package service

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// CheckModelTestAccess is a paid-test-only check, applied immediately before
// reserving quota and again at final HTTP dispatch. Ordinary requests do not
// query the settings and continue to work even if they are unavailable.
// A rejection after reservation is definitely unsent and follows the existing
// refund/finalization path; a later disable never interrupts admitted delivery.
func CheckModelTestAccess(c *gin.Context) *types.NewAPIError {
	if !common.GetContextKeyBool(c, constant.ContextKeyModelTestSingleAttempt) {
		return nil
	}
	// The manual Key diagnostic passed both live dashboard authorization and
	// ordinary TokenAuth. Its authority is the owned key, never legacy site settings.
	if selected := common.GetContextKeyInt(c, constant.ContextKeyKeyDiagnosticTokenID); selected > 0 {
		if c.GetInt("token_id") == selected && c.GetInt("key_diagnostic_owner") > 0 && c.GetInt("key_diagnostic_owner") == c.GetInt("id") {
			return nil
		}
		return types.NewErrorWithStatusCode(errors.New("Key diagnostic owner unavailable"), types.ErrorCodeAccessDenied, http.StatusForbidden, types.ErrOptionWithSkipRetry())
	}
	err := model.RequireModelTestTarget(c.Request.Context(),
		common.GetContextKeyString(c, constant.ContextKeyUsingGroup),
		common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
	if err == nil {
		return nil
	}
	code, status := "model_tests_disabled", http.StatusForbidden
	if errors.Is(err, model.ErrModelTestTargetUnavailable) {
		code = "model_or_group_unavailable"
	} else if !errors.Is(err, model.ErrModelTestsDisabled) {
		code, status = "model_test_settings_unavailable", http.StatusServiceUnavailable
	}
	c.Set("model_test_error_code", code)
	return types.NewErrorWithStatusCode(err, types.ErrorCodeAccessDenied, status, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}
