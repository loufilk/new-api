package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
)

func TestGrokFeeRequiresSpecificProviderEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, code, message string
		channel, status     int
		fee                 bool
	}{
		{"Grok CSAM", "safety_check_type_csam", "Request blocked", constant.ChannelTypeXai, http.StatusBadRequest, true},
		{"Grok ordinary policies", "content_policy", "Content violates usage guidelines", constant.ChannelTypeXai, http.StatusBadRequest, false},
		{"other provider", "safety_check_type_csam", "Request blocked", constant.ChannelTypeOpenAI, http.StatusBadRequest, false},
		{"server failure", "safety_check_type_csam", "Request blocked", constant.ChannelTypeXai, http.StatusInternalServerError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native := types.OpenAIError{Type: "invalid_request_error", Code: tc.code, Message: tc.message}
			got := NormalizeViolationFeeError(types.WithOpenAIError(native, tc.status), tc.channel)
			assert.Equal(t, native, got.ToOpenAIError(), "preserve the native public error")
			assert.Equal(t, tc.fee, shouldChargeViolationFee(got, tc.channel))
		})
	}
}
