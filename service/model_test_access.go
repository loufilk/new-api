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
// query the switch and continue to work even if its configuration is unavailable.
// A rejection after reservation is definitely unsent and follows the existing
// refund/finalization path; a later disable never interrupts admitted delivery.
func CheckModelTestAccess(c *gin.Context) *types.NewAPIError {
	if !common.GetContextKeyBool(c, constant.ContextKeyModelTestSingleAttempt) {
		return nil
	}
	err := model.RequireModelTestEnabled(c.Request.Context())
	if err == nil {
		return nil
	}
	code, status := "model_tests_disabled", http.StatusForbidden
	if !errors.Is(err, model.ErrModelTestsDisabled) {
		code, status = "model_test_settings_unavailable", http.StatusServiceUnavailable
	}
	c.Set("model_test_error_code", code)
	return types.NewErrorWithStatusCode(err, types.ErrorCodeAccessDenied, status, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}
