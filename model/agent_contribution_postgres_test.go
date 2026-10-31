package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentContributionsVerifiedReceiptsRefundReplayCurrenciesPrivacyPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("display_name", "Invited synthetic customer").Error)
	require.NoError(t, DB.Create(&AgentInvitation{UserId: f.buyer.Id, InviterId: f.inviter.Id, InviterUsername: f.inviter.Username, InviterRole: common.RoleCommonUser, Source: InvitationSourcePassword, BonusReason: "none"}).Error)
	order := commissionPaidTopup(t, f, "contribution-live-usd", 10000)
	facts, ok := NewAgentVerifiedPaymentFacts(PaymentProviderStripe, "pi_contribution-live-usd", "synthetic-merchant", "live", "USD", "10000", true)
	require.True(t, ok)
	require.NoError(t, Recharge(order.TradeNo, "synthetic-customer", "127.0.0.1", facts))
	commissionDrain(t)
	commissionRefund(t, f, order, "contribution-refund-one", 3000)
	commissionRefund(t, f, order, "contribution-refund-replay-cumulative", 3000)
	commissionDrain(t)
	for i, item := range []struct {
		currency, purpose, environment string
		paid                           int64
	}{{"JPY", "topup", "live", 800}, {"USD", "purchase", "live", 9999}, {"USD", "deposit", "live", 9999}, {"USD", "topup", "test", 9999}} {
		exponent, ok := AgentCurrencyExponent(item.currency)
		require.True(t, ok)
		receipt := AgentPaymentReceipt{Provider: "bank_transfer", ProviderReference: fmt.Sprintf("synthetic-sensitive-bank-reference-%d", i), MerchantScope: "synthetic-bank-scope", Environment: item.environment, Purpose: item.purpose, LocalOrderKey: fmt.Sprintf("contribution-order-%d", i), OwnerUserID: f.buyer.Id, Currency: item.currency, CurrencyExponent: exponent, PaidMinor: item.paid, PaidAt: common.GetTimestamp(), VerifiedAt: common.GetTimestamp()}
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &receipt); return err }))
		var stored AgentPaymentReceipt
		require.NoError(t, DB.Where("provider_reference = ?", receipt.ProviderReference).First(&stored).Error)
		assert.Equal(t, receipt.CurrencyExponent, stored.CurrencyExponent)
		assert.Equal(t, receipt.CounterpartyDigest, stored.CounterpartyDigest)
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &receipt); return err }))
	}
	legacy := User{Username: "legacy-contribution-customer", DisplayName: "Legacy customer without receipts", AffCode: "legacy-contribution-customer", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, InviterId: f.inviter.Id, Quota: 999999, QuotaPaid: 999999}
	require.NoError(t, DB.Create(&legacy).Error)
	hidden := User{Username: "inconsistent-referral", AffCode: "inconsistent-referral", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, InviterId: f.inviter.Id}
	require.NoError(t, DB.Create(&hidden).Error)
	require.NoError(t, DB.Create(&AgentInvitation{UserId: hidden.Id, InviterId: f.root.Id, InviterUsername: f.root.Username, InviterRole: common.RoleRootUser, Source: InvitationSourcePassword, BonusReason: "none"}).Error)
	report, err := GetAgentContributions(f.identity, f.inviter.Id, 1, 1, false)
	require.NoError(t, err)
	assert.EqualValues(t, 2, report.Total)
	require.Len(t, report.Items, 1)
	assert.Equal(t, f.buyer.Id, report.Items[0].CustomerUserID)
	assert.Equal(t, "Invited synthetic customer", report.Items[0].DisplayName)
	require.Len(t, report.Items[0].Currencies, 2)
	jpy, usd := report.Items[0].Currencies[0], report.Items[0].Currencies[1]
	assert.Equal(t, AgentContributionCurrency{Currency: "JPY", CurrencyExponent: 0, ConfirmedTopupPrincipalMinor: "800", RefundedPrincipalMinor: "0", NetTopupPrincipalMinor: "800", CommissionEarnedMinor: "0", CommissionReversedMinor: "0", CommissionNetMinor: "0"}, jpy)
	assert.Equal(t, AgentContributionCurrency{Currency: "USD", CurrencyExponent: 2, ConfirmedTopupPrincipalMinor: "10000", RefundedPrincipalMinor: "3000", NetTopupPrincipalMinor: "7000", CommissionEarnedMinor: "500", CommissionReversedMinor: "150", CommissionNetMinor: "350"}, usd)
	assert.False(t, report.Coverage.HistoricalComplete)
	assert.Equal(t, "verified_live_topup_receipts_only", report.Coverage.Scope)
	assert.Positive(t, report.Coverage.EarliestVerifiedReceiptAt)
	assert.Equal(t, f.policy.CommissionCutoverAt, report.Coverage.CommissionCutoverAt)
	encoded, err := common.Marshal(report)
	require.NoError(t, err)
	for _, secret := range []string{"synthetic-sensitive-bank-reference", "pi_contribution-live-usd", "synthetic-bank-scope", "contribution-order-", "refund-one"} {
		assert.NotContains(t, string(encoded), secret)
	}
	second, err := GetAgentContributions(f.identity, f.inviter.Id, 2, 1, false)
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	assert.Equal(t, legacy.Id, second.Items[0].CustomerUserID)
	assert.Empty(t, second.Items[0].Currencies, "legacy paid wallet is not reconstructed payment principal")
	finance, err := GetAgentContributions(f.rootIdentity, f.inviter.Id, 1, 1, true)
	require.NoError(t, err)
	assert.Equal(t, report.Items, finance.Items)
	_, err = GetAgentContributions(f.identity, f.root.Id, 1, 1, false)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	_, err = GetAgentContributions(f.identity, f.root.Id, 1, 1, true)
	require.ErrorIs(t, err, ErrAgentProfilePermission)
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", f.identity.SessionID).Update("status", UserSessionStatusRevoked).Error)
	_, err = GetAgentContributions(f.identity, f.inviter.Id, 1, 1, false)
	require.ErrorIs(t, err, ErrUserSessionInactive)
}
func TestAgentContributionsLargeExactTotalsDoNotRoundOrInventCommissionPostgres(t *testing.T) {
	f := setupAgentGovernancePostgres(t)
	for i := range 2 {
		receipt := AgentPaymentReceipt{Provider: "bank_transfer", ProviderReference: fmt.Sprintf("synthetic-exact-principal-%d", i), MerchantScope: "synthetic-bank-scope", Environment: "live", Purpose: "topup", LocalOrderKey: fmt.Sprintf("contribution-exact-%d", i), OwnerUserID: f.buyer.Id, Currency: "USD", CurrencyExponent: 2, PaidMinor: int64(common.MaxWalletQuota), PaidAt: common.GetTimestamp(), VerifiedAt: common.GetTimestamp()}
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { _, err := ClaimAgentPaymentReceiptTx(tx, &receipt); return err }))
	}
	// An old award without a corresponding authoritative receipt is excluded.
	old := AgentCommissionAward{EventID: 991, UserID: f.inviter.Id, SourceUserID: f.buyer.Id, ReferenceID: "unproven-historical-award", Provider: "stripe", PaymentReference: "pi_unproven_historical", Currency: "USD", CurrencyExponent: 2, PaidMinor: 10000, RewardMinor: 500, PendingMinor: 500, PolicySnapshot: "{}", Version: 1}
	require.NoError(t, DB.Create(&old).Error)
	report, err := GetAgentContributions(f.identity, f.inviter.Id, 1, 100, false)
	require.NoError(t, err)
	require.Len(t, report.Items, 1)
	require.Len(t, report.Items[0].Currencies, 1)
	assert.Equal(t, fmt.Sprint(int64(common.MaxWalletQuota)*2), report.Items[0].Currencies[0].ConfirmedTopupPrincipalMinor)
	assert.Equal(t, "0", report.Items[0].Currencies[0].CommissionEarnedMinor)
	encoded, err := common.Marshal(report)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(encoded), `"confirmed_topup_principal_minor":"`))
}
