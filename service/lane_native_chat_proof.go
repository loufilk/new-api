package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/shopspring/decimal"
	"github.com/tidwall/gjson"
)

const (
	laneNativeChatBodyMax = 64 << 10
	// V1 deliberately admits only short, single-output requests. This is
	// stricter than the ordinary API limit and cannot widen ordinary input.
	laneNativeChatCompletionMax = 4096
)

var ErrLaneNativeChatProof = errors.New("native Chat lane proof is unavailable")

// LaneNativeChatPreparedPrice is an immutable snapshot of the ordinary price
// after ModelPriceHelper. It is not a monetary owner or an input-token bound.
// No application path calls the dormant constructor until the complete funding
// and dispatch integration exists.
type LaneNativeChatPreparedPrice struct {
	quote laneNativeChatQuote
}

type laneNativeChatQuote struct {
	RequestID, Group, Model          string
	GroupConfigVersion, UserRevision int64
	ModelRatio, CompletionRatio      float64
	GroupRatio, CacheRatio           float64
	CacheCreation, CacheCreation12h  float64
}

func PrepareLaneNativeChatPrice(info *relaycommon.RelayInfo, groupConfigVersion int64) (LaneNativeChatPreparedPrice, error) {
	if info == nil || groupConfigVersion < 0 || !laneNativeRequestID(info.RequestId) || !laneNativeName(info.UsingGroup) || info.UsingGroup == "auto" ||
		!laneNativeName(info.OriginModelName) || strings.HasSuffix(info.OriginModelName, "search-preview") ||
		info.GetBillingModelName() != info.OriginModelName || info.IsPlayground || !info.IsStream || info.TieredBillingSnapshot != nil ||
		info.QuotaClamp != nil || info.RelayMode != relayconstant.RelayModeChatCompletions || info.RelayFormat != types.RelayFormatOpenAI ||
		info.GetFinalRequestRelayFormat() != types.RelayFormatOpenAI {
		return LaneNativeChatPreparedPrice{}, ErrLaneNativeChatProof
	}
	p := info.PriceData
	if p.FreeModel || p.UsePrice || len(p.OtherRatios()) != 0 || p.GroupRatioInfo.UserPricingRevision < 0 {
		return LaneNativeChatPreparedPrice{}, ErrLaneNativeChatProof
	}
	for _, ratio := range []float64{p.ModelRatio, p.CompletionRatio, p.GroupRatioInfo.GroupRatio} {
		if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return LaneNativeChatPreparedPrice{}, ErrLaneNativeChatProof
		}
	}
	for _, ratio := range []float64{p.CacheRatio, p.CacheCreationRatio, p.CacheCreation12hRatio} {
		if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return LaneNativeChatPreparedPrice{}, ErrLaneNativeChatProof
		}
	}
	return LaneNativeChatPreparedPrice{quote: laneNativeChatQuote{
		RequestID: info.RequestId, Group: info.UsingGroup, Model: info.OriginModelName, GroupConfigVersion: groupConfigVersion,
		UserRevision: p.GroupRatioInfo.UserPricingRevision, ModelRatio: p.ModelRatio, CompletionRatio: p.CompletionRatio,
		GroupRatio: p.GroupRatioInfo.GroupRatio, CacheRatio: p.CacheRatio, CacheCreation: p.CacheCreationRatio, CacheCreation12h: p.CacheCreation12hRatio,
	}}, nil
}

// LaneNativeChatRoute is the trusted, final local routing decision, not a
// request header and not an arbitrary provider URL. AuthoritySHA256 must come
// from a vetted route configuration covering the actual origin, adaptor,
// credential generation and retry/policy revisions. This struct grants none of
// that authority by itself; the verifier must check its exact scope.
type LaneNativeChatRoute struct {
	ChannelID, ChannelType int
	Group, Model, Path     string
	AuthoritySHA256        string
}

// LaneNativeChatPromptBound verifies hard upper bounds for the exact vetted
// route and the exact unmodified final Chat envelope. The byte slice is a
// disposable copy for the verifier, never the send buffer and never persisted.
// A prompt estimate or byte-count heuristic is not production authority. The
// route must guarantee both the explicit output limit and no unsupported usage
// dimensions. No live verifier is installed by this change.
type LaneNativeChatPromptBound interface {
	VerifyNativeChatPromptBound(context.Context, LaneNativeChatRoute, []byte, int) (int, error)
}

// LaneNativeChatEvidence carries only bounded financial and route facts. The
// final-wire digest is HMAC-SHA256 under an internal key and is scoped to this
// request. None of the body, mutable request, arbitrary URL, auth headers or key
// is exposed to the durable owner.
type LaneNativeChatEvidence struct {
	RequestID, Group, Model            string
	ChannelID                          int
	PromptCap, CompletionCap, QuotaCap int
	FrozenPriceSHA256, RouteSHA256     string
	FinalWireSHA256                    string
}

// LaneNativeChatProof owns a bounded copy of the actual encoded bytes. A future
// sender must use OpenBody and BodySize, never the caller's mutable input or an
// unrelated GetBody clone. Building this proof does not touch the input and a
// pre-binding decline leaves it available for the existing ordinary path.
type LaneNativeChatProof struct {
	evidence LaneNativeChatEvidence
	quote    laneNativeChatQuote
	body     []byte
}

func (p LaneNativeChatProof) Evidence() LaneNativeChatEvidence { return p.evidence }
func (p LaneNativeChatProof) OpenBody() io.Reader {
	return laneNativeChatBodyReader{bytes.NewReader(p.body)}
}
func (p LaneNativeChatProof) BodySize() int64 { return int64(len(p.body)) }

// Hide bytes.Reader.WriteTo so a caller receives only copy-out reads from
// the sealed bytes. The HTTP request must set ContentLength from BodySize.
type laneNativeChatBodyReader struct{ bytes *bytes.Reader }

func (r laneNativeChatBodyReader) Read(p []byte) (int, error) { return r.bytes.Read(p) }

func (p LaneNativeChatPreparedPrice) VerifyFinalWire(ctx context.Context, route LaneNativeChatRoute, final []byte, key [32]byte, verifier LaneNativeChatPromptBound) (LaneNativeChatProof, error) {
	q := p.quote
	if ctx == nil || ctx.Err() != nil || q.RequestID == "" || verifier == nil || key == ([32]byte{}) ||
		route.ChannelID <= 0 || route.ChannelType != constant.ChannelTypeOpenAI || route.Group != q.Group || route.Model != q.Model ||
		route.Path != "/v1/chat/completions" || !laneNativeHash(route.AuthoritySHA256) || len(final) == 0 || len(final) > laneNativeChatBodyMax {
		return LaneNativeChatProof{}, ErrLaneNativeChatProof
	}
	body := bytes.Clone(final)
	completion, ok := laneNativeChatCompletionBound(body, q.Model)
	if !ok {
		return LaneNativeChatProof{}, ErrLaneNativeChatProof
	}
	prompt, err := verifier.VerifyNativeChatPromptBound(ctx, route, bytes.Clone(body), completion)
	if err != nil || ctx.Err() != nil || prompt <= 0 || prompt > common.MaxQuota-completion {
		return LaneNativeChatProof{}, ErrLaneNativeChatProof
	}
	// Support a conservative price upper bound for native text. Cache read
	// and write accounting can overlap, so reserve the sum of their most
	// expensive ratios if higher than the ordinary prompt ratio. The measured
	// usage gate still must bound those reported dimensions before settlement.
	cacheWrite := decimal.NewFromFloat(max(q.CacheCreation, q.CacheCreation12h))
	promptRatio := decimal.Max(decimal.NewFromInt(1), decimal.NewFromFloat(q.CacheRatio).Add(cacheWrite))
	tokens := decimal.NewFromInt(int64(prompt)).Mul(promptRatio).
		Add(decimal.NewFromInt(int64(completion)).Mul(decimal.NewFromFloat(q.CompletionRatio)))
	cap, err := common.QuotaFromDecimalStrict(tokens.Mul(decimal.NewFromFloat(q.ModelRatio)).Mul(decimal.NewFromFloat(q.GroupRatio)).Ceil())
	if err != nil || cap <= 0 {
		return LaneNativeChatProof{}, ErrLaneNativeChatProof
	}
	priceBytes, err := common.Marshal(q)
	if err != nil {
		return LaneNativeChatProof{}, ErrLaneNativeChatProof
	}
	routeBytes, err := common.Marshal(route)
	if err != nil {
		return LaneNativeChatProof{}, ErrLaneNativeChatProof
	}
	priceHash, routeHash := sha256.Sum256(priceBytes), sha256.Sum256(routeBytes)
	wire := hmac.New(sha256.New, key[:])
	_, _ = io.WriteString(wire, "newapi/lane/native-chat-wire/v1\x00"+http.MethodPost+"\x00"+q.RequestID+"\x00")
	_, _ = wire.Write(body)
	return LaneNativeChatProof{
		body: body, quote: q,
		evidence: LaneNativeChatEvidence{RequestID: q.RequestID, Group: q.Group, Model: q.Model, ChannelID: route.ChannelID,
			PromptCap: prompt, CompletionCap: completion, QuotaCap: cap,
			FrozenPriceSHA256: hex.EncodeToString(priceHash[:]), RouteSHA256: hex.EncodeToString(routeHash[:]), FinalWireSHA256: hex.EncodeToString(wire.Sum(nil))},
	}, nil
}

func laneNativeRequestID(s string) bool {
	return s != "" && len(s) <= 64 && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) == -1
}

func laneNativeName(s string) bool {
	return s != "" && len(s) <= 128 && utf8.ValidString(s) && strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) == -1
}

func laneNativeHash(s string) bool {
	if len(s) != sha256.Size*2 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

type laneNativeChatInput struct {
	Model     string `json:"model"`
	Stream    bool   `json:"stream"`
	MaxTokens *uint  `json:"max_tokens"`
	MaxOutput *uint  `json:"max_completion_tokens"`
	N         *uint  `json:"n"`
	Messages  []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func laneNativeChatCompletionBound(body []byte, model string) (int, bool) {
	if !utf8.Valid(body) || !gjson.ValidBytes(body) {
		return 0, false
	}
	root := gjson.ParseBytes(body)
	// Check unique exact keys before strict decoding: Go's strict decoder
	// alone permits repeated/case-insensitive keys and later-wins aliases.
	if !laneNativeChatObject(root, "model", "stream", "max_tokens", "max_completion_tokens", "n", "messages", "stream_options") ||
		!laneNativeChatObject(root.Get("stream_options"), "include_usage") || (root.Get("max_tokens").Exists() == root.Get("max_completion_tokens").Exists()) ||
		!root.Get("messages").IsArray() {
		return 0, false
	}
	validMessages := true
	root.Get("messages").ForEach(func(_, message gjson.Result) bool {
		validMessages = laneNativeChatObject(message, "role", "content")
		return validMessages
	})
	var chat laneNativeChatInput
	if !validMessages || common.DecodeJsonStrict(bytes.NewReader(body), &chat) != nil || chat.Model != model || !chat.Stream ||
		!chat.StreamOptions.IncludeUsage || len(chat.Messages) == 0 || root.Get("n").Exists() && (chat.N == nil || *chat.N != 1) {
		return 0, false
	}
	for _, msg := range chat.Messages {
		if (msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system") || strings.TrimSpace(msg.Content) == "" {
			return 0, false
		}
	}
	limit := chat.MaxTokens
	if limit == nil {
		limit = chat.MaxOutput
	}
	if limit == nil || *limit == 0 || *limit > laneNativeChatCompletionMax {
		return 0, false
	}
	return int(*limit), true
}

func laneNativeChatObject(value gjson.Result, allowed ...string) bool {
	if !value.IsObject() {
		return false
	}
	seen, valid := uint64(0), true
	value.ForEach(func(key, _ gjson.Result) bool {
		for i, field := range allowed {
			if key.String() == field {
				bit := uint64(1) << i
				valid = seen&bit == 0
				seen |= bit
				return valid
			}
		}
		valid = false
		return false
	})
	return valid
}
