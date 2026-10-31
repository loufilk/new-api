package model

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

func ExecuteAgentDepositOperation(auth AgentTransactionAuthorization, action string, c AgentDepositCommand) (*AgentOperationStatus, error) {
	if err := validateAgentDepositCommand(action, c); err != nil {
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
		plan, err := buildAgentDepositPlanTx(tx, p, auth.Identity(), action, c)
		if err != nil {
			return err
		}
		actor := auth.Identity().UserID
		event := "deposit:" + c.RequestID
		evidence, _ := AgentTransactionParametersHash(c.EvidenceReference)
		out = &AgentOperationStatus{ResourceType: "deposit"}
		switch action {
		case "deposit.receipt.record":
			receipt, err := ClaimAgentPaymentReceiptTx(tx, &AgentPaymentReceipt{Provider: "bank_transfer", ProviderReference: c.ExternalReference, MerchantScope: c.MerchantScope, Environment: "live", Purpose: "deposit", CounterpartyDigest: plan.payerDigest, LocalOrderKey: "deposit:" + c.RequestID, OwnerUserID: plan.owner, Currency: plan.currency, CurrencyExponent: plan.exponent, PaidMinor: plan.amount, PaidAt: c.PaidAt, VerifiedAt: common.GetTimestamp(), VerifiedBy: actor, EvidenceHash: evidence})
			if err != nil {
				return err
			}
			row := AgentDepositReceipt{PayerReference: c.PayerReference, PayerDigest: plan.payerDigest, AgentUserID: plan.owner, PaymentReceiptID: receipt.ID, AmountMinor: plan.amount, Currency: plan.currency, CurrencyExponent: plan.exponent, RecordedBy: actor, State: "recorded", PolicyVersion: p.ID, Version: 1}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			out.Resource = row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = row.State
			out.ResourceType = "deposit_receipt"
		case "deposit.receipt.confirm":
			a, err := lockAgentDepositAccountTx(tx, plan.owner, plan.currency, plan.exponent, true)
			if err != nil {
				return err
			}
			r := &plan.deposit
			r.State = "confirmed"
			r.ConfirmedBy = actor
			if err := appendAgentDepositLedgerTx(tx, a, r, AgentDepositLedger{EventKey: event, ActorUserID: actor, Kind: "receipt", ReceivedDelta: plan.amount, AvailableDelta: plan.amount, EvidenceHash: evidence}); err != nil {
				return err
			}
			out.Resource = *r
			out.ResourceID = strconv.FormatInt(r.ID, 10)
			out.ResourceState = r.State
			out.ResourceType = "deposit_receipt"
		case "deposit.offset.request", "deposit.refund.request":
			a, err := lockAgentDepositAccountTx(tx, plan.owner, plan.currency, plan.exponent, false)
			if err != nil {
				return err
			}
			allocations, err := common.Marshal(plan.allocations)
			if err != nil {
				return err
			}
			kind := "refund"
			if action == "deposit.offset.request" {
				kind = "offset"
			}
			row := AgentDepositOperationRequest{AgentUserID: plan.owner, DepositID: plan.deposit.ID, Kind: kind, RecipientDigest: plan.context.RecipientDigest, RecipientVersion: plan.context.RecipientVersion, AmountMinor: plan.amount, Currency: plan.currency, CurrencyExponent: plan.exponent, State: "requested", Version: 1, BusinessKey: "agent-deposit-" + kind + ":" + c.RequestID, RequestedBy: actor, AllocationSnapshot: string(allocations), Allocations: plan.allocations, Reason: c.Reason, PolicyVersion: p.ID}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			if err := appendAgentDepositLedgerTx(tx, a, &plan.deposit, AgentDepositLedger{EventKey: event, ActorUserID: actor, Kind: kind + "_hold", RequestID: row.ID, AvailableDelta: -plan.amount, HeldDelta: plan.amount}); err != nil {
				return err
			}
			out.Resource = row
			out.ResourceID = strconv.FormatInt(row.ID, 10)
			out.ResourceState = row.State
			out.ResourceType = "deposit_request"
		default:
			r := &plan.request
			if err := common.UnmarshalJsonStr(r.AllocationSnapshot, &r.Allocations); err != nil {
				return ErrAgentDepositInvariant
			}
			a, err := lockAgentDepositAccountTx(tx, plan.owner, plan.currency, plan.exponent, false)
			if err != nil {
				return err
			}
			entry := AgentDepositLedger{EventKey: event, ActorUserID: actor, Kind: strings.TrimPrefix(action, "deposit."), RequestID: r.ID, EvidenceHash: evidence}
			switch action {
			case "deposit.offset.reject", "deposit.refund.reject":
				r.State = "rejected"
				r.ApprovedBy = actor
				entry.HeldDelta = -r.AmountMinor
				entry.AvailableDelta = r.AmountMinor
			case "deposit.refund.approve":
				r.State = "approved"
				r.ApprovedBy = actor
			case "deposit.offset.approve":
				ca, err := lockAgentCreditAccountTx(tx, plan.owner, plan.currency, plan.exponent, false)
				if err != nil {
					return err
				}
				rows, err := agentCreditAllocationsTx(tx, plan.owner, plan.currency, plan.exponent, plan.amount, plan.allocations)
				if err != nil {
					return err
				}
				for i := range rows {
					ar := &rows[i]
					v, _ := agentCreditMinor(plan.allocations[i].AmountMinor, true)
					ar.OffsetMinor += v
					if err := saveAgentReceivableTx(tx, ar); err != nil {
						return err
					}
					if err := appendAgentCreditLedgerTx(tx, ca, AgentCreditLedger{EventKey: fmt.Sprintf("%s:%d", event, ar.ID), ActorUserID: actor, Kind: "deposit_offset", OutstandingDelta: -v, ReceivableID: ar.ID, EvidenceHash: evidence}); err != nil {
						return err
					}
				}
				r.State = "succeeded"
				r.ApprovedBy = actor
				entry.HeldDelta = -r.AmountMinor
				entry.OffsetDelta = r.AmountMinor
			case "deposit.refund.submit":
				r.State = "payment_pending"
				r.SubmittedBy = actor
				r.SubmittedAt = common.GetTimestamp()
			case "deposit.refund.resolve":
				r.ResolvedBy = actor
				r.ResolvedAt = common.GetTimestamp()
				switch c.Outcome {
				case "unknown":
					r.State = "payment_unknown"
				case "failure":
					r.State = "failed"
					entry.HeldDelta = -r.AmountMinor
					entry.AvailableDelta = r.AmountMinor
				case "success":
					receipt, err := ClaimAgentOutboundPaymentReceiptTx(tx, &AgentOutboundPaymentReceipt{Provider: "bank_transfer", ProviderReference: c.ExternalReference, MerchantScope: c.MerchantScope, Environment: "live", Purpose: "deposit_refund", BeneficiaryDigest: r.RecipientDigest, PaidAt: c.PaidAt, BusinessKey: r.BusinessKey, OwnerUserID: plan.owner, Currency: plan.currency, CurrencyExponent: plan.exponent, PaidMinor: plan.amount, VerifiedBy: actor, EvidenceHash: evidence})
					if err != nil {
						return err
					}
					r.PayoutReceiptID = receipt.ID
					r.State = "succeeded"
					entry.HeldDelta = -r.AmountMinor
					entry.RefundedDelta = r.AmountMinor
				}
			}
			r.Version++
			if err := appendAgentDepositLedgerTx(tx, a, &plan.deposit, entry); err != nil {
				return err
			}
			if err := tx.Model(r).Select("state", "version", "approved_by", "submitted_by", "submitted_at", "resolved_by", "resolved_at", "payout_receipt_id").Updates(r).Error; err != nil {
				return err
			}
			out.Resource = *r
			out.ResourceID = strconv.FormatInt(r.ID, 10)
			out.ResourceState = r.State
			out.ResourceType = "deposit_request"
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, auth, plan.context); err != nil {
			return err
		}
		return storeAgentFinanceOperationTx(tx, auth.Identity(), plan.owner, action, c.RequestID, c, out)
	})
	return out, err
}
