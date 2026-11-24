package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const syntheticWire = `{"model":"gpt-4o-mini","stream":true,"max_completion_tokens":17,"messages":[{"role":"user","content":"synthetic private prompt"}],"stream_options":{"include_usage":true}}`

var syntheticWireKey = sha256.Sum256([]byte("synthetic-proof-key-no-secrets"))

type fixturePromptBound func(context.Context, service.LaneNativeChatRoute, []byte, int) (int, error)

func (f fixturePromptBound) VerifyNativeChatPromptBound(ctx context.Context, route service.LaneNativeChatRoute, body []byte, completion int) (int, error) {
	return f(ctx, route, body, completion)
}

func nativeProofFixture(t *testing.T) (*relaycommon.RelayInfo, service.LaneNativeChatRoute) {
	t.Helper()
	original := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-mini":1.5}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(original)) })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(syntheticWire))
	info := &relaycommon.RelayInfo{RequestId: "synthetic-proof-1", IsStream: true, UsingGroup: "default", UserGroup: "default",
		OriginModelName: "gpt-4o-mini", RelayMode: relayconstant.RelayModeChatCompletions, RelayFormat: types.RelayFormatOpenAI}
	price, err := helper.ModelPriceHelper(c, info, 13, &types.TokenCountMeta{MaxTokens: 17})
	require.NoError(t, err)
	require.Equal(t, info.PriceData.ModelRatio, price.ModelRatio)
	return info, service.LaneNativeChatRoute{ChannelID: 43, ChannelType: constant.ChannelTypeOpenAI,
		Group: info.UsingGroup, Model: info.OriginModelName, Path: "/v1/chat/completions", AuthoritySHA256: strings.Repeat("a", 64)}
}

func TestLaneNativeChatExactBecomesTheSentBody(t *testing.T) {
	info, route := nativeProofFixture(t)
	// Exercise the most expensive overlap case, not just default cache rates.
	info.PriceData.CacheRatio, info.PriceData.CacheCreationRatio, info.PriceData.CacheCreation12hRatio = 2, 3, 4
	prepared, err := service.PrepareLaneNativeChatPrice(info, 7)
	require.NoError(t, err)
	var verifies int
	verify := fixturePromptBound(func(_ context.Context, got service.LaneNativeChatRoute, body []byte, completion int) (int, error) {
		verifies++
		assert.Equal(t, route, got)
		assert.Equal(t, syntheticWire, string(body))
		assert.Equal(t, 17, completion)
		body[0] = '!'   // even a buggy trusted verifier cannot change the sent bytes
		return 113, nil // isolated fixture has this separately proved prompt cap
	})
	original := []byte(syntheticWire)
	proof, err := prepared.VerifyFinalWire(t.Context(), route, original, syntheticWireKey, verify)
	require.NoError(t, err)
	assert.Equal(t, 1, verifies)
	assert.Equal(t, syntheticWire, string(original), "verification must not mutate ordinary input")
	frozen := proof.Evidence()
	p := info.PriceData
	want, err := common.QuotaFromDecimalStrict(decimal.NewFromInt(113 * 6).
		Add(decimal.NewFromInt(17).Mul(decimal.NewFromFloat(p.CompletionRatio))).
		Mul(decimal.NewFromFloat(p.ModelRatio)).Mul(decimal.NewFromFloat(p.GroupRatioInfo.GroupRatio)).Ceil())
	require.NoError(t, err)
	assert.Equal(t, want, frozen.QuotaCap)
	assert.Equal(t, 113, frozen.PromptCap)
	assert.Equal(t, 17, frozen.CompletionCap)
	assert.Equal(t, route.ChannelID, frozen.ChannelID)
	assert.Len(t, frozen.FrozenPriceSHA256, 64)
	assert.Len(t, frozen.RouteSHA256, 64)
	rawDigest := sha256.Sum256([]byte(syntheticWire))
	assert.NotEqual(t, hex.EncodeToString(rawDigest[:]), frozen.FinalWireSHA256)
	payload, err := common.Marshal(frozen)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "synthetic private prompt")
	// Original caller data can be destroyed/reused after proof. Sending only
	// the independent proof reader gives the provider the exact inspected bytes.
	original[0] = '!'
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(r.Body)
		assert.NoError(t, readErr)
		received <- string(body)
		assert.Equal(t, proof.BodySize(), r.ContentLength)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+route.Path, proof.OpenBody())
	require.NoError(t, err)
	req.ContentLength = proof.BodySize()
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, syntheticWire, <-received)
	assert.Equal(t, int64(len(syntheticWire)), proof.BodySize())
	second, err := io.ReadAll(proof.OpenBody())
	require.NoError(t, err)
	assert.Equal(t, syntheticWire, string(second))
	mutated := frozen
	mutated.QuotaCap = 0
	assert.Equal(t, frozen, proof.Evidence())
	// Price mutation after Prepare cannot change the frozen proof.
	info.PriceData.ModelRatio *= 2
	replay, err := prepared.VerifyFinalWire(t.Context(), route, []byte(syntheticWire), syntheticWireKey, verify)
	require.NoError(t, err)
	assert.Equal(t, frozen, replay.Evidence())
	repriced, err := service.PrepareLaneNativeChatPrice(info, 7)
	require.NoError(t, err)
	later, err := repriced.VerifyFinalWire(t.Context(), route, []byte(syntheticWire), syntheticWireKey, verify)
	require.NoError(t, err)
	assert.NotEqual(t, frozen.FrozenPriceSHA256, later.Evidence().FrozenPriceSHA256)
	assert.Greater(t, later.Evidence().QuotaCap, frozen.QuotaCap)
	assert.Equal(t, frozen.FinalWireSHA256, later.Evidence().FinalWireSHA256)
}

func TestLaneNativeChatRejectsAmbiguousFinalWireBeforeVerifier(t *testing.T) {
	info, route := nativeProofFixture(t)
	quote, err := service.PrepareLaneNativeChatPrice(info, 7)
	require.NoError(t, err)
	verifier := fixturePromptBound(func(context.Context, service.LaneNativeChatRoute, []byte, int) (int, error) {
		t.Error("unsupported shape must not reach the vetted verifier")
		return 1, nil
	})
	for _, tc := range []struct{ name, wire string }{
		{"duplicate maximum", strings.Replace(syntheticWire, `"max_completion_tokens":17`, `"max_completion_tokens":17,"max_completion_tokens":2`, 1)},
		{"escaped duplicate", strings.Replace(syntheticWire, `"max_completion_tokens":17`, `"max_completion_tokens":17,"max_completi\u006fn_tokens":2`, 1)},
		{"case variant", strings.Replace(syntheticWire, `"max_completion_tokens"`, `"MAX_COMPLETION_TOKENS"`, 1)},
		{"both aliases", strings.Replace(syntheticWire, `"max_completion_tokens":17`, `"max_completion_tokens":17,"max_tokens":null`, 1)},
		{"zero", strings.Replace(syntheticWire, `:17`, `:0`, 1)},
		{"above V1 bound", strings.Replace(syntheticWire, `:17`, `:4097`, 1)},
		{"above ingress bound", strings.Replace(syntheticWire, `:17`, `:1073741824`, 1)},
		{"overflow", strings.Replace(syntheticWire, `:17`, `:2147483648`, 1)},
		{"multiple choices", strings.Replace(syntheticWire, `:17`, `:17,"n":2`, 1)},
		{"null choices", strings.Replace(syntheticWire, `:17`, `:17,"n":null`, 1)},
		{"no usage stream", strings.Replace(syntheticWire, `"include_usage":true`, `"include_usage":false`, 1)},
		{"duplicate nested usage", strings.Replace(syntheticWire, `"include_usage":true`, `"include_usage":true,"include_usage":false`, 1)},
		{"nontext", strings.Replace(syntheticWire, `"synthetic private prompt"`, `[{"type":"text","text":"synthetic"}]`, 1)},
		{"tools", strings.Replace(syntheticWire, `:17`, `:17,"tools":[]`, 1)},
		{"route model differs", strings.Replace(syntheticWire, `gpt-4o-mini`, `alternate-model`, 1)},
		{"trailing value", syntheticWire + "{}"},
		{"oversize", syntheticWire + strings.Repeat(" ", 64<<10)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(tc.wire)
			proof, err := quote.VerifyFinalWire(t.Context(), route, body, syntheticWireKey, verifier)
			assert.ErrorIs(t, err, service.ErrLaneNativeChatProof)
			assert.Zero(t, proof.BodySize())
			assert.Equal(t, tc.wire, string(body))
		})
	}
}

func TestLaneNativeChatRequiresAnAuditedBoundNotOrdinaryEstimate(t *testing.T) {
	info, route := nativeProofFixture(t)
	quote, err := service.PrepareLaneNativeChatPrice(info, 7)
	require.NoError(t, err)
	body := []byte(syntheticWire)
	_, err = quote.VerifyFinalWire(t.Context(), route, body, syntheticWireKey, nil)
	require.ErrorIs(t, err, service.ErrLaneNativeChatProof)
	for _, tc := range []struct {
		name   string
		change func(*service.LaneNativeChatRoute)
		prompt int
		err    error
	}{
		{name: "unvetted provider", change: func(route *service.LaneNativeChatRoute) { route.AuthoritySHA256 = "" }, prompt: 35},
		{name: "alternate path", change: func(route *service.LaneNativeChatRoute) { route.Path = "/v1/responses" }, prompt: 35},
		{name: "different group", change: func(route *service.LaneNativeChatRoute) { route.Group = "other" }, prompt: 35},
		{name: "verifier refused", prompt: 35, err: errors.New("do not expose synthetic provider internals")},
		{name: "zero bound"},
		{name: "overflowing bound", prompt: math.MaxInt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := route
			if tc.change != nil {
				tc.change(&current)
			}
			verify := fixturePromptBound(func(_ context.Context, _ service.LaneNativeChatRoute, _ []byte, _ int) (int, error) {
				if tc.change != nil {
					t.Error("wrong route reached the verifier")
				}
				return tc.prompt, tc.err
			})
			_, err := quote.VerifyFinalWire(t.Context(), current, bytes.Clone(body), syntheticWireKey, verify)
			require.ErrorIs(t, err, service.ErrLaneNativeChatProof)
			assert.NotContains(t, err.Error(), "synthetic provider internals")
		})
	}
	assert.Nil(t, info.Billing, "preparation/proof must not install or charge a billing owner")
}
