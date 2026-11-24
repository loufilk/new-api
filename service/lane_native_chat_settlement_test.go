package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const laneSettlementReceipt = `{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cached_tokens":80,"cache_write_tokens":70,"cache_write_tokens_details":{"ttl_30m_tokens":30,"ttl_12h_tokens":40}}}`

type laneSettlementPromptBound struct{}

func (laneSettlementPromptBound) VerifyNativeChatPromptBound(context.Context, LaneNativeChatRoute, []byte, int) (int, error) {
	return 113, nil
}

type laneSettlementOwnerFixture struct {
	t               *testing.T
	measurement     LaneNativeChatMeasurement
	settlement      []LaneNativeChatMeasured
	unknown, legacy int
	settleErr       error
}

func (f *laneSettlementOwnerFixture) Settle(int) error            { f.legacy++; return nil }
func (f *laneSettlementOwnerFixture) Refund(*gin.Context)         { f.legacy++ }
func (*laneSettlementOwnerFixture) NeedsRefund() bool             { return true }
func (*laneSettlementOwnerFixture) GetPreConsumedQuota() int      { return 0 }
func (*laneSettlementOwnerFixture) Reserve(int) error             { return nil }
func (*laneSettlementOwnerFixture) ReserveBeforeSubmit(int) error { return nil }
func (*laneSettlementOwnerFixture) ClaimBillingHTTPDispatch(context.Context) error {
	return errors.New("no synthetic dispatch installer")
}
func (f *laneSettlementOwnerFixture) NativeChatMeasurement() LaneNativeChatMeasurement {
	return f.measurement
}
func (f *laneSettlementOwnerFixture) SettleNativeChatMeasured(ctx context.Context, measured LaneNativeChatMeasured) error {
	assert.NoError(f.t, ctx.Err(), "post-delivery accounting is not cancelled with client")
	f.settlement = append(f.settlement, measured)
	return f.settleErr
}
func (f *laneSettlementOwnerFixture) PreserveNativeChatDeliveryUnknown(ctx context.Context) error {
	assert.NoError(f.t, ctx.Err())
	f.unknown++
	return nil
}

func setupNativeChatSettlement(t *testing.T) (*gin.Context, *relaycommon.RelayInfo, LaneNativeChatProof, *dto.Usage) {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{RequestId: "measured-native-1", IsStream: true, BillingSource: BillingSourceWallet,
		OriginModelName: "synthetic", UsingGroup: "test", RelayFormat: types.RelayFormatOpenAI, RelayMode: relayconstant.RelayModeChatCompletions,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI, ChannelId: 75},
		PriceData: hosttypes.PriceData{ModelRatio: 2, CompletionRatio: 4, CacheRatio: .5, CacheCreationRatio: 1.5,
			CacheCreation12hRatio: 3, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1.5}}}
	prepared, err := PrepareLaneNativeChatPrice(info, 4)
	require.NoError(t, err)
	route := LaneNativeChatRoute{ChannelID: 75, ChannelType: constant.ChannelTypeOpenAI, Group: "test", Model: "synthetic",
		Path: "/v1/chat/completions", AuthoritySHA256: strings.Repeat("a", 64)}
	body := []byte(`{"model":"synthetic","stream":true,"max_tokens":20,"messages":[{"role":"user","content":"synthetic"}],"stream_options":{"include_usage":true}}`)
	proof, err := prepared.VerifyFinalWire(t.Context(), route, body, sha256.Sum256([]byte("simple synthetic fixture")), laneSettlementPromptBound{})
	require.NoError(t, err)
	var usage dto.Usage
	require.NoError(t, common.Unmarshal([]byte(laneSettlementReceipt), &usage))
	return c, info, proof, &usage
}

func TestLaneNativeChatMeasuredSettlementOwnsEntireProjection(t *testing.T) {
	c, info, proof, usage := setupNativeChatSettlement(t)
	measurement, err := proof.VerifyTerminalUsage([]byte(laneSettlementReceipt))
	require.NoError(t, err)
	owner := &laneSettlementOwnerFixture{t: t, measurement: measurement}
	info.Billing = owner
	cancellation, cancel := context.WithCancel(c.Request.Context())
	cancel()
	c.Request = c.Request.WithContext(cancellation)
	PostTextConsumeQuota(c, info, usage, nil)
	owner.settleErr = errors.New("synthetic COMMIT acknowledgement lost")
	PostTextConsumeQuota(c, info, usage, nil)
	require.Len(t, owner.settlement, 2)
	first := owner.settlement[0]
	assert.Equal(t, first, owner.settlement[1], "exact immutable usage replay")
	assert.Equal(t, proof.Evidence(), first.Binding)
	// (max(0,100-80-70) + 80*.5 + 30*1.5 + 40*3 + 10*4) * 2 * 1.5
	assert.Equal(t, 735, first.ActualQuota)
	assert.Equal(t, 80, first.CacheReadTokens)
	assert.Equal(t, 70, first.CacheWriteTokens)
	assert.Equal(t, 40, first.CacheWrite12hTokens)
	assert.True(t, laneNativeHash(first.UsageSHA256))
	assert.EqualValues(t, 10, info.PerformanceOutputTokens)
	assert.Zero(t, owner.unknown, "uncertain settlement cannot start a different owner action")
	assert.Zero(t, owner.legacy)
	_, projected := common.GetContextKey(c, constant.ContextKeyConsumeLog)
	assert.False(t, projected, "ordinary consume projection must not run")
}

func TestLaneNativeChatUnconfirmedUsageNeverFallsBack(t *testing.T) {
	for _, reason := range []string{"no terminal proof", "local estimate", "incomplete", "mismatched merged usage", "changed price", "wrong channel", "unexpected audio", "provider delivery unknown"} {
		t.Run(reason, func(t *testing.T) {
			c, info, proof, usage := setupNativeChatSettlement(t)
			measurement, err := proof.VerifyTerminalUsage([]byte(laneSettlementReceipt))
			require.NoError(t, err)
			owner := &laneSettlementOwnerFixture{t: t, measurement: measurement}
			info.Billing = owner
			switch reason {
			case "no terminal proof":
				owner.measurement = LaneNativeChatMeasurement{}
			case "local estimate":
				common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
			case "incomplete":
				common.SetContextKey(c, constant.ContextKeyUpstreamResponseUsageIncomplete, true)
			case "mismatched merged usage":
				usage.PromptTokens++
			case "changed price":
				info.PriceData.GroupRatioInfo.GroupRatio *= 2
			case "wrong channel":
				info.ChannelId++
			}
			switch reason {
			case "unexpected audio":
				PostAudioConsumeQuota(c, info, usage, nil)
			case "provider delivery unknown":
				PreserveUnknownBillingDelivery(info)
			default:
				PostTextConsumeQuota(c, info, usage, nil)
			}
			assert.Equal(t, 1, owner.unknown)
			assert.Empty(t, owner.settlement)
			assert.Zero(t, owner.legacy)
			_, projected := common.GetContextKey(c, constant.ContextKeyConsumeLog)
			assert.False(t, projected)
		})
	}
}

func TestLaneNativeChatTerminalUsageIsStrictAndWithinProof(t *testing.T) {
	_, _, proof, _ := setupNativeChatSettlement(t)
	for _, tc := range []struct{ name, value string }{
		{"duplicate", `{"prompt_tokens":100,"prompt_tokens":1,"completion_tokens":10,"total_tokens":110}`},
		{"missing", `{"prompt_tokens":100,"total_tokens":100}`},
		{"mismatch", `{"prompt_tokens":100,"completion_tokens":10,"total_tokens":100}`},
		{"prompt over", `{"prompt_tokens":114,"completion_tokens":0,"total_tokens":114}`},
		{"completion over", `{"prompt_tokens":100,"completion_tokens":21,"total_tokens":121}`},
		{"cache read over", strings.Replace(laneSettlementReceipt, `"cached_tokens":80`, `"cached_tokens":101`, 1)},
		{"cache write over", strings.Replace(laneSettlementReceipt, `"cache_write_tokens":70`, `"cache_write_tokens":101`, 1)},
		{"TTL mismatch", strings.Replace(laneSettlementReceipt, `"ttl_12h_tokens":40`, `"ttl_12h_tokens":41`, 1)},
		{"null detail", strings.Replace(laneSettlementReceipt, `"cached_tokens":80`, `"cached_tokens":null`, 1)},
		{"audio detail", strings.Replace(laneSettlementReceipt, `"cached_tokens":80`, `"cached_tokens":80,"audio_tokens":1`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := proof.VerifyTerminalUsage([]byte(tc.value))
			assert.ErrorIs(t, err, ErrLaneNativeChatUsage)
		})
	}
}

func TestLaneNativeChatDispatchMarkerBlocksOrdinarySettlementAfterOwnerReplacement(t *testing.T) {
	c, info, _, usage := setupNativeChatSettlement(t)
	owner := &laneSettlementOwnerFixture{t: t}
	info.Billing = owner
	assert.ErrorIs(t, SettleBilling(c, info, 735), ErrLaneNativeChatUsage)
	req := httptest.NewRequest(http.MethodPost, "https://synthetic.invalid/v1/chat/completions", strings.NewReader("{}"))
	require.NotNil(t, relaycommon.ClaimBillingHTTPDispatch(c, info, req), "simulated ambiguous dispatch claim")
	info.Billing = nil
	assert.ErrorIs(t, SettleBilling(c, info, 735), ErrLaneNativeChatUsage)
	PostTextConsumeQuota(c, info, usage, nil)
	PostAudioConsumeQuota(c, info, usage, nil)
	assert.Zero(t, owner.legacy)
	assert.Zero(t, owner.unknown, "do not guess a replacement owner or resolution")
	_, projected := common.GetContextKey(c, constant.ContextKeyConsumeLog)
	assert.False(t, projected)
}
