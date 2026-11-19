package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDistributorSetupFailureAndChannelFreeOperations(t *testing.T) {
	require.NoError(t, i18n.Init())
	router := gin.New()
	ch := &model.Channel{Id: 94219, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, ChannelInfo: model.ChannelInfo{IsMultiKey: true}}
	router.Use(func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "route-a")
		c.Set(contextKeyTaskPluginEndpointSelection, taskPluginEndpointSelection{Channel: ch, Model: "m"})
	}, Distribute())
	reached := false
	router.POST("/v1/chat/completions", func(c *gin.Context) { reached = true })
	router.GET("/v1/videos/:task_id", func(c *gin.Context) { reached = true; c.Status(http.StatusNoContent) })
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.False(t, reached)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), string(types.ErrorCodeChannelNoAvailableKey))
	require.NotContains(t, rec.Body.String(), "no keys available")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/videos/synthetic", nil))
	require.True(t, reached)
	require.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDistributorOrganizationClearedOnNextChannel(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	a := &model.Channel{Id: 1, Type: constant.ChannelTypeOpenAI, OpenAIOrganization: common.GetPointer("synthetic-org"), Key: "test"}
	b := &model.Channel{Id: 2, Type: constant.ChannelTypeOpenAI, Key: "test"}
	require.Nil(t, SetupContextForSelectedChannel(ctx, a, "m"))
	require.Equal(t, "synthetic-org", ctx.GetString("channel_organization"))
	require.Nil(t, SetupContextForSelectedChannel(ctx, b, "m"))
	require.Empty(t, ctx.GetString("channel_organization"))
}

func TestDistributorAffinityRequiresSuccessfulOutcome(t *testing.T) {
	set := operation_setting.GetChannelAffinitySetting()
	before := *set
	t.Cleanup(func() { *set = before })
	set.Enabled = true
	set.Rules = []operation_setting.ChannelAffinityRule{{Name: "synthetic-affinity", ModelRegex: []string{"^m$"}, PathRegex: []string{"^/v1/chat/completions$"}, KeySources: []operation_setting.ChannelAffinityKeySource{{Type: "request_header", Key: "X-Fixture"}}, SessionMode: "prefer"}}
	for _, outcome := range []string{"plain-200", "stream-error", "business-refusal", "clean", "retry-switched", "no-switch-after-failed-first"} {
		t.Run(outcome, func(t *testing.T) {
			set.SwitchOnSuccess = outcome != "no-switch-after-failed-first"
			first := &model.Channel{Id: 94229, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "synthetic"}
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(contextKeyTaskPluginEndpointSelection, taskPluginEndpointSelection{Channel: first, Model: "m"})
				_, hit := service.GetPreferredChannelByAffinity(c, "m", "g")
				require.False(t, hit)
			}, Distribute())
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				policy := service.RequestPolicy(c)
				id := first.Id
				if outcome == "retry-switched" || outcome == "no-switch-after-failed-first" {
					id++
					c.Set("channel_id", id)
				}
				policy.BeginAttempt(&model.Channel{Id: id}, "g")
				if outcome != "plain-200" {
					info := &relaycommon.RelayInfo{PerformanceBusinessRejection: outcome == "business-refusal"}
					if outcome == "stream-error" {
						s := &relaycommon.StreamStatus{}
						s.MarkFailed("synthetic", "server_error", 503)
						info.StreamStatus = s
					}
					service.MarkRequestPolicySuccess(c, info.StreamStatus, info)
				}
				c.Status(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"m"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Fixture", outcome+time.Now().Format("150405.000000000"))
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, 200, rec.Code)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req.Clone(req.Context())
			id, hit := service.GetPreferredChannelByAffinity(c, "m", "g")
			t.Cleanup(func() { service.ClearCurrentChannelAffinityCache(c) })
			require.Equal(t, outcome == "clean" || outcome == "retry-switched", hit)
			if hit && outcome == "retry-switched" {
				require.Equal(t, first.Id+1, id)
			}
		})
	}
}
