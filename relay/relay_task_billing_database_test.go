package relay

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/walletport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The token-update hook is evidence of accounting progress, not completion of
// the enclosing refund transaction or its post-commit cache invalidation tail.
func waitRelayTaskRefund(t *testing.T, refunded <-chan struct{}, billing relaycommon.BillingSettler) {
	t.Helper()
	select {
	case <-refunded:
	case <-time.After(5 * time.Second):
		t.Fatal("real task refund did not reach token credit")
	}
	// Official refunded means scheduled, not financially completed. This serial
	// fixture joins the actual gopool callbacks before restoring DB/cache globals.
	require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
}

// Real BillingSession + three-account ledger + token rows. The upstream checks
// committed balances itself, so reserving after the paid request cannot pass.
func TestRelayTaskSubmitRetryWalletDatabase(t *testing.T) {
	policy, err := walletport.Default()
	if errors.Is(err, walletport.ErrNotConfigured) {
		require.NoError(t, walletmodule.RegisterRuntime())
	} else {
		require.NoError(t, err)
		require.IsType(t, walletmodule.Policy{}, policy)
	}
	factories := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(factories))
	require.NoError(t, walletmodule.RegisterFundingFactory(factories))
	if err := service.ConfigureFundingFactoryRegistry(factories); err != nil {
		require.EqualError(t, err, "service: funding factory registry already configured")
	}
	oldRedis, oldBatch, oldQuota := common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit
	common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = false, false, 1000
	t.Cleanup(func() {
		common.RedisEnabled, common.BatchUpdateEnabled, common.QuotaPerUnit = oldRedis, oldBatch, oldQuota
	})
	service.InitHttpClient()
	const source = `
export const meta={apiVersion:1,key:"KEY",name:"Retry wallet",version:"1.0.0",author:{name:"Test"},models:["declared-model"],fetchMode:"per_task",usageSchema:{seconds:{type:"number",unit:"second",description:"Video generation unit price"}}};
export function buildSubmitRequest(ctx){return {url:ctx.baseUrl+"/submit",method:"POST",body:{model:ctx.model},action:"text_to_video"};}
export function parseSubmitResponse(ctx,response){return {taskId:response.body.id};}
export function buildQueryRequest(ctx){return {url:ctx.baseUrl+"/query"};}
export function parseTaskResult(){return {status:"SUCCESS"};}
export function extractUsage(){return {seconds:2};}
`
	for _, tc := range []struct {
		name                               string
		paid, bonus, reward, token, target int
		perCall, ineligible, reject        bool
	}{
		{name: "higher_quote_reserved_before_paid_request", paid: 1000, bonus: 70, reward: 30, token: 2000, target: 800},
		{name: "wallet_shortage_never_calls_retry_upstream", paid: 100, bonus: 70, reward: 30, token: 2000, target: 800, reject: true},
		{name: "token_shortage_never_calls_retry_upstream", paid: 1000, bonus: 70, reward: 30, token: 200, target: 800, reject: true},
		{name: "bonus_ineligible_retry_keeps_bonus_untouched", paid: 100, bonus: 1000, reward: 30, token: 2000, target: 800, ineligible: true, reject: true},
		{name: "per_call_retry_clears_expression_and_refunds_all", paid: 1000, bonus: 70, reward: 30, token: 2000, target: 400, perCall: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupRelayChannelDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Model{}, &model.Ability{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}))
			eligible := !tc.ineligible
			require.NoError(t, db.Create(&model.Model{ModelName: "declared-model", Status: 1, BonusEligible: &eligible}).Error)
			model.InvalidateModelBonusEligibleCache()
			t.Cleanup(model.InvalidateModelBonusEligibleCache)
			user := model.User{Username: "task-retry", AffCode: "task-retry", QuotaPaid: tc.paid, QuotaBonus: tc.bonus, QuotaReward: tc.reward, Quota: tc.paid + tc.bonus + tc.reward, Status: common.UserStatusEnabled, Group: "default"}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: "task-retry-token", RemainQuota: tc.token, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			saveBillingConfig(t)
			prices := ratio_setting.ModelPrice2JSONString()
			t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices)) })
			expressions := map[string]string{"retry-a::declared-model": `tier("a",u("seconds")*0.05)`}
			price := `{}`
			if tc.perCall {
				price = `{"declared-model":0.2}`
			} else {
				expressions["retry-b::declared-model"] = `tier("b",u("seconds")*0.4)`
			}
			require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(price))
			raw, err := common.Marshal(expressions)
			require.NoError(t, err)
			require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{billing_setting.PluginBillingExprOption: string(raw), "billing_setting.billing_mode": `{}`, "billing_setting.billing_expr": `{}`, "group_ratio_setting.group_ratio": `{"default":1}`}))
			registry := pluginruntime.NewRegistry()
			for _, key := range []string{"retry-a", "retry-b"} {
				_, err := registry.Register(strings.ReplaceAll(source, "KEY", key), pluginruntime.Options{})
				require.NoError(t, err)
			}
			var callsA, callsB atomic.Int32
			type reservation struct {
				wallet, token int
				err           error
			}
			observed := make(chan reservation, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/a/") {
					callsA.Add(1)
				} else {
					callsB.Add(1)
				}
				var owner model.User
				var key model.Token
				err := db.First(&owner, user.Id).Error
				if err == nil {
					err = db.First(&key, token.Id).Error
				}
				observed <- reservation{wallet: user.Quota - owner.Quota, token: token.RemainQuota - key.RemainQuota, err: err}
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"failed before accepting a job"}`))
			}))
			t.Cleanup(server.Close)
			c, info := newTaskSubmitContext(t, "declared-model", "")
			c.Set("group", "default")
			info.UserId, info.TokenId, info.TokenKey = user.Id, token.Id, token.Key
			info.UserGroup, info.UsingGroup, info.OriginModelName = "default", "default", "declared-model"
			info.UserSetting.BillingPreference = "wallet_only"
			info.RequestId = "retry-wallet-" + tc.name
			for _, attempt := range []string{"a", "b"} {
				plugin, ok := registry.Generation().Get("retry-" + attempt)
				require.True(t, ok)
				c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: registry.Generation(), Plugin: plugin})
				c.Set("task_plugin_key", plugin.Meta.Key)
				common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL+"/"+attempt)
				_, taskErr := RelayTaskSubmit(c, info)
				require.NotNil(t, taskErr)
				if attempt == "b" && tc.reject {
					assert.Equal(t, http.StatusForbidden, taskErr.StatusCode)
				} else {
					require.Equal(t, http.StatusInternalServerError, taskErr.StatusCode, "%+v", taskErr)
				}
			}
			require.IsType(t, &service.BillingSession{}, info.Billing)
			assert.EqualValues(t, 1, callsA.Load())
			first := <-observed
			require.NoError(t, first.err)
			assert.Equal(t, 100, first.wallet)
			assert.Equal(t, 100, first.token)
			held := 100
			if tc.reject {
				assert.Zero(t, callsB.Load())
			} else {
				assert.EqualValues(t, 1, callsB.Load())
				second := <-observed
				require.NoError(t, second.err)
				assert.Equal(t, tc.target, second.wallet)
				assert.Equal(t, tc.target, second.token)
				held = tc.target
			}
			assert.Equal(t, held, info.Billing.GetPreConsumedQuota())
			if tc.perCall {
				assert.Nil(t, info.TieredBillingSnapshot)
			} else {
				require.NotNil(t, info.TieredBillingSnapshot)
				assert.Equal(t, expressions["retry-b::declared-model"], info.TieredBillingSnapshot.ExprString)
			}
			// Observe token-credit progress; join the full callback below before cleanup.
			refunded := make(chan struct{}, 1)
			callback := "test:task-retry-refund"
			require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table != "tokens" || tx.Error != nil {
					return
				}
				updates, ok := tx.Statement.Dest.(map[string]any)
				if !ok {
					return
				}
				expr, ok := updates["remain_quota"].(clause.Expr)
				if ok && expr.SQL == "remain_quota + ?" {
					select {
					case refunded <- struct{}{}:
					default:
					}
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
			t.Cleanup(func() {
				require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
			})
			info.Billing.Refund(c)
			waitRelayTaskRefund(t, refunded, info.Billing)
			require.False(t, info.Billing.NeedsRefund(), "official Refund closes the local flag when scheduled")
			info.Billing.Refund(c)
			var finalUser model.User
			var finalToken model.Token
			require.NoError(t, db.First(&finalUser, user.Id).Error)
			require.NoError(t, db.First(&finalToken, token.Id).Error)
			assert.Equal(t, tc.paid, finalUser.QuotaPaid)
			assert.Equal(t, tc.bonus, finalUser.QuotaBonus)
			assert.Equal(t, tc.reward, finalUser.QuotaReward)
			assert.Equal(t, user.Quota, finalUser.Quota)
			assert.Equal(t, tc.token, finalToken.RemainQuota)
			assert.Zero(t, finalToken.UsedQuota)
			assert.Zero(t, finalUser.UsedQuota)
			assert.Zero(t, finalUser.RequestCount)
			var net int64
			require.NoError(t, db.Model(&model.BalanceLedger{}).Select("COALESCE(SUM(delta), 0)").Where("user_id = ?", user.Id).Scan(&net).Error)
			assert.Zero(t, net, fmt.Sprintf("every held account must be refunded: %s", tc.name))
		})
	}
}

func TestRelayTaskSensitiveRejectionPrecedesPluginAndBilling(t *testing.T) {
	oldEnabled, oldPrompt := setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled
	oldWords, oldExempt := setting.SensitiveWordsToString(), setting.SensitiveCheckExemptGroupsToString()
	setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = true, true
	setting.SensitiveWordsFromString("blocked")
	setting.LoadSensitiveCheckExemptGroups("")
	t.Cleanup(func() {
		setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = oldEnabled, oldPrompt
		setting.SensitiveWordsFromString(oldWords)
		setting.LoadSensitiveCheckExemptGroups(oldExempt)
	})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = w.Write([]byte(`{"id":"unexpected-paid-work"}`))
	}))
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name, contentType, body string
		normalized              bool
	}{
		{name: "json", contentType: "application/json", body: `{"prompt":"blocked"}`},
		{name: "vendor_json", contentType: "application/vnd.task+json", body: `{"prompt":"blocked"}`},
		{name: "normalized_vendor_prompt", contentType: "application/json", body: `{"vendor_alias":"blocked"}`, normalized: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, info := newTaskSubmitContext(t, "declared-model", "")
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", tc.contentType)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			if tc.normalized {
				c.Set("task_request", map[string]any{"prompt": "blocked"})
			}
			pinMappingOrderPlugin(t, c, strings.Replace(mappingOrderSubmitPlugin, `return {url:`, `throw new Error("build hook must not run before keyword rejection"); return {url:`, 1))
			result, taskErr := RelayTaskSubmit(c, info)
			require.Nil(t, result)
			require.NotNil(t, taskErr)
			assert.Equal(t, "sensitive_words_detected", taskErr.Code)
			assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
			assert.Nil(t, info.Billing, "no precharge can be created for local rejection")
			assert.Nil(t, info.TieredBillingSnapshot, "pricing hooks cannot precede local rejection")
			assert.Zero(t, calls.Load(), "no paid or helper upstream request")
		})
	}
}
