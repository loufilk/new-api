package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/relay_timing"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type timingMissingMetaFixture struct{ *dto.GeneralOpenAIRequest }

func (timingMissingMetaFixture) GetTokenCountMeta() *types.TokenCountMeta { return nil }

func TestTimedBillingPreservesEstimatorFailure(t *testing.T) {
	// The test asserts elapsed-stage presence on a local estimator error,
	// not CPU attribution or coverage of later selected-channel metadata.
	old := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = old })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{Request: timingMissingMetaFixture{&dto.GeneralOpenAIRequest{}},
		RelayFormat: types.RelayFormatOpenAI}

	s := &relay_timing.Sample{}
	normal := PrepareRequestBilling(c, info)
	measured := PrepareRequestBillingWithTiming(c, info, s)
	require.NotNil(t, normal)
	require.NotNil(t, measured)
	require.Equal(t, types.ErrorCodeCountTokenFailed, measured.GetErrorCode())
	require.Equal(t, normal.GetErrorCode(), measured.GetErrorCode())
	require.Equal(t, normal.Error(), measured.Error())
	require.Nil(t, info.Billing, "estimator failure never reaches pricing or precharge")
	got := s.Finish(http.StatusBadRequest, true, time.Time{}, time.Time{})
	require.Nil(t, got.RequestDecodeValidateUS, "the HTTP controller was not called")
	require.NotNil(t, got.BillingTokenMetadataUS)
	require.NotNil(t, got.BillingTokenEstimateUS)
}
