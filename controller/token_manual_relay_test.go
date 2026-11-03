package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The public auth, discovery, distributor, relay and billing paths remain real.
// Only upstream HTTP is synthetic; each case owns its PostgreSQL schema/process.
func TestManualTokenRelayScopeOrderAndFinalGroupChargePostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL wrapper")
	}
	for _, tc := range []struct {
		name                  string
		groups                []string
		onlyB, smart, noRetry bool
		calls                 []string
	}{
		{"manual_fallback", []string{"default", "vip"}, false, false, true, []string{"a", "b"}},
		{"manual_reordered", []string{"vip", "default"}, false, false, true, []string{"b"}},
		{"manual_model_only_in_b", []string{"default", "vip"}, true, false, false, []string{"b"}},
		{"smart_model_only_in_b", []string{"default"}, true, true, true, []string{"b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if runStalePaymentFixtureChild(t) {
				return
			}
			f := newInitialReservationHTTPFixture(t, service.BillingSourceWallet)
			require.NoError(t, f.db.AutoMigrate(&model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{}, &model.TokenRoutingMetric{}))
			configureTokenAutoGroupsTest(t, "5", `[]`)
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"A","vip":"B"}`))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2,"private":0.1}`))
			require.False(t, service.GroupInUserUsableGroups("default", "auto"))
			f.user.QuotaPaid, f.user.QuotaBonus, f.user.QuotaReward = 1000, 0, 0
			f.user.Status = common.UserStatusEnabled
			f.user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningEnabled: common.GetPointer(false)})
			require.NoError(t, f.db.Save(&f.user).Error)
			f.token.Key, f.token.Group = strings.Repeat("m", 48), "auto"
			f.token.CrossGroupRetry, f.token.NoRetryWithinGroup, f.token.SmartRouting = true, tc.noRetry, tc.smart
			require.NoError(t, f.token.SetAutoGroups(tc.groups))
			require.NoError(t, f.db.Save(&f.token).Error)
			prices, err := common.Marshal(map[string]float64{f.modelName: 100 / common.QuotaPerUnit})
			require.NoError(t, err)
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(prices)))
			model.InvalidatePricingCache()
			common.LogConsumeEnabled = true
			var mu sync.Mutex
			calls := []string{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				label := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
				var body struct {
					Model string `json:"model"`
				}
				if err := common.DecodeJson(r.Body, &body); err != nil || body.Model != f.modelName {
					t.Errorf("upstream model changed or invalid body: %q %v", body.Model, err)
				}
				mu.Lock()
				calls = append(calls, label)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if label != "b" {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `{"error":{"message":"synthetic unavailable","type":"server_error","code":"synthetic_unavailable"}}`)
					return
				}
				_, _ = io.WriteString(w, `{"id":"synthetic-response","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"synthetic success"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
			}))
			defer upstream.Close()
			require.NoError(t, f.db.Where("1 = 1").Delete(&model.Ability{}).Error)
			require.NoError(t, f.db.Where("1 = 1").Delete(&model.Channel{}).Error)
			var bID int
			for _, entry := range []struct {
				label, group string
				priority     int64
			}{{"a", "default", 10}, {"a-second", "default", 0}, {"b", "vip", 0}, {"private", "private", 99}} {
				models := f.modelName
				if tc.onlyB && entry.group == "default" {
					models = "other-synthetic-model"
				}
				ch := model.Channel{Name: entry.label, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "synthetic-provider-key", BaseURL: common.GetPointer(upstream.URL + "/" + entry.label), Models: models, Group: entry.group, Priority: common.GetPointer(entry.priority)}
				require.NoError(t, f.db.Create(&ch).Error)
				require.NoError(t, f.db.Create(&model.Ability{Group: entry.group, Model: models, ChannelId: ch.Id, Enabled: true, Priority: common.GetPointer(entry.priority)}).Error)
				if entry.label == "b" {
					bID = ch.Id
				}
			}
			engine := gin.New()
			relayDone := make(chan struct{})
			engine.Use(func(c *gin.Context) {
				c.Next()
				if c.Request.Method == http.MethodPost {
					close(relayDone)
				}
			})
			engine.Use(middleware.RequestId(), middleware.BodyStorageCleanup(), middleware.TokenAuth())
			engine.GET("/v1/models", func(c *gin.Context) { ListModels(c, constant.ChannelTypeOpenAI) })
			engine.POST("/v1/chat/completions", middleware.Distribute(), func(c *gin.Context) { Relay(c, types.RelayFormatOpenAI) })
			gateway := httptest.NewServer(engine)
			defer gateway.Close()
			client := &http.Client{Timeout: 15 * time.Second}
			inventory, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/models", nil)
			require.NoError(t, err)
			inventory.Header.Set("Authorization", "Bearer sk-"+f.token.Key)
			invResp, err := client.Do(inventory)
			require.NoError(t, err)
			invBody, err := io.ReadAll(invResp.Body)
			invResp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, invResp.StatusCode, string(invBody))
			require.Contains(t, string(invBody), f.modelName)
			req, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", strings.NewReader(`{"model":"`+f.modelName+`","messages":[{"role":"user","content":"hello"}]}`))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer sk-"+f.token.Key)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			require.NoError(t, err)
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
			require.Contains(t, string(body), "synthetic success")
			select {
			case <-relayDone:
			case <-time.After(15 * time.Second):
				t.Fatal("relay handler did not finish settlement")
			}
			waitTaskSubmissionFixtureQuiescence(t)
			mu.Lock()
			actualCalls := append([]string(nil), calls...)
			mu.Unlock()
			require.Equal(t, tc.calls, actualCalls, "manual ordering, model eligibility and one channel per group; unauthorized group excluded")
			var user model.User
			var token model.Token
			var b model.Channel
			require.NoError(t, f.db.First(&user, f.user.Id).Error)
			require.NoError(t, f.db.First(&token, f.token.Id).Error)
			require.NoError(t, f.db.First(&b, bID).Error)
			require.Equal(t, []int{800, 800, 0, 0, 200, 1}, []int{user.Quota, user.QuotaPaid, user.QuotaBonus, user.QuotaReward, user.UsedQuota, user.RequestCount})
			require.Equal(t, []int{800, 200}, []int{token.RemainQuota, token.UsedQuota})
			require.Equal(t, int64(200), b.UsedQuota)
			var owners []model.BillingFinalization
			require.NoError(t, f.db.Find(&owners).Error)
			require.Len(t, owners, 1)
			require.Equal(t, model.BillingSettled, owners[0].State)
			require.Equal(t, 200, owners[0].ActualQuota)
			var ledger []model.BalanceLedger
			require.NoError(t, f.db.Find(&ledger).Error)
			net := 0
			for _, row := range ledger {
				net += row.Delta
				require.Equal(t, "paid", row.Account)
			}
			require.Equal(t, -200, net, "final successful group's multiplier charged once")
			var logs []model.Log
			require.NoError(t, f.db.Where("type = ?", model.LogTypeConsume).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, "vip", logs[0].Group)
			require.Equal(t, 200, logs[0].Quota)
		})
	}
}
