package model

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ReconcileAgentCreditLiabilities is a read-only, policy-fenced audit. It never
// repairs suspicious history by overwriting an aggregate or assigning money.
// Empty findings mean the checked owner's credit/deposit source journals agree,
// not that an external bank statement has been reconciled.
type AgentCreditLiabilityFinding struct {
	Code       string `json:"code"`
	ResourceID string `json:"resource_id"`
}

func ReconcileAgentCreditLiabilities(id AuthSessionIdentity, owner int) ([]AgentCreditLiabilityFinding, error) {
	if owner == 0 {
		owner = id.UserID
	}
	out := []AgentCreditLiabilityFinding{}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireAgentFinanceReaderTx(tx, id, owner, owner != id.UserID); err != nil {
			return err
		}
		add := func(code string, resource int64) {
			out = append(out, AgentCreditLiabilityFinding{Code: code, ResourceID: strconv.FormatInt(resource, 10)})
		}
		var accounts []AgentCreditAccount
		if err := tx.Where("agent_user_id = ?", owner).Find(&accounts).Error; err != nil {
			return err
		}
		for _, a := range accounts {
			if validateAgentCreditAccount(&a) != nil {
				add("credit_account_invalid", int64(owner))
			}
			var rows []AgentCreditLedger
			if err := tx.Where("agent_user_id = ?", owner).Order("id").Find(&rows).Error; err != nil {
				return err
			}
			var reserved, outstanding, writtenOff int64
			for _, r := range rows {
				reserved += r.ReservedDelta
				outstanding += r.OutstandingDelta
				writtenOff += r.WrittenOffDelta
				if reserved < 0 || outstanding < 0 || writtenOff < 0 || reserved != r.ReservedAfter || outstanding != r.OutstandingAfter || writtenOff != r.WrittenOffAfter || r.Currency != a.Currency || r.CurrencyExponent != a.CurrencyExponent {
					add("credit_journal_mismatch", r.ID)
				}
			}
			if reserved != a.ReservedMinor || outstanding != a.OutstandingMinor || writtenOff != a.WrittenOffMinor {
				add("credit_journal_account_mismatch", int64(owner))
			}
			var ars []AgentReceivable
			if err := tx.Where("agent_user_id = ?", owner).Find(&ars).Error; err != nil {
				return err
			}
			outstanding = 0
			writtenOff = 0
			var recognized int64
			for i := range ars {
				r := &ars[i]
				left, err := agentReceivableRemaining(r)
				if err != nil {
					add("receivable_invalid", r.ID)
					continue
				}
				outstanding += left
				writtenOff += r.WrittenOffMinor
				recognized += r.PrincipalMinor
			}
			if outstanding != a.OutstandingMinor || writtenOff != a.WrittenOffMinor {
				add("receivable_account_mismatch", int64(owner))
			}
			var batches []AgentFundBatch
			if err := tx.Where("agent_user_id = ? AND source_kind = ?", owner, AgentFundCredit).Find(&batches).Error; err != nil {
				return err
			}
			reserved = 0
			var spent int64
			for _, b := range batches {
				var draw AgentCreditDraw
				if err := tx.Where("batch_id = ? AND agent_user_id = ?", b.ID, owner).First(&draw).Error; err != nil {
					if !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					add("credit_source_draw_missing", int64(b.ID))
					continue
				}
				if b.CashRefundable || b.PaymentReceiptID != 0 || b.WalletAccount != AccountReward || b.Currency != a.Currency || b.OriginalPaidMinor != strconv.FormatInt(draw.PrincipalMinor, 10) || draw.OriginalQuota != b.OriginalQuota {
					add("credit_source_invalid", int64(b.ID))
				}
				var slices []AgentFundSlice
				if err := tx.Where("batch_id = ?", b.ID).Order("start_quota").Find(&slices).Error; err != nil {
					return err
				}
				start := 0
				for _, s := range slices {
					_, err := AgentFundSliceCost(&b, s.StartQuota, s.Quota)
					if err != nil || s.StartQuota != start || s.AgentUserID != owner {
						add("credit_source_interval_invalid", int64(s.ID))
						continue
					}
					start += s.Quota
					switch s.State {
					case AgentSliceAvailable, AgentSliceReserved:
					case AgentSliceSpent:
					case AgentSliceReclaimed:
					default:
						add("credit_source_state_invalid", int64(s.ID))
					}
				}
				var economics AgentFundEconomics
				if err := tx.Where("batch_id = ?", b.ID).First(&economics).Error; err != nil {
					if !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					add("credit_economics_missing", int64(b.ID))
				} else if validateAgentFundEconomics(&economics) != nil {
					add("credit_economics_invalid", int64(b.ID))
				} else {
					reserved += economics.PrincipalMinor - economics.ChargedMinor - economics.ReleasedMinor
					spent += economics.ChargedMinor
					var batchAR []AgentReceivable
					if err := tx.Where("batch_id = ?", b.ID).Find(&batchAR).Error; err != nil {
						return err
					}
					var batchRecognized int64
					for _, ar := range batchAR {
						if ar.AgentUserID != owner || ar.DrawID != draw.ID || ar.PrincipalMinor <= 0 || ar.PrincipalMinor > economics.PrincipalMinor-batchRecognized || ar.Currency != b.Currency || ar.CurrencyExponent != b.CurrencyExponent {
							add("credit_batch_receivable_invalid", ar.ID)
							continue
						}
						batchRecognized += ar.PrincipalMinor
					}
					if batchRecognized != economics.ChargedMinor {
						add("credit_batch_recognition_mismatch", int64(b.ID))
					}
					var releases []AgentCreditLedger
					if err := tx.Where("draw_id = ? AND kind = ?", draw.ID, "release").Find(&releases).Error; err != nil {
						return err
					}
					var batchReleased int64
					for _, release := range releases {
						if release.AgentUserID != owner || release.ReservedDelta > 0 || release.ReservedDelta < -(economics.PrincipalMinor-batchReleased) || release.Currency != b.Currency || release.CurrencyExponent != b.CurrencyExponent {
							add("credit_batch_release_invalid", release.ID)
							continue
						}
						batchReleased -= release.ReservedDelta
					}
					if batchReleased != economics.ReleasedMinor {
						add("credit_batch_release_mismatch", int64(b.ID))
					}
				}
				if start != b.OriginalQuota {
					add("credit_source_incomplete", int64(b.ID))
				}
			}
			if reserved != a.ReservedMinor || spent != recognized {
				add("credit_source_receivable_mismatch", int64(owner))
			}
		}
		var deposits []AgentDepositAccount
		if err := tx.Where("agent_user_id = ?", owner).Find(&deposits).Error; err != nil {
			return err
		}
		for _, a := range deposits {
			if validateAgentDepositAccount(&a) != nil {
				add("deposit_account_invalid", int64(owner))
			}
			totals := [5]int64{}
			var rows []AgentDepositLedger
			if err := tx.Where("agent_user_id = ?", owner).Order("id").Find(&rows).Error; err != nil {
				return err
			}
			for _, r := range rows {
				d := [5]int64{r.ReceivedDelta, r.AvailableDelta, r.HeldDelta, r.OffsetDelta, r.RefundedDelta}
				after := [5]int64{r.ReceivedAfter, r.AvailableAfter, r.HeldAfter, r.OffsetAfter, r.RefundedAfter}
				for i := range totals {
					totals[i] += d[i]
					if totals[i] < 0 || totals[i] != after[i] {
						add("deposit_journal_mismatch", r.ID)
						break
					}
				}
			}
			want := [5]int64{a.ReceivedMinor, a.AvailableMinor, a.HeldMinor, a.OffsetMinor, a.RefundedMinor}
			if totals != want {
				add("deposit_journal_account_mismatch", int64(owner))
			}
			totals = [5]int64{}
			var sources []AgentDepositReceipt
			if err := tx.Where("agent_user_id = ?", owner).Find(&sources).Error; err != nil {
				return err
			}
			for i := range sources {
				r := &sources[i]
				if validateAgentDepositReceipt(r) != nil {
					add("deposit_source_invalid", r.ID)
				}
				var receipt AgentPaymentReceipt
				if err := tx.First(&receipt, r.PaymentReceiptID).Error; err != nil {
					if !errors.Is(err, gorm.ErrRecordNotFound) {
						return err
					}
					add("deposit_payment_missing", r.ID)
				} else if receipt.Purpose != "deposit" || receipt.Environment != "live" || receipt.OwnerUserID != owner || receipt.PaidMinor != r.AmountMinor || receipt.Currency != r.Currency || receipt.CurrencyExponent != r.CurrencyExponent || receipt.CounterpartyDigest != r.PayerDigest {
					add("deposit_payment_mismatch", r.ID)
				}
				if r.State == "confirmed" {
					for j, v := range [5]int64{r.AmountMinor, r.AvailableMinor, r.HeldMinor, r.OffsetMinor, r.RefundedMinor} {
						totals[j] += v
					}
				}
				var held []AgentDepositOperationRequest
				if err := tx.Where("deposit_id = ? AND state IN ?", r.ID, []string{"requested", "approved", "payment_pending", "payment_unknown"}).Find(&held).Error; err != nil {
					return err
				}
				var heldTotal int64
				for _, h := range held {
					if h.AmountMinor <= 0 || h.AmountMinor > int64(common.MaxWalletQuota)-heldTotal {
						add("deposit_hold_invalid", h.ID)
						continue
					}
					heldTotal += h.AmountMinor
				}
				if heldTotal != r.HeldMinor {
					add("deposit_hold_mismatch", r.ID)
				}
			}
			if totals != want {
				add("deposit_source_account_mismatch", int64(owner))
			}
		}
		// Orphans are findings even when there is no account row to iterate.
		for _, check := range []struct {
			table string
			count int
		}{{"agent_credit_draws", len(accounts)}, {"agent_receivables", len(accounts)}, {"agent_deposit_receipts", len(deposits)}} {
			if check.count == 0 {
				query := tx.Table(check.table).Where("agent_user_id = ?", owner)
				if check.table == "agent_deposit_receipts" {
					query = query.Where("state = ?", "confirmed")
				}
				var n int64
				if err := query.Count(&n).Error; err != nil {
					return err
				}
				if n > 0 {
					add(fmt.Sprintf("orphan_%s", check.table), int64(owner))
				}
			}
		}
		findings, err := ReconcileAgentFundEconomicsTx(tx, owner)
		if err != nil {
			return err
		}
		out = append(out, findings...)
		return nil
	})
	return out, err
}
