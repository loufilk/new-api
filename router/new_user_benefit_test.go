package router

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type benefitRouteRoundTripper func(*http.Request) (*http.Response, error)

func (f benefitRouteRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNewUserBenefitRoutes(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an owned PostgreSQL TEST_POSTGRES_DSN fixture")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	schema := "benefit_routes_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: parsed.String(), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldLimit := common.RedisEnabled, common.MemoryCacheEnabled, common.CriticalRateLimitEnable
	oldTurnstile := common.TurnstileCheckEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.MemoryCacheEnabled, common.CriticalRateLimitEnable = false, false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.CriticalRateLimitEnable = oldRedis, oldMemory, oldLimit
		common.TurnstileCheckEnabled = oldTurnstile
		require.NoError(t, db.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserAccessToken{}, &model.UserSession{}, &model.Option{},
		&model.Token{}, &model.Channel{}, &model.Ability{}, &model.SubscriptionPlan{}, &model.AuditLog{},
		&model.NewUserBenefitConfig{}, &model.NewUserBenefitConfigAudit{}, &model.NewUserBenefitOrder{}))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	user := seedReferralRouteUser(t, db, 7101, "", 0, common.UserStatusEnabled)
	root := seedReferralRouteUser(t, db, 7102, "", 0, common.UserStatusEnabled)
	require.NoError(t, db.Model(&root).Update("role", common.RoleRootUser).Error)
	reader := model.AccessTokenPrefix + "benefit-user-reader"
	writer := model.AccessTokenPrefix + "benefit-user-writer"
	wrongScope := model.AccessTokenPrefix + "benefit-profile-reader"
	rootReader := model.AccessTokenPrefix + "benefit-root-reader"
	rootWriter := model.AccessTokenPrefix + "benefit-root-writer"
	userOptions := model.AccessTokenPrefix + "benefit-common-options"
	seedRouteScopedToken(t, db, user.Id, reader, "wallet:read")
	seedRouteScopedToken(t, db, user.Id, writer, "wallet:write")
	seedRouteScopedToken(t, db, user.Id, wrongScope, "profile:read")
	seedRouteScopedToken(t, db, user.Id, userOptions, "option:write")
	seedRouteScopedToken(t, db, root.Id, rootReader, "option:read")
	seedRouteScopedToken(t, db, root.Id, rootWriter, "option:read", "option:write")
	body := `{"enabled":false,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":0,"max_total_orders":1000,"max_orders_per_24h":100,"max_orders_per_network_24h":3,"total_bonus_budget_cny":"900.00"}`
	for _, test := range []struct {
		method, path, token, body string
		status                    int
	}{
		{http.MethodGet, "/api/user/new-user-benefit", "", "", http.StatusUnauthorized},
		{http.MethodPost, "/api/user/new-user-benefit/pay", "", "{}", http.StatusUnauthorized},
		{http.MethodGet, "/api/option/new-user-benefit", "", "", http.StatusUnauthorized},
		{http.MethodPut, "/api/option/new-user-benefit", "", body, http.StatusUnauthorized},
		{http.MethodGet, "/api/user/new-user-benefit", wrongScope, "", http.StatusForbidden},
		{http.MethodPost, "/api/user/new-user-benefit/pay", reader, "{}", http.StatusForbidden},
		{http.MethodPut, "/api/option/new-user-benefit", userOptions, body, http.StatusForbidden},
		{http.MethodPut, "/api/option/new-user-benefit", rootReader, body, http.StatusForbidden},
		{http.MethodGet, "/api/user/new-user-benefit", reader, "", http.StatusOK},
		{http.MethodGet, "/api/option/new-user-benefit", rootReader, "", http.StatusOK},
	} {
		response := performReferralRouteRequest(engine, test.method, test.path, test.body, test.token)
		assert.Equal(t, test.status, response.Code, "%s %s: %s", test.method, test.path, response.Body.String())
	}
	common.TurnstileCheckEnabled = true
	missingChallenge := performReferralRouteRequest(engine, http.MethodPost, "/api/user/new-user-benefit/pay", `{"payment_method":"alipay","config_version":0}`, writer)
	require.Equal(t, http.StatusForbidden, missingChallenge.Code, missingChallenge.Body.String())
	assert.Contains(t, missingChallenge.Body.String(), "TURNSTILE_VERIFICATION_FAILED", "wallet:write permission does not bypass the route's fresh human challenge")
	var count int64
	require.NoError(t, db.Model(&model.NewUserBenefitOrder{}).Count(&count).Error)
	assert.Zero(t, count, "denied requests and reads must never reserve payment orders")
	require.NoError(t, db.Model(&model.NewUserBenefitConfigAudit{}).Count(&count).Error)
	assert.Zero(t, count, "denied writes must never change the offer")
	saved := performReferralRouteRequest(engine, http.MethodPut, "/api/option/new-user-benefit", body, rootWriter)
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	assert.Contains(t, saved.Body.String(), `"version":1`)
	var audit model.NewUserBenefitConfigAudit
	require.NoError(t, db.First(&audit).Error)
	assert.Equal(t, root.Id, audit.ActorID, "the authenticated operator owns the price change")

	t.Run("cross-account admission uses trusted network and verified challenge", func(t *testing.T) {
		require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}, &model.BalanceLedger{}))
		confirmReferralRouteCompliance(t)
		oldAddress, oldMerchant, oldKey, oldMethods := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods
		oldServer, oldCallback := system_setting.ServerAddress, operation_setting.CustomCallbackAddress
		oldRate, oldUnit := operation_setting.USDExchangeRate, common.QuotaPerUnit
		oldSiteKey, oldSecret, oldTransport := common.TurnstileSiteKey, common.TurnstileSecretKey, http.DefaultTransport
		t.Cleanup(func() {
			operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods = oldAddress, oldMerchant, oldKey, oldMethods
			system_setting.ServerAddress, operation_setting.CustomCallbackAddress = oldServer, oldCallback
			operation_setting.USDExchangeRate, common.QuotaPerUnit = oldRate, oldUnit
			common.TurnstileSiteKey, common.TurnstileSecretKey, http.DefaultTransport = oldSiteKey, oldSecret, oldTransport
		})
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://pay.example.test", "benefit-route-merchant", "synthetic-epay-test-secret"
		operation_setting.PayMethods = []map[string]string{{"type": "alipay", "name": "Alipay"}}
		system_setting.ServerAddress, operation_setting.CustomCallbackAddress = "https://benefit.example.test", ""
		operation_setting.USDExchangeRate, common.QuotaPerUnit = 10, 500000
		common.TurnstileCheckEnabled, common.TurnstileSiteKey, common.TurnstileSecretKey = true, "synthetic-public-site-key", "synthetic-turnstile-test-secret"
		require.NoError(t, engine.SetTrustedProxies(nil))
		verifiedTokens := make(map[string]bool)
		http.DefaultTransport = benefitRouteRoundTripper(func(request *http.Request) (*http.Response, error) {
			require.Equal(t, "https://challenges.cloudflare.com/turnstile/v0/siteverify", request.URL.String())
			require.NoError(t, request.ParseForm())
			assert.Equal(t, "192.0.2.1", request.Form.Get("remoteip"), "untrusted X-Forwarded-For must not alter the challenge's client IP")
			token := request.Form.Get("response")
			response := `{"success":false,"error-codes":["timeout-or-duplicate"]}`
			if token != "" && !verifiedTokens[token] {
				verifiedTokens[token] = true
				response = `{"success":true,"action":"new_user_benefit","hostname":"benefit.example.test"}`
			}
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response)), Header: make(http.Header)}, nil
		})
		userBToken, userCToken := model.AccessTokenPrefix+"benefit-second-account", model.AccessTokenPrefix+"benefit-third-account"
		userB := seedReferralRouteUser(t, db, 7103, userBToken, 0, common.UserStatusEnabled)
		userC := seedReferralRouteUser(t, db, 7104, userCToken, 0, common.UserStatusEnabled)
		for _, person := range []struct {
			id    int
			email string
		}{{user.Id, "first@example.test"}, {userB.Id, "second@example.test"}, {userC.Id, "third@example.test"}} {
			require.NoError(t, db.Model(&model.User{}).Where("id = ?", person.id).Updates(map[string]any{
				"email": person.email, "email_verified_address": person.email,
				"email_verification_version": 1, "email_verified_at": common.GetTimestamp(),
			}).Error)
		}
		activate := performReferralRouteRequest(engine, http.MethodPut, "/api/option/new-user-benefit", `{"enabled":true,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":1,"max_total_orders":1000,"max_orders_per_24h":100,"max_orders_per_network_24h":1,"total_bonus_budget_cny":"900.00"}`, rootWriter)
		require.Equal(t, http.StatusOK, activate.Code, activate.Body.String())
		intent := `{"payment_method":"alipay","config_version":2}`
		pay := func(token, challenge, forwardedIP string) *http.Response {
			response := performReferralRouteRequest(engine, http.MethodPost, "/api/user/new-user-benefit/pay?turnstile="+challenge, intent, token,
				routeHeader{"Idempotency-Key", uuid.NewString()}, routeHeader{"X-Forwarded-For", forwardedIP}, routeHeader{"turnstile_verified_action", "new_user_benefit"})
			return response.Result()
		}
		first := pay(writer, "first-proof", "198.51.100.1")
		require.Equal(t, http.StatusOK, first.StatusCode)
		require.NoError(t, first.Body.Close())
		second := pay(userBToken, "second-proof", "198.51.100.2")
		secondBody, err := io.ReadAll(second.Body)
		require.NoError(t, err)
		require.NoError(t, second.Body.Close())
		assert.Equal(t, http.StatusTooManyRequests, second.StatusCode, string(secondBody))
		assert.Contains(t, string(secondBody), "new_user_benefit_limit_reached")
		resume := pay(writer, "resume-proof", "198.51.100.3")
		assert.Equal(t, http.StatusOK, resume.StatusCode, "a pending promise resumes without spending a second network slot")
		require.NoError(t, resume.Body.Close())
		replay := pay(writer, "resume-proof", "198.51.100.4")
		assert.Equal(t, http.StatusForbidden, replay.StatusCode, "checkout attempts still need a fresh challenge while Turnstile is enabled")
		require.NoError(t, replay.Body.Close())
		common.TurnstileCheckEnabled = false
		bypass := pay(userCToken, "forged-proof", "198.51.100.5")
		bypassBody, err := io.ReadAll(bypass.Body)
		require.NoError(t, err)
		require.NoError(t, bypass.Body.Close())
		assert.Equal(t, http.StatusServiceUnavailable, bypass.StatusCode, string(bypassBody))
		assert.Contains(t, string(bypassBody), "new_user_benefit_verification_unavailable", "disabling a general challenge must not authorize fresh subsidy orders")
		var admitted int64
		require.NoError(t, db.Model(&model.NewUserBenefitOrder{}).Count(&admitted).Error)
		assert.EqualValues(t, 1, admitted)
		require.NoError(t, db.Model(&model.TopUp{}).Count(&admitted).Error)
		assert.EqualValues(t, 1, admitted, "blocked attempts must not leak a payable order")
	})
}
