package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

var ErrLaneNativeChatUsage = errors.New("native Chat lane usage is unconfirmed")

// LaneNativeChatObservedExecution is an already-bound paid/key owner, not a
// funding selector. No application code implements or installs it in this
// slice. Its settle method is responsible for atomic immutable evidence,
// original owner replay, money, and durable projection of both usage stats
// and the consume log. Failure or unknown COMMIT must retain this ownership;
// returning an error can never delegate projection or money to ordinary code.
// A real adapter must durably freeze these facts before an uncertain terminal
// operation, or preserve a tested owner-specific repair contract. It must pin
// the same typed owner from binding through claim, measurement and settlement.
// PreserveNativeChatDeliveryUnknown must not reopen observed/terminal owners.
type LaneNativeChatObservedExecution interface {
	relaycommon.BillingHTTPDispatchClaimer
	NativeChatMeasurement() LaneNativeChatMeasurement
	SettleNativeChatMeasured(context.Context, LaneNativeChatMeasured) error
	PreserveNativeChatDeliveryUnknown(context.Context) error
}

// LaneNativeChatMeasured contains sanitized measured usage and pricing facts.
// It is not the full durable projection record. The owner must additionally
// freeze original user/Key and any required logging metadata under that owner.
// This value contains no provider content, credential, body or raw frame.
type LaneNativeChatMeasured struct {
	Binding                                                                                LaneNativeChatEvidence
	PromptTokens, CompletionTokens, CacheReadTokens, CacheWriteTokens, CacheWrite12hTokens int
	ActualQuota                                                                            int
	UsageSHA256                                                                            string
}

// LaneNativeChatMeasurement is a sealed value, validated against exact wire
// bounds before the ordinary measured pricing function can calculate its charge.
// The trusted adaptor must prove this was the one strict terminal usage object
// from the existing dispatch (not an estimate, aggregate of several frames or
// a replay from another request). This parser alone does not prove that fact.
type LaneNativeChatMeasurement struct {
	binding LaneNativeChatEvidence
	quote   laneNativeChatQuote
	usage   laneNativeChatTerminalUsage
}

type laneNativeChatTerminalUsage struct {
	Prompt     *int `json:"prompt_tokens"`
	Completion *int `json:"completion_tokens"`
	Total      *int `json:"total_tokens"`
	Details    struct {
		Read  int `json:"cached_tokens"`
		Write int `json:"cache_write_tokens"`
		TTL   *struct {
			Short int `json:"ttl_30m_tokens"`
			Long  int `json:"ttl_12h_tokens"`
		} `json:"cache_write_tokens_details"`
	} `json:"prompt_tokens_details"`
}

func (p LaneNativeChatProof) VerifyTerminalUsage(raw []byte) (LaneNativeChatMeasurement, error) {
	if p.BodySize() == 0 || len(raw) == 0 || len(raw) > 4096 || !gjson.ValidBytes(raw) {
		return LaneNativeChatMeasurement{}, ErrLaneNativeChatUsage
	}
	root := gjson.ParseBytes(raw)
	if !laneNativeChatObject(root, "prompt_tokens", "completion_tokens", "total_tokens", "prompt_tokens_details") {
		return LaneNativeChatMeasurement{}, ErrLaneNativeChatUsage
	}
	if details := root.Get("prompt_tokens_details"); details.Exists() {
		if !laneNativeChatObject(details, "cached_tokens", "cache_write_tokens", "cache_write_tokens_details") {
			return LaneNativeChatMeasurement{}, ErrLaneNativeChatUsage
		}
		for _, field := range []string{"cached_tokens", "cache_write_tokens"} {
			if count := details.Get(field); count.Exists() && count.Type != gjson.Number {
				return LaneNativeChatMeasurement{}, ErrLaneNativeChatUsage
			}
		}
		if ttl := details.Get("cache_write_tokens_details"); ttl.Exists() && (!laneNativeChatObject(ttl, "ttl_30m_tokens", "ttl_12h_tokens") ||
			ttl.Get("ttl_30m_tokens").Type != gjson.Number || ttl.Get("ttl_12h_tokens").Type != gjson.Number) {
			return LaneNativeChatMeasurement{}, ErrLaneNativeChatUsage
		}
	}
	var u laneNativeChatTerminalUsage
	b := p.evidence
	if common.DecodeJsonStrict(bytes.NewReader(raw), &u) != nil || u.Prompt == nil || u.Completion == nil || u.Total == nil ||
		*u.Prompt <= 0 || *u.Prompt > b.PromptCap || *u.Completion < 0 || *u.Completion > b.CompletionCap || *u.Total != *u.Prompt+*u.Completion ||
		u.Details.Read < 0 || u.Details.Read > *u.Prompt || u.Details.Write < 0 || u.Details.Write > *u.Prompt {
		return LaneNativeChatMeasurement{}, ErrLaneNativeChatUsage
	}
	if ttl := u.Details.TTL; ttl != nil && (ttl.Long < 0 || ttl.Long > u.Details.Write || ttl.Short != u.Details.Write-ttl.Long) {
		return LaneNativeChatMeasurement{}, ErrLaneNativeChatUsage
	}
	return LaneNativeChatMeasurement{binding: b, quote: p.quote, usage: u}, nil
}

func settleLaneNativeChatObserved(c *gin.Context, info *relaycommon.RelayInfo, origin *dto.Usage, owner LaneNativeChatObservedExecution) {
	measurement := owner.NativeChatMeasurement()
	b, u := measurement.binding, measurement.usage
	prepared, quoteErr := PrepareLaneNativeChatPrice(info, measurement.quote.GroupConfigVersion)
	usage := effectiveBillingUsage(origin)
	if quoteErr != nil || prepared.quote != measurement.quote || b.RequestID != info.RequestId || info.ChannelMeta == nil || b.ChannelID != info.ChannelId ||
		info.ChannelType != constant.ChannelTypeOpenAI || u.Prompt == nil || u.Completion == nil || u.Total == nil || usage == nil ||
		common.GetContextKeyBool(c, constant.ContextKeyLocalCountTokens) || common.GetContextKeyBool(c, constant.ContextKeyUpstreamResponseUsageIncomplete) ||
		origin.BillingUsage != nil && (origin.BillingUsage.Estimated || origin.BillingUsage.Source != dto.BillingUsageSourceOAIChat) || usageSemanticFromUsage(info, usage) != "openai" {
		preserveUnknownLaneNativeChat(c.Request.Context(), owner)
		return
	}
	long := 0
	if u.Details.TTL != nil {
		long = u.Details.TTL.Long
	}
	prompt, completion := usage.PromptTokensDetails, usage.CompletionTokenDetails
	if usage.PromptTokens != *u.Prompt || usage.CompletionTokens != *u.Completion || usage.TotalTokens != *u.Total ||
		prompt.CachedTokens != u.Details.Read || prompt.CacheCreationTokensTotal() != u.Details.Write || prompt.CacheWrite12hTokens() != long ||
		prompt.AudioTokens != 0 || prompt.ImageTokens != 0 || completion.AudioTokens != 0 || completion.ImageTokens != 0 ||
		usage.ClaudeCacheCreation5mTokens != 0 || usage.ClaudeCacheCreation1hTokens != 0 || info.ResponsesUsageInfo != nil ||
		usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.InputTokensDetails != nil || usage.OutputTokensDetails != nil {
		preserveUnknownLaneNativeChat(c.Request.Context(), owner)
		return
	}
	// Invoke ordinary native pricing on only the verified dimensions. Any
	// divergence/saturation or new surcharge must fail closed, not re-price.
	canonical := dto.Usage{PromptTokens: *u.Prompt, CompletionTokens: *u.Completion, TotalTokens: *u.Total,
		PromptTokensDetails: dto.InputTokenDetails{CachedTokens: u.Details.Read, CacheWriteTokens: u.Details.Write}}
	if u.Details.TTL != nil {
		canonical.PromptTokensDetails.CacheWriteTokensDetails = &dto.CacheWriteTokenDetails{TTL30mTokens: u.Details.TTL.Short, TTL12hTokens: long}
	}
	q := measurement.quote
	pricing := &relaycommon.RelayInfo{OriginModelName: q.Model, UsingGroup: q.Group, RelayMode: info.RelayMode, RelayFormat: info.RelayFormat,
		StartTime: info.StartTime, PriceData: hosttypes.PriceData{ModelRatio: q.ModelRatio, CompletionRatio: q.CompletionRatio, CacheRatio: q.CacheRatio,
			CacheCreationRatio: q.CacheCreation, CacheCreation12hRatio: q.CacheCreation12h, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: q.GroupRatio}}}
	summary := calculateTextQuotaSummary(c, pricing, &canonical)
	if pricing.QuotaClamp != nil || summary.Quota <= 0 || summary.Quota > b.QuotaCap || !summary.ToolCallSurchargeQuota.IsZero() {
		preserveUnknownLaneNativeChat(c.Request.Context(), owner)
		return
	}
	measured := LaneNativeChatMeasured{Binding: b, PromptTokens: *u.Prompt, CompletionTokens: *u.Completion,
		CacheReadTokens: u.Details.Read, CacheWriteTokens: u.Details.Write, CacheWrite12hTokens: long, ActualQuota: summary.Quota}
	encoded, err := common.Marshal(measured)
	if err != nil {
		preserveUnknownLaneNativeChat(c.Request.Context(), owner)
		return
	}
	digest := sha256.Sum256(encoded)
	measured.UsageSHA256 = hex.EncodeToString(digest[:])
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
	defer cancel()
	if err := owner.SettleNativeChatMeasured(ctx, measured); err != nil {
		// The owned settlement may have committed. Do not attempt a second
		// financial action or ordinary log, counters, notification or refund.
		logger.LogError(c, "native Chat lane settlement remains unconfirmed")
		return
	}
	info.PerformanceOutputTokens = int64(measured.CompletionTokens)
}

// PreserveUnknownLaneNativeChatBilling is capability-scoped. Existing callers
// without the typed owner continue to use their original ordinary behavior.
func PreserveUnknownLaneNativeChatBilling(ctx context.Context, info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	owner, ok := info.Billing.(LaneNativeChatObservedExecution)
	if ok {
		preserveUnknownLaneNativeChat(ctx, owner)
	}
	return ok
}

func preserveUnknownLaneNativeChat(ctx context.Context, owner LaneNativeChatObservedExecution) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := owner.PreserveNativeChatDeliveryUnknown(ctx); err != nil {
		logger.LogError(ctx, "native Chat lane unknown delivery remains unconfirmed")
	}
}
