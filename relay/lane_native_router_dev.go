//go:build lane_billing_integration_dev

package relay

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const laneNativeSyntheticPath = "/v1/chat/completions"
const laneNativeSyntheticMaxBody = 64 << 10

var errLaneNativeSyntheticUnavailable = errors.New("synthetic native Chat billing is unavailable")

type laneNativeSyntheticPlanContextKey struct{}

// LaneNativeSyntheticPlan is an explicit loopback fixture only. There is no
// environment, header, HTTP endpoint or normal startup installer for the plan.
// No production provider auth, funding selector, or exposure proof is implied.
type LaneNativeSyntheticPlan struct {
	DB                                   *gorm.DB
	Owner                                model.LaneBillingChildDevBinding
	Group, Model                         string
	ProviderOrigin, SyntheticProviderKey string
	FrozenWireHMACKey                    [32]byte
	Verifier                             service.LaneNativeChatPromptBound
	// Complete expected synthetic prefix, including prior output and finish
	// frames. Usage JSON and closing terminal suffix are verified independently.
	TerminalPrefix string
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
	quote       service.LaneNativeChatPreparedPrice
	version     int64
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
		p.DB == nil || p.DB != model.DB || p.Verifier == nil || !strings.HasPrefix(p.TerminalPrefix, "data: ") || len(p.TerminalPrefix) > 8192 ||
		b.Owner.RequestID != "" {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	version, exists := c.Get(model.GroupConfigVersionContextKey)
	v, allowed := version.(int64)
	if !exists || !allowed || model.CheckRequestGroupConfigVersion(c) != nil {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	quote, err := service.PrepareLaneNativeChatPrice(info, v)
	if err != nil {
		return true, newAPIErrorLaneSyntheticUnsupported()
	}
	relaycommon.PinLaneNativeSyntheticHTTPIntent(c)
	info.Billing = &laneNativeSyntheticOwner{p: p, quote: quote, version: v}
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

func bindLaneNativeSyntheticBilling(c *gin.Context, info *relaycommon.RelayInfo, adaptor channel.Adaptor, body io.Reader) (io.Reader, *types.NewAPIError) {
	if !hasLaneNativeSyntheticBilling(c) {
		return body, nil
	}
	if info == nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	o, ok := info.Billing.(*laneNativeSyntheticOwner)
	if !ok || o == nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	p := o.p
	if o.child != nil || adaptor == nil || body == nil || info.RetryIndex != 0 || info.ChannelMeta == nil ||
		info.RelayMode != relayconstant.RelayModeChatCompletions || info.ChannelType != constant.ChannelTypeOpenAI ||
		info.ChannelId != p.Owner.ChannelID || info.UpstreamModelName != p.Model || info.IsModelMapped || info.UsingGroup != p.Group ||
		info.ChannelIsMultiKey || info.ApiKey != p.SyntheticProviderKey || info.Organization != "" || info.ChannelSetting.Proxy != "" ||
		len(info.HeadersOverride) != 0 || info.ChannelBaseUrl != p.ProviderOrigin || model.CheckRequestGroupConfigVersion(c) != nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	routeURL, err := adaptor.GetRequestURL(info)
	if err != nil || routeURL != p.ProviderOrigin+laneNativeSyntheticPath {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	quote, err := service.PrepareLaneNativeChatPrice(info, o.version)
	if err != nil || quote != o.quote {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	final, err := io.ReadAll(io.LimitReader(body, laneNativeSyntheticMaxBody+1))
	if err != nil || len(final) > laneNativeSyntheticMaxBody {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	h := sha256.Sum256([]byte("synthetic-native-loopback:v1\x00" + routeURL))
	route := service.LaneNativeChatRoute{ChannelID: info.ChannelId, ChannelType: info.ChannelType, Group: info.UsingGroup, Model: p.Model,
		Path: laneNativeSyntheticPath, AuthoritySHA256: hex.EncodeToString(h[:])}
	proof, err := o.quote.VerifyFinalWire(c.Request.Context(), route, final, p.FrozenWireHMACKey, p.Verifier)
	if err != nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	binding, evidence := p.Owner, proof.Evidence()
	binding.Owner.RequestID = evidence.RequestID
	binding.FrozenPriceSHA256 = evidence.FrozenPriceSHA256
	binding.RouteSHA256 = evidence.RouteSHA256
	binding.FinalWireSHA256 = evidence.FinalWireSHA256
	child, err := model.ReserveLaneBillingChildDev(c.Request.Context(), p.DB, binding, evidence.QuotaCap)
	if err != nil || child == nil {
		return nil, newAPIErrorLaneSyntheticUnsupported()
	}
	o.proof, o.child = proof, child
	info.FinalPreConsumedQuota = evidence.QuotaCap
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
	raw, err := io.ReadAll(io.LimitReader(response.Body, laneNativeSyntheticMaxBody+1))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || len(raw) > laneNativeSyntheticMaxBody || response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		return newAPIErrorLaneSyntheticUnsupported()
	}
	const suffix = "}\n\ndata: [DONE]\n\n"
	prefix := o.p.TerminalPrefix
	if !bytes.HasPrefix(raw, []byte(prefix)) || !bytes.HasSuffix(raw, []byte(suffix)) || len(raw) <= len(prefix)+len(suffix) {
		return newAPIErrorLaneSyntheticUnsupported()
	}
	measurement, err := o.proof.VerifyTerminalUsage(raw[len(prefix) : len(raw)-len(suffix)])
	if err != nil {
		return newAPIErrorLaneSyntheticUnsupported()
	}
	o.measurement = measurement
	// Buffering is fixture-only. The unchanged native adapter emits and decodes
	// the same bytes. A production streaming path needs raw terminal proof
	// without buffering or sacrificing first-token latency.
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return nil
}
