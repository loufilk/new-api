package relay

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/walletport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// This specifically exercises the production PrepareRequestBilling free-model
// branch; constructing a session directly would hide the nil-session regression.
// Real duplex transport/reader shutdown is covered in openai's WS matrix. Here
// both segment calls and final accounting are real service/database operations.
func TestPrepareFreeRealtimeBilling(t *testing.T) {
	require.NoError(t, i18n.Init())
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
	for _, preference := range []string{"wallet_only", "subscription_only"} {
		for _, freeBy := range []string{"model", "group"} {
			t.Run(preference+"/"+freeBy, func(t *testing.T) {
				db := setupRelayChannelDB(t)
				require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Log{}, &model.Ability{}, &model.Model{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}, &model.UserSubscription{}, &model.SubscriptionPlan{}, &model.SubscriptionPreConsumeRecord{}))
				oldLog, oldLogType := model.LOG_DB, common.LogDatabaseType()
				oldRedis, oldBatch, oldConsume, oldMaster := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.IsMasterNode
				common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.IsMasterNode = false, false, true, false
				t.Setenv("LOG_SQL_DSN", "")
				require.NoError(t, model.InitLogDB())
				t.Cleanup(func() {
					model.LOG_DB = oldLog
					common.SetLogDatabaseType(oldLogType)
					common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled, common.IsMasterNode = oldRedis, oldBatch, oldConsume, oldMaster
				})
				quotaSettings := operation_setting.GetQuotaSetting()
				previousSettings := *quotaSettings
				quotaSettings.EnableFreeModelPreConsume = false
				t.Cleanup(func() { *quotaSettings = previousSettings })
				modelRatios, modelPrices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
				groups, err := common.Marshal(ratio_setting.GetGroupRatioCopy())
				require.NoError(t, err)
				t.Cleanup(func() {
					require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(modelRatios))
					require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(modelPrices))
					require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(groups)))
				})
				const modelName, group = "review-free-realtime", "review-free-realtime-group"
				modelRatio, groupRatio := "0", "1"
				if freeBy == "group" {
					modelRatio, groupRatio = "1", "0"
				}
				require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
				require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"`+modelName+`":`+modelRatio+`}`))
				require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"`+group+`":`+groupRatio+`}`))
				user := model.User{Username: "free-realtime", AffCode: "free-realtime", Group: group, Status: common.UserStatusEnabled}
				require.NoError(t, db.Create(&user).Error)
				token := model.Token{UserId: user.Id, Key: "synthetic-free-realtime", Status: common.TokenStatusEnabled}
				require.NoError(t, db.Create(&token).Error)
				channel := model.Channel{Name: "free-realtime"}
				require.NoError(t, db.Create(&channel).Error)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil)
				info := &relaycommon.RelayInfo{
					UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: "review-free-realtime-request",
					OriginModelName: modelName, BillingModelName: modelName, UserGroup: group, UsingGroup: group,
					RelayFormat: types.RelayFormatOpenAIRealtime, StartTime: time.Now(),
					ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channel.Id, UpstreamModelName: modelName},
					UserSetting: dto.UserSetting{BillingPreference: preference, QuotaWarningThreshold: -1},
				}
				c.Set(common.RequestIdKey, info.RequestId)
				require.Nil(t, PrepareRequestBilling(c, info), "zero wallet/token must still admit a genuinely free request")
				require.True(t, info.PriceData.FreeModel)
				require.NotNil(t, info.Billing, "free realtime still needs one zero-money accounting owner")
				require.Zero(t, info.Billing.GetPreConsumedQuota())
				for _, n := range []int{20, 30} {
					require.NoError(t, service.PreWssConsumeQuota(c, info, &dto.RealtimeUsage{TotalTokens: n, InputTokens: n, InputTokenDetails: dto.InputTokenDetails{TextTokens: n}}))
				}
				final := &dto.RealtimeUsage{TotalTokens: 50, InputTokens: 50, InputTokenDetails: dto.InputTokenDetails{TextTokens: 50}}
				service.PostWssConsumeQuota(c, info, modelName, final, nil)
				service.PostWssConsumeQuota(c, info, modelName, final, nil)
				require.False(t, info.Billing.NeedsRefund())
				require.NoError(t, db.First(&user, user.Id).Error)
				require.NoError(t, db.First(&token, token.Id).Error)
				require.Zero(t, user.Quota)
				require.Zero(t, user.QuotaPaid)
				require.Zero(t, user.UsedQuota)
				require.Equal(t, 1, user.RequestCount, "two segments and final replay count as one free request")
				require.Zero(t, token.RemainQuota)
				require.Zero(t, token.UsedQuota)
				for _, table := range []any{&model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.SubscriptionPreConsumeRecord{}, &model.UserSubscription{}} {
					var rows int64
					require.NoError(t, db.Model(table).Count(&rows).Error)
					require.Zero(t, rows, "free admission must not debit a source or reserve the subscription minimum")
				}
				var receipt model.BillingFinalization
				require.NoError(t, db.First(&receipt, "request_id = ?", info.RequestId).Error)
				require.Equal(t, model.BillingSettled, receipt.State)
				require.Zero(t, receipt.SourceHeld)
				require.Zero(t, receipt.TokenHeld)
				require.Zero(t, receipt.ActualQuota)
				require.Equal(t, model.BillingTokenFingerprint(token.Key), receipt.TokenFingerprint)
				var logs int64
				require.NoError(t, db.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Count(&logs).Error)
				require.EqualValues(t, 1, logs)
				var consumeLog model.Log
				require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).First(&consumeLog).Error)
				require.Equal(t, info.RequestId, consumeLog.RequestId)
			})
		}
	}
}
