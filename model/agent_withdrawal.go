package model

import (
	"context"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

type AgentCommissionWithdrawal struct {
	ID                 int64                             `json:"id" gorm:"primaryKey"`
	BusinessNo         string                            `json:"business_no" gorm:"type:varchar(64);not null;uniqueIndex"`
	UserID             int                               `json:"user_id" gorm:"not null;index"`
	RequestID          string                            `json:"request_id" gorm:"type:varchar(96);not null;index"`
	QuoteID            string                            `json:"quote_id" gorm:"type:varchar(36);not null;uniqueIndex"`
	Currency           string                            `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent   int                               `json:"currency_exponent" gorm:"not null"`
	AmountMinor        int64                             `json:"amount_minor,string" gorm:"not null"`
	FeeMinor           int64                             `json:"fee_minor,string" gorm:"not null"`
	NetMinor           int64                             `json:"net_minor,string" gorm:"not null"`
	RecipientID        string                            `json:"recipient_id" gorm:"type:varchar(36);not null"`
	RecipientVersion   int64                             `json:"recipient_version" gorm:"not null"`
	RecipientDigest    string                            `json:"recipient_digest" gorm:"type:char(64);not null"`
	RecipientLabel     string                            `json:"recipient_label" gorm:"type:varchar(80);not null"`
	RecipientReference string                            `json:"-" gorm:"type:varchar(255);not null"`
	RecipientMasked    string                            `json:"recipient_masked" gorm:"type:varchar(40);not null"`
	Status             string                            `json:"status" gorm:"type:varchar(16);not null;index"`
	Version            int64                             `json:"version" gorm:"not null"`
	RequestedBy        int                               `json:"requested_by" gorm:"not null"`
	ApprovedBy         int                               `json:"approved_by" gorm:"not null"`
	SubmittedBy        int                               `json:"submitted_by" gorm:"not null"`
	PolicyVersion      int64                             `json:"policy_version" gorm:"not null"`
	PayoutReceiptID    int64                             `json:"payout_receipt_id" gorm:"index"`
	EvidenceReference  string                            `json:"evidence_reference" gorm:"type:varchar(255)"`
	Reason             string                            `json:"reason" gorm:"type:varchar(255)"`
	CreatedAt          int64                             `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          int64                             `json:"updated_at" gorm:"autoUpdateTime"`
	AllowedActions     []string                          `json:"allowed_actions" gorm:"-"`
	ManualInstruction  *AgentCommissionPayoutInstruction `json:"manual_instruction,omitempty" gorm:"-"`
}

type AgentCommissionWithdrawalAudit struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	WithdrawalID      int64  `json:"withdrawal_id" gorm:"not null;index"`
	ActorUserID       int    `json:"actor_user_id" gorm:"not null"`
	RequestID         string `json:"request_id" gorm:"type:varchar(96);not null"`
	FromStatus        string `json:"from_status" gorm:"type:varchar(16)"`
	ToStatus          string `json:"to_status" gorm:"type:varchar(16);not null"`
	Version           int64  `json:"version" gorm:"not null"`
	Reason            string `json:"reason" gorm:"type:varchar(255)"`
	EvidenceReference string `json:"evidence_reference" gorm:"type:varchar(255)"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCommissionWithdrawalAudit) BeforeUpdate(*gorm.DB) error {
	return ErrAgentCommissionImmutable
}
func (*AgentCommissionWithdrawalAudit) BeforeDelete(*gorm.DB) error {
	return ErrAgentCommissionImmutable
}

type AgentCommissionPayoutEvidence struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	ReferenceHash     string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	WithdrawalID      int64  `json:"withdrawal_id" gorm:"not null;uniqueIndex"`
	EvidenceReference string `json:"evidence_reference" gorm:"type:varchar(255);not null"`
	CreatedAt         int64  `json:"created_at" gorm:"autoCreateTime"`
}

// The adapter contract is intentionally side-effect free. Automated payout
// execution is not enabled in this phase. An adapter may render instructions,
// but must not send money; a human records the verified outcome afterwards.
type AgentCommissionPayoutInstruction struct {
	BusinessNo          string `json:"business_no"`
	Currency            string `json:"currency"`
	AmountMinor         string `json:"amount_minor"`
	RecipientDigest     string `json:"recipient_digest"`
	CurrencyExponent    int    `json:"currency_exponent"`
	RecipientRegistryID string `json:"recipient_registry_id"`
	RecipientLabel      string `json:"recipient_label"`
	ManualOnly          bool   `json:"manual_only"`
}
type AgentCommissionPayoutAdapter interface {
	PrepareInstruction(context.Context, AgentCommissionWithdrawal) (AgentCommissionPayoutInstruction, error)
}
type ManualAgentCommissionPayoutAdapter struct{}

func (ManualAgentCommissionPayoutAdapter) PrepareInstruction(_ context.Context, w AgentCommissionWithdrawal) (AgentCommissionPayoutInstruction, error) {
	if (w.Status != "paying" && w.Status != "unknown") || w.ApprovedBy <= 0 || w.SubmittedBy <= 0 || w.ApprovedBy == w.UserID || w.ApprovedBy == w.RequestedBy || w.SubmittedBy == w.UserID || w.SubmittedBy == w.RequestedBy {
		return AgentCommissionPayoutInstruction{}, ErrAgentCommissionConflict
	}
	return AgentCommissionPayoutInstruction{BusinessNo: w.BusinessNo, Currency: w.Currency, AmountMinor: strconv.FormatInt(w.NetMinor, 10), RecipientDigest: w.RecipientDigest, CurrencyExponent: w.CurrencyExponent, RecipientRegistryID: w.RecipientReference, RecipientLabel: w.RecipientLabel, ManualOnly: true}, nil
}

func RequestAgentCommissionWithdrawal(authorization AgentTransactionAuthorization, input AgentCommissionExecuteRequest) (*AgentCommissionWithdrawal, error) {
	actor := authorization.Identity()
	var result AgentCommissionWithdrawal
	err := DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		op, replayed, err := claimAgentCommissionOperationTx(tx, actor.UserID, "withdrawal", input.RequestID, input)
		if err != nil {
			return err
		}
		if replayed {
			if err := ValidateAuthSessionWithTx(tx, actor); err != nil {
				return err
			}
			return tx.Where("quote_id = ? AND user_id = ?", input.QuoteID, actor.UserID).First(&result).Error
		}
		var quote AgentCommissionQuote
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", input.QuoteID, actor.UserID).First(&quote).Error; err != nil {
			return err
		}
		if quote.RequestID != input.RequestID || quote.Target != "withdrawal" || quote.UsedAt != 0 || quote.ExpiresAt <= common.GetTimestamp() || quote.PolicyVersion != policy.ID {
			return ErrAgentCommissionConflict
		}
		if !policy.WithdrawalEnabled || !policy.RiskPolicyApproved {
			return ErrAgentFeatureDisabled
		}
		account, err := lockAgentCommissionAccount(tx, actor.UserID, quote.Currency, quote.CurrencyExponent)
		if err != nil {
			return err
		}
		if err := matureAgentCommissionAccountTx(tx, account, common.GetTimestamp()); err != nil {
			return err
		}
		now := common.GetTimestamp()
		if policy.WithdrawalCalendarMonthLimit <= 0 || policy.WithdrawalMinimumMinor <= 0 || quote.AmountMinor < policy.WithdrawalMinimumMinor {
			return ErrAgentCommissionInvalid
		}
		var monthCount int64
		query := tx.Model(&AgentCommissionWithdrawal{}).Where("user_id = ? AND currency = ? AND status NOT IN ?", actor.UserID, quote.Currency, []string{"failed", "rejected"})
		if err := query.Where("created_at >= ?", agentWithdrawalMonthStart(now)).Count(&monthCount).Error; err != nil {
			return err
		}
		if monthCount >= int64(policy.WithdrawalCalendarMonthLimit) {
			return ErrAgentCommissionUnavailable
		}
		if policy.WithdrawalIntervalSeconds > 0 {
			var recent int64
			if err := tx.Model(&AgentCommissionWithdrawal{}).Where("user_id = ? AND currency = ? AND status NOT IN ? AND created_at > ?", actor.UserID, quote.Currency, []string{"failed", "rejected"}, now-policy.WithdrawalIntervalSeconds).Count(&recent).Error; err != nil {
				return err
			}
			if recent > 0 {
				return ErrAgentCommissionUnavailable
			}
		}
		var recipient AgentCommissionRecipient
		if err := tx.Where("id = ? AND version = ? AND user_id = ?", quote.RecipientID, quote.RecipientVersion, actor.UserID).First(&recipient).Error; err != nil {
			return err
		}
		var latest int64
		if err := tx.Model(&AgentCommissionRecipient{}).Where("id = ? AND user_id = ?", quote.RecipientID, actor.UserID).Select("MAX(version)").Scan(&latest).Error; err != nil {
			return err
		}
		if latest != quote.RecipientVersion || recipient.ReferenceHash != quote.RecipientDigest {
			return ErrAgentCommissionConflict
		}
		result = AgentCommissionWithdrawal{BusinessNo: "cw_" + common.GetUUID(), UserID: actor.UserID, RequestID: input.RequestID, QuoteID: quote.ID, Currency: quote.Currency, CurrencyExponent: quote.CurrencyExponent, AmountMinor: quote.AmountMinor, FeeMinor: quote.FeeMinor, NetMinor: quote.NetMinor, RecipientID: quote.RecipientID, RecipientVersion: quote.RecipientVersion, RecipientDigest: quote.RecipientDigest, RecipientLabel: recipient.Label, RecipientReference: recipient.Reference, RecipientMasked: recipient.ReferenceMasked, Status: "applied", Version: 1, RequestedBy: actor.UserID, PolicyVersion: policy.ID}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		before := *account
		if err := allocateAgentCommissionLotsTx(tx, account, "withdrawal", result.ID, quote.AmountMinor); err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, agentCommissionQuoteContext(&quote)); err != nil {
			return err
		}
		if err := saveAgentCommissionAccount(tx, before, account, "withdrawal_hold", "withdrawal", strconv.FormatInt(result.ID, 10), input.RequestID, actor.UserID); err != nil {
			return err
		}
		if err := tx.Create(&AgentCommissionWithdrawalAudit{WithdrawalID: result.ID, ActorUserID: actor.UserID, RequestID: input.RequestID, ToStatus: "applied", Version: 1}).Error; err != nil {
			return err
		}
		quote.UsedAt = now
		if err := tx.Save(&quote).Error; err != nil {
			return err
		}
		op.ResourceType = "commission_withdrawal"
		op.ResourceID = strconv.FormatInt(result.ID, 10)
		return tx.Save(op).Error
	})
	return &result, err
}

type AgentCommissionWithdrawalAction struct {
	RequestID              string `json:"request_id"`
	WithdrawalID           int64  `json:"withdrawal_id"`
	Version                int64  `json:"version"`
	Decision               string `json:"decision"`
	Status                 string `json:"status"`
	Reason                 string `json:"reason"`
	EvidenceReference      string `json:"evidence_reference"`
	FailureConfirmedUnpaid bool   `json:"failure_confirmed_unpaid"`
	PayoutProvider         string `json:"payout_provider"`
	PayoutReference        string `json:"payout_reference"`
	PayoutMerchantScope    string `json:"payout_merchant_scope"`
	PayoutEnvironment      string `json:"payout_environment"`
}

func withdrawalActionContext(tx *gorm.DB, policy *AgentPolicy, w *AgentCommissionWithdrawal, action string, input AgentCommissionWithdrawalAction) (AgentTransactionContext, error) {
	if input.WithdrawalID != w.ID || input.Version != w.Version || validateAgentRequestID(input.RequestID) != nil || len(input.Reason) > 255 || len(input.EvidenceReference) > 255 {
		return AgentTransactionContext{}, ErrAgentCommissionConflict
	}
	if strings.TrimSpace(input.Reason) != input.Reason || strings.TrimSpace(input.EvidenceReference) != input.EvidenceReference {
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	if action == "commission.withdraw.resolve" && input.Status == "succeeded" {
		if input.PayoutProvider == "" || input.PayoutReference == "" || input.PayoutMerchantScope == "" || input.PayoutEnvironment != "live" {
			return AgentTransactionContext{}, ErrAgentCommissionInvalid
		}
	} else if input.PayoutProvider != "" || input.PayoutReference != "" || input.PayoutMerchantScope != "" || input.PayoutEnvironment != "" {
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	switch action {
	case "commission.withdraw.approve", "commission.withdraw.reject":
		if (action == "commission.withdraw.reject" && input.Decision != "reject") || (action == "commission.withdraw.approve" && input.Decision != "approve") {
			return AgentTransactionContext{}, ErrAgentCommissionConflict
		}
		if w.Status != "applied" || (input.Decision != "approve" && input.Decision != "reject") || input.Reason == "" || input.Status != "" || input.EvidenceReference != "" || input.FailureConfirmedUnpaid {
			return AgentTransactionContext{}, ErrAgentCommissionConflict
		}
	case "commission.withdraw.submit":
		if w.Status != "approved" || input.Decision != "" || input.Status != "" || input.FailureConfirmedUnpaid || input.Reason == "" {
			return AgentTransactionContext{}, ErrAgentCommissionConflict
		}
	case "commission.withdraw.resolve":
		if (w.Status != "paying" && w.Status != "unknown") || (input.Status != "succeeded" && input.Status != "failed" && input.Status != "unknown") || input.Decision != "" || input.EvidenceReference == "" || input.Reason == "" || (input.Status == "failed" && !input.FailureConfirmedUnpaid) || (input.Status != "failed" && input.FailureConfirmedUnpaid) {
			return AgentTransactionContext{}, ErrAgentCommissionConflict
		}
	default:
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	version, err := agentCommissionProfileVersionTx(tx, w.UserID)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	hash, err := agentCommissionHash([]any{input, w.BusinessNo, w.AmountMinor, w.FeeMinor, w.NetMinor, w.RecipientID, w.RecipientVersion, w.RecipientDigest, w.Status, w.Version, w.ApprovedBy})
	if err != nil {
		return AgentTransactionContext{}, err
	}
	return AgentTransactionContext{Action: action, RequestID: input.RequestID, AgentUserID: w.UserID, ProfileVersion: version, PolicyVersion: policy.ID, Currency: w.Currency, AmountMinor: strconv.FormatInt(w.AmountMinor, 10), TargetType: "commission_withdrawal", TargetID: strconv.FormatInt(w.ID, 10), TargetVersion: w.Version, RecipientVersion: w.RecipientVersion, RecipientDigest: w.RecipientDigest, ParametersHash: hash}, nil
}

func releaseAgentWithdrawalHoldTx(tx *gorm.DB, account *AgentCommissionAccount, w *AgentCommissionWithdrawal, paid bool) error {
	var allocations []AgentCommissionAllocation
	if err := tx.Where("kind = ? AND resource_id = ?", "withdrawal", w.ID).Order("award_id").Find(&allocations).Error; err != nil {
		return err
	}
	total := int64(0)
	for _, allocation := range allocations {
		var award AgentCommissionAward
		if err := lockForUpdate(tx).First(&award, allocation.AwardID).Error; err != nil {
			return err
		}
		if award.UserID != w.UserID || award.Currency != w.Currency || award.HeldMinor < allocation.AmountMinor {
			return ErrAgentCommissionInvariant
		}
		award.HeldMinor -= allocation.AmountMinor
		if paid {
			award.SpentMinor += allocation.AmountMinor
		} else {
			remaining, err := recoverAgentCommissionDebtTx(tx, account, allocation.AmountMinor)
			if err != nil {
				return err
			}
			award.AvailableMinor += remaining
			account.AvailableMinor += remaining
			award.SpentMinor += allocation.AmountMinor - remaining
		}
		award.Version++
		if err := tx.Save(&award).Error; err != nil {
			return err
		}
		total += allocation.AmountMinor
	}
	if total != w.AmountMinor || account.HeldMinor < total {
		return ErrAgentCommissionInvariant
	}
	account.HeldMinor -= total
	if paid {
		account.TransferredMinor += total
	}
	return nil
}

func ActOnAgentCommissionWithdrawal(authorization AgentTransactionAuthorization, action string, input AgentCommissionWithdrawalAction) (*AgentCommissionWithdrawal, error) {
	actor := authorization.Identity()
	var result AgentCommissionWithdrawal
	err := DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		op, replayed, err := claimAgentCommissionOperationTx(tx, actor.UserID, action, input.RequestID, input)
		if err != nil {
			return err
		}
		if replayed {
			if err := requireAgentCommissionReaderTx(tx, actor, 0, true); err != nil {
				return err
			}
			return tx.First(&result, input.WithdrawalID).Error
		}
		if err := lockForUpdate(tx).First(&result, input.WithdrawalID).Error; err != nil {
			return err
		}
		if actor.UserID == result.RequestedBy || actor.UserID == result.UserID {
			return ErrAgentProfilePermission
		}
		context, err := withdrawalActionContext(tx, policy, &result, action, input)
		if err != nil {
			return err
		}
		account, err := lockAgentCommissionAccount(tx, result.UserID, result.Currency, result.CurrencyExponent)
		if err != nil {
			return err
		}
		before := *account
		oldStatus := result.Status
		switch action {
		case "commission.withdraw.approve", "commission.withdraw.reject":
			if input.Decision == "approve" {
				if account.DebtMinor > 0 {
					return ErrAgentCommissionDebt
				}
				result.Status = "approved"
				result.ApprovedBy = actor.UserID
			} else {
				result.Status = "rejected"
				if err := releaseAgentWithdrawalHoldTx(tx, account, &result, false); err != nil {
					return err
				}
			}
		case "commission.withdraw.submit":
			if !policy.WithdrawalEnabled || !policy.RiskPolicyApproved {
				return ErrAgentFeatureDisabled
			}
			if account.DebtMinor > 0 {
				return ErrAgentCommissionDebt
			}
			result.Status = "paying"
			result.SubmittedBy = actor.UserID
		case "commission.withdraw.resolve":
			result.Status = input.Status
			if result.Status == "succeeded" {
				hash, err := agentCommissionHash(input.EvidenceReference)
				if err != nil {
					return err
				}
				receipt, err := ClaimAgentOutboundPaymentReceiptTx(tx, &AgentOutboundPaymentReceipt{Provider: input.PayoutProvider, ProviderReference: input.PayoutReference, MerchantScope: input.PayoutMerchantScope, Environment: input.PayoutEnvironment, Purpose: "withdrawal", BusinessKey: result.BusinessNo, OwnerUserID: result.UserID, Currency: result.Currency, CurrencyExponent: result.CurrencyExponent, PaidMinor: result.NetMinor, VerifiedBy: actor.UserID, EvidenceHash: hash})
				if err != nil {
					return err
				}
				result.PayoutReceiptID = receipt.ID
				evidence := AgentCommissionPayoutEvidence{ReferenceHash: hash, WithdrawalID: result.ID, EvidenceReference: input.EvidenceReference}
				created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&evidence)
				if created.Error != nil {
					return created.Error
				}
				if created.RowsAffected != 1 {
					return ErrAgentCommissionConflict
				}
				if err := releaseAgentWithdrawalHoldTx(tx, account, &result, true); err != nil {
					return err
				}
			} else if result.Status == "failed" {
				if err := releaseAgentWithdrawalHoldTx(tx, account, &result, false); err != nil {
					return err
				}
			}
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		result.Version++
		result.Reason = input.Reason
		result.EvidenceReference = input.EvidenceReference
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		if err := tx.Create(&AgentCommissionWithdrawalAudit{WithdrawalID: result.ID, ActorUserID: actor.UserID, RequestID: input.RequestID, FromStatus: oldStatus, ToStatus: result.Status, Version: result.Version, Reason: input.Reason, EvidenceReference: input.EvidenceReference}).Error; err != nil {
			return err
		}
		if err := saveAgentCommissionAccount(tx, before, account, "withdrawal_"+result.Status, "withdrawal_action", strconv.FormatInt(op.ID, 10), input.RequestID, actor.UserID); err != nil {
			return err
		}
		op.ResourceType = "commission_withdrawal"
		op.ResourceID = strconv.FormatInt(result.ID, 10)
		return tx.Save(op).Error
	})
	if err == nil && action == "commission.withdraw.submit" && result.Status == "paying" {
		instruction, instructionErr := (ManualAgentCommissionPayoutAdapter{}).PrepareInstruction(context.Background(), result)
		if instructionErr == nil {
			result.ManualInstruction = &instruction
		}
	}

	return &result, err
}

func requireAgentCommissionReaderTx(tx *gorm.DB, identity AuthSessionIdentity, ownerID int, admin bool) error {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return ErrAgentProfilePermission
	}
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return err
	}
	var user User
	if err := tx.Select("id", "role", "status").First(&user, identity.UserID).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled {
		return ErrAgentProfilePermission
	}
	isAdmin := user.Role == common.RoleAdminUser || user.Role == common.RoleRootUser
	if (admin || (ownerID > 0 && ownerID != identity.UserID)) && !isAdmin {
		return ErrAgentProfilePermission
	}
	return nil
}

var _ AgentCommissionPayoutAdapter = ManualAgentCommissionPayoutAdapter{}

// GetAgentCommissionPayoutInstruction reveals an existing controlled external
// payee registry identifier, never bank credentials. The registry must already
// exist and be independently operated before withdrawals are enabled. Reading
// these manual-only instructions does not initiate or retry any payment.
func GetAgentCommissionPayoutInstruction(identity AuthSessionIdentity, id int64) (*AgentCommissionPayoutInstruction, error) {
	var result AgentCommissionPayoutInstruction
	err := DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentCommissionReaderTx(tx, identity, 0, true); err != nil {
			return err
		}
		var withdrawal AgentCommissionWithdrawal
		if err := tx.First(&withdrawal, id).Error; err != nil {
			return err
		}
		if identity.UserID == withdrawal.UserID || identity.UserID == withdrawal.RequestedBy {
			return ErrAgentProfilePermission
		}
		var err error
		result, err = (ManualAgentCommissionPayoutAdapter{}).PrepareInstruction(context.Background(), withdrawal)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
