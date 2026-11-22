package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	refundFixtureTopology["TestAudioFixedPriceBilling"] = []string{"", "fixed_price", "token_ratios"}
}

// Exercise the real session factory and audio settlement, not just its quota
// calculator. This entry-point regression is not concurrent native-lock proof.
func TestAudioFixedPriceBilling(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	// Initialize the same-DB log dialect exactly as startup; never open an
	// external display database or claim cross-database atomic log delivery.
	t.Setenv("LOG_SQL_DSN", "")
	oldLogDB, oldMaster := model.LOG_DB, common.IsMasterNode
	oldLogType := common.LogDatabaseType()
	common.IsMasterNode = false
	err := model.InitLogDB()
	common.IsMasterNode = oldMaster
	require.NoError(t, err)
	t.Cleanup(func() {
		model.LOG_DB = oldLogDB
		common.SetLogDatabaseType(oldLogType)
	})

	for _, tc := range []struct {
		name     string
		usePrice bool
		actual   int
	}{
		{name: "fixed_price", usePrice: true, actual: 100},
		{name: "token_ratios", actual: 378},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const hold = 400
			s := currentRefundSession(t, "audio-price-"+tc.name, hold)
			info := s.relayInfo
			info.UserQuota = 1000
			info.UserSetting.QuotaWarningEnabled = boolPtr(false)
			info.StartTime = time.Now()
			info.FirstResponseTime = info.StartTime
			info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 93204}
			require.NoError(t, model.DB.Create(&model.Channel{Id: info.ChannelId, Type: 1, Name: "audio-price-fixture"}).Error)
			require.Nil(t, info.TieredBillingSnapshot, "exercise the non-tiered audio constructor")
			info.PriceData.UsePrice = tc.usePrice
			info.PriceData.ModelPrice = 50 / common.QuotaPerUnit
			info.PriceData.ModelRatio = 3
			info.PriceData.GroupRatioInfo.GroupRatio = 2

			oldCompletion := ratio_setting.CompletionRatio2JSONString()
			oldAudio := ratio_setting.AudioRatio2JSONString()
			oldAudioCompletion := ratio_setting.AudioCompletionRatio2JSONString()
			t.Cleanup(func() {
				require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(oldCompletion))
				require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(oldAudio))
				require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(oldAudioCompletion))
			})
			require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"current-refund-model":4}`))
			require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(`{"current-refund-model":5}`))
			require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(`{"current-refund-model":6}`))

			var held model.BillingFinalization
			require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&held).Error)
			require.Equal(t, model.BillingHeld, held.State)
			require.Equal(t, hold, held.SourceHeld)
			require.Equal(t, hold, held.TokenHeld)
			require.Equal(t, model.BillingTokenFingerprint(info.TokenKey), held.TokenFingerprint)
			require.Equal(t, 600, getUserBalances(t, info.UserId).QuotaPaid)
			require.Equal(t, 600, getTokenRemainQuota(t, info.TokenId))

			c := testGinContext(t)
			c.Request = httptest.NewRequest("POST", "/audio-billing", nil)
			c.Set(common.RequestIdKey, info.RequestId)
			usage := &dto.Usage{
				PromptTokens: 13, CompletionTokens: 3, TotalTokens: 16,
				PromptTokensDetails:    dto.InputTokenDetails{TextTokens: 10, AudioTokens: 3},
				CompletionTokenDetails: dto.OutputTokenDetails{TextTokens: 2, AudioTokens: 1},
			}
			// Fixed: (50/QuotaPerUnit) * QuotaPerUnit * 2 = 100.
			// Token control: (10 + 2*4 + 3*5 + 1*5*6) * 3 * 2 = 378.
			// Call the entire Post function once; this patch does not redesign its
			// pre-existing display-log/statistics replay contract.
			PostAudioConsumeQuota(c, info, usage, nil)

			var owner model.BillingFinalization
			require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&owner).Error)
			require.Equal(t, model.BillingSettled, owner.State, "the real terminal transaction must finish")
			assert.Equal(t, tc.actual, owner.ActualQuota)
			assert.Equal(t, held.TokenFingerprint, owner.TokenFingerprint)
			user := getUserBalances(t, info.UserId)
			assert.Equal(t, 1000-tc.actual, user.QuotaPaid)
			assert.Zero(t, user.QuotaBonus)
			assert.Zero(t, user.QuotaReward)
			assertQuotaInvariant(t, user)
			assert.Equal(t, tc.actual, user.UsedQuota)
			assert.Equal(t, 1, user.RequestCount)
			var token model.Token
			require.NoError(t, model.DB.First(&token, info.TokenId).Error)
			assert.Equal(t, 1000-tc.actual, token.RemainQuota)
			assert.Equal(t, tc.actual, token.UsedQuota)
			var channel model.Channel
			require.NoError(t, model.DB.First(&channel, info.ChannelId).Error)
			assert.EqualValues(t, tc.actual, channel.UsedQuota)
			rows := ledgerRowsFor(t, info.UserId)
			require.Len(t, rows, 2, "one actual hold and one unused-hold refund")
			assert.Equal(t, string(model.AccountPaid), rows[0].Account)
			assert.Equal(t, string(model.AccountPaid), rows[1].Account)
			assert.Equal(t, -hold, rows[0].Delta)
			assert.Equal(t, hold-tc.actual, rows[1].Delta)
			assert.Equal(t, -tc.actual, rows[0].Delta+rows[1].Delta)

			var logs []model.Log
			require.NoError(t, model.LOG_DB.Where("user_id = ? AND request_id = ?", info.UserId, info.RequestId).Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, model.LogTypeConsume, logs[0].Type)
			assert.Equal(t, tc.actual, logs[0].Quota)
			assert.Equal(t, info.OriginModelName, logs[0].ModelName)
			var other map[string]any
			require.NoError(t, common.Unmarshal([]byte(logs[0].Other), &other))
			assert.Equal(t, info.PriceData.ModelPrice, other["model_price"])
			assert.Equal(t, float64(2), other["group_ratio"])
			assert.Equal(t, true, other["audio"])
			assert.Equal(t, float64(3), other["audio_input"])
			assert.Equal(t, float64(1), other["audio_output"])
		})
	}
}
