//go:build lane_billing_integration_dev

package relay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const laneNativeSyntheticPath = "/v1/chat/completions"

var errLaneNativeSyntheticUnavailable = errors.New("synthetic native Chat billing is unavailable")

type laneNativeSyntheticPlanContextKey struct{}

// LaneNativeSyntheticPlan is an explicit loopback fixture only. There is no
// environment, header, HTTP endpoint or normal startup installer for the plan.
// No production provider auth, funding selector, or exposure proof is implied.
type LaneNativeSyntheticPlan struct {
	DB                                   *gorm.DB
	Owner                                model.LaneBillingChildDevBinding
	Quote                                model.LaneStoreQuoteInputV1
	Group, Model                         string
	ProviderOrigin, SyntheticProviderKey string
	FrozenWireHMACKey                    [32]byte
	Verifier                             service.LaneNativeChatPromptBound
}

func WithLaneNativeSyntheticPlan(ctx context.Context, p LaneNativeSyntheticPlan) context.Context {
	return context.WithValue(ctx, laneNativeSyntheticPlanContextKey{}, p)
}

func newAPIErrorLaneSyntheticUnsupported() *types.NewAPIError {
	return types.NewError(errLaneNativeSyntheticUnavailable, "billing_lane_dev_unavailable",
		types.ErrOptionWithStatusCode(http.StatusServiceUnavailable), types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}

func isLaneNativeFixtureOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || u.Port() == "" ||
		origin != "http://"+u.Host {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.IsLoopback()
}

func isLaneNativeFixtureClient(r *http.Request) bool {
	if r == nil || r.TLS != nil {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

type laneNativeSyntheticOwner struct {
	p           LaneNativeSyntheticPlan
	prepared    service.LaneNativeReleasePreparedV1
	route       LaneNativeReleaseObservationPlanV1
	attempt     uint64
	routeConfig [32]byte
	proof       service.LaneNativeChatProof
	child       *model.LaneBillingChildDev
	measurement service.LaneNativeChatMeasurement
}

var _ relaycommon.BillingSettler = (*laneNativeSyntheticOwner)(nil)
var _ service.LaneNativeChatObservedExecution = (*laneNativeSyntheticOwner)(nil)

// Implement the dispatch capability as soon as the synthetic intent is armed.
// This pending value is not a financial receipt and cannot send anything. It
// keeps failures and ambiguous binds off retry, ordinary charge, and refund.
func (o *laneNativeSyntheticOwner) Settle(int) error    { return errLaneNativeSyntheticUnavailable }
func (o *laneNativeSyntheticOwner) Refund(*gin.Context) {}
func (o *laneNativeSyntheticOwner) NeedsRefund() bool   { return false }
func (o *laneNativeSyntheticOwner) Reserve(int) error   { return errLaneNativeSyntheticUnavailable }
func (o *laneNativeSyntheticOwner) ReserveBeforeSubmit(int) error {
	return errLaneNativeSyntheticUnavailable
}
func (o *laneNativeSyntheticOwner) GetPreConsumedQuota() int { return o.proof.Evidence().QuotaCap }
func (o *laneNativeSyntheticOwner) NativeChatMeasurement() service.LaneNativeChatMeasurement {
	return o.measurement
}

func (o *laneNativeSyntheticOwner) ClaimBillingHTTPDispatch(ctx context.Context) error {
	if o.child == nil {
		return errLaneNativeSyntheticUnavailable
	}
	return o.child.ClaimDispatch(ctx, o.proof.Evidence().FinalWireSHA256)
}

func (o *laneNativeSyntheticOwner) SettleNativeChatMeasured(ctx context.Context, m service.LaneNativeChatMeasured) error {
	if o.child == nil || m.Binding != o.proof.Evidence() {
		return errLaneNativeSyntheticUnavailable
	}
	return o.child.SettleMeasured(ctx, model.LaneBillingMeasuredDev{
		RequestID: m.Binding.RequestID, ChannelID: m.Binding.ChannelID, Model: m.Binding.Model, Group: m.Binding.Group,
		PromptTokens: m.PromptTokens, CompletionTokens: m.CompletionTokens, CacheReadTokens: m.CacheReadTokens, CacheWriteTokens: m.CacheWriteTokens,
		CacheWrite12hTokens: m.CacheWrite12hTokens, ActualQuota: m.ActualQuota, UsageSHA256: m.UsageSHA256,
	})
}

func (o *laneNativeSyntheticOwner) PreserveNativeChatDeliveryUnknown(ctx context.Context) error {
	if o.child == nil {
		return errLaneNativeSyntheticUnavailable
	}
	return o.child.PreserveUnknown(ctx)
}

func armLaneNativeSyntheticBilling(c *gin.Context, info *relaycommon.RelayInfo) (bool, *types.NewAPIError) {
	if c == nil || c.Request == nil {
		return false, nil
	}
	p, ok := c.Request.Context().Value(laneNativeSyntheticPlanContextKey{}).(LaneNativeSyntheticPlan)
	if !ok {
		return false, nil
	}
	b := p.Owner
	if info == nil || info.Billing != nil || !isLaneNativeFixtureClient(c.Request) || !isLaneNativeFixtureOrigin(p.ProviderOrigin) ||
		c.Request.Method != http.MethodPost || c.Request.URL == nil || c.Request.URL.EscapedPath() != laneNativeSyntheticPath || c.Request.URL.RawQuery != "" ||
		info.UserId != b.Owner.UserID || info.TokenId != b.Owner.TokenID || info.TokenUnlimited ||
		model.BillingTokenFingerprint(info.TokenKey) != b.Owner.TokenFingerprint || info.TokenGroup != p.Group || info.UsingGroup != p.Group ||
		info.OriginModelName != p.Model || info.UserSetting.BillingPreference != "wallet_only" || p.SyntheticProviderKey == "" ||
		p.DB == nil || p.DB != model.DB || p.Verifier == nil ||
		b.Owner.RequestID != "" {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	version, exists := c.Get(model.GroupConfigVersionContextKey)
	v, allowed := version.(int64)
	if !exists || !allowed || model.CheckRequestGroupConfigVersion(c) != nil {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	attempt, hasAttempt := common.GetContextKeyType[uint64](c, constant.ContextKeyChannelEgressAttempt)
	quote, err := service.PrepareLaneNativeChatPrice(info, v)
	if err != nil || !hasAttempt || attempt != 1 || p.Quote.RequestID != "" || p.Quote.UserID != b.Owner.UserID ||
		p.Quote.TokenID != b.Owner.TokenID || p.Quote.TokenFingerprint != b.Owner.TokenFingerprint ||
		p.Quote.Group != p.Group || p.Quote.GroupConfigRevision != v || p.Quote.Release.Epoch != b.Owner.Epoch {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	h := sha256.Sum256([]byte("synthetic-native-loopback:v1\x00" + p.ProviderOrigin + laneNativeSyntheticPath))
	route := LaneNativeReleaseObservationPlanV1{Input: p.Quote,
		Route: service.LaneNativeChatRoute{ChannelID: b.ChannelID, ChannelType: constant.ChannelTypeOpenAI,
			Group: p.Group, Model: p.Model, Path: laneNativeSyntheticPath, AuthoritySHA256: hex.EncodeToString(h[:])},
		ExpectedOrigin: p.ProviderOrigin, ExpectedProviderKeyTag: LaneNativeReleaseProviderKeyTagV1(p.FrozenWireHMACKey, p.SyntheticProviderKey),
		WireHMACKey: p.FrozenWireHMACKey, Verifier: p.Verifier}
	route.Input.RequestID = info.RequestId
	config, ok := laneNativeReleaseSelectedRoute(c, route)
	if !ok {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	facts, err := model.CaptureLaneNativeChildQuoteDev(c.Request.Context(), p.DB, route.Input)
	if err != nil {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	prepared, err := service.PrepareLaneNativeReleaseV1(quote, facts, route.Route)
	if err != nil {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	relaycommon.PinLaneNativeSyntheticHTTPIntent(c)
	info.Billing = &laneNativeSyntheticOwner{p: p, prepared: prepared, route: route, attempt: attempt, routeConfig: config}
	info.BillingSource = service.BillingSourceWallet
	info.DisablePing = true
	return true, nil
}

func hasLaneNativeSyntheticBilling(c *gin.Context) bool {
	return relaycommon.LaneNativeSyntheticHTTPIntentPinned(c)
}

func prepareLaneNativeSyntheticSelectedBilling(c *gin.Context, info *relaycommon.RelayInfo) (bool, *types.NewAPIError) {
	if !hasLaneNativeSyntheticBilling(c) {
		return false, nil
	}
	if info == nil {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	pending, ok := info.Billing.(*laneNativeSyntheticOwner)
	if !ok || pending == nil || pending.child != nil || info.RetryIndex != 0 || info.UsingGroup != pending.p.Group {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	return true, nil
}

type laneNativeSyntheticBody struct {
	io.Reader
	proof service.LaneNativeChatProof
}

func (b laneNativeSyntheticBody) Size() int64 { return b.proof.BodySize() }
func (b laneNativeSyntheticBody) NewReader() (io.ReadCloser, error) {
	return io.NopCloser(b.proof.OpenBody()), nil
}

func bindLaneNativeSyntheticBilling(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, final []byte) (io.Reader, *types.NewAPIError) {
	if !hasLaneNativeSyntheticBilling(c) {
		return nil, nil
	}
	if info == nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	o, ok := info.Billing.(*laneNativeSyntheticOwner)
	if !ok || o == nil || o.child != nil ||
		!laneNativeReleaseFinalRouteMatches(c, info, adaptor, o.route, o.attempt, o.routeConfig) ||
		model.CheckRequestGroupConfigVersion(c) != nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	current, err := service.PrepareLaneNativeChatPrice(info, o.route.Input.GroupConfigRevision)
	if err != nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	sealed, err := o.prepared.SealFinalWire(c.Request.Context(), current, o.route.Route, final, o.route.WireHMACKey, o.route.Verifier)
	if err != nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	child, proof, err := sealed.BindLaneNativeReleaseDev(c.Request.Context(), o.p.DB, o.p.Owner)
	if err != nil || child == nil {
		// Pinned before any reservation attempt: an error or unknown COMMIT
		// never permits ordinary fallback, retry, or an extra outbound send.
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	o.proof, o.child = proof, child
	info.FinalPreConsumedQuota = proof.Evidence().QuotaCap
	return laneNativeSyntheticBody{Reader: proof.OpenBody(), proof: proof}, nil
}

func observeLaneNativeSyntheticTerminal(c *gin.Context, info *relaycommon.RelayInfo, response *http.Response) *types.NewAPIError {
	if !hasLaneNativeSyntheticBilling(c) {
		return nil
	}
	if response == nil || response.Body == nil {
		return newAPIErrorLaneSyntheticUnsupported()
	}
	if info == nil {
		_ = response.Body.Close()
		return newAPIErrorLaneSyntheticUnsupported()
	}
	o, ok := info.Billing.(*laneNativeSyntheticOwner)
	if !ok || o == nil || o.child == nil || response.Request == nil || response.Request.URL == nil ||
		response.Request.Method != http.MethodPost || response.Request.URL.String() != o.p.ProviderOrigin+laneNativeSyntheticPath {
		_ = response.Body.Close()
		return newAPIErrorLaneSyntheticUnsupported()
	}
	if response.StatusCode != http.StatusOK || len(response.Header.Values("Content-Type")) != 1 ||
		response.Header.Get("Content-Type") != "text/event-stream" {
		_ = response.Body.Close()
		return newAPIErrorLaneSyntheticUnsupported()
	}
	// The ordinary adapter remains the sole downstream writer. It joins its
	// scanner before settlement. No runtime or client installs this fixture.
	response.Body = newLaneNativeTerminalBody(response.Body, o.p.Model, func(raw []byte) bool {
		measurement, err := o.proof.VerifyTerminalUsage(raw)
		if err == nil {
			o.measurement = measurement
		}
		return err == nil
	})
	return nil
}
