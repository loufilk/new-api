package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func TestGroupRenameTaskBillingUsesFrozenGroupRatio(t *testing.T) {
	originalGroup, originalModels := ratio_setting.GroupRatio2JSONString(), ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroup))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModels))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"group-snapshot-model":2}`))
	task := &model.Task{Group: "old", Quota: 777, PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{OriginModelName: "group-snapshot-model", GroupRatio: 0.25}}}
	for _, groups := range []string{`{"new":0.25}`, `{"new":0.25,"old":99}`} {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
		amount, _, _, ok := taskQuotaByTokens(task, 100)
		require.True(t, ok)
		assert.Equal(t, 50, amount, "historical name removal/reuse must not choose fallback or new identity price")
		assert.Equal(t, "old", task.Group)
		assert.Equal(t, 777, task.Quota, "quote helper must not mutate historical reservation")
	}
	task.PrivateData.BillingContext.GroupRatio = 0
	amount, _, _, ok := taskQuotaByTokens(task, 100)
	require.True(t, ok)
	assert.Zero(t, amount, "explicit zero quote is not missing")
}

func TestGroupRenameTaskBillingPaidSnapshotSurvivesCurrentFreeGroup(t *testing.T) {
	originalGroups, originalModels := ratio_setting.GroupRatio2JSONString(), ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroups))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModels))
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"test-model":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"review-free":0}`))
	user, token, channel := seedReceiptActors(t, 0, 0)
	task := seedReceiptTask(t, user, token, channel, 300)
	task.Status, task.Group = model.TaskStatusInProgress, "review-free"
	require.NotNil(t, task.PrivateData.BillingContext)
	require.Equal(t, float64(1), task.PrivateData.BillingContext.GroupRatio, "the admitted price stays paid even when the current display group is free")
	require.NoError(t, model.DB.Save(task).Error)
	result := &relaycommon.TaskInfo{Status: model.TaskStatusFailure, TotalTokens: 10, Reason: "failed with measured usage after the group price changed"}
	adaptor := &reviewUsagePollingAdaptor{}
	require.NoError(t, ApplyTaskPollResult(t.Context(), adaptor, task, result, []byte(`{"usage":10}`)))
	var saved model.Task
	require.NoError(t, model.DB.First(&saved, task.ID).Error)
	require.Equal(t, 10, saved.Quota)
	require.False(t, saved.SettlementPending)
	require.NotNil(t, saved.PrivateData.BillingContext)
	require.Equal(t, float64(1), saved.PrivateData.BillingContext.GroupRatio)
	require.Contains(t, saved.PrivateData.FinancialOperationID, ":settle:10")
	after := reviewAccounting(t, user, token, channel)
	require.Equal(t, 990, after.Quota)
	require.Equal(t, 1990, after.TokenRemain)
	require.Equal(t, 10, after.Used)
	require.Equal(t, 10, after.TokenUsed)
	require.Equal(t, 10, after.ChannelUsed)
	require.Equal(t, 1, after.Requests)
	require.NoError(t, ApplyTaskPollResult(t.Context(), adaptor, &saved, result, []byte(`{"usage":10}`)))
	reviewRecoveryPass(t)
	require.Equal(t, after, reviewAccounting(t, user, token, channel), "replay and recovery cannot reprice or settle the same usage twice")
}

func TestGroupRenameLegacyTaskDoesNotRepriceRetiredName(t *testing.T) {
	originalGroup, originalModels := ratio_setting.GroupRatio2JSONString(), ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroup))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalModels))
		model.DB.Where("key = ?", "GroupRenameAliases").Delete(&model.Option{})
	})
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"group-snapshot-legacy":2}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"new":0.25,"old":99}`))
	require.NoError(t, model.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&model.Option{Key: "GroupRenameAliases", Value: `{"old":"new"}`}).Error)
	task := &model.Task{Group: "old", Quota: 777, Properties: model.Properties{OriginModelName: "group-snapshot-legacy"}}
	_, _, _, ok := taskQuotaByTokens(task, 100)
	assert.False(t, ok, "without a quote, retired name reuse must retain reservation rather than guess")
	assert.Equal(t, 777, task.Quota)
}

func TestGroupRenameRealtimeSegmentKeepsAdmittedMultiplier(t *testing.T) {
	truncate(t)
	configureWSSBillingIdentityRatios(t)
	seedBonusEligibleModel(t, wssBillingModel, boolPtr(true))
	originalGroup := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroup)) })
	// The old name is gone; fallback 1 would double the admitted 0.5 price.
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"new":0.5}`))
	const userID, tokenID = 9530, 9531
	const tokenKey, requestID = "synthetic-group-rename-wss", "synthetic-group-rename-wss-request"
	seedBonusUser(t, userID, 1000, 0)
	seedToken(t, tokenID, userID, tokenKey, 10000)
	c := testGinContext(t)
	info := &relaycommon.RelayInfo{UserId: userID, ChannelMeta: &relaycommon.ChannelMeta{}, TokenId: tokenID, TokenKey: tokenKey, RequestId: requestID, UsingGroup: "old", UserGroup: "old", OriginModelName: wssOriginModel, BillingModelName: wssBillingModel, BillingSource: BillingSourceWallet, ForcePreConsume: true, UserQuota: 1000}
	info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1, QuotaWarningEnabled: boolPtr(false)}
	info.PriceData.GroupRatioInfo.GroupRatio = 0.5
	c.Set("token_quota", 10000)
	require.Nil(t, PreConsumeBilling(c, 0, info))
	require.NoError(t, PreWssConsumeQuota(c, info, wssBillingIdentityUsage()))
	assertUserBalances(t, userID, 1000-wssExpectedQuota/2, 0, 0)
	assert.Equal(t, "old", info.UsingGroup)
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	assert.Equal(t, tokenKey, token.Key)
	assert.Equal(t, common.TokenStatusEnabled, token.Status)
}
