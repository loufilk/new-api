package privacy

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/require"
)

func TestNativeGatewayErrorRestoresClientContract(t *testing.T) {
	c := newPrivacyTestContext(common.RoleCommonUser, "public-model")
	c.Set(common.RequestIdKey, "native-error-1")
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, "https://private-upstream.example.net/v1")
	common.SetContextKey(c, constant.ContextKeyChannelKey, "auth-fixture-123")
	message := "model is not available at https://private-upstream.example.net/v1?key=fixture-query (key auth-fixture-123)"
	err := types.NewErrorWithStatusCode(errors.New(common.MessageWithRequestId(message, "native-error-1")), types.ErrorCodeModelNotFound, http.StatusNotFound)
	oa := ClientOpenAIError(c, err)
	require.Equal(t, "new_api_error", oa.Type)
	require.Equal(t, types.ErrorCodeModelNotFound, oa.Code)
	require.Contains(t, oa.Message, "model is not available")
	require.NotContains(t, oa.Message, "private-upstream")
	require.NotContains(t, oa.Message, "fixture-query")
	require.NotContains(t, oa.Message, "auth-fixture-123")
	require.Equal(t, 1, strings.Count(oa.Message, "request id: native-error-1"))
	claude := ClientClaudeError(c, err)
	require.Equal(t, "new_api_error", claude.Type)
	require.Equal(t, oa.Message, claude.Message)
}

func TestNativeGatewayErrorDoesNotExposeProviderOrInternalDetails(t *testing.T) {
	c := newPrivacyTestContext(common.RoleCommonUser, "public-model")
	provider := types.WithOpenAIError(types.OpenAIError{
		Type: "new_api_error", Code: "model_not_found", Message: "provider diagnostic",
		Metadata: []byte(`{"secret":"fixture-only"}`),
	}, http.StatusBadRequest)
	oa := ClientOpenAIError(c, provider)
	require.Equal(t, "invalid request", oa.Message)
	require.Empty(t, oa.Metadata)
	internal := types.NewErrorWithStatusCode(errors.New("internal query fixture"), types.ErrorCodeQueryDataError, http.StatusInternalServerError)
	require.Equal(t, string(types.ErrorCodeQueryDataError), ClientOpenAIError(c, internal).Code)
	require.Equal(t, "upstream request failed", ClientOpenAIError(c, internal).Message)
	for _, tc := range []struct {
		code   types.ErrorCode
		status int
	}{
		{types.ErrorCodeReadRequestBodyFailed, http.StatusRequestEntityTooLarge},
		{types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden},
	} {
		err := types.NewErrorWithStatusCode(errors.New("private fixture diagnostic"), tc.code, tc.status)
		got := ClientOpenAIError(c, err)
		require.Equal(t, string(tc.code), got.Code)
		require.NotContains(t, got.Message, "private fixture diagnostic")
	}

	require.Contains(t, string(SanitizeClientPayload(c, []byte(`{"type":"error","message":"provider diagnostic"}`))), "upstream request failed")
}
