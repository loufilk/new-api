//go:build enterprise_lane_lab

package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relay/requestplugin"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

const (
	enterpriseLaneChatPath = "/v1/chat/completions"
	enterpriseLaneBodyMax  = 64 << 10
	enterpriseLaneSQLTime  = 5 * time.Second
)

type enterpriseLaneMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type enterpriseLaneChat struct {
	Model         string                  `json:"model"`
	Messages      []enterpriseLaneMessage `json:"messages"`
	MaxTokens     *uint                   `json:"max_tokens"`
	Stream        *bool                   `json:"stream"`
	N             *uint                   `json:"n,omitempty"`
	StreamOptions struct {
		IncludeUsage *bool `json:"include_usage"`
	} `json:"stream_options"`
}

// Decode the allowed subset and marshal a new body. No input bytes, extensions
// or contradictory aliases are forwarded. The fixture's only input-usage
// contract is <= canonical wire bytes; this is NOT a real-model tokenizer cap.
func enterpriseLaneWireBody(body []byte, modelName string) (enterpriseLaneChat, []byte, error) {
	var chat enterpriseLaneChat
	bad := errors.New("unsupported enterprise lane fixture request")
	if len(body) == 0 || len(body) > enterpriseLaneBodyMax || !utf8.Valid(body) || !enterpriseLaneUniqueJSON(body) || common.DecodeJsonStrict(bytes.NewReader(body), &chat) != nil {
		return chat, nil, bad
	}
	if chat.Model == "" || chat.Model != modelName || chat.Stream == nil || !*chat.Stream || chat.MaxTokens == nil ||
		*chat.MaxTokens == 0 || helper.ExceedsMaxTokensLimit(chat.MaxTokens) || chat.N != nil && *chat.N != 1 ||
		chat.StreamOptions.IncludeUsage == nil || !*chat.StreamOptions.IncludeUsage || len(chat.Messages) == 0 {
		return chat, nil, bad
	}
	for _, message := range chat.Messages {
		if (message.Role != "user" && message.Role != "assistant" && message.Role != "system") || strings.TrimSpace(message.Content) == "" {
			return chat, nil, bad
		}
	}
	data, err := common.Marshal(chat)
	if err != nil || len(data) > enterpriseLaneBodyMax {
		return chat, nil, bad
	}
	return chat, data, nil
}

type enterpriseLaneRate struct{ prompt, completion decimal.Decimal }

func enterpriseLaneFrozenRate(c *gin.Context, chat enterpriseLaneChat, promptCap int) (enterpriseLaneRate, error) {
	info, err := relaycommon.GenRelayInfo(c, types.RelayFormatOpenAI, &dto.GeneralOpenAIRequest{Model: chat.Model, MaxTokens: chat.MaxTokens, Stream: chat.Stream}, nil)
	if err != nil {
		return enterpriseLaneRate{}, err
	}
	price, err := helper.ModelPriceHelper(c, info, promptCap, &types.TokenCountMeta{MaxTokens: int(*chat.MaxTokens)})
	if err != nil {
		return enterpriseLaneRate{}, err
	}
	if price.FreeModel || price.UsePrice || info.TieredBillingSnapshot != nil || info.GetBillingModelName() != chat.Model || len(price.OtherRatios()) != 0 {
		return enterpriseLaneRate{}, errors.New("unsupported enterprise lane pricing")
	}
	for _, ratio := range []float64{price.ModelRatio, price.CompletionRatio, price.GroupRatioInfo.GroupRatio} {
		if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return enterpriseLaneRate{}, errors.New("invalid enterprise lane pricing")
		}
	}
	prompt := decimal.NewFromFloat(price.ModelRatio).Mul(decimal.NewFromFloat(price.GroupRatioInfo.GroupRatio))
	return enterpriseLaneRate{prompt: prompt, completion: prompt.Mul(decimal.NewFromFloat(price.CompletionRatio))}, nil
}

func (p enterpriseLaneRate) quota(prompt, completion int, cap bool) (int, error) {
	value := p.prompt.Mul(decimal.NewFromInt(int64(prompt))).Add(p.completion.Mul(decimal.NewFromInt(int64(completion))))
	if cap {
		value = value.Ceil()
	}
	quota, err := common.QuotaFromDecimalStrict(value)
	return max(1, quota), err // existing positive measured text minimum
}

func enterpriseLaneLoopback(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return false
	}
	ip, err := netip.ParseAddr(host)
	p, pErr := strconv.ParseUint(port, 10, 16)
	return err == nil && pErr == nil && p > 0 && ip.Zone() == "" && ip.IsLoopback()
}

func enterpriseLaneUpstream(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Scheme != "http" || u.User != nil || u.Opaque != "" || u.ForceQuery ||
		u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || !enterpriseLaneLoopback(u.Host) {
		return "", errors.New("expected a literal loopback fixture upstream")
	}
	u.Path = enterpriseLaneChatPath
	return u.String(), nil
}

var enterpriseLaneHTTP = &http.Client{
	Timeout:       20 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || !enterpriseLaneLoopback(address) {
				return nil, errors.New("enterprise lane refuses non-loopback network egress")
			}
			dialer := net.Dialer{Timeout: 2 * time.Second}
			return dialer.DialContext(ctx, network, address)
		},
		DisableCompression:  true,
		MaxIdleConns:        128,
		MaxIdleConnsPerHost: 128,
		IdleConnTimeout:     30 * time.Second,
	},
}

func maybeRelayEnterpriseLane(c *gin.Context, format types.RelayFormat) bool {
	identity, bound := middleware.EnterpriseLaneForRequest(c)
	if !bound {
		return false
	}
	// A bound request MUST terminate here even on a local admission error.
	serveEnterpriseLane(c, format, identity)
	return true
}

func serveEnterpriseLane(c *gin.Context, format types.RelayFormat, identity model.EnterpriseLaneIdentity) {
	timing := model.BeginEnterpriseLaneTiming(common.GetContextKeyTime(c, constant.ContextKeyGatewayIngressTime))
	if timing != nil {
		defer func() { timing.Finish(c.Writer.Status()) }()
	}
	channelID, modelName, ok := middleware.EnterpriseLaneRoutePolicy(c)
	liveChannel := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	selectedModel := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	groupName := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if groupName == "auto" {
		groupName = common.GetContextKeyString(c, constant.ContextKeyAutoGroup)
	}
	if !ok || format != types.RelayFormatOpenAI || c.Request.Method != http.MethodPost || c.Request.URL.EscapedPath() != enterpriseLaneChatPath ||
		c.Request.URL.RawQuery != "" || groupName == "" || liveChannel <= 0 || liveChannel != channelID || selectedModel != modelName ||
		common.GetContextKeyInt(c, constant.ContextKeyChannelType) != constant.ChannelTypeOpenAI || !enterpriseLaneNativeChannel(c) ||
		model_setting.GetGlobalSettings().PassThroughRequestEnabled || service.ShouldChatCompletionsUseResponsesGlobal(liveChannel, constant.ChannelTypeOpenAI, modelName) {
		enterpriseLaneReplyError(c, http.StatusForbidden, "enterprise_lane_unsupported_route")
		return
	}
	endpoint, err := enterpriseLaneUpstream(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl))
	if err != nil {
		enterpriseLaneReplyError(c, http.StatusForbidden, "enterprise_lane_unsupported_upstream")
		return
	}
	media, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil || media != "application/json" || requestplugin.NewPlan() != nil {
		enterpriseLaneReplyError(c, http.StatusBadRequest, "enterprise_lane_unsupported_request")
		return
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil || storage.Size() > enterpriseLaneBodyMax {
		enterpriseLaneReplyError(c, http.StatusBadRequest, "enterprise_lane_invalid_request")
		return
	}
	body, err := storage.Bytes()
	if err != nil {
		enterpriseLaneReplyError(c, http.StatusBadRequest, "enterprise_lane_invalid_request")
		return
	}
	chat, wire, err := enterpriseLaneWireBody(body, modelName)
	if err != nil {
		enterpriseLaneReplyError(c, http.StatusBadRequest, "enterprise_lane_invalid_request")
		return
	}
	var text strings.Builder
	for _, m := range chat.Messages {
		text.WriteString(m.Content)
		text.WriteByte('\n')
	}
	if service.CheckRequestSensitiveText(c, text.String()) != nil || service.CheckModelTestAccess(c) != nil {
		enterpriseLaneReplyError(c, http.StatusForbidden, "enterprise_lane_policy_rejected")
		return
	}
	price, err := enterpriseLaneFrozenRate(c, chat, len(wire))
	if err != nil {
		enterpriseLaneReplyError(c, http.StatusBadRequest, "enterprise_lane_pricing_unavailable")
		return
	}
	promptCap, completionCap := len(wire), int(*chat.MaxTokens)
	cap, err := price.quota(promptCap, completionCap, true)
	if err != nil || cap <= 0 {
		enterpriseLaneReplyError(c, http.StatusBadRequest, "enterprise_lane_invalid_exposure")
		return
	}
	id := c.GetString(common.RequestIdKey)
	ctx := c.Request.Context()
	if id == "" || ctx.Err() != nil || model.CheckRequestGroupConfigVersion(c) != nil {
		enterpriseLaneReplyError(c, http.StatusServiceUnavailable, "enterprise_lane_admission_unavailable")
		return
	}
	timing.Mark(model.EnterpriseLaneTimingPreflight)
	billingCtx := timing.WithContext(ctx)
	// Prepare a nonreplayable POST before allocating/claiming. A positive content
	// length and nil GetBody prevent Transport retry; no credential/header from
	// the client or channel is sent to the synthetic provider.
	upstream, cancelUpstream := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancelUpstream()
	req, err := http.NewRequestWithContext(upstream, http.MethodPost, endpoint, io.NopCloser(bytes.NewReader(wire)))
	if err != nil {
		enterpriseLaneReplyError(c, http.StatusBadRequest, "enterprise_lane_unsupported_upstream")
		return
	}
	req.ContentLength = int64(len(wire))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	permit, denial := middleware.AcquireEnterpriseLaneAdmission(c)
	if denial != "" {
		enterpriseLaneReplyError(c, http.StatusServiceUnavailable, denial)
		return
	}
	defer permit.Release() // Panic safety; ordinary paths release before writing a response.
	if permit.Waited() && !recheckEnterpriseLaneAfterWait(c, identity, liveChannel, groupName, endpoint, text.String(), chat, price, promptCap, completionCap, cap) {
		permit.Release()
		enterpriseLaneReplyError(c, http.StatusServiceUnavailable, "enterprise_lane_admission_stale")
		return
	}
	reservation, cancel := context.WithTimeout(billingCtx, enterpriseLaneSQLTime)
	_, err = model.ReserveEnterpriseLane(reservation, model.DB, identity, id, cap)
	cancel()
	if err != nil {
		permit.Release()
		status := http.StatusServiceUnavailable
		if errors.Is(err, model.ErrEnterpriseLaneCapacity) {
			status = http.StatusTooManyRequests
		}
		enterpriseLaneReplyError(c, status, "enterprise_lane_reservation_rejected")
		return
	}
	if ctx.Err() != nil || model.CheckRequestGroupConfigVersion(c) != nil || service.CheckModelTestAccess(c) != nil || middleware.RecheckEnterpriseLaneAuthorization(c) != nil {
		// No mark has been attempted here. Refund itself must prove the owner
		// remained undispatched, so even a conflicting proof cannot release it.
		refundCtx, stop := context.WithTimeout(context.WithoutCancel(billingCtx), enterpriseLaneSQLTime)
		_, _ = model.RefundEnterpriseLane(refundCtx, model.DB, identity, id)
		stop()
		permit.Release()
		enterpriseLaneReplyError(c, http.StatusServiceUnavailable, "enterprise_lane_admission_unavailable")
		return
	}
	timing.Mark(model.EnterpriseLaneTimingRecheck)
	dispatch, cancel := context.WithTimeout(billingCtx, enterpriseLaneSQLTime)
	_, err = model.MarkEnterpriseLaneDispatched(dispatch, model.DB, identity, id)
	cancel()
	permit.Release() // Mark has returned; never keep an intake permit across any upstream or client I/O.
	if err != nil {
		// A lost dispatch ACK is not a resend nor a refund grant.
		enterpriseLaneReplyError(c, http.StatusServiceUnavailable, "enterprise_lane_dispatch_unconfirmed")
		return
	}
	unknown := true
	defer func() {
		if unknown {
			markCtx, stop := context.WithTimeout(context.WithoutCancel(billingCtx), enterpriseLaneSQLTime)
			defer stop()
			if _, err := model.MarkEnterpriseLaneDeliveryUnknown(markCtx, model.DB, identity, id); err != nil {
				common.SysError("enterprise lane delivery remains unresolved")
			}
		}
	}()
	if ctx.Err() != nil {
		return
	}
	timing.Mark(model.EnterpriseLaneTimingProviderDo)
	resp, err := enterpriseLaneHTTP.Do(req)
	if err != nil {
		enterpriseLaneReplyError(c, http.StatusBadGateway, "enterprise_lane_delivery_unconfirmed")
		return
	}
	defer resp.Body.Close()
	typ, _, mimeErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK || mimeErr != nil || typ != "text/event-stream" || resp.Header.Get("Content-Encoding") != "" {
		enterpriseLaneReplyError(c, http.StatusBadGateway, "enterprise_lane_delivery_unconfirmed")
		return
	}
	terminal, err := readEnterpriseLaneStream(resp.Body, modelName, promptCap, completionCap, func(frame []byte) {
		timing.Mark(model.EnterpriseLaneTimingFirstOutputAttempt)
		writeEnterpriseLaneFrame(c, frame)
	})
	if err != nil {
		enterpriseLaneReplyError(c, http.StatusBadGateway, "enterprise_lane_usage_unconfirmed")
		return
	}
	actual, err := price.quota(terminal.prompt, terminal.completion, false)
	if err != nil || actual > cap {
		enterpriseLaneReplyError(c, http.StatusBadGateway, "enterprise_lane_usage_out_of_bounds")
		return
	}
	timing.Mark(model.EnterpriseLaneTimingUsageConfirmed)
	unknown = false // verified evidence may already be durable on a lost ACK
	settle, cancel := context.WithTimeout(context.WithoutCancel(billingCtx), enterpriseLaneSQLTime)
	usage := model.EnterpriseLaneUsage{ChannelID: liveChannel, PromptTokens: terminal.prompt, CompletionTokens: terminal.completion, Model: modelName, Group: groupName, ObservedAt: time.Now().Unix()}
	owner, err := model.SettleEnterpriseLaneWithUsage(settle, model.DB, identity, id, actual, usage)
	cancel()
	if err != nil || owner.State != model.EnterpriseLaneSettled || owner.Actual != actual || owner.Usage != usage {
		enterpriseLaneReplyError(c, http.StatusServiceUnavailable, "enterprise_lane_settlement_unconfirmed")
		return
	}
	timing.Mark(model.EnterpriseLaneTimingTerminalAttempt)
	writeEnterpriseLaneFrame(c, terminal.finishFrame)
	writeEnterpriseLaneFrame(c, terminal.usageFrame)
	writeEnterpriseLaneFrame(c, []byte("[DONE]"))
}

func enterpriseLaneNativeChannel(c *gin.Context) bool {
	settings, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	other, otherOK := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	params, _ := common.GetContextKeyType[map[string]any](c, constant.ContextKeyChannelParamOverride)
	headers, _ := common.GetContextKeyType[map[string]any](c, constant.ContextKeyChannelHeaderOverride)
	mapping := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping))
	return ok && otherOK && other.AdvancedCustom == nil && !settings.ForceFormat && !settings.ThinkingToContent && !settings.PassThroughBodyEnabled &&
		settings.Proxy == "" && settings.SystemPrompt == "" && !settings.SystemPromptOverride && settings.TaskPluginKey == "" &&
		len(settings.TaskExtendPluginKeys) == 0 && len(settings.RequestPlugins) == 0 && len(params) == 0 && len(headers) == 0 &&
		(mapping == "" || mapping == "{}") && !common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) &&
		common.GetContextKeyString(c, constant.ContextKeyChannelOrganization) == ""
}

func writeEnterpriseLaneFrame(c *gin.Context, data []byte) {
	if c.Request.Context().Err() != nil {
		return // still consume the bounded upstream for settlement evidence
	}
	if !c.Writer.Written() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Accel-Buffering", "no")
	}
	_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
	c.Writer.Flush()
}

func enterpriseLaneReplyError(c *gin.Context, status int, code string) {
	if c.Request.Context().Err() != nil {
		return
	}
	value := gin.H{"error": gin.H{"code": code, "message": "Enterprise lane request did not complete", "type": "api_error"}}
	if c.Writer.Written() {
		data, _ := common.Marshal(value)
		writeEnterpriseLaneFrame(c, data)
		return
	}
	c.JSON(status, value)
}
