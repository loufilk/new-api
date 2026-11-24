package relay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const laneNativeReleasePath = "/v1/chat/completions"

type laneNativeReleaseObservationKey struct{}

// LaneNativeReleaseObservationPlanV1 is an internal, read-only observer input.
// Its route authority and key tag must come from a separately vetted release
// policy, never from a value the client supplies or an unaudited selected route.
// No default runtime component installs this context value.
type LaneNativeReleaseObservationPlanV1 struct {
	Input                  model.LaneStoreQuoteInputV1
	Route                  service.LaneNativeChatRoute
	ExpectedOrigin         string
	ExpectedProviderKeyTag [32]byte
	WireHMACKey            [32]byte
	Verifier               service.LaneNativeChatPromptBound
}

// LaneNativeReleaseObservationV1 lives on one request, which is processed
// sequentially until ordinary DoRequest. Read its result after the handler
// returns. There is no asynchronous worker and no response-header effect.
type LaneNativeReleaseObservationV1 struct {
	plan        LaneNativeReleaseObservationPlanV1
	prepared    service.LaneNativeReleasePreparedV1
	sealed      service.LaneNativeReleaseWireV1
	egress      uint64
	routeConfig [32]byte
	ready       bool
	captured    bool
	tried       bool
}

func WithLaneNativeReleaseObservationV1(ctx context.Context, plan LaneNativeReleaseObservationPlanV1) (context.Context, *LaneNativeReleaseObservationV1) {
	o := &LaneNativeReleaseObservationV1{plan: plan}
	return context.WithValue(ctx, laneNativeReleaseObservationKey{}, o), o
}

func (o *LaneNativeReleaseObservationV1) CapturedPrehold() bool { return o.captured }

func (o *LaneNativeReleaseObservationV1) SealedWire() service.LaneNativeReleaseWireV1 {
	return o.sealed
}

// LaneNativeReleaseProviderKeyTagV1 is domain separated from the wire's HMAC.
// Neither the upstream credential nor its unsalted digest leaves the relay.
func LaneNativeReleaseProviderKeyTagV1(wireKey [32]byte, credential string) [32]byte {
	h := hmac.New(sha256.New, wireKey[:])
	_, _ = h.Write([]byte("newapi/lane/native-chat-route-key/v1\x00"))
	_, _ = h.Write([]byte(credential))
	var tag [32]byte
	copy(tag[:], h.Sum(nil))
	return tag
}

func observeLaneNativeReleasePrehold(c *gin.Context, info *relaycommon.RelayInfo) {
	if c == nil || c.Request == nil {
		return
	}
	o, _ := c.Request.Context().Value(laneNativeReleaseObservationKey{}).(*LaneNativeReleaseObservationV1)
	if o == nil || o.tried {
		return
	}
	o.tried = true
	p, in := o.plan, o.plan.Input
	if info == nil || info.Billing != nil || model.DB == nil || p.Verifier == nil || p.WireHMACKey == ([32]byte{}) ||
		p.ExpectedOrigin == "" || c.Request.Method != http.MethodPost || c.Request.URL == nil ||
		c.Request.URL.EscapedPath() != laneNativeReleasePath || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery ||
		info.UserId != in.UserID || info.TokenId != in.TokenID || info.TokenUnlimited ||
		!hmac.Equal([]byte(model.BillingTokenFingerprint(info.TokenKey)), []byte(in.TokenFingerprint)) ||
		info.RequestId != in.RequestID || info.TokenGroup != in.Group || info.UsingGroup != in.Group ||
		info.UserSetting.BillingPreference != "wallet_only" || len(info.RequestPluginRecord) != 0 {
		return
	}
	revision, ok := c.Get(model.GroupConfigVersionContextKey)
	if v, valid := revision.(int64); !ok || !valid || v != in.GroupConfigRevision {
		return
	}
	attempt, ok := common.GetContextKeyType[uint64](c, constant.ContextKeyChannelEgressAttempt)
	routeConfig, validRoute := laneNativeReleaseSelectedRoute(c, p)
	if !ok || attempt != 1 || !validRoute {
		return
	}
	price, err := service.PrepareLaneNativeChatPrice(info, in.GroupConfigRevision)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Second)
	defer cancel()
	var facts model.LaneStoreQuoteAuthorityV1
	err = model.DB.Session(&gorm.Session{NewDB: true, Logger: logger.Discard}).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var captureErr error
		facts, captureErr = model.CaptureLaneStoreQuoteAuthorityV1Tx(tx, in)
		return captureErr
	})
	if err != nil {
		return // observation cannot veto the ordinary prehold
	}
	o.prepared, err = service.PrepareLaneNativeReleaseV1(price, facts, p.Route)
	o.egress, o.routeConfig, o.ready, o.captured = attempt, routeConfig, err == nil, err == nil
}

func laneNativeReleaseSelectedRoute(c *gin.Context, p LaneNativeReleaseObservationPlanV1) ([32]byte, bool) {
	s, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	providerKey := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	tag := LaneNativeReleaseProviderKeyTagV1(p.WireHMACKey, providerKey)
	mapping := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelModelMapping))
	valid := common.GetContextKeyInt(c, constant.ContextKeyChannelId) == p.Route.ChannelID &&
		common.GetContextKeyInt(c, constant.ContextKeyChannelType) == constant.ChannelTypeOpenAI &&
		common.GetContextKeyString(c, constant.ContextKeyOriginalModel) == p.Route.Model &&
		common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl) == p.ExpectedOrigin &&
		providerKey != "" && hmac.Equal(tag[:], p.ExpectedProviderKeyTag[:]) &&
		!common.GetContextKeyBool(c, constant.ContextKeyChannelIsMultiKey) &&
		common.GetContextKeyString(c, constant.ContextKeyChannelOrganization) == "" &&
		len(common.GetContextKeyStringMap(c, constant.ContextKeyChannelHeaderOverride)) == 0 &&
		(mapping == "" || mapping == "{}") && !s.PassThroughBodyEnabled && s.Proxy == "" &&
		!s.TLSInsecureSkipVerify && !s.ForceFormat && !s.ThinkingToContent && len(s.RequestPlugins) == 0 &&
		!model_setting.GetGlobalSettings().PassThroughRequestEnabled
	if !valid {
		return [32]byte{}, false
	}
	other, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	return laneNativeReleaseRouteConfig(p.WireHMACKey, s, other, common.GetContextKeyStringMap(c, constant.ContextKeyChannelParamOverride))
}

// A request-local config pin is not the operator's independent route audit
// digest. It notices replaced/edited overrides or settings between capture
// and serialization without persisting channel config or provider credentials.
func laneNativeReleaseRouteConfig(wireKey [32]byte, settings dto.ChannelSettings, other dto.ChannelOtherSettings, param map[string]any) ([32]byte, bool) {
	if len(param) == 0 {
		param = nil
	}
	raw, err := common.Marshal(struct {
		Settings dto.ChannelSettings
		Other    dto.ChannelOtherSettings
		Param    map[string]any
	}{settings, other, param})
	if err != nil || len(raw) > 64<<10 {
		return [32]byte{}, false
	}
	h := hmac.New(sha256.New, wireKey[:])
	_, _ = h.Write([]byte("newapi/lane/native-chat-selected-config/v1\x00"))
	_, _ = h.Write(raw)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, true
}

// Called at the final native JSON handoff. Observing this slice neither consumes
// the body reader nor creates an owned request. Ordinary prehold already ran;
// its possibly changed rows are not treated as live lane backing here.
func observeLaneNativeReleaseFinalWire(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, final []byte) {
	o, _ := c.Request.Context().Value(laneNativeReleaseObservationKey{}).(*LaneNativeReleaseObservationV1)
	if o == nil || !o.ready {
		return
	}
	o.ready = false // attempts after the first can never overwrite this artifact
	p := o.plan
	if !laneNativeReleaseFinalRouteMatches(c, info, adaptor, p, o.egress, o.routeConfig) {
		return
	}
	price, err := service.PrepareLaneNativeChatPrice(info, p.Input.GroupConfigRevision)
	if err != nil {
		return
	}
	o.sealed, _ = o.prepared.SealFinalWire(c.Request.Context(), price, p.Route, final, p.WireHMACKey, p.Verifier)
}

// Both the shadow observer and the isolated concrete owner check the same
// conversion, settings, route, and credential at the adaptor handoff.
func laneNativeReleaseFinalRouteMatches(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor,
	p LaneNativeReleaseObservationPlanV1, expectedAttempt uint64, expectedConfig [32]byte) bool {
	attempt, ok := common.GetContextKeyType[uint64](c, constant.ContextKeyChannelEgressAttempt)
	if !ok || info == nil || adaptor == nil || attempt != expectedAttempt || info.RetryIndex != 0 || info.ChannelMeta == nil || info.ChannelId != p.Route.ChannelID ||
		info.ChannelType != p.Route.ChannelType || info.UpstreamModelName != p.Route.Model || info.IsModelMapped ||
		info.ChannelIsMultiKey || info.ChannelBaseUrl != p.ExpectedOrigin || info.ChannelSetting.Proxy != "" ||
		info.ChannelSetting.TLSInsecureSkipVerify || info.Organization != "" || len(info.HeadersOverride) != 0 ||
		len(relaycommon.GetEffectiveHeaderOverride(info)) != 0 || len(info.RequestPluginHeaders) != 0 || len(info.RequestPluginRecord) != 0 {
		return false
	}
	selectedConfig, validSelected := laneNativeReleaseSelectedRoute(c, p)
	adaptorConfig, validAdaptor := laneNativeReleaseRouteConfig(p.WireHMACKey, info.ChannelSetting, info.ChannelOtherSettings, info.ParamOverride)
	if !validSelected || !validAdaptor || selectedConfig != expectedConfig || adaptorConfig != expectedConfig {
		return false
	}
	tag := LaneNativeReleaseProviderKeyTagV1(p.WireHMACKey, info.ApiKey)
	if !hmac.Equal(tag[:], p.ExpectedProviderKeyTag[:]) {
		return false
	}
	upstream, err := adaptor.GetRequestURL(info)
	if err != nil || upstream != p.ExpectedOrigin+p.Route.Path {
		return false
	}
	return true
}
