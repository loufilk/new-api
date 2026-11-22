package model

import (
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func ExecuteAgentCreditOperation(auth AgentTransactionAuthorization, action string, c AgentCreditCommand) (*AgentOperationStatus, error) {
	if err := validateAgentCreditCommand(action, c); err != nil {
		return nil, err
	}
	var out *AgentOperationStatus
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		p, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		replay, found, err := agentFinanceOperationReplayTx(tx, auth.Identity(), action, c.RequestID, c)
		if err != nil {
			return err
		}
		if found {
			out = replay
			return nil
		}
		plan, err := buildAgentCreditPlanTx(tx, p, auth.Identity(), action, c)
		if err != nil {
			return err
		}
		out = &AgentOperationStatus{ResourceType: "credit", ResourceState: "completed"}
		actor := auth.Identity().UserID
		event := "credit:" + c.RequestID
		switch action {
		case "credit.limit.request":
			row := AgentCreditLimitRequest{AgentUserID: plan.owner, RequestedBy: actor, AmountMinor: plan.amount, Currency: plan.currency, CurrencyExponent: plan.exponent, TermSeconds: c.TermSeconds, ExpiresAt: c.ExpiresAt, Frozen: c.Frozen, ExpectedLimitID: c.ExpectedLimitID, PolicyVersion: p.ID, EvidenceReference: c.EvidenceReference, Reason: c.Reason, State: "requested", Version: 1}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out.Resource = row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = row.State
			out.ResourceType = "credit_limit_request"
		case "credit.limit.approve", "credit.limit.reject":
			r := &plan.limitRequest
			r.ReviewedBy = actor
			r.Version++
			r.State = "rejected"
			if action == "credit.limit.approve" {
				a, err := lockAgentCreditAccountTx(tx, plan.owner, plan.currency, plan.exponent, true)
				if err != nil {
					return err
				}
				row := AgentCreditLimitVersion{RequestID: r.ID, AgentUserID: r.AgentUserID, PreviousID: a.LimitVersionID, AmountMinor: r.AmountMinor, Currency: r.Currency, CurrencyExponent: r.CurrencyExponent, TermSeconds: r.TermSeconds, ExpiresAt: r.ExpiresAt, Frozen: r.Frozen, RequestedBy: r.RequestedBy, ApprovedBy: actor, PolicyVersion: p.ID}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				a.LimitVersionID = row.ID
				a.Version++
				if err := tx.Model(a).Select("limit_version_id", "version").Updates(a).Error; err != nil {
					return err
				}
				r.State = "approved"
				evidence, _ := AgentTransactionParametersHash(c.EvidenceReference)
				if err := appendAgentCreditLedgerTx(tx, a, AgentCreditLedger{EventKey: event, ActorUserID: actor, Kind: "limit_approved", EvidenceHash: evidence}); err != nil {
					return err
				}
			}
			if err := tx.Model(r).Select("reviewed_by", "version", "state").Updates(r).Error; err != nil {
				return err
			}
			out.Resource = *r
			out.ResourceID = strconv.FormatInt(r.ID, 10)
			out.ResourceState = r.State
			out.ResourceType = "credit_limit_request"
		case "credit.draw":
			aggregate, err := lockAgentRedemptionAccount(tx, plan.owner, true)
			if err != nil {
				return err
			}
			if err := ensureAgentFundSourcesTx(tx, aggregate); err != nil {
				return err
			}
			snapshot, err := common.Marshal(p)
			if err != nil {
				return err
			}
			batch := AgentFundBatch{SourceKey: event, AgentUserID: plan.owner, SourceKind: AgentFundCredit, OriginalQuota: c.Quota, OriginalPaidMinor: strconv.FormatInt(plan.amount, 10), Currency: plan.currency, CurrencyExponent: plan.exponent, WalletAccount: AccountReward, PolicyVersion: p.ID, PolicySnapshot: string(snapshot)}
			if err := createAgentFundBatchTx(tx, &batch); err != nil {
				return err
			}
			row := AgentCreditDraw{AgentUserID: plan.owner, BatchID: batch.ID, LimitVersionID: plan.limit.ID, OriginalQuota: c.Quota, PrincipalMinor: plan.amount, Currency: plan.currency, CurrencyExponent: plan.exponent, TermSeconds: plan.limit.TermSeconds, RecognitionMode: p.CreditRecognitionMode, PolicyVersion: p.ID, PolicySnapshot: string(snapshot)}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			if err := appendAgentCreditLedgerTx(tx, plan.account, AgentCreditLedger{EventKey: event, ActorUserID: actor, Kind: "draw_reserve", ReservedDelta: plan.amount, DrawID: row.ID}); err != nil {
				return err
			}
			if err := applyAgentQuotaChangeTx(tx, aggregate, AgentRedemptionLedger{OperationKey: event, ActorUserID: actor, Kind: "credit_draw", Delta: c.Quota, AllocatedDelta: c.Quota, RequestID: c.RequestID, Reason: "Approved noncash credit capacity reserved; no receivable until source spend"}); err != nil {
				return err
			}
			out.Resource = row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = "reserved"
			out.ResourceType = "credit_draw"
		case "credit.release":
			aggregate, err := lockAgentRedemptionAccount(tx, plan.owner, false)
			if err != nil {
				return err
			}
			if _, err := takeAgentFundSlicesTx(tx, plan.owner, c.Quota, plan.batch.ID, []string{AgentFundCredit}, AgentSliceReclaimed, event, 0, 0); err != nil {
				return err
			}
			if err := applyAgentQuotaChangeTx(tx, aggregate, AgentRedemptionLedger{OperationKey: event, ActorUserID: actor, Kind: "credit_release", Delta: -c.Quota, AllocatedDelta: -c.Quota, RequestID: c.RequestID, Reason: "Only unencumbered unused credit source capacity released"}); err != nil {
				return err
			}
			out.Resource = map[string]any{"draw_id": plan.draw.ID, "quota": c.Quota, "released_minor": strconv.FormatInt(plan.amount, 10)}
			out.ResourceID = strconv.FormatInt(plan.draw.ID, 10)
			out.ResourceState = "released"
			out.ResourceType = "credit_draw"
		case "credit.invoice.issue":
			r := &plan.receivable
			evidence, _ := AgentTransactionParametersHash(c.EvidenceReference)
			row := AgentReceivableInvoice{ReceivableID: r.ID, InvoiceNumber: c.InvoiceNumber, PrincipalMinor: r.PrincipalMinor, Currency: r.Currency, CurrencyExponent: r.CurrencyExponent, DueAt: r.DueAt, IssuedBy: actor, EvidenceHash: evidence}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			if err := saveAgentReceivableTx(tx, r); err != nil {
				return err
			}
			out.Resource = row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = "issued"
			out.ResourceType = "credit_invoice"
		case "credit.repayment.record":
			evidence, _ := AgentTransactionParametersHash(c.EvidenceReference)
			receipt, err := ClaimAgentPaymentReceiptTx(tx, &AgentPaymentReceipt{Provider: "bank_transfer", ProviderReference: c.ExternalReference, MerchantScope: c.MerchantScope, Environment: "live", Purpose: "credit_repayment", LocalOrderKey: "credit-repayment:" + c.RequestID, OwnerUserID: plan.owner, Currency: plan.currency, CurrencyExponent: plan.exponent, PaidMinor: plan.amount, PaidAt: c.PaidAt, VerifiedAt: common.GetTimestamp(), VerifiedBy: actor, EvidenceHash: evidence})
			if err != nil {
				return err
			}
			allocationJSON, err := common.Marshal(plan.allocations)
			if err != nil {
				return err
			}
			row := AgentCreditRepayment{AgentUserID: plan.owner, AmountMinor: plan.amount, Currency: plan.currency, CurrencyExponent: plan.exponent, ReceiptID: receipt.ID, RecordedBy: actor, State: "recorded", Version: 1, AllocationSnapshot: string(allocationJSON), Allocations: plan.allocations}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out.Resource = row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = row.State
			out.ResourceType = "credit_repayment"
		case "credit.repayment.confirm", "credit.repayment.reallocate":
			r := &plan.repayment
			r.Allocations = plan.allocations
			r.Version++
			if action == "credit.repayment.reallocate" {
				b, err := common.Marshal(plan.allocations)
				if err != nil {
					return err
				}
				r.AllocationSnapshot = string(b)
			} else {
				rows, err := agentCreditAllocationsTx(tx, plan.owner, plan.currency, plan.exponent, plan.amount, plan.allocations)
				if err != nil {
					return err
				}
				for i := range rows {
					ar := &rows[i]
					v, _ := agentCreditMinor(plan.allocations[i].AmountMinor, true)
					ar.PaidMinor += v
					if err := saveAgentReceivableTx(tx, ar); err != nil {
						return err
					}
					if err := tx.Create(&AgentCreditRepaymentAllocation{RepaymentID: r.ID, ReceivableID: ar.ID, AmountMinor: v}).Error; err != nil {
						return err
					}
					if err := appendAgentCreditLedgerTx(tx, plan.account, AgentCreditLedger{EventKey: fmt.Sprintf("%s:%d", event, ar.ID), ActorUserID: actor, Kind: "repayment", OutstandingDelta: -v, ReceivableID: ar.ID}); err != nil {
						return err
					}
				}
				r.ConfirmedBy = actor
				r.State = "confirmed"
			}
			if err := tx.Model(r).Select("version", "allocation_snapshot", "confirmed_by", "state").Updates(r).Error; err != nil {
				return err
			}
			out.Resource = *r
			out.ResourceID = strconv.FormatInt(r.ID, 10)
			out.ResourceState = r.State
			out.ResourceType = "credit_repayment"
		case "credit.adjustment.request":
			evidence, _ := AgentTransactionParametersHash(c.EvidenceReference)
			row := AgentReceivableAdjustment{AgentUserID: plan.owner, ReceivableID: plan.receivable.ID, Kind: c.AdjustmentKind, AmountMinor: plan.amount, Currency: plan.currency, RequestedBy: actor, EvidenceHash: evidence, Reason: c.Reason, State: "requested", Version: 1}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out.Resource = row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = row.State
			out.ResourceType = "credit_adjustment"
		case "credit.adjustment.approve", "credit.adjustment.reject":
			row := &plan.adjustment
			row.Version++
			row.ReviewedBy = actor
			row.State = "rejected"
			if action == "credit.adjustment.approve" {
				a, err := lockAgentCreditAccountTx(tx, plan.owner, plan.currency, plan.exponent, false)
				if err != nil {
					return err
				}
				ar := &plan.receivable
				e := AgentCreditLedger{EventKey: event, ActorUserID: actor, Kind: row.Kind, ReceivableID: ar.ID, EvidenceHash: row.EvidenceHash}
				switch row.Kind {
				case "write_off":
					ar.WrittenOffMinor += row.AmountMinor
					e.OutstandingDelta = -row.AmountMinor
					e.WrittenOffDelta = row.AmountMinor
				case "reverse_write_off":
					ar.WrittenOffMinor -= row.AmountMinor
					e.OutstandingDelta = row.AmountMinor
					e.WrittenOffDelta = -row.AmountMinor
				case "correction_credit":
					ar.CorrectedMinor += row.AmountMinor
					e.OutstandingDelta = -row.AmountMinor
				default:
					return ErrAgentCreditInvariant
				}
				if err := saveAgentReceivableTx(tx, ar); err != nil {
					return err
				}
				if err := appendAgentCreditLedgerTx(tx, a, e); err != nil {
					return err
				}
				row.State = "approved"
			}
			if err := tx.Model(row).Select("version", "reviewed_by", "state").Updates(row).Error; err != nil {
				return err
			}
			out.Resource = *row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = row.State
			out.ResourceType = "credit_adjustment"
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, plan.context); err != nil {
			return err
		}
		if action == "credit.limit.request" || action == "credit.draw" {
			if err := RequireAgentGovernanceCreditTx(tx, plan.owner); err != nil {
				return err
			}
		}
		return storeAgentFinanceOperationTx(tx, auth.Identity(), plan.owner, action, c.RequestID, c, out)
	})
	return out, err
}
