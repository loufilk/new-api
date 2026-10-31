package model

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

type AgentCommissionOperation struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	UserID       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_commission_operation_request,priority:1"`
	RequestID    string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex:idx_commission_operation_request,priority:2"`
	Action       string `json:"action" gorm:"type:varchar(48);not null"`
	Signature    string `json:"-" gorm:"type:char(64);not null"`
	ResourceType string `json:"resource_type" gorm:"type:varchar(32)"`
	ResourceID   string `json:"resource_id" gorm:"type:varchar(128)"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
}

type AgentCommissionRecipient struct {
	ID              string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	Version         int64  `json:"version" gorm:"primaryKey;autoIncrement:false"`
	UserID          int    `json:"user_id" gorm:"not null;index"`
	Label           string `json:"label" gorm:"type:varchar(80);not null"`
	Reference       string `json:"-" gorm:"type:varchar(255);not null"`
	ReferenceMasked string `json:"reference_masked" gorm:"type:varchar(40);not null"`
	ReferenceHash   string `json:"reference_hash" gorm:"type:char(64);not null"`
	RequestID       string `json:"request_id" gorm:"type:varchar(96);not null"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCommissionRecipient) BeforeUpdate(*gorm.DB) error { return ErrAgentCommissionImmutable }
func (*AgentCommissionRecipient) BeforeDelete(*gorm.DB) error { return ErrAgentCommissionImmutable }

type AgentCommissionQuote struct {
	ID                  string                   `json:"quote_id" gorm:"primaryKey;type:varchar(36)"`
	UserID              int                      `json:"user_id" gorm:"not null;uniqueIndex:idx_commission_quote_request,priority:1"`
	RequestID           string                   `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex:idx_commission_quote_request,priority:2"`
	Action              string                   `json:"action" gorm:"type:varchar(48);not null"`
	Target              string                   `json:"target" gorm:"type:varchar(16);not null"`
	Currency            string                   `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent    int                      `json:"currency_exponent" gorm:"not null"`
	AmountMinor         int64                    `json:"amount_minor,string" gorm:"not null"`
	FeeMinor            int64                    `json:"fee_minor,string" gorm:"not null"`
	NetMinor            int64                    `json:"net_minor,string" gorm:"not null"`
	Quota               int                      `json:"quota" gorm:"type:bigint;not null"`
	RecipientID         string                   `json:"recipient_id" gorm:"type:varchar(36)"`
	RecipientVersion    int64                    `json:"recipient_version"`
	RecipientDigest     string                   `json:"recipient_digest" gorm:"type:varchar(64)"`
	PolicyVersion       int64                    `json:"policy_version" gorm:"not null"`
	ProfileVersion      int64                    `json:"profile_version" gorm:"not null"`
	ParametersHash      string                   `json:"params_hash" gorm:"type:char(64);not null"`
	ExpiresAt           int64                    `json:"expires_at" gorm:"not null"`
	UsedAt              int64                    `json:"used_at"`
	CreatedAt           int64                    `json:"created_at" gorm:"autoCreateTime"`
	VerificationContext *AgentTransactionContext `json:"verification_context,omitempty" gorm:"-"`
}

type AgentCommissionQuoteRequest struct {
	RequestID        string `json:"request_id"`
	Target           string `json:"target"`
	Currency         string `json:"currency"`
	AmountMinor      int64  `json:"amount_minor,string"`
	RecipientID      string `json:"recipient_id"`
	RecipientVersion int64  `json:"recipient_version"`
}

type AgentCommissionExecuteRequest struct {
	RequestID string `json:"request_id"`
	QuoteID   string `json:"quote_id"`
}

type AgentCommissionConversion struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	UserID           int    `json:"user_id" gorm:"not null;index"`
	RequestID        string `json:"request_id" gorm:"type:varchar(96);not null"`
	QuoteID          string `json:"quote_id" gorm:"type:varchar(36);not null;uniqueIndex"`
	Target           string `json:"target" gorm:"type:varchar(16);not null"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent" gorm:"not null"`
	AmountMinor      int64  `json:"amount_minor,string" gorm:"not null"`
	Quota            int    `json:"quota" gorm:"type:bigint;not null"`
	PolicyVersion    int64  `json:"policy_version" gorm:"not null"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
}

// Every movement out of available balance retains its original award lots.
// Withdrawal holds can later be released to those exact lots; conversions and
// successful payments are irreversible transfers for refund-debt purposes.
type AgentCommissionAllocation struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	UserID      int    `json:"user_id" gorm:"not null;index"`
	Kind        string `json:"kind" gorm:"type:varchar(16);not null;uniqueIndex:idx_commission_allocation,priority:1"`
	ResourceID  int64  `json:"resource_id" gorm:"not null;uniqueIndex:idx_commission_allocation,priority:2"`
	AwardID     int64  `json:"award_id" gorm:"not null;uniqueIndex:idx_commission_allocation,priority:3"`
	AmountMinor int64  `json:"amount_minor,string" gorm:"not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func claimAgentCommissionOperationTx(tx *gorm.DB, actorID int, action, requestID string, payload any) (*AgentCommissionOperation, bool, error) {
	if actorID <= 0 || validateAgentRequestID(requestID) != nil {
		return nil, false, ErrAgentCommissionInvalid
	}
	signature, err := agentCommissionHash(payload)
	if err != nil {
		return nil, false, err
	}
	row := AgentCommissionOperation{UserID: actorID, RequestID: requestID, Action: action, Signature: signature}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected == 1 {
		return &row, false, nil
	}
	if err := lockForUpdate(tx).Where("user_id = ? AND request_id = ?", actorID, requestID).First(&row).Error; err != nil {
		return nil, false, err
	}
	if row.Action != action || row.Signature != signature {
		return nil, false, ErrAgentCommissionConflict
	}
	return &row, true, nil
}

func agentCommissionProfileVersionTx(tx *gorm.DB, userID int) (int64, error) {
	var profile AgentProfile
	err := tx.Where("user_id = ?", userID).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return profile.Version, err
}

func agentCommissionQuoteContext(q *AgentCommissionQuote) AgentTransactionContext {
	return AgentTransactionContext{Action: q.Action, RequestID: q.RequestID, AgentUserID: q.UserID, ProfileVersion: q.ProfileVersion, PolicyVersion: q.PolicyVersion, Currency: q.Currency, AmountMinor: strconv.FormatInt(q.AmountMinor, 10), Quota: int64(q.Quota), TargetType: "commission_quote", TargetID: q.ID, TargetVersion: 1, RecipientVersion: q.RecipientVersion, RecipientDigest: q.RecipientDigest, ParametersHash: q.ParametersHash}
}

func CreateAgentCommissionQuote(identity AuthSessionIdentity, input AgentCommissionQuoteRequest) (*AgentCommissionQuote, error) {
	if identity.UserID <= 0 || validateAgentRequestID(input.RequestID) != nil || input.AmountMinor <= 0 || input.AmountMinor > MaxAgentMoneyMinor || (input.Target != "withdrawal" && input.Target != "wallet" && input.Target != "procurement") {
		return nil, ErrAgentCommissionInvalid
	}
	var quote AgentCommissionQuote
	err := DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if !policy.RiskPolicyApproved || (input.Target == "withdrawal" && !policy.WithdrawalEnabled) || (input.Target != "withdrawal" && !policy.CommissionConversionEnabled) {
			return ErrAgentFeatureDisabled
		}
		if input.Currency != policy.CommissionCurrency {
			return ErrAgentCommissionInvalid
		}
		var existing AgentCommissionQuote
		err = tx.Where("user_id = ? AND request_id = ?", identity.UserID, input.RequestID).First(&existing).Error
		if err == nil {
			if existing.Target != input.Target || existing.Currency != input.Currency || existing.AmountMinor != input.AmountMinor || existing.RecipientID != input.RecipientID || existing.RecipientVersion != input.RecipientVersion {
				return ErrAgentCommissionConflict
			}
			if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
				return err
			}
			quote = existing
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		quote = AgentCommissionQuote{ID: common.GetUUID(), UserID: identity.UserID, RequestID: input.RequestID, Target: input.Target, Currency: input.Currency, CurrencyExponent: policy.CommissionCurrencyExponent, AmountMinor: input.AmountMinor, NetMinor: input.AmountMinor, RecipientID: input.RecipientID, RecipientVersion: input.RecipientVersion, PolicyVersion: policy.ID, ExpiresAt: common.GetTimestamp() + min(max(policy.QuoteLifetimeSeconds, 60), 3600)}
		quote.ProfileVersion, err = agentCommissionProfileVersionTx(tx, identity.UserID)
		if err != nil {
			return err
		}
		if input.Target == "withdrawal" {
			quote.Action = "commission.withdraw.request"
			if input.AmountMinor < policy.WithdrawalMinimumMinor || input.RecipientID == "" || input.RecipientVersion <= 0 {
				return ErrAgentCommissionInvalid
			}
			var recipient AgentCommissionRecipient
			if err := tx.Where("id = ? AND version = ? AND user_id = ?", input.RecipientID, input.RecipientVersion, identity.UserID).First(&recipient).Error; err != nil {
				return err
			}
			var latest int64
			if err := tx.Model(&AgentCommissionRecipient{}).Where("id = ? AND user_id = ?", input.RecipientID, identity.UserID).Select("MAX(version)").Scan(&latest).Error; err != nil {
				return err
			}
			if latest != input.RecipientVersion {
				return ErrAgentCommissionConflict
			}
			quote.RecipientDigest = recipient.ReferenceHash
			quote.FeeMinor, err = agentMinorFraction(input.AmountMinor, policy.WithdrawalFeeBPS, 10000)
			if err != nil {
				return err
			}
			quote.NetMinor -= quote.FeeMinor
			if quote.NetMinor <= 0 {
				return ErrAgentCommissionInvalid
			}
		} else {
			if input.RecipientID != "" || input.RecipientVersion != 0 {
				return ErrAgentCommissionInvalid
			}
			quote.Action = "commission.convert." + input.Target
			if policy.ConversionQuotaNumerator <= 0 || policy.ConversionQuotaDenominator <= 0 {
				return ErrAgentCommissionInvalid
			}
			quota, ratioErr := AgentMoneyRatio(input.AmountMinor, policy.ConversionQuotaNumerator, policy.ConversionQuotaDenominator, false)
			if ratioErr != nil {
				return ratioErr
			}
			quote.Quota, err = common.WalletQuotaFromDecimalStrict(decimal.NewFromInt(quota))
			if err != nil || quote.Quota <= 0 {
				return ErrAgentCommissionInvalid
			}
		}
		quote.ParametersHash, err = agentCommissionHash([]any{quote.Action, quote.Target, quote.Currency, quote.CurrencyExponent, quote.AmountMinor, quote.FeeMinor, quote.NetMinor, quote.Quota, quote.RecipientID, quote.RecipientVersion, quote.RecipientDigest, quote.PolicyVersion, quote.ProfileVersion, quote.ExpiresAt})
		if err != nil {
			return err
		}
		// A quote confers no financial authority. Still validate the live browser
		// identity so PATs cannot prepare a transferable financial operation.
		if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
			return ErrAgentProfilePermission
		}
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			return err
		}
		return tx.Create(&quote).Error
	})
	if err != nil {
		return nil, err
	}
	context := agentCommissionQuoteContext(&quote)
	quote.VerificationContext = &context
	return &quote, nil
}

func allocateAgentCommissionLotsTx(tx *gorm.DB, account *AgentCommissionAccount, kind string, resourceID, amount int64) error {
	if account.DebtMinor != 0 {
		return ErrAgentCommissionDebt
	}
	if amount <= 0 || account.AvailableMinor < amount {
		return ErrAgentCommissionInsufficient
	}
	var awards []AgentCommissionAward
	if err := lockForUpdate(tx).Where("user_id = ? AND currency = ? AND available_minor > 0", account.UserID, account.Currency).Order("id").Find(&awards).Error; err != nil {
		return err
	}
	remaining := amount
	for i := range awards {
		award := &awards[i]
		take := min(remaining, award.AvailableMinor)
		if take == 0 {
			break
		}
		award.AvailableMinor -= take
		if kind == "withdrawal" {
			award.HeldMinor += take
		} else {
			award.SpentMinor += take
		}
		award.Version++
		if err := tx.Save(award).Error; err != nil {
			return err
		}
		if err := tx.Create(&AgentCommissionAllocation{UserID: account.UserID, Kind: kind, ResourceID: resourceID, AwardID: award.ID, AmountMinor: take}).Error; err != nil {
			return err
		}
		remaining -= take
	}
	if remaining != 0 {
		return ErrAgentCommissionInvariant
	}
	account.AvailableMinor -= amount
	if kind == "withdrawal" {
		account.HeldMinor += amount
	} else {
		account.TransferredMinor += amount
	}
	return nil
}

func ExecuteAgentCommissionConversion(authorization AgentTransactionAuthorization, input AgentCommissionExecuteRequest) (*AgentCommissionConversion, error) {
	actor := authorization.Identity()
	var result AgentCommissionConversion
	var walletResult *LedgerResult
	err := DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		op, replayed, err := claimAgentCommissionOperationTx(tx, actor.UserID, "conversion", input.RequestID, input)
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
		if quote.RequestID != input.RequestID || quote.UsedAt != 0 || quote.ExpiresAt <= common.GetTimestamp() || quote.PolicyVersion != policy.ID || (quote.Target != "wallet" && quote.Target != "procurement") {
			return ErrAgentCommissionConflict
		}
		if !policy.CommissionConversionEnabled || !policy.RiskPolicyApproved {
			return ErrAgentFeatureDisabled
		}
		account, err := lockAgentCommissionAccount(tx, actor.UserID, quote.Currency, quote.CurrencyExponent)
		if err != nil {
			return err
		}
		if err := matureAgentCommissionAccountTx(tx, account, common.GetTimestamp()); err != nil {
			return err
		}
		before := *account
		result = AgentCommissionConversion{UserID: actor.UserID, RequestID: input.RequestID, QuoteID: quote.ID, Target: quote.Target, Currency: quote.Currency, CurrencyExponent: quote.CurrencyExponent, AmountMinor: quote.AmountMinor, Quota: quote.Quota, PolicyVersion: quote.PolicyVersion}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := allocateAgentCommissionLotsTx(tx, account, "conversion", result.ID, quote.AmountMinor); err != nil {
			return err
		}
		if quote.Target == "procurement" {
			if err := CreditAgentCommissionBatchTx(tx, actor.UserID, "commission-conversion:"+strconv.FormatInt(result.ID, 10), quote.Currency, quote.AmountMinor, quote.Quota, quote.PolicyVersion); err != nil {
				return err
			}
		} else {
			walletResult, err = CreditQuotaTx(tx, LedgerEntry{UserId: actor.UserID, Account: AccountReward, Delta: quote.Quota, Reason: "commission_conversion", ReferenceType: "commission_conversion", ReferenceId: strconv.FormatInt(result.ID, 10), IdempotencyKey: "commission_conversion:" + strconv.FormatInt(result.ID, 10)})
			if err != nil {
				return err
			}
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, agentCommissionQuoteContext(&quote)); err != nil {
			return err
		}
		if err := saveAgentCommissionAccount(tx, before, account, "conversion", "conversion", strconv.FormatInt(result.ID, 10), input.RequestID, actor.UserID); err != nil {
			return err
		}
		quote.UsedAt = common.GetTimestamp()
		if err := tx.Save(&quote).Error; err != nil {
			return err
		}
		op.ResourceType = "commission_conversion"
		op.ResourceID = strconv.FormatInt(result.ID, 10)
		return tx.Save(op).Error
	})
	if err != nil {
		return nil, err
	}
	if walletResult != nil {
		RefreshUserQuotaCache(actor.UserID, walletResult.TotalAfter)
	}
	return &result, nil
}

type AgentCommissionRecipientRequest struct {
	RequestID          string `json:"request_id"`
	RecipientID        string `json:"recipient_id"`
	Version            int64  `json:"version"`
	Label              string `json:"label"`
	RecipientReference string `json:"recipient_reference"`
}

func recipientChangeContext(tx *gorm.DB, identity AuthSessionIdentity, policy *AgentPolicy, input AgentCommissionRecipientRequest) (AgentTransactionContext, error) {
	if validateAgentRequestID(input.RequestID) != nil || input.Version < 0 || len(input.Label) == 0 || len(input.Label) > 80 || strings.TrimSpace(input.Label) != input.Label || input.RecipientReference == "" || len(input.RecipientReference) > 255 || strings.TrimSpace(input.RecipientReference) != input.RecipientReference || len(input.RecipientID) > 36 {
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	for _, c := range input.RecipientReference {
		if c < 32 || c == 127 {
			return AgentTransactionContext{}, ErrAgentCommissionInvalid
		}
	}
	if input.RecipientID == "" && input.Version != 0 {
		return AgentTransactionContext{}, ErrAgentCommissionInvalid
	}
	if input.RecipientID != "" {
		var recipient AgentCommissionRecipient
		if err := tx.Where("id = ? AND user_id = ?", input.RecipientID, identity.UserID).Order("version DESC").First(&recipient).Error; err != nil {
			return AgentTransactionContext{}, err
		}
		if recipient.Version != input.Version {
			return AgentTransactionContext{}, ErrAgentCommissionConflict
		}
	}
	profileVersion, err := agentCommissionProfileVersionTx(tx, identity.UserID)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	digest, err := agentCommissionHash(input.RecipientReference)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	parameters, err := agentCommissionHash(input)
	if err != nil {
		return AgentTransactionContext{}, err
	}
	target := input.RecipientID
	if target == "" {
		target = "new:" + input.RequestID
	}
	return AgentTransactionContext{Action: "recipient.change", RequestID: input.RequestID, AgentUserID: identity.UserID, ProfileVersion: profileVersion, PolicyVersion: policy.ID, Currency: "", AmountMinor: "0", TargetType: "commission_recipient", TargetID: target, TargetVersion: input.Version, RecipientVersion: input.Version + 1, RecipientDigest: digest, ParametersHash: parameters}, nil
}

func ChangeAgentCommissionRecipient(authorization AgentTransactionAuthorization, input AgentCommissionRecipientRequest) (*AgentCommissionRecipient, error) {
	actor := authorization.Identity()
	var result AgentCommissionRecipient
	err := DB.Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		op, replayed, err := claimAgentCommissionOperationTx(tx, actor.UserID, "recipient.change", input.RequestID, input)
		if err != nil {
			return err
		}
		if replayed {
			if err := ValidateAuthSessionWithTx(tx, actor); err != nil {
				return err
			}
			return tx.Where("user_id = ? AND request_id = ?", actor.UserID, input.RequestID).First(&result).Error
		}
		context, err := recipientChangeContext(tx, actor, policy, input)
		if err != nil {
			return err
		}
		if err := ConsumeAgentTransactionAuthorizationTx(tx, authorization, context); err != nil {
			return err
		}
		id := input.RecipientID
		if id == "" {
			id = common.GetUUID()
		}
		masked := "••••"
		if len(input.RecipientReference) > 4 {
			masked += input.RecipientReference[len(input.RecipientReference)-4:]
		}
		result = AgentCommissionRecipient{ID: id, Version: input.Version + 1, UserID: actor.UserID, Label: input.Label, Reference: input.RecipientReference, ReferenceMasked: masked, ReferenceHash: context.RecipientDigest, RequestID: input.RequestID}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		op.ResourceType = "commission_recipient"
		op.ResourceID = id
		return tx.Save(op).Error
	})
	return &result, err
}

func agentWithdrawalMonthStart(now int64) int64 {
	t := time.Unix(now, 0).UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).Unix()
}
