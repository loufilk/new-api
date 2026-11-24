package relay

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dispatchFailureBilling struct {
	recordedSettlement
	claimErr error
}

func (s *dispatchFailureBilling) ClaimBillingHTTPDispatch(context.Context) error { return s.claimErr }

func TestBillingHTTPDispatchFailuresCannotRetryOrRefund(t *testing.T) {
	for _, reason := range []string{"preflight", "refused", "lost ACK", "dial failure", "provider rejection", "cancelled after claim", "owner replaced"} {
		t.Run(reason, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			billing := &dispatchFailureBilling{}
			info := &relaycommon.RelayInfo{Billing: billing, RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI}
			failure := errors.New(reason)
			if reason == "refused" || reason == "lost ACK" {
				billing.claimErr = failure
			}
			if reason != "preflight" {
				req := httptest.NewRequest(http.MethodPost, "https://synthetic.invalid/v1/chat/completions", strings.NewReader("{}"))
				claimErr := relaycommon.ClaimBillingHTTPDispatch(c, info, req)
				if billing.claimErr != nil {
					require.NotNil(t, claimErr)
				} else {
					require.Nil(t, claimErr)
				}
			}
			if reason == "dial failure" {
				failure = &net.OpError{Op: "dial", Err: errors.New("unreachable")}
			}
			if reason == "provider rejection" {
				common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
			}
			if reason == "cancelled after claim" {
				failure = context.Canceled
			}
			if reason == "owner replaced" {
				info.Billing = &billing.recordedSettlement
			}
			apiErr := types.NewError(failure, types.ErrorCodeDoRequestFailed)
			preserved := PreserveFailedRequestBilling(c, info, apiErr)
			assert.True(t, types.IsSkipRetryError(preserved))
			final := RefundFailedRequestBilling(c, info, preserved)
			assert.True(t, types.IsSkipRetryError(final))
			assert.False(t, billing.refunded)
			assert.Empty(t, billing.settled)
		})
	}
}

func TestBillingHTTPDispatchCapabilityNotInstalledOnOrdinarySession(t *testing.T) {
	assert.NotImplements(t, (*relaycommon.BillingHTTPDispatchClaimer)(nil), &service.BillingSession{})
	// This seam is inert for current ordinary billing. HTTP headers are not an
	// eligibility mechanism, and no current funding factory installs the port.
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("X-Enterprise-Lane", "true")
	billing := &recordedSettlement{}
	info := &relaycommon.RelayInfo{Billing: billing}
	err := types.NewError(&net.OpError{Op: "dial", Err: errors.New("unreachable")}, types.ErrorCodeDoRequestFailed)
	assert.False(t, types.IsSkipRetryError(PreserveFailedRequestBilling(c, info, err)))
	assert.False(t, relaycommon.BillingHTTPDispatchOwned(c, info))
}

type observedDispatchFailureBilling struct {
	dispatchFailureBilling
	unknown int
}

func (*observedDispatchFailureBilling) NativeChatMeasurement() service.LaneNativeChatMeasurement {
	return service.LaneNativeChatMeasurement{}
}

func (*observedDispatchFailureBilling) SettleNativeChatMeasured(context.Context, service.LaneNativeChatMeasured) error {
	return errors.New("unmeasured test failure cannot settle")
}

func (s *observedDispatchFailureBilling) PreserveNativeChatDeliveryUnknown(context.Context) error {
	s.unknown++
	return nil
}

func TestBillingHTTPDispatchReportsAttemptedFailureOnlyToObservedOwner(t *testing.T) {
	for _, attempted := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		billing := &observedDispatchFailureBilling{}
		info := &relaycommon.RelayInfo{Billing: billing}
		common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, attempted)
		apiErr := types.NewError(errors.New("synthetic provider failure"), types.ErrorCodeDoRequestFailed)
		if attempted {
			common.SetContextKey(c, constant.ContextKeyUpstreamExplicitRejection, true)
		}
		for range 2 {
			final := RefundFailedRequestBilling(c, info, apiErr)
			assert.True(t, types.IsSkipRetryError(final))
		}
		if attempted {
			assert.Equal(t, 1, billing.unknown, "an attempted failure is preserved under the exact owner only")
		} else {
			assert.Zero(t, billing.unknown, "a refused or ambiguous claim remains held for owner reconciliation")
		}
		assert.False(t, billing.refunded)
		assert.Empty(t, billing.settled)
	}
}
