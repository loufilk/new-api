package service

// billing_usage_ledger_test.go 锁定 authoritative BillingUsage 链路的资金
// 一致性：upstream usage → canonical BillingUsage → effectiveBillingUsage →
// official quota → SettleBilling → ChuanAPI wallet ledger。
//
// 断言钱包账本的净扣减与官方 quota 完全相等，且计费用量取自 canonical
// BillingUsage（而非顶层 usage 字段），杜绝自算 usage 绕过官方口径。

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostTextConsumeQuotaLedgerMatchesOfficialQuotaFromBillingUsage(t *testing.T) {
	truncate(t)

	const userID = 930
	const initialQuota = 10_000
	seedUser(t, userID, initialQuota)

	ctx := testGinContext(t)
	ctx.Set(common.RequestIdKey, "billing-usage-ledger-1")
	relayInfo := &relaycommon.RelayInfo{
		UserId:          userID,
		UserQuota:       initialQuota,
		UserSetting:     dto.UserSetting{QuotaWarningEnabled: boolPtr(false)},
		RequestId:       "billing-usage-ledger-1",
		OriginModelName: "billing-usage-model",
		IsPlayground:    true,
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: 1},
	}
	relayInfo.PriceData.ModelRatio = 2
	relayInfo.PriceData.CompletionRatio = 4
	relayInfo.PriceData.GroupRatioInfo.GroupRatio = 1

	// 无预扣费（预扣 0），结算差额 = 官方 quota。
	require.Nil(t, PreConsumeBilling(ctx, 0, relayInfo))
	require.NotNil(t, relayInfo.Billing)

	// 顶层 usage 是误导值；结算必须以 canonical BillingUsage 为准。
	usage := &dto.Usage{
		PromptTokens:     999_999,
		CompletionTokens: 999_999,
		BillingUsage: &dto.BillingUsage{
			Source:   dto.BillingUsageSourceOAIResponses,
			Semantic: dto.BillingUsageSemanticOpenAI,
			OpenAIUsage: &dto.Usage{
				InputTokens:  1_000,
				OutputTokens: 100,
			},
		},
	}

	PostTextConsumeQuota(ctx, relayInfo, usage, nil)

	// 官方 quota：(1000 prompt + 100 completion × 4) × modelRatio 2 × group 1 = 2800。
	const officialQuota = 2800

	u := getUserBalances(t, userID)
	assert.Equal(t, initialQuota-officialQuota, u.Quota, "用户余额扣减必须等于官方 quota")
	assertQuotaInvariant(t, u)

	var ledgerTotal int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).
		Where("user_id = ?", userID).
		Select("COALESCE(SUM(delta), 0)").
		Scan(&ledgerTotal).Error)
	assert.Equal(t, -int64(officialQuota), ledgerTotal,
		"钱包账本净扣减必须与官方 quota 完全相等")

	var logQuota int
	require.NoError(t, model.DB.Table("logs").
		Where("user_id = ? AND request_id = ?", userID, relayInfo.RequestId).
		Select("COALESCE(MAX(quota), 0)").
		Scan(&logQuota).Error)
	assert.Equal(t, officialQuota, logQuota, "消费日志记录的官方 quota 必须与账本一致")
}
