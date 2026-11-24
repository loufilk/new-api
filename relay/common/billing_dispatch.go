package common

import (
	"context"
	"errors"
	"net/http"
	"strings"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// BillingHTTPDispatchClaimer is an optional, one-way dispatch capability for an
// already bound billing owner. Ordinary BillingSession does not implement it.
// It does not select a source, prove paid-only eligibility, reserve money, or
// opt a route into lanes. Only the native Chat common HTTP transport uses it.
//
// The implementation must atomically persist a single-use dispatch claim under
// the same durable owner as the reservation. Nil means COMMIT was acknowledged
// and grants only this outbound attempt. Replays, refusals and unknown COMMIT
// outcomes return errors and never grant another send, fallback or refund.
// Cross-request/process serialization and eventual reconciliation belong to
// that implementation, not to the request-local fence below. In particular,
// absence of provider bytes after a successful claim is not refund authority.
type BillingHTTPDispatchClaimer interface {
	ClaimBillingHTTPDispatch(context.Context) error
}

const billingHTTPDispatchAttemptKey = "billing_http_dispatch_owner_attempt"

// BillingHTTPDispatchOwned identifies an already bound owner even before its
// dispatch claim. The sticky request marker also survives a replaced RelayInfo
// or Billing field; later transport errors cannot return it to ordinary funding.
func BillingHTTPDispatchOwned(c *gin.Context, info *RelayInfo) bool {
	if (c != nil && c.GetBool(billingHTTPDispatchAttemptKey)) || laneNativeSyntheticHTTPIntentPinned(c) {
		return true
	}
	if info == nil {
		return false
	}
	_, owned := info.Billing.(BillingHTTPDispatchClaimer)
	return owned
}

// ClaimBillingHTTPDispatch runs only after normal HTTP preflight, immediately
// before the common transport sends. There is no header or configuration opt-in.
// Unsupported bindings fail closed; this seam does not advertise WebSocket,
// Responses conversions, plugins, asynchronous tasks or alternate transports.
func ClaimBillingHTTPDispatch(c *gin.Context, info *RelayInfo, req *http.Request) *types.NewAPIError {
	if c != nil && c.GetBool(billingHTTPDispatchAttemptKey) {
		return billingHTTPDispatchError(errors.New("billing HTTP dispatch was already attempted"))
	}
	if info == nil {
		if laneNativeSyntheticHTTPIntentPinned(c) {
			return billingHTTPDispatchError(errors.New("pinned billing owner is absent"))
		}
		return nil
	}
	claimer, owned := info.Billing.(BillingHTTPDispatchClaimer)
	if !owned {
		if laneNativeSyntheticHTTPIntentPinned(c) {
			return billingHTTPDispatchError(errors.New("pinned billing owner is absent"))
		}
		return nil
	}
	if c == nil || c.Request == nil {
		return billingHTTPDispatchError(errors.New("billing HTTP dispatch context is missing"))
	}
	// Mark BEFORE calling the durable implementation: an error can mean its
	// COMMIT succeeded. This marker is not durable evidence or a refund proof.
	c.Set(billingHTTPDispatchAttemptKey, true)
	if req == nil || req.URL == nil || req.Method != http.MethodPost || req.Body == nil || req.Body == http.NoBody ||
		!strings.HasSuffix(req.URL.EscapedPath(), "/chat/completions") || req.URL.RawQuery != "" ||
		c.Request.Method != http.MethodPost || c.Request.URL == nil || c.Request.URL.EscapedPath() != "/v1/chat/completions" || c.Request.URL.RawQuery != "" ||
		info.RelayMode != relayconstant.RelayModeChatCompletions || info.RelayFormat != types.RelayFormatOpenAI || info.GetFinalRequestRelayFormat() != types.RelayFormatOpenAI {
		return billingHTTPDispatchError(errors.New("billing owner does not support this HTTP transport"))
	}
	if err := claimer.ClaimBillingHTTPDispatch(c.Request.Context()); err != nil {
		return billingHTTPDispatchError(err)
	}
	return nil
}

func billingHTTPDispatchError(err error) *types.NewAPIError {
	return types.NewError(err, "billing_dispatch_unavailable", types.ErrOptionWithStatusCode(http.StatusServiceUnavailable),
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithHideErrMsg("billing dispatch unavailable"))
}
