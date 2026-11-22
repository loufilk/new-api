package model

import (
	"errors"
	"math/big"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func lockAgentCreditAccountTx(tx *gorm.DB, owner int, currency string, exponent int, create bool) (*AgentCreditAccount, error) {
	if owner <= 0 || !validAgentCurrency(currency, exponent) {
		return nil, ErrAgentCreditInvalid
	}
	if create {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentCreditAccount{AgentUserID: owner, Currency: currency, CurrencyExponent: exponent, Version: 1}).Error; err != nil {
			return nil, err
		}
	}
	var a AgentCreditAccount
	if err := lockForUpdate(tx).Where("agent_user_id = ?", owner).First(&a).Error; err != nil {
		return nil, err
	}
	if a.Currency != currency || a.CurrencyExponent != exponent {
		return nil, ErrAgentCreditInvariant
	}
	if err := validateAgentCreditAccount(&a); err != nil {
		return nil, err
	}
	return &a, nil
}
func validateAgentCreditAccount(a *AgentCreditAccount) error {
	if a.AgentUserID <= 0 || a.Version <= 0 || !validAgentCurrency(a.Currency, a.CurrencyExponent) {
		return ErrAgentCreditInvariant
	}
	left := int64(common.MaxWalletQuota)
	for _, v := range []int64{a.ReservedMinor, a.OutstandingMinor, a.WrittenOffMinor} {
		if v < 0 || v > left {
			return ErrAgentCreditInvariant
		}
		left -= v
	}
	return nil
}
func appendAgentCreditLedgerTx(tx *gorm.DB, a *AgentCreditAccount, e AgentCreditLedger) error {
	if err := validateAgentCreditAccount(a); err != nil {
		return err
	}
	// Every operand is JS-safe, so addition cannot wrap int64. Validate after too.
	a.ReservedMinor += e.ReservedDelta
	a.OutstandingMinor += e.OutstandingDelta
	a.WrittenOffMinor += e.WrittenOffDelta
	if err := validateAgentCreditAccount(a); err != nil {
		return err
	}
	a.Version++
	e.AgentUserID = a.AgentUserID
	e.Currency = a.Currency
	e.CurrencyExponent = a.CurrencyExponent
	e.ReservedAfter = a.ReservedMinor
	e.OutstandingAfter = a.OutstandingMinor
	e.WrittenOffAfter = a.WrittenOffMinor
	if err := tx.Create(&e).Error; err != nil {
		return err
	}
	return tx.Model(a).Select("reserved_minor", "outstanding_minor", "written_off_minor", "version").Updates(a).Error
}
func requireAgentCreditRiskTx(tx *gorm.DB, a *AgentCreditAccount, additional int64) error {
	if err := validateAgentCreditAccount(a); err != nil {
		return err
	}
	var limit AgentCreditLimitVersion
	if a.LimitVersionID <= 0 {
		return ErrAgentCreditRisk
	}
	if err := tx.First(&limit, a.LimitVersionID).Error; err != nil {
		return err
	}
	exposure := a.ReservedMinor + a.OutstandingMinor + a.WrittenOffMinor
	if limit.AgentUserID != a.AgentUserID || limit.Currency != a.Currency || limit.CurrencyExponent != a.CurrencyExponent || limit.Frozen || limit.ExpiresAt <= common.GetTimestamp() || limit.AmountMinor < 0 || limit.AmountMinor > int64(common.MaxWalletQuota) || additional < 0 || exposure > limit.AmountMinor || additional > limit.AmountMinor-exposure || a.WrittenOffMinor > 0 {
		return ErrAgentCreditRisk
	}
	var valuationExceptions int64
	if err := tx.Model(&AgentFundBatch{}).Where("agent_user_id = ? AND source_kind = ?", a.AgentUserID, AgentFundCredit).Where("id NOT IN (?)", tx.Model(&AgentFundEconomics{}).Select("batch_id").Where("agent_user_id = ? AND source_kind = ? AND scheme = ?", a.AgentUserID, AgentFundCredit, AgentFundEconomicCumulative)).Count(&valuationExceptions).Error; err != nil {
		return err
	}
	if valuationExceptions > 0 {
		return ErrAgentCreditRisk
	}
	var overdue int64
	if err := tx.Model(&AgentReceivable{}).Where("agent_user_id = ? AND due_at <= ? AND principal_minor > paid_minor + offset_minor + written_off_minor + corrected_minor", a.AgentUserID, common.GetTimestamp()).Count(&overdue).Error; err != nil {
		return err
	}
	if overdue > 0 {
		return ErrAgentCreditRisk
	}
	return nil
}
func agentCreditMinor(raw string, positive bool) (int64, error) {
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 || (positive && v == 0) || v > int64(common.MaxWalletQuota) || strconv.FormatInt(v, 10) != raw {
		return 0, ErrAgentCreditInvalid
	}
	return v, nil
}
func agentCreditDrawPrice(p *AgentPolicy, quota int) (int64, error) {
	if !p.CreditEnabled || !p.RiskPolicyApproved || !p.CreditTermsApproved {
		return 0, ErrAgentFeatureDisabled
	}
	if ValidateAgentCreditPolicy(p) != nil || quota <= 0 || quota > common.MaxWalletQuota {
		return 0, ErrAgentCreditInvalid
	}
	n := new(big.Int).Mul(big.NewInt(int64(quota)), big.NewInt(p.CreditRateBPS))
	n.Mul(n, big.NewInt(p.CreditQuotaDenominator))
	d := new(big.Int).Mul(big.NewInt(10000), big.NewInt(p.CreditQuotaNumerator))
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, d, r)
	if r.Sign() > 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Sign() <= 0 || q.Int64() > int64(common.MaxWalletQuota) {
		return 0, ErrWalletQuotaLimitExceeded
	}
	return q.Int64(), nil
}
func agentCreditAllocationsTx(tx *gorm.DB, owner int, currency string, exponent int, amount int64, allocations []AgentReceivableAllocationInput) ([]AgentReceivable, error) {
	if len(allocations) == 0 || len(allocations) > 100 || amount <= 0 {
		return nil, ErrAgentCreditInvalid
	}
	rows := make([]AgentReceivable, 0, len(allocations))
	remaining := amount
	previous := int64(0)
	// Sorted IDs are mandatory so request bytes, review display and lock order agree.
	for _, a := range allocations {
		value, err := agentCreditMinor(a.AmountMinor, true)
		if err != nil {
			return nil, err
		}
		if a.ReceivableID <= previous || value > remaining {
			return nil, ErrAgentCreditInvalid
		}
		previous = a.ReceivableID
		var row AgentReceivable
		if err := lockForUpdate(tx).Where("id = ? AND agent_user_id = ?", a.ReceivableID, owner).First(&row).Error; err != nil {
			return nil, err
		}
		left, err := agentReceivableRemaining(&row)
		if err != nil {
			return nil, err
		}
		if row.Currency != currency || row.CurrencyExponent != exponent || value > left {
			return nil, ErrAgentCreditState
		}
		rows = append(rows, row)
		remaining -= value
	}
	if remaining != 0 {
		return nil, ErrAgentCreditInvalid
	}
	return rows, nil
}
func agentCreditReadAccountTx(tx *gorm.DB, owner int) (*AgentCreditAccount, error) {
	var a AgentCreditAccount
	err := tx.Where("agent_user_id = ?", owner).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, validateAgentCreditAccount(&a)
}

func agentCreditSourceSelectableTx(tx *gorm.DB, b *AgentFundBatch, state string) error {
	p, err := ReadCurrentAgentPolicyTx(tx)
	if err != nil {
		return err
	}
	if !p.CreditEnabled || ValidateAgentCreditPolicy(p) != nil || (state == AgentSliceSpent && !p.CreditSelfUseEnabled) {
		return ErrAgentFeatureDisabled
	}
	a, err := lockAgentCreditAccountTx(tx, b.AgentUserID, b.Currency, b.CurrencyExponent, false)
	if err != nil {
		return err
	}
	return requireAgentCreditRiskTx(tx, a, 0)
}
