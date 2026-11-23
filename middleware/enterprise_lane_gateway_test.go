//go:build enterprise_lane_lab

package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseLaneGatewayLaunchIsolation(t *testing.T) {
	for _, k := range EnterpriseLaneGatewayLaunchKeys() {
		t.Setenv(k, "")
	}
	t.Cleanup(func() { enterpriseLaneGatewayLaunch = nil })
	require.NoError(t, PrepareEnterpriseLaneGateway(false))
	require.Nil(t, enterpriseLaneGatewayLaunch)
	for k, v := range map[string]string{
		laneEnabledEnv: "true", laneTokenIDEnv: "1", laneCountEnv: "4", lanePaidBudgetEnv: "200", laneTokenBudgetEnv: "100", laneChannelEnv: "9", laneModelEnv: "gpt-4o",
		"HTTP_BIND_ADDRESS": "127.0.0.1", "TRUSTED_PROXIES": "none", "SQL_DSN": "postgres://qa@127.0.0.1:56830/enterprise_lane_test?sslmode=disable",
		"LOG_SQL_DSN": "", "REDIS_CONN_STRING": "redis://127.0.0.1:56831/0",
	} {
		t.Setenv(k, v)
	}
	require.NoError(t, PrepareEnterpriseLaneGateway(false))
	require.NotNil(t, enterpriseLaneGatewayLaunch)
	require.Error(t, PrepareEnterpriseLaneGateway(true))
	for _, tc := range []struct{ key, value string }{
		{"HTTP_BIND_ADDRESS", "0.0.0.0"}, {"TRUSTED_PROXIES", "127.0.0.1"},
		{"SQL_DSN", "postgres://qa@127.0.0.1:56830/production?sslmode=disable"},
		{"SQL_DSN", "postgres://qa@production.example:56830/enterprise_lane_test?sslmode=disable"},
		{"SQL_DSN", "postgres://qa@127.0.0.1:56830/enterprise_lane_test?host=production.example"},
		{"SQL_DSN", "postgres://qa@127.0.0.1:56830/enterprise_lane_test?options=-csearch_path%3Dproduction"},
		{"LOG_SQL_DSN", "postgres://qa@127.0.0.1:56830/enterprise_lane_logs?sslmode=disable"},
		{"REDIS_CONN_STRING", "redis://production.example:6379/0"},
		{laneCountEnv, "17"}, {laneTokenBudgetEnv, "0"}, {laneStopEnv, "anything"}, {laneModelEnv, "other"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			require.Error(t, PrepareEnterpriseLaneGateway(false))
			require.Nil(t, enterpriseLaneGatewayLaunch)
		})
	}
	t.Setenv(laneStopEnv, "true")
	require.NoError(t, PrepareEnterpriseLaneGateway(false))
	require.True(t, enterpriseLaneGatewayLaunch.stop)
}

func TestEnterpriseLaneGatewayCanonicalAuthorizationPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL 18.6")
	}
	t.Setenv("NO_AGENT_FIXTURE_OWNED", "1")
	t.Setenv("LOG_SQL_DSN", "")
	setupDeletedAPIKeyAuthTest(t)
	oldMaster := common.IsMasterNode
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB()) // Also initializes the real PostgreSQL column quoting.
	t.Cleanup(func() { common.IsMasterNode = oldMaster })
	require.NoError(t, i18n.Init())
	gin.SetMode(gin.TestMode)
	oldLogs := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() {
		common.LogConsumeEnabled = oldLogs
		enterpriseLaneGatewayRuntime.Store(nil)
		enterpriseLaneGatewayLaunch = nil
	})
	require.NoError(t, model.DB.AutoMigrate(&model.BillingFinalization{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{},
		&model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{}))
	require.NoError(t, model.DB.AutoMigrate(model.BonusSourceModels()...))
	require.NoError(t, model.DB.AutoMigrate(model.EnterpriseLaneModels()...))
	user := model.User{Username: "lane-auth", AffCode: "lane-auth-001", Group: "default", Status: common.UserStatusEnabled, Quota: 100, QuotaPaid: 100}
	require.NoError(t, model.DB.Create(&user).Error)
	bound := model.Token{UserId: user.Id, Key: strings.Repeat("q", 48), Group: "default", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	control := model.Token{UserId: user.Id, Key: strings.Repeat("t", 48), Group: "default", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	require.NoError(t, model.DB.Create(&bound).Error)
	require.NoError(t, model.DB.Create(&control).Error)
	base := "http://127.0.0.1:56839"
	channel := model.Channel{Id: 9, Name: "isolated-auth-channel", Type: constant.ChannelTypeOpenAI, Key: "fixture-inert", BaseURL: &base, Models: "gpt-4o", Group: "default"}
	require.NoError(t, model.DB.Create(&channel).Error)
	enterpriseLaneGatewayLaunch = &enterpriseLaneGatewayConfig{tokenID: bound.Id, count: 2, paid: 50, token: 50, channelID: 9, modelName: "gpt-4o"}
	stop, err := ConfigureEnterpriseLaneGateway(t.Context())
	require.NoError(t, err)
	require.False(t, stop)
	require.True(t, EnterpriseLaneGatewayEnabled())
	lanes := enterpriseLaneGatewayRuntime.Load().lanes
	require.NoError(t, model.DB.First(&bound, bound.Id).Error)
	require.Zero(t, bound.RemainQuota)

	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	engine.Use(RequestId())
	engine.POST("/v1/chat/completions", TokenAuth(), func(c *gin.Context) {
		id, hasLane := EnterpriseLaneForRequest(c)
		if c.GetInt("token_id") == bound.Id {
			require.True(t, hasLane)
			require.Contains(t, lanes, id)
			channel, modelName, ok := EnterpriseLaneRoutePolicy(c)
			require.True(t, ok)
			require.Equal(t, 9, channel)
			require.Equal(t, "gpt-4o", modelName)
			require.NoError(t, RecheckEnterpriseLaneAuthorization(c))
		} else {
			require.False(t, hasLane)
		}
		c.Status(http.StatusNoContent)
	})
	engine.POST("/v1/responses", TokenAuth(), func(c *gin.Context) {
		require.Equal(t, control.Id, c.GetInt("token_id"), "bound key must never reach a legacy route")
		c.Status(http.StatusNoContent)
	})
	engine.GET("/v1/realtime", TokenAuth(), func(c *gin.Context) {
		t.Error("bound key must never reach a websocket handler")
	})
	send := func(method, path, key, remote string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("Authorization", "Bearer sk-"+key)
		req.RemoteAddr = remote + ":54545"
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		t.Logf("%s %s -> %d %s", method, path, w.Code, w.Body.String())
		return w.Code
	}
	require.Equal(t, http.StatusNoContent, send("POST", "/v1/chat/completions", bound.Key, "127.0.0.1"))
	require.Equal(t, http.StatusNoContent, send("POST", "/v1/chat/completions", control.Key, "127.0.0.1"))
	require.Equal(t, http.StatusNoContent, send("POST", "/v1/responses", control.Key, "127.0.0.1"))
	require.Equal(t, http.StatusForbidden, send("POST", "/v1/responses", bound.Key, "127.0.0.1"))
	require.Equal(t, http.StatusForbidden, send("GET", "/v1/realtime", bound.Key, "127.0.0.1"))
	require.NotEqual(t, http.StatusNoContent, send("POST", "/v1/chat/completions", bound.Key+"-9", "127.0.0.1"))

	common.LogConsumeEnabled = false
	require.Equal(t, http.StatusServiceUnavailable, send("POST", "/v1/chat/completions", bound.Key, "127.0.0.1"))
	common.LogConsumeEnabled = true
	require.NoError(t, model.DB.Model(&bound).Update("expired_time", common.GetTimestamp()-1).Error)
	require.Equal(t, http.StatusUnauthorized, send("POST", "/v1/chat/completions", bound.Key, "127.0.0.1"))
	require.NoError(t, model.DB.Model(&bound).Updates(map[string]any{"expired_time": -1, "status": common.TokenStatusDisabled}).Error)
	require.Equal(t, http.StatusUnauthorized, send("POST", "/v1/chat/completions", bound.Key, "127.0.0.1"))
	require.NoError(t, model.DB.Model(&bound).Updates(map[string]any{"status": common.TokenStatusEnabled, "allow_ips": "10.0.0.0/8"}).Error)
	require.Equal(t, http.StatusForbidden, send("POST", "/v1/chat/completions", bound.Key, "127.0.0.1"))
	enterpriseLaneGatewayLaunch = nil
	_, err = ConfigureEnterpriseLaneGateway(t.Context())
	require.ErrorIs(t, err, model.ErrEnterpriseLaneState)
	require.False(t, EnterpriseLaneGatewayEnabled())
}
