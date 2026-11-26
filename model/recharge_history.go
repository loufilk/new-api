package model

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type RechargeHistoryUSDT struct {
	Network         string `json:"network"`
	Status          string `json:"status"`
	QuoteUSDT       string `json:"quote_usdt,omitempty"`
	WalletCreditCNY string `json:"wallet_credit_cny,omitempty"`
}

// RechargeHistoryFiat is a read-only projection, distinct from the legacy
// TopUp API. Order IDs and timestamps live on the shared envelope.
type RechargeHistoryFiat struct {
	PaymentMethod                   string  `json:"payment_method"`
	PaymentProvider                 string  `json:"payment_provider,omitempty"`
	PaidCurrency                    string  `json:"paid_currency,omitempty"`
	PaidCurrencyExponent            int     `json:"paid_currency_exponent"`
	PaidAmountMinor                 int64   `json:"paid_amount_minor,string"`
	Amount                          int64   `json:"amount"`
	Money                           float64 `json:"money"`
	Status                          string  `json:"status"`
	NewUserBenefit                  bool    `json:"new_user_benefit,omitempty"`
	NewUserBenefitRewardStatus      string  `json:"new_user_benefit_reward_status,omitempty"`
	NewUserBenefitCreditedQuota     int     `json:"new_user_benefit_credited_quota,omitempty"`
	NewUserBenefitCreditedAmountCNY string  `json:"new_user_benefit_credited_amount_cny,omitempty"`
	NewUserBenefitCreditQuota       int     `json:"new_user_benefit_credit_quota,omitempty"`
	NewUserBenefitCreditAmountCNY   string  `json:"new_user_benefit_credit_amount_cny,omitempty"`
	AgentSimpleTopUp                bool    `json:"agent_simple_topup,omitempty"`
}

// Purchases and on-chain orders are distinct sources. Never synthesize orders
// from ledger entries: a single successful payment can credit several balances.
type RechargeHistoryItem struct {
	OrderNo   string               `json:"order_no"`
	CreatedAt int64                `json:"created_at"`
	Fiat      *RechargeHistoryFiat `json:"fiat,omitempty"`
	USDT      *RechargeHistoryUSDT `json:"usdt,omitempty"`
}

type RechargeHistoryPage struct {
	Items   []RechargeHistoryItem `json:"items"`
	Total   int64                 `json:"total"`
	Methods []string              `json:"methods"`
}

// GetRechargeHistory lists only the authenticated owner's orders. The legacy
// fiat history's 30-day boundary does not apply to the owner's USDT orders.
func GetRechargeHistory(ctx context.Context, userID int, orderNo, method string, page *common.PageInfo) (RechargeHistoryPage, error) {
	result := RechargeHistoryPage{Items: []RechargeHistoryItem{}, Methods: []string{}}
	if userID <= 0 {
		return result, errors.New("wallet owner is required")
	}
	// method describes the actual tender, not the payment processor. Prefix
	// fiat values to leave USDT unambiguous even for custom legacy methods.
	fiatMethod, fiatOnly := strings.CutPrefix(method, "fiat:")
	cutoff, now := topUpQueryCutoff(), common.GetTimestamp()
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		fiat := tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userID, cutoff)
		usdt := tx.Model(&USDTTopUpOrder{}).Where("user_id = ?", userID)
		// List historical methods independently of active filters or checkout
		// configuration, so disabled payment channels are still searchable.
		var methods []string
		if err := fiat.Session(&gorm.Session{}).Distinct("payment_method").Order("payment_method").Pluck("payment_method", &methods).Error; err != nil {
			return err
		}
		for _, m := range methods {
			result.Methods = append(result.Methods, "fiat:"+m)
		}
		var hasUSDT []string
		if err := usdt.Session(&gorm.Session{}).Limit(1).Pluck("id", &hasUSDT).Error; err != nil {
			return err
		}
		if len(hasUSDT) > 0 {
			result.Methods = append(result.Methods, "usdt")
		}

		fiatKeys := fiat.Session(&gorm.Session{}).Select("'fiat' AS source, trade_no AS order_no, create_time AS created_at")
		usdtKeys := usdt.Session(&gorm.Session{}).Select("'usdt' AS source, id AS order_no, created_at")
		if orderNo != "" {
			fiatKeys = fiatKeys.Where("trade_no = ?", orderNo)
			usdtKeys = usdtKeys.Where("id = ?", orderNo)
		}
		var orders *gorm.DB
		switch {
		case fiatOnly:
			orders = fiatKeys.Where("payment_method = ?", fiatMethod)
		case method == "usdt":
			orders = usdtKeys
		case method == "":
			orders = tx.Raw("? UNION ALL ?", fiatKeys, usdtKeys)
		default:
			return nil
		}
		history := tx.Table("(?) AS recharge_history", orders)
		if err := history.Session(&gorm.Session{}).Count(&result.Total).Error; err != nil {
			return err
		}
		type historyKey struct {
			Source    string
			OrderNo   string
			CreatedAt int64
		}
		var keys []historyKey
		if err := history.Order("created_at DESC, source DESC, order_no DESC").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Scan(&keys).Error; err != nil {
			return err
		}
		var fiatIDs, usdtIDs []string
		for _, key := range keys {
			if key.Source == "fiat" {
				fiatIDs = append(fiatIDs, key.OrderNo)
			} else {
				usdtIDs = append(usdtIDs, key.OrderNo)
			}
		}
		fiatRows := make(map[string]*RechargeHistoryFiat, len(fiatIDs))
		if len(fiatIDs) > 0 {
			var rows []*TopUp
			if err := fiat.Where("trade_no IN ?", fiatIDs).Find(&rows).Error; err != nil {
				return err
			}
			if err := hydrateNewUserBenefitTopUpsTx(tx, rows); err != nil {
				return err
			}
			for _, row := range rows {
				fiatRows[row.TradeNo] = &RechargeHistoryFiat{
					PaymentMethod: row.PaymentMethod, PaymentProvider: row.PaymentProvider,
					PaidCurrency: row.PaidCurrency, PaidCurrencyExponent: row.PaidCurrencyExponent, PaidAmountMinor: row.PaidAmountMinor,
					Amount: row.Amount, Money: row.Money, Status: row.Status,
					NewUserBenefit: row.NewUserBenefit, NewUserBenefitRewardStatus: row.NewUserBenefitRewardStatus,
					NewUserBenefitCreditedQuota: row.NewUserBenefitCreditedQuota, NewUserBenefitCreditedAmountCNY: row.NewUserBenefitCreditedAmountCNY,
					NewUserBenefitCreditQuota: row.NewUserBenefitCreditQuota, NewUserBenefitCreditAmountCNY: row.NewUserBenefitCreditAmountCNY,
					AgentSimpleTopUp: row.AgentSimpleTopUp,
				}
			}
		}
		usdtRows := make(map[string]*RechargeHistoryUSDT, len(usdtIDs))
		if len(usdtIDs) > 0 {
			var rows []USDTTopUpOrder
			if err := usdt.Select("id, network, status, usdt_amount, amount_minor, bonus_minor, created_at, expires_at").
				Where("id IN ?", usdtIDs).Find(&rows).Error; err != nil {
				return err
			}
			for _, row := range rows {
				item := &RechargeHistoryUSDT{Network: row.Network, Status: row.DisplayStatus(now), QuoteUSDT: row.USDTAmount}
				if row.Status == USDTPaid {
					item.WalletCreditCNY = decimal.New(row.AmountMinor, -2).Add(decimal.New(row.BonusMinor, -2)).StringFixed(2)
				}
				usdtRows[row.ID] = item
			}
		}
		for _, key := range keys {
			item := RechargeHistoryItem{OrderNo: key.OrderNo, CreatedAt: key.CreatedAt}
			// Two tables have independent ID spaces. A same-text ID in
			// both tables is two distinct orders, not a cross-source join.
			if key.Source == "fiat" {
				item.Fiat = fiatRows[key.OrderNo]
			} else {
				item.USDT = usdtRows[key.OrderNo]
			}
			result.Items = append(result.Items, item)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}
