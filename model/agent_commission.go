package model

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Cash liabilities are integer currency minor units, never wallet quota. The
// bound also makes sums safe to render in clients which use JavaScript numbers.
const MaxAgentMoneyMinor int64 = 9_007_199_254_740_991

var (
	ErrAgentCommissionInvalid      = errors.New("Invalid commission request")
	ErrAgentCommissionConflict     = errors.New("Commission request conflicts with an earlier operation")
	ErrAgentCommissionUnavailable  = errors.New("Commission settlement is not available")
	ErrAgentCommissionInsufficient = errors.New("Insufficient available commission balance")
	ErrAgentCommissionDebt         = errors.New("Outstanding commission debt must be resolved first")
	ErrAgentCommissionInvariant    = errors.New("Commission accounting invariant violated")
	ErrAgentCommissionImmutable    = errors.New("Commission accounting records are immutable")
)

// AgentCommissionAccount is the authoritative cash-liability balance. Award
// lots and allocation rows explain its provenance; every change commits with
// the corresponding append-only ledger entry in the same database transaction.
type AgentCommissionAccount struct {
	ID                 int64  `json:"id" gorm:"primaryKey"`
	UserID             int    `json:"user_id" gorm:"not null;uniqueIndex:idx_commission_account,priority:1"`
	Currency           string `json:"currency" gorm:"type:varchar(3);not null;uniqueIndex:idx_commission_account,priority:2"`
	CurrencyExponent   int    `json:"currency_exponent" gorm:"not null"`
	PendingMinor       int64  `json:"pending_minor,string" gorm:"not null"`
	AvailableMinor     int64  `json:"available_minor,string" gorm:"not null"`
	ReservedMinor      int64  `json:"reserved_minor,string" gorm:"not null"`
	HeldMinor          int64  `json:"withdrawal_held_minor,string" gorm:"not null"`
	DebtMinor          int64  `json:"debt_minor,string" gorm:"not null"`
	EarnedMinor        int64  `json:"total_earned_minor,string" gorm:"not null"`
	ReversedMinor      int64  `json:"total_reversed_minor,string" gorm:"not null"`
	TransferredMinor   int64  `json:"total_transferred_minor,string" gorm:"not null"`
	DebtRecoveredMinor int64  `json:"total_debt_recovered_minor,string" gorm:"not null"`
	Version            int64  `json:"version" gorm:"not null"`
	CreatedAt          int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt          int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// Cap consumption is deliberately independent of qualification and policy
// versions, and is not released by a refund or by disabling/reopening an agent.
type AgentCommissionCap struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	UserID       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_commission_cap,priority:1"`
	SourceUserID int    `json:"source_user_id" gorm:"not null;uniqueIndex:idx_commission_cap,priority:2"`
	Currency     string `json:"currency" gorm:"type:varchar(3);not null;uniqueIndex:idx_commission_cap,priority:3"`
	EarnedMinor  int64  `json:"earned_minor,string" gorm:"not null"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
}

type AgentCommissionAward struct {
	ID               int64  `json:"id" gorm:"primaryKey"`
	EventID          int64  `json:"event_id" gorm:"not null;uniqueIndex"`
	UserID           int    `json:"user_id" gorm:"not null;index"`
	SourceUserID     int    `json:"source_user_id" gorm:"not null;index"`
	ReferenceID      string `json:"reference_id" gorm:"type:varchar(255);not null;uniqueIndex"`
	Provider         string `json:"provider" gorm:"type:varchar(64);not null"`
	PaymentReference string `json:"-" gorm:"type:varchar(255);not null;index"`
	Currency         string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent int    `json:"currency_exponent" gorm:"not null"`
	PaidMinor        int64  `json:"paid_minor,string" gorm:"not null"`
	RewardMinor      int64  `json:"reward_minor,string" gorm:"not null"`
	RefundedMinor    int64  `json:"refunded_minor,string" gorm:"not null"`
	ReversedMinor    int64  `json:"reversed_minor,string" gorm:"not null"`
	PendingMinor     int64  `json:"pending_minor,string" gorm:"not null"`
	AvailableMinor   int64  `json:"available_minor,string" gorm:"not null"`
	ReservedMinor    int64  `json:"reserved_minor,string" gorm:"not null"`
	HeldMinor        int64  `json:"held_minor,string" gorm:"not null"`
	SpentMinor       int64  `json:"spent_minor,string" gorm:"not null"`
	RateBPS          int64  `json:"rate_bps" gorm:"not null"`
	CapMinor         int64  `json:"cap_minor,string" gorm:"not null"`
	ReserveBPS       int64  `json:"reserve_bps" gorm:"not null"`
	PolicyVersion    int64  `json:"policy_version" gorm:"not null"`
	ProfileVersion   int64  `json:"profile_version" gorm:"not null"`
	Qualification    string `json:"qualification" gorm:"type:varchar(32);not null"`
	PolicySnapshot   string `json:"-" gorm:"type:text;not null"`
	MaturesAt        int64  `json:"matures_at" gorm:"not null;index"`
	Matured          bool   `json:"matured" gorm:"not null"`
	Version          int64  `json:"version" gorm:"not null"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt        int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

// A debt is a separate receivable, never a negative legacy aff_quota.
type AgentCommissionDebt struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	UserID         int    `json:"user_id" gorm:"not null;index"`
	AwardID        int64  `json:"award_id" gorm:"not null;index"`
	EventID        int64  `json:"event_id" gorm:"not null;uniqueIndex"`
	Currency       string `json:"currency" gorm:"type:varchar(3);not null"`
	AmountMinor    int64  `json:"amount_minor,string" gorm:"not null"`
	RecoveredMinor int64  `json:"recovered_minor,string" gorm:"not null"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime"`
}

type AgentCommissionLedger struct {
	ID                  int64  `json:"id" gorm:"primaryKey"`
	OperationKey        string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	UserID              int    `json:"user_id" gorm:"not null;index"`
	ActorUserID         int    `json:"actor_user_id" gorm:"not null"`
	Currency            string `json:"currency" gorm:"type:varchar(3);not null"`
	CurrencyExponent    int    `json:"currency_exponent" gorm:"not null"`
	Kind                string `json:"kind" gorm:"type:varchar(32);not null"`
	ReferenceType       string `json:"reference_type" gorm:"type:varchar(32);not null"`
	ReferenceID         string `json:"reference_id" gorm:"type:varchar(255);not null"`
	RequestID           string `json:"request_id" gorm:"type:varchar(96);index"`
	DeltaPendingMinor   int64  `json:"delta_pending_minor,string" gorm:"not null"`
	DeltaAvailableMinor int64  `json:"delta_available_minor,string" gorm:"not null"`
	DeltaReservedMinor  int64  `json:"delta_reserved_minor,string" gorm:"not null"`
	DeltaHeldMinor      int64  `json:"delta_held_minor,string" gorm:"not null"`
	DeltaDebtMinor      int64  `json:"delta_debt_minor,string" gorm:"not null"`
	AfterPendingMinor   int64  `json:"after_pending_minor,string" gorm:"not null"`
	AfterAvailableMinor int64  `json:"after_available_minor,string" gorm:"not null"`
	AfterReservedMinor  int64  `json:"after_reserved_minor,string" gorm:"not null"`
	AfterHeldMinor      int64  `json:"after_held_minor,string" gorm:"not null"`
	AfterDebtMinor      int64  `json:"after_debt_minor,string" gorm:"not null"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentCommissionLedger) BeforeUpdate(*gorm.DB) error { return ErrAgentCommissionImmutable }
func (*AgentCommissionLedger) BeforeDelete(*gorm.DB) error { return ErrAgentCommissionImmutable }

func agentCommissionHash(value any) (string, error) {
	data, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

// Fraction products use arbitrary precision, then enforce the cash boundary.
// This function must not be used to convert wallet quota: that has its own
// strict conversion contract in common.WalletQuotaFromDecimalStrict.
func agentMinorFraction(value, numerator, denominator int64) (int64, error) {
	if value < 0 || value > MaxAgentMoneyMinor || numerator < 0 || denominator <= 0 {
		return 0, ErrAgentCommissionInvalid
	}
	result := new(big.Int).Mul(big.NewInt(value), big.NewInt(numerator))
	result.Quo(result, big.NewInt(denominator))
	if !result.IsInt64() || result.Int64() > MaxAgentMoneyMinor {
		return 0, ErrAgentCommissionInvalid
	}
	return result.Int64(), nil
}

func lockAgentCommissionAccount(tx *gorm.DB, userID int, currency string, exponent int) (*AgentCommissionAccount, error) {
	if userID <= 0 || !validAgentCurrency(currency, exponent) {
		return nil, ErrAgentCommissionInvalid
	}
	seed := AgentCommissionAccount{UserID: userID, Currency: currency, CurrencyExponent: exponent, Version: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return nil, err
	}
	var account AgentCommissionAccount
	if err := lockForUpdate(tx).Where("user_id = ? AND currency = ?", userID, currency).First(&account).Error; err != nil {
		return nil, err
	}
	if account.CurrencyExponent != exponent {
		return nil, ErrAgentCommissionInvariant
	}
	return &account, nil
}

func validateAgentCommissionAccount(a *AgentCommissionAccount) error {
	for _, value := range []int64{a.PendingMinor, a.AvailableMinor, a.ReservedMinor, a.HeldMinor, a.DebtMinor, a.EarnedMinor, a.ReversedMinor, a.TransferredMinor, a.DebtRecoveredMinor} {
		if value < 0 || value > MaxAgentMoneyMinor {
			return ErrAgentCommissionInvariant
		}
	}
	// Earned - reversed - transferred + unrecovered debt must equal the
	// remaining liability. Debt recovery consumes liabilities, not new cash.
	left := new(big.Int).SetInt64(a.EarnedMinor)
	left.Sub(left, big.NewInt(a.ReversedMinor)).Sub(left, big.NewInt(a.TransferredMinor)).Add(left, big.NewInt(a.DebtMinor))
	right := new(big.Int).SetInt64(a.PendingMinor)
	right.Add(right, big.NewInt(a.AvailableMinor)).Add(right, big.NewInt(a.ReservedMinor)).Add(right, big.NewInt(a.HeldMinor))
	if left.Cmp(right) != 0 {
		return ErrAgentCommissionInvariant
	}
	return nil
}

func saveAgentCommissionAccount(tx *gorm.DB, before AgentCommissionAccount, after *AgentCommissionAccount, kind, referenceType, referenceID, requestID string, actorID int) error {
	if err := validateAgentCommissionAccount(after); err != nil {
		return err
	}
	key, err := agentCommissionHash([]any{kind, referenceType, referenceID, after.UserID, after.Currency})
	if err != nil {
		return err
	}
	row := AgentCommissionLedger{OperationKey: key, UserID: after.UserID, ActorUserID: actorID, Currency: after.Currency, CurrencyExponent: after.CurrencyExponent, Kind: kind, ReferenceType: referenceType, ReferenceID: referenceID, RequestID: requestID,
		DeltaPendingMinor: after.PendingMinor - before.PendingMinor, DeltaAvailableMinor: after.AvailableMinor - before.AvailableMinor, DeltaReservedMinor: after.ReservedMinor - before.ReservedMinor, DeltaHeldMinor: after.HeldMinor - before.HeldMinor, DeltaDebtMinor: after.DebtMinor - before.DebtMinor,
		AfterPendingMinor: after.PendingMinor, AfterAvailableMinor: after.AvailableMinor, AfterReservedMinor: after.ReservedMinor, AfterHeldMinor: after.HeldMinor, AfterDebtMinor: after.DebtMinor}
	if err := tx.Create(&row).Error; err != nil {
		return err
	}
	after.Version++
	return tx.Save(after).Error
}

func matureAgentCommissionAccountTx(tx *gorm.DB, account *AgentCommissionAccount, now int64) error {
	var awards []AgentCommissionAward
	if err := lockForUpdate(tx).Where("user_id = ? AND currency = ? AND matured = ? AND matures_at <= ?", account.UserID, account.Currency, false, now).Order("id").Find(&awards).Error; err != nil {
		return err
	}
	for i := range awards {
		award := &awards[i]
		before := *account
		reserve, err := agentMinorFraction(award.PendingMinor, award.ReserveBPS, 10_000)
		if err != nil {
			return err
		}
		released := award.PendingMinor - reserve
		available, err := recoverAgentCommissionDebtTx(tx, account, released)
		if err != nil {
			return err
		}
		award.SpentMinor += released - available
		account.PendingMinor -= award.PendingMinor
		account.ReservedMinor += reserve
		account.AvailableMinor += available
		award.AvailableMinor += available
		award.ReservedMinor += reserve
		award.PendingMinor = 0
		award.Matured = true
		award.Version++
		if err := tx.Save(award).Error; err != nil {
			return err
		}
		if err := saveAgentCommissionAccount(tx, before, account, "mature", "award", fmt.Sprint(award.ID), "", 0); err != nil {
			return err
		}
	}
	return nil
}

// recoverAgentCommissionDebtTx consumes newly matured or explicitly released
// value, or a definitely unpaid returned hold, before any remainder becomes
// available. It never touches pending reserve or UNKNOWN/paying holds.
func recoverAgentCommissionDebtTx(tx *gorm.DB, account *AgentCommissionAccount, amount int64) (int64, error) {
	remaining := amount
	var debts []AgentCommissionDebt
	if err := lockForUpdate(tx).Where("user_id = ? AND currency = ? AND amount_minor > recovered_minor", account.UserID, account.Currency).Order("id").Find(&debts).Error; err != nil {
		return 0, err
	}
	for i := range debts {
		take := min(remaining, debts[i].AmountMinor-debts[i].RecoveredMinor)
		if take == 0 {
			break
		}
		debts[i].RecoveredMinor += take
		account.DebtMinor -= take
		account.DebtRecoveredMinor += take
		remaining -= take
		if err := tx.Save(&debts[i]).Error; err != nil {
			return 0, err
		}
	}
	return remaining, nil
}
