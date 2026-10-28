package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	wssOriginModel   = "wss-origin-a"
	wssBillingModel  = "wss-billing-b"
	wssExpectedQuota = 96
)

func configureWSSBillingIdentityRatios(t *testing.T) {
	t.Helper()
	require.NoError(t, model.InitLogDB())
	require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))

	modelRatioJSON := ratio_setting.ModelRatio2JSONString()
	completionRatioJSON := ratio_setting.CompletionRatio2JSONString()
	audioRatioJSON := ratio_setting.AudioRatio2JSONString()
	audioCompletionRatioJSON := ratio_setting.AudioCompletionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(modelRatioJSON))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(completionRatioJSON))
		require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(audioRatioJSON))
		require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(audioCompletionRatioJSON))
	})

	modelRatios := ratio_setting.GetModelRatioCopy()
	modelRatios[wssOriginModel] = 1
	modelRatios[wssBillingModel] = 2
	completionRatios := ratio_setting.GetCompletionRatioCopy()
	completionRatios[wssOriginModel] = 1
	completionRatios[wssBillingModel] = 3
	audioRatios := ratio_setting.GetAudioRatioCopy()
	audioRatios[wssOriginModel] = 1
	audioRatios[wssBillingModel] = 4
	audioCompletionRatios := ratio_setting.GetAudioCompletionRatioCopy()
	audioCompletionRatios[wssOriginModel] = 1
	audioCompletionRatios[wssBillingModel] = 5

	modelRatiosJSON, err := common.Marshal(modelRatios)
	require.NoError(t, err)
	completionRatiosJSON, err := common.Marshal(completionRatios)
	require.NoError(t, err)
	audioRatiosJSON, err := common.Marshal(audioRatios)
	require.NoError(t, err)
	audioCompletionRatiosJSON, err := common.Marshal(audioCompletionRatios)
	require.NoError(t, err)

	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(string(modelRatiosJSON)))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(string(completionRatiosJSON)))
	require.NoError(t, ratio_setting.UpdateAudioRatioByJSONString(string(audioRatiosJSON)))
	require.NoError(t, ratio_setting.UpdateAudioCompletionRatioByJSONString(string(audioCompletionRatiosJSON)))
}

func wssBillingIdentityUsage() *dto.RealtimeUsage {
	return &dto.RealtimeUsage{
		TotalTokens:  16,
		InputTokens:  13,
		OutputTokens: 3,
		InputTokenDetails: dto.InputTokenDetails{
			TextTokens:  10,
			AudioTokens: 3,
		},
		OutputTokenDetails: dto.OutputTokenDetails{
			TextTokens:  2,
			AudioTokens: 1,
		},
	}
}

func TestPreWssConsumeQuotaUsesBillingModelIdentity(t *testing.T) {
	truncate(t)
	configureWSSBillingIdentityRatios(t)
	seedBonusEligibleModel(t, wssOriginModel, boolPtr(false))
	seedBonusEligibleModel(t, wssBillingModel, boolPtr(true))

	const (
		userID    = 9410
		tokenID   = 9411
		tokenKey  = "wss-pre-billing-token"
		requestID = "wss-pre-billing-request"
	)
	seedBonusUser(t, userID, 200, 0)
	seedToken(t, tokenID, userID, tokenKey, 10_000)
	ctx := testGinContext(t)
	relayInfo := &relaycommon.RelayInfo{
		UserId:           userID,
		ChannelMeta:      &relaycommon.ChannelMeta{},
		TokenId:          tokenID,
		TokenKey:         tokenKey,
		RequestId:        requestID,
		UsingGroup:       "default",
		UserGroup:        "default",
		OriginModelName:  wssOriginModel,
		BillingModelName: wssBillingModel,
		IsPlayground:     true,
		BillingSource:    BillingSourceWallet,
		ForcePreConsume:  true,
	}

	relayInfo.UserQuota = 200
	relayInfo.UserSetting = dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1, QuotaWarningEnabled: boolPtr(false)}
	relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1
	ctx.Set("token_quota", 10_000)
	require.Nil(t, PreConsumeBilling(ctx, 0, relayInfo))

	require.NoError(t, PreWssConsumeQuota(ctx, relayInfo, wssBillingIdentityUsage()))
	assertUserBalances(t, userID, 200-wssExpectedQuota, 0, 0)
	assert.True(t, WalletBonusEligible(relayInfo))
	assert.Equal(t, []ledgerRow{{
		Account:        string(model.AccountBonus),
		Delta:          -wssExpectedQuota,
		Reason:         model.LedgerReasonBillingReserve,
		IdempotencyKey: "billing:reserve:" + requestID + ":96",
	}}, ledgerRowsFor(t, userID))
}

func TestPreWssConsumeQuotaChargesEqualSegmentsExactlyOnceEach(t *testing.T) {
	truncate(t)
	configureWSSBillingIdentityRatios(t)
	seedBonusEligibleModel(t, wssBillingModel, boolPtr(true))

	const (
		userID    = 9430
		tokenID   = 9431
		tokenKey  = "wss-segment-token"
		requestID = "wss-segment-request"
	)
	seedBonusUser(t, userID, 1000, 0)
	seedToken(t, tokenID, userID, tokenKey, 10_000)
	ctx := testGinContext(t)
	relayInfo := &relaycommon.RelayInfo{
		UserId:           userID,
		ChannelMeta:      &relaycommon.ChannelMeta{},
		TokenId:          tokenID,
		TokenKey:         tokenKey,
		RequestId:        requestID,
		UsingGroup:       "default",
		UserGroup:        "default",
		OriginModelName:  wssOriginModel,
		BillingModelName: wssBillingModel,
		BillingSource:    BillingSourceWallet,
		ForcePreConsume:  true,
	}

	relayInfo.UserQuota = 1000
	relayInfo.UserSetting = dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1, QuotaWarningEnabled: boolPtr(false)}
	relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1
	ctx.Set("token_quota", 10_000)
	require.Nil(t, PreConsumeBilling(ctx, 0, relayInfo))

	require.NoError(t, PreWssConsumeQuota(ctx, relayInfo, wssBillingIdentityUsage()))
	require.NoError(t, PreWssConsumeQuota(ctx, relayInfo, wssBillingIdentityUsage()))

	assert.Equal(t, uint64(2), relayInfo.WssPostConsumeSequence)
	assertUserBalances(t, userID, 1000-2*wssExpectedQuota, 0, 0)
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	assert.Equal(t, 10_000-2*wssExpectedQuota, token.RemainQuota)
	assert.Equal(t, 2*wssExpectedQuota, token.UsedQuota)
	assert.Equal(t, []ledgerRow{
		{Account: string(model.AccountBonus), Delta: -wssExpectedQuota, Reason: model.LedgerReasonBillingReserve, IdempotencyKey: "billing:reserve:" + requestID + ":96"},
		{Account: string(model.AccountBonus), Delta: -wssExpectedQuota, Reason: model.LedgerReasonBillingReserve, IdempotencyKey: "billing:reserve:" + requestID + ":192"},
	}, ledgerRowsFor(t, userID))
}

func TestPostWssConsumeQuotaUsesBillingModelIdentity(t *testing.T) {
	truncate(t)
	configureWSSBillingIdentityRatios(t)
	seedBonusEligibleModel(t, wssOriginModel, boolPtr(false))
	seedBonusEligibleModel(t, wssBillingModel, boolPtr(true))

	const (
		userID    = 9420
		channelID = 9421
		requestID = "wss-post-billing-request"
	)
	seedBonusUser(t, userID, 200, 0)
	seedChannel(t, channelID)
	ctx := testGinContext(t)
	ctx.Set(common.RequestIdKey, requestID)
	relayInfo := &relaycommon.RelayInfo{
		UserId:           userID,
		ChannelMeta:      &relaycommon.ChannelMeta{ChannelId: channelID},
		RequestId:        requestID,
		UsingGroup:       "default",
		UserGroup:        "default",
		OriginModelName:  wssOriginModel,
		BillingModelName: wssBillingModel,
		IsPlayground:     true,
		BillingSource:    BillingSourceWallet,
		ForcePreConsume:  true,
		UserQuota:        200,
		UserSetting: dto.UserSetting{
			// Keep this billing-identity fixture out of the asynchronous quota
			// notification path so the package race suite cannot leak a
			// PaymentReturnURL read into the following return-path test.
			QuotaWarningThreshold: -1,
			QuotaWarningEnabled:   boolPtr(false),
		},
		StartTime: time.Now(),
		PriceData: types.PriceData{
			ModelRatio: 1,
			GroupRatioInfo: types.GroupRatioInfo{
				GroupRatio: 1,
			},
		},
	}

	require.Nil(t, PreConsumeBilling(ctx, 0, relayInfo))
	PostWssConsumeQuota(ctx, relayInfo, wssOriginModel, wssBillingIdentityUsage(), nil)

	assertUserBalances(t, userID, 200-wssExpectedQuota, 0, 0)
	assert.True(t, WalletBonusEligible(relayInfo))
	assert.Equal(t, []ledgerRow{{
		Account:        string(model.AccountBonus),
		Delta:          -wssExpectedQuota,
		Reason:         model.LedgerReasonBillingSettle,
		IdempotencyKey: "billing:settle:" + requestID + ":96",
	}}, ledgerRowsFor(t, userID))

	var consumeLog model.Log
	require.NoError(t, model.DB.Where("user_id = ? AND request_id = ?", userID, requestID).First(&consumeLog).Error)
	assert.Equal(t, wssExpectedQuota, consumeLog.Quota)
}
