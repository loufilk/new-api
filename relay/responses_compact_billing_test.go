package relay

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponsesCompactSettlesAdmittedQuote(t *testing.T) {
	service.InitHttpClient()
	batch, logConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() { common.BatchUpdateEnabled, common.LogConsumeEnabled = batch, logConsume })

	const (
		path         = "/v1/responses/compact"
		clientModel  = "compact-fixture-model"
		billingModel = "compact-admitted-quote-fixture"
		group        = "compact-admitted-group"
		expr         = `tier("admitted", p * 4 + c * 10 + cr) * (param("service_tier") == "priority" ? 2 : 1)`
		rawRequest   = `{"model":"compact-fixture-model","input":"hello","service_tier":"priority"}`
		rawResponse  = `{"id":"cmp_fixture","object":"response.compaction","output":[],"usage":{"input_tokens":10,"output_tokens":4,"total_tokens":14,"input_tokens_details":{"cached_tokens":2}}}`
	)
	for _, tc := range []struct {
		name string
		snap *billingexpr.BillingSnapshot
		want int
	}{
		{name: "legacy ratios", want: 63}, // (8 input + 2*0.5 cached + 4*3 output) * 2 model * 1.5 group
		{
			name: "frozen expression and request input",
			snap: &billingexpr.BillingSnapshot{
				BillingMode: "tiered_expr", ModelName: billingModel,
				ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), ExprVersion: 1,
				GroupRatio: 1.5, QuotaPerUnit: 500_000,
			},
			want: 111, // (8*4 + 4*10 + 2 cache) * 2 priority * 0.5 quota unit * 1.5 group
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var upstreamCalls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				assert.Equal(t, path, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, rawResponse)
			}))
			t.Cleanup(upstream.Close)

			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(rawRequest))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, clientModel)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "test-only")
			t.Cleanup(func() { common.CleanupBodyStorage(c) })

			var req dto.OpenAIResponsesCompactionRequest
			require.NoError(t, common.UnmarshalJsonStr(rawRequest, &req))
			info := relaycommon.GenRelayInfoResponsesCompaction(c, &req)
			// Only the admitted quote is supplied; completion must not need a
			// second catalogue entry or a second group authorization.
			info.BillingModelName = billingModel
			quote := hosttypes.PriceData{
				ModelRatio: 2, CompletionRatio: 3, CacheRatio: 0.5,
				GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1.5},
			}
			info.PriceData = quote
			info.TieredBillingSnapshot = tc.snap
			input := &billingexpr.RequestInput{Body: []byte(rawRequest)}
			info.BillingRequestInput = input
			if tc.snap != nil {
				_, _, err := billingexpr.RunExprWithRequest(expr, billingexpr.TokenParams{P: 10, Len: 10}, *input)
				require.NoError(t, err, "the fixture's frozen expression must be valid at admission")
			}
			info.UserSetting.QuotaWarningEnabled = new(bool)
			settlement := &recordedSettlement{}
			info.Billing = settlement

			require.Nil(t, ResponsesHelper(c, info))
			assert.Equal(t, int32(1), upstreamCalls.Load())
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.JSONEq(t, rawResponse, rec.Body.String())
			assert.Equal(t, []int{tc.want}, settlement.settled)
			assert.False(t, settlement.refunded)
			assert.True(t, c.GetBool(requestBillingUsageObservedKey))
			assert.Equal(t, quote, info.PriceData)
			assert.Same(t, input, info.BillingRequestInput)
			if tc.snap != nil {
				assert.Same(t, tc.snap, info.TieredBillingSnapshot)
			}

			log, ok := common.GetContextKeyType[*model.RecordConsumeLogParams](c, constant.ContextKeyConsumeLog)
			require.True(t, ok)
			assert.Equal(t, tc.want, log.Quota)
			assert.Equal(t, billingModel, log.ModelName)
			assert.Equal(t, group, log.Group)
			var other map[string]any
			require.NoError(t, common.UnmarshalJsonStr(log.Other.JSONString(), &other))
			assert.Equal(t, 1.5, other["group_ratio"])
			if tc.snap != nil {
				assert.Equal(t, "admitted", other["matched_tier"])
				assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(expr)), other["expr_b64"])
			} else {
				assert.Equal(t, float64(2), other["model_ratio"])
				assert.NotContains(t, other, "billing_mode")
			}
		})
	}
}
