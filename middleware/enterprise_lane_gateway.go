//go:build enterprise_lane_lab

package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"hash/fnv"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const (
	laneEnabledEnv     = "ENTERPRISE_LANE_LAB_ENABLED"
	laneStopEnv        = "ENTERPRISE_LANE_LAB_STOP"
	laneTokenIDEnv     = "ENTERPRISE_LANE_LAB_TOKEN_ID"
	laneCountEnv       = "ENTERPRISE_LANE_LAB_COUNT"
	lanePaidBudgetEnv  = "ENTERPRISE_LANE_LAB_PAID_PER_LANE"
	laneTokenBudgetEnv = "ENTERPRISE_LANE_LAB_TOKEN_PER_LANE"
	laneChannelEnv     = "ENTERPRISE_LANE_LAB_CHANNEL_ID"
	laneModelEnv       = "ENTERPRISE_LANE_LAB_MODEL"

	enterpriseLaneGatewayContextKey = "enterprise_lane_gateway_authorized"
)

type enterpriseLaneGatewayConfig struct {
	tokenID, count, paid, token, channelID int
	modelName                              string
	stop                                   bool
	lanes                                  []model.EnterpriseLaneIdentity
}

type enterpriseLaneGatewayAuthorization struct {
	lane      model.EnterpriseLaneIdentity
	channelID int
	modelName string
	revision  int64
}

var enterpriseLaneGatewayLaunch *enterpriseLaneGatewayConfig
var enterpriseLaneGatewayRuntime atomic.Pointer[enterpriseLaneGatewayConfig]

func EnterpriseLaneGatewayLaunchKeys() []string {
	return []string{laneEnabledEnv, laneStopEnv, laneTokenIDEnv, laneCountEnv, lanePaidBudgetEnv, laneTokenBudgetEnv, laneChannelEnv, laneModelEnv}
}

func enterpriseLaneLoopbackURL(raw string, sql bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Hostname() != "127.0.0.1" || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
		return false
	}
	port, err := strconv.ParseUint(u.Port(), 10, 16)
	if err != nil || port < 1024 {
		return false
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return false
	}
	if !sql {
		return u.Scheme == "redis" && len(q) == 0
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return false
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !strings.HasPrefix(name, "enterprise_lane_") || len(name) <= len("enterprise_lane_") || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	for key, values := range q {
		if len(values) != 1 || (key != "sslmode" && key != "application_name") || (key == "sslmode" && values[0] != "disable") {
			return false
		}
	}
	return true
}

// PrepareEnterpriseLaneGateway runs before any DB/Redis connections. Never
// permit server startup over a production-like address or ambiguous DSN. Even
// the tagged executable is inert without an explicit launch opt-in.
func PrepareEnterpriseLaneGateway(migrating bool) error {
	enterpriseLaneGatewayLaunch = nil
	flag := os.Getenv(laneEnabledEnv)
	if flag == "" || flag == "false" {
		if stop := os.Getenv(laneStopEnv); stop != "" && stop != "false" {
			return errors.New("enterprise lane lab stop requires explicit isolated opt-in")
		}
		return nil
	}
	if flag != "true" || migrating || os.Getenv("HTTP_BIND_ADDRESS") != "127.0.0.1" || os.Getenv("TRUSTED_PROXIES") != "none" ||
		!enterpriseLaneLoopbackURL(os.Getenv("SQL_DSN"), true) {
		return errors.New("enterprise lane lab requires explicit isolated loopback serving settings")
	}
	if os.Getenv("LOG_SQL_DSN") != "" {
		return errors.New("enterprise lane lab requires logs to share the main database")
	}
	if redisDSN := os.Getenv("REDIS_CONN_STRING"); redisDSN != "" && !enterpriseLaneLoopbackURL(redisDSN, false) {
		return errors.New("enterprise lane lab requires isolated loopback Redis")
	}
	var config enterpriseLaneGatewayConfig
	for key, target := range map[string]*int{laneTokenIDEnv: &config.tokenID, laneCountEnv: &config.count, lanePaidBudgetEnv: &config.paid, laneTokenBudgetEnv: &config.token, laneChannelEnv: &config.channelID} {
		n, err := strconv.Atoi(os.Getenv(key))
		if err != nil || n <= 0 {
			return errors.New("missing or invalid " + key)
		}
		*target = n
	}
	config.modelName = os.Getenv(laneModelEnv)
	if config.modelName != "gpt-4o" ||
		config.count > model.EnterpriseLaneGatewayMaxLanes ||
		config.paid > common.MaxWalletQuota/config.count || config.token > common.MaxWalletQuota/config.count {
		return errors.New("enterprise lane lab requires the bounded gpt-4o fixture manifest")
	}
	switch os.Getenv(laneStopEnv) {
	case "", "false":
	case "true":
		config.stop = true
	default:
		return errors.New("invalid enterprise lane lab stop mode")
	}
	enterpriseLaneGatewayLaunch = &config
	return nil
}

// ConfigureEnterpriseLaneGateway never exposes an HTTP provisioning endpoint.
// The caller finishes ordinary startup first, then prepares the explicit lab
// schema and original paid/finite-token partition before any listener starts.
func ConfigureEnterpriseLaneGateway(ctx context.Context) (stop bool, err error) {
	enterpriseLaneGatewayRuntime.Store(nil)
	pending := enterpriseLaneGatewayLaunch
	if pending == nil {
		return false, model.CheckEnterpriseLaneGatewayDisabled(ctx, model.DB)
	}
	if !pending.stop {
		if !common.LogConsumeEnabled || model.LOG_DB != model.DB {
			return false, errors.New("enterprise lane lab requires enabled same-database usage logs")
		}
		var channel model.Channel
		if err := model.DB.WithContext(ctx).First(&channel, pending.channelID).Error; err != nil ||
			channel.Status != common.ChannelStatusEnabled || channel.Type != constant.ChannelTypeOpenAI ||
			!slices.Contains(channel.GetModels(), pending.modelName) {
			return false, errors.New("enterprise lane lab requires the designated native fixture channel")
		}
		target, err := url.Parse(channel.GetBaseURL())
		if err != nil || target == nil || target.Scheme != "http" || target.Hostname() != "127.0.0.1" || target.Port() == "" ||
			target.User != nil || target.RawQuery != "" || target.Fragment != "" ||
			(target.Path != "" && target.Path != "/") {
			return false, errors.New("enterprise lane lab fixture channel must be a direct loopback HTTP origin")
		}
		if err := model.DB.WithContext(ctx).AutoMigrate(model.EnterpriseLaneModels()...); err != nil {
			return false, errors.New("enterprise lane lab schema unavailable")
		}
	}
	lanes, err := model.BootstrapEnterpriseLaneGateway(ctx, model.DB, pending.tokenID, pending.count, pending.paid, pending.token, pending.stop)
	if err != nil {
		return false, errors.New("enterprise lane lab manifest activation or stop failed")
	}
	if pending.stop {
		return true, nil
	}
	runtime := *pending
	runtime.lanes = lanes
	enterpriseLaneGatewayRuntime.Store(&runtime)
	return false, nil
}

// authorizeEnterpriseLaneGateway runs only after ordinary credential, user,
// IP, group, and interception checks. A true return means the bound key was
// handled: the caller must not fall through to its ordinary finite balance.
func authorizeEnterpriseLaneGateway(c *gin.Context, token *model.Token, keyParts []string) bool {
	config := enterpriseLaneGatewayRuntime.Load()
	if config == nil || token.Id != config.tokenID {
		return false
	}
	if len(config.lanes) == 0 || len(keyParts) != 1 || token.UnlimitedQuota || token.UserId != config.lanes[0].UserID ||
		subtle.ConstantTimeCompare([]byte(model.BillingTokenFingerprint(token.Key)), []byte(config.lanes[0].TokenFingerprint)) != 1 {
		abortWithOpenAiMessage(c, http.StatusUnauthorized, i18n.T(c, i18n.MsgTokenInvalid))
		return true
	}
	if c.Request.Method != http.MethodPost || c.Request.URL.Path != "/v1/chat/completions" || c.FullPath() != "/v1/chat/completions" || c.Request.URL.RawPath != "" {
		abortWithOpenAiMessage(c, http.StatusForbidden, i18n.T(c, i18n.MsgTokenInvalid), "enterprise_lane_route_not_allowed")
		return true
	}
	requestID := c.GetString(common.RequestIdKey)
	if requestID == "" || !common.LogConsumeEnabled || model.LOG_DB != model.DB {
		abortWithOpenAiMessage(c, http.StatusServiceUnavailable, i18n.T(c, i18n.MsgDatabaseError))
		return true
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(requestID))
	lane := config.lanes[h.Sum32()%uint32(len(config.lanes))]
	if err := model.AuthorizeEnterpriseLaneGateway(c.Request.Context(), model.DB, lane, token.CacheRevision); err != nil {
		if errors.Is(err, model.ErrEnterpriseLaneIdentity) {
			abortWithOpenAiMessage(c, http.StatusUnauthorized, i18n.T(c, i18n.MsgTokenInvalid))
		} else {
			abortWithOpenAiMessage(c, http.StatusServiceUnavailable, i18n.T(c, i18n.MsgDatabaseError))
		}
		return true
	}
	c.Set(enterpriseLaneGatewayContextKey, enterpriseLaneGatewayAuthorization{lane: lane, channelID: config.channelID, modelName: config.modelName, revision: token.CacheRevision})
	c.Next()
	return true
}

func EnterpriseLaneForRequest(c *gin.Context) (model.EnterpriseLaneIdentity, bool) {
	auth, ok := common.GetContextKeyType[enterpriseLaneGatewayAuthorization](c, enterpriseLaneGatewayContextKey)
	return auth.lane, ok
}

func EnterpriseLaneRoutePolicy(c *gin.Context) (int, string, bool) {
	auth, ok := common.GetContextKeyType[enterpriseLaneGatewayAuthorization](c, enterpriseLaneGatewayContextKey)
	return auth.channelID, auth.modelName, ok
}

// RecheckEnterpriseLaneAuthorization narrows the ordinary credential-revocation
// window immediately before dispatch. The explicit stop-all lane transaction,
// not this nonlocking read, is the atomic barrier against new dispatch claims.
func RecheckEnterpriseLaneAuthorization(c *gin.Context) error {
	auth, ok := common.GetContextKeyType[enterpriseLaneGatewayAuthorization](c, enterpriseLaneGatewayContextKey)
	if !ok {
		return model.ErrEnterpriseLaneIdentity
	}
	if !common.LogConsumeEnabled || model.LOG_DB != model.DB {
		return model.ErrEnterpriseLaneUnavailable
	}
	return model.AuthorizeEnterpriseLaneGateway(c.Request.Context(), model.DB, auth.lane, auth.revision)
}

// EnterpriseLaneGatewayEnabled reports whether an isolated manifest was bound at startup.
func EnterpriseLaneGatewayEnabled() bool { return enterpriseLaneGatewayRuntime.Load() != nil }
