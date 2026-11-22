package model

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Run after legacy source migration. Every baseline records only observed
// historical money. Incompatible history remains an explicit exception; no
// wallet, payment, AR, source entitlement or credit limit is rewritten.
func MigrateAgentFundEconomicsWithDB(db *gorm.DB) error {
	var batches []AgentFundBatch
	if err := db.Where("source_kind IN ?", []string{AgentFundPurchase, AgentFundCredit}).Order("id").Find(&batches).Error; err != nil {
		return err
	}
	for _, b := range batches {
		err := db.Transaction(func(tx *gorm.DB) error {
			if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
				return err
			}
			if _, err := lockAgentRedemptionAccount(tx, b.AgentUserID, false); err != nil {
				return err
			}
			_, err := ensureAgentFundEconomicsTx(tx, &b)
			return err
		})
		if errors.Is(err, ErrAgentFundInvariant) || errors.Is(err, ErrAgentCreditInvariant) || errors.Is(err, ErrAgentQuotaInvariant) || errors.Is(err, gorm.ErrRecordNotFound) {
			snapshot, marshalErr := common.Marshal(b)
			if marshalErr != nil {
				return marshalErr
			}
			if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&AgentFundEconomicAnomaly{EventKey: fmt.Sprintf("economics:invalid:%d", b.ID), BatchID: b.ID, AgentUserID: b.AgentUserID, Code: "historical_source_invalid", Snapshot: string(snapshot)}).Error; err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Read-only audit for callers already holding the policy fence and reader
// authorization. Unlike initialization this never creates or repairs a carry.
func ReconcileAgentFundEconomicsTx(tx *gorm.DB, owner int) ([]AgentCreditLiabilityFinding, error) {
	out := []AgentCreditLiabilityFinding{}
	add := func(code string, id int64) {
		out = append(out, AgentCreditLiabilityFinding{Code: code, ResourceID: strconv.FormatInt(id, 10)})
	}
	var batches []AgentFundBatch
	if err := tx.Where("agent_user_id = ? AND source_kind IN ?", owner, []string{AgentFundPurchase, AgentFundCredit}).Order("id").Find(&batches).Error; err != nil {
		return nil, err
	}
	for _, b := range batches {
		var e AgentFundEconomics
		if err := tx.Where("batch_id = ?", b.ID).First(&e).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			add("fund_economics_missing", int64(b.ID))
			continue
		}
		if validateAgentFundEconomics(&e) != nil || e.AgentUserID != owner || e.SourceKind != b.SourceKind || e.OriginalQuota != b.OriginalQuota || strconv.FormatInt(e.PrincipalMinor, 10) != b.OriginalPaidMinor {
			add("fund_economics_invalid", int64(b.ID))
		}
		if e.Scheme != AgentFundEconomicCumulative {
			add("fund_economics_legacy_exception", int64(b.ID))
		}
		var slices []AgentFundSlice
		if err := tx.Where("batch_id = ?", b.ID).Order("start_quota").Find(&slices).Error; err != nil {
			return nil, err
		}
		start, spent, released := 0, 0, 0
		for _, s := range slices {
			if s.AgentUserID != owner || s.StartQuota != start || s.Quota <= 0 || s.Quota > b.OriginalQuota-start {
				add("fund_economics_source_invalid", int64(s.ID))
				break
			}
			start += s.Quota
			switch s.State {
			case AgentSliceSpent:
				spent += s.Quota
			case AgentSliceRefunded, AgentSliceReclaimed:
				released += s.Quota
			case AgentSliceAvailable, AgentSliceReserved, AgentSliceRefundFrozen:
			default:
				add("fund_economics_source_state_invalid", int64(s.ID))
			}
		}
		if start != b.OriginalQuota || spent != e.SpentQuota || released != e.ReleasedQuota {
			add("fund_economics_source_mismatch", int64(b.ID))
		}
		var rows []AgentFundEconomicLedger
		if err := tx.Where("batch_id = ?", b.ID).Order("id").Find(&rows).Error; err != nil {
			return nil, err
		}
		s, r := 0, 0
		var c, l int64
		for _, row := range rows {
			beforeC, beforeL, err := agentFundEconomicTargets(e.PrincipalMinor, e.OriginalQuota, s, r)
			if err != nil {
				add("fund_economics_journal_invalid", row.ID)
				break
			}
			// Limits make accumulation bounded and prevent malicious journal overflow.
			if row.SpentDelta < 0 || row.SpentDelta > e.OriginalQuota-s || row.ReleasedDelta < 0 || row.ReleasedDelta > e.OriginalQuota-r || row.ChargedDeltaMinor < 0 || row.ChargedDeltaMinor > e.PrincipalMinor-c || row.ReleasedDeltaMinor < 0 || row.ReleasedDeltaMinor > e.PrincipalMinor-l {
				add("fund_economics_journal_invalid", row.ID)
				break
			}
			s += row.SpentDelta
			r += row.ReleasedDelta
			c += row.ChargedDeltaMinor
			l += row.ReleasedDeltaMinor
			targetC, targetL, err := agentFundEconomicTargets(e.PrincipalMinor, e.OriginalQuota, s, r)
			if err != nil || row.AgentUserID != owner || row.SpentAfter != s || row.ReleasedAfter != r || row.ChargedAfterMinor != c || row.ReleasedAfterMinor != l || row.TargetChargedDeltaMinor != targetC-beforeC || row.TargetReleasedDeltaMinor != targetL-beforeL || c > e.PrincipalMinor-l {
				add("fund_economics_journal_mismatch", row.ID)
			}
		}
		if len(rows) == 0 || e.Version != int64(len(rows)) || s != e.SpentQuota || r != e.ReleasedQuota || c != e.ChargedMinor || l != e.ReleasedMinor {
			add("fund_economics_journal_carry_mismatch", int64(b.ID))
		}
		if b.SourceKind == AgentFundPurchase {
			var refunds []AgentPurchaseRefund
			if err := tx.Where("batch_id = ? AND state = ?", b.ID, "succeeded").Find(&refunds).Error; err != nil {
				return nil, err
			}
			var paid int64
			q := 0
			for _, f := range refunds {
				v, err := agentCreditMinor(f.PrincipalMinor, false)
				if err != nil || v > e.PrincipalMinor-paid || f.Quota < 0 || f.Quota > e.OriginalQuota-q {
					add("fund_economics_refund_invalid", int64(f.ID))
					continue
				}
				paid += v
				q += f.Quota
			}
			if paid != e.ReleasedMinor || q != e.ReleasedQuota {
				add("fund_economics_payment_mismatch", int64(b.ID))
			}
		}
	}
	var anomalies []AgentFundEconomicAnomaly
	if err := tx.Where("agent_user_id = ?", owner).Order("id").Find(&anomalies).Error; err != nil {
		return nil, err
	}
	for _, a := range anomalies {
		add("fund_economics_"+a.Code, int64(a.BatchID))
	}
	return out, nil
}
