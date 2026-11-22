package model

import (
	"database/sql"
	"math/big"
	"sort"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentContributionCurrency struct {
	Currency                     string `json:"currency"`
	CurrencyExponent             int    `json:"currency_exponent"`
	ConfirmedTopupPrincipalMinor string `json:"confirmed_topup_principal_minor"`
	RefundedPrincipalMinor       string `json:"refunded_principal_minor"`
	NetTopupPrincipalMinor       string `json:"net_topup_principal_minor"`
	CommissionEarnedMinor        string `json:"commission_earned_minor"`
	CommissionReversedMinor      string `json:"commission_reversed_minor"`
	CommissionNetMinor           string `json:"commission_net_minor"`
}
type AgentContributionCustomer struct {
	CustomerUserID int                         `json:"customer_user_id"`
	DisplayName    string                      `json:"display_name"`
	Currencies     []AgentContributionCurrency `json:"currencies"`
}
type AgentContributionCoverage struct {
	Scope                     string `json:"scope"`
	HistoricalComplete        bool   `json:"historical_complete"`
	CommissionCutoverAt       int64  `json:"commission_cutover_at"`
	EarliestVerifiedReceiptAt int64  `json:"earliest_verified_receipt_at"`
	Note                      string `json:"note"`
}
type AgentContributionReport struct {
	Items      []AgentContributionCustomer `json:"items"`
	Total      int64                       `json:"total"`
	Page       int                         `json:"page"`
	PageSize   int                         `json:"page_size"`
	Coverage   AgentContributionCoverage   `json:"coverage"`
	ObservedAt int64                       `json:"observed_at"`
}

// This report counts only the payment-purpose registry's unique, confirmed live
// top-up receipts. Internal transfers, campaigns, purchases and legacy wallet
// balance guesses are not payment evidence. Commission totals require the same
// authoritative receipt; no currency is converted or merged with another.
func GetAgentContributions(identity AuthSessionIdentity, ownerID, page, size int, finance bool) (*AgentContributionReport, error) {
	if ownerID <= 0 || page < 1 || page > 1_000_000 || size < 1 || size > 100 {
		return nil, ErrAgentGovernanceInvalid
	}
	result := AgentContributionReport{Items: []AgentContributionCustomer{}, Page: page, PageSize: size, ObservedAt: common.GetTimestamp(), Coverage: AgentContributionCoverage{Scope: "verified_live_topup_receipts_only", HistoricalComplete: false, Note: "Only recorded verified live top-up receipts and linked commission awards are included; historical payments without receipts are unavailable, not zero."}}
	// Repeatable read keeps principal, refund and award aggregates on one DB
	// snapshot. The actor/session is nevertheless rechecked under durable locks.
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if _, _, err := readAgentGovernanceAuthorityTx(tx, identity, ownerID, finance, true); err != nil {
			return err
		}
		result.Coverage.CommissionCutoverAt = p.CommissionCutoverAt
		// The immutable attribution wins if present; never expose a customer whose
		// persisted inviter binding disagrees with its registration-time record.
		customers := tx.Model(&User{}).Where("inviter_id = ?", ownerID).Where("NOT EXISTS (SELECT 1 FROM agent_invitations ai WHERE ai.user_id = users.id AND ai.inviter_id <> ?)", ownerID)
		if err := customers.Count(&result.Total).Error; err != nil {
			return err
		}
		var users []User
		if err := customers.Select("id", "display_name", "username").Order("id").Offset((page - 1) * size).Limit(size).Find(&users).Error; err != nil {
			return err
		}
		var earliest int64
		if err := tx.Model(&AgentPaymentReceipt{}).Select("COALESCE(MIN(verified_at), 0)").Where("purpose = ? AND environment = ? AND verified_at > 0 AND owner_user_id IN (?)", "topup", "live", customers.Select("id")).Scan(&earliest).Error; err != nil {
			return err
		}
		result.Coverage.EarliestVerifiedReceiptAt = earliest
		if len(users) == 0 {
			return nil
		}
		ids := make([]int, 0, len(users))
		positions := map[int]int{}
		for _, user := range users {
			name := user.DisplayName
			if name == "" {
				name = user.Username
			}
			positions[user.Id] = len(result.Items)
			ids = append(ids, user.Id)
			result.Items = append(result.Items, AgentContributionCustomer{CustomerUserID: user.Id, DisplayName: name, Currencies: []AgentContributionCurrency{}})
		}
		refunds := tx.Model(&AgentCommissionRefundFact{}).Select("top_up_reference_id, provider, environment, merchant_scope, payment_reference, currency, currency_exponent, MAX(cumulative_refunded_minor) AS refunded_minor").Group("top_up_reference_id, provider, environment, merchant_scope, payment_reference, currency, currency_exponent")
		type aggregate struct {
			OwnerUserID      int
			Currency         string
			CurrencyExponent int
			Principal        string
			Refunded         string
			Earned           string
			Reversed         string
		}
		var principal []aggregate
		err = tx.Table("agent_payment_receipts AS r").Select("r.owner_user_id, r.currency, r.currency_exponent, SUM(r.paid_minor) AS principal, SUM(COALESCE(f.refunded_minor, 0)) AS refunded").Joins("LEFT JOIN (?) AS f ON f.top_up_reference_id = r.local_order_key AND f.provider = r.provider AND f.environment = r.environment AND f.merchant_scope = r.merchant_scope AND f.payment_reference = r.provider_reference AND f.currency = r.currency AND f.currency_exponent = r.currency_exponent", refunds).Where("r.owner_user_id IN ? AND r.purpose = ? AND r.environment = ? AND r.verified_at > 0", ids, "topup", "live").Group("r.owner_user_id, r.currency, r.currency_exponent").Scan(&principal).Error
		if err != nil {
			return err
		}
		var awards []aggregate
		err = tx.Table("agent_commission_awards AS a").Select("a.source_user_id AS owner_user_id, a.currency, a.currency_exponent, SUM(a.reward_minor) AS earned, SUM(a.reversed_minor) AS reversed").Joins("JOIN agent_payment_receipts r ON r.local_order_key = a.reference_id AND r.owner_user_id = a.source_user_id AND r.provider = a.provider AND r.provider_reference = a.payment_reference AND r.currency = a.currency AND r.currency_exponent = a.currency_exponent AND r.paid_minor = a.paid_minor").Where("a.user_id = ? AND a.source_user_id IN ? AND r.purpose = ? AND r.environment = ? AND r.verified_at > 0", ownerID, ids, "topup", "live").Group("a.source_user_id, a.currency, a.currency_exponent").Scan(&awards).Error
		if err != nil {
			return err
		}
		type key struct {
			user     int
			currency string
			exponent int
		}
		byCurrency := map[key]*AgentContributionCurrency{}
		for _, row := range principal {
			net, err := agentContributionDifference(row.Principal, row.Refunded)
			if err != nil {
				return err
			}
			byCurrency[key{row.OwnerUserID, row.Currency, row.CurrencyExponent}] = &AgentContributionCurrency{Currency: row.Currency, CurrencyExponent: row.CurrencyExponent, ConfirmedTopupPrincipalMinor: row.Principal, RefundedPrincipalMinor: row.Refunded, NetTopupPrincipalMinor: net, CommissionEarnedMinor: "0", CommissionReversedMinor: "0", CommissionNetMinor: "0"}
		}
		for _, row := range awards {
			currency := byCurrency[key{row.OwnerUserID, row.Currency, row.CurrencyExponent}]
			if currency == nil {
				return ErrAgentCommissionInvariant
			}
			net, err := agentContributionDifference(row.Earned, row.Reversed)
			if err != nil {
				return err
			}
			currency.CommissionEarnedMinor = row.Earned
			currency.CommissionReversedMinor = row.Reversed
			currency.CommissionNetMinor = net
		}
		for k, currency := range byCurrency {
			position, ok := positions[k.user]
			if !ok {
				return ErrAgentCommissionInvariant
			}
			result.Items[position].Currencies = append(result.Items[position].Currencies, *currency)
		}
		for i := range result.Items {
			sort.Slice(result.Items[i].Currencies, func(a, b int) bool {
				return result.Items[i].Currencies[a].Currency < result.Items[i].Currencies[b].Currency
			})
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	return &result, err
}
func agentContributionDifference(gross, reversed string) (string, error) {
	a, ok := new(big.Int).SetString(gross, 10)
	if !ok || a.Sign() < 0 {
		return "", ErrAgentCommissionInvariant
	}
	b, ok := new(big.Int).SetString(reversed, 10)
	if !ok || b.Sign() < 0 || b.Cmp(a) > 0 {
		return "", ErrAgentCommissionInvariant
	}
	return a.Sub(a, b).String(), nil
}
