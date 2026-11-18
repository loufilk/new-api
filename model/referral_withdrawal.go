package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/sha3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ReferralWithdrawalPending         = "pending"
	ReferralWithdrawalApproved        = "approved"
	ReferralWithdrawalPaying          = "paying"
	ReferralWithdrawalPaid            = "paid"
	ReferralWithdrawalRejected        = "rejected"
	ReferralWithdrawalCancelled       = "cancelled"
	ReferralUSDTPrecision       int64 = 1_000_000
)

var (
	ErrReferralWithdrawalInvalid    = errors.New("Invalid withdrawal details")
	ErrReferralWithdrawalConflict   = errors.New("This withdrawal changed. Reload and try again.")
	ErrReferralWithdrawalDisabled   = errors.New("Referral withdrawals are currently closed")
	ErrReferralWithdrawalIneligible = errors.New("The withdrawal eligibility requirements are not met")
	ErrReferralWithdrawalForbidden  = errors.New("Unauthorized, insufficient privileges")
	ErrReferralWithdrawalReceipt    = errors.New("This payment transaction has already been recorded")
	ErrReferralWithdrawalUnsettled  = errors.New("Resolve pending referral withdrawals before deleting this account")
	referralEVMAddress              = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	referralHexHash                 = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
)

// Conversion is an explicit administrator policy, never the display currency or
// a live exchange-rate estimate. USDT is stored in millionths; quotas are integers.
type ReferralWithdrawalConfig struct {
	ID                 int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Revision           int64  `json:"revision" gorm:"not null"`
	Enabled            bool   `json:"enabled"`
	MinConsumedQuota   int    `json:"min_consumed_quota" gorm:"type:bigint;not null"`
	MinWithdrawalQuota int    `json:"min_withdrawal_quota" gorm:"type:bigint;not null"`
	QuotaPerUSDT       int    `json:"quota_per_usdt" gorm:"type:bigint;not null"`
	BSCFeeBPS          int    `json:"bsc_fee_bps"`
	PolygonFeeBPS      int    `json:"polygon_fee_bps"`
	TRC20FeeBPS        int    `json:"trc20_fee_bps"`
	Instructions       string `json:"instructions" gorm:"type:text"`
	UpdatedAt          int64  `json:"updated_at" gorm:"autoUpdateTime:false"`
	UpdatedBy          int    `json:"updated_by"`
}

type ReferralWithdrawal struct {
	ID               int64                     `json:"id" gorm:"primaryKey"`
	UserID           int                       `json:"user_id" gorm:"not null;index;uniqueIndex:idx_referral_withdrawal_request,priority:1"`
	RequestKey       string                    `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_referral_withdrawal_request,priority:2"`
	RequestHash      string                    `json:"-" gorm:"type:char(64);not null"`
	AmountQuota      int                       `json:"amount_quota" gorm:"type:bigint;not null"`
	Network          string                    `json:"network" gorm:"type:varchar(16);not null"`
	Address          string                    `json:"address" gorm:"type:varchar(64);not null"`
	QuotaPerUSDT     int                       `json:"quota_per_usdt" gorm:"type:bigint;not null"`
	GrossUSDTMicro   int64                     `json:"gross_usdt_micro,string" gorm:"type:bigint;not null"`
	FeeUSDTMicro     int64                     `json:"fee_usdt_micro,string" gorm:"type:bigint;not null"`
	NetUSDTMicro     int64                     `json:"net_usdt_micro,string" gorm:"type:bigint;not null"`
	FeeBPS           int                       `json:"fee_bps" gorm:"not null"`
	PolicyRevision   int64                     `json:"policy_revision" gorm:"not null"`
	Status           string                    `json:"status" gorm:"type:varchar(16);not null;index"`
	Revision         int64                     `json:"revision" gorm:"not null"`
	ReviewedBy       int                       `json:"reviewed_by"`
	PaymentStartedBy int                       `json:"payment_started_by"`
	PaidBy           int                       `json:"paid_by"`
	TransactionHash  string                    `json:"transaction_hash" gorm:"type:varchar(66)"`
	ReviewNote       string                    `json:"review_note" gorm:"type:varchar(1000)"`
	CreatedAt        int64                     `json:"created_at" gorm:"autoCreateTime:false;index"`
	UpdatedAt        int64                     `json:"updated_at" gorm:"autoUpdateTime:false"`
	PaidAt           int64                     `json:"paid_at"`
	Events           []ReferralWithdrawalEvent `json:"events,omitempty" gorm:"-"`
}

// The immutable event journal records both reservation and final balance deltas.
// It is separate from balance_ledger because aff_quota is not a spendable wallet.
type ReferralWithdrawalEvent struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	WithdrawalID int64  `json:"withdrawal_id" gorm:"not null;index;uniqueIndex:idx_referral_withdrawal_action,priority:1"`
	ActorID      int    `json:"actor_id" gorm:"not null;uniqueIndex:idx_referral_withdrawal_action,priority:2"`
	RequestKey   string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex:idx_referral_withdrawal_action,priority:3"`
	RequestHash  string `json:"-" gorm:"type:char(64);not null"`
	UserID       int    `json:"user_id" gorm:"not null;index"`
	Action       string `json:"action" gorm:"type:varchar(24);not null"`
	FromStatus   string `json:"from_status" gorm:"type:varchar(16)"`
	ToStatus     string `json:"to_status" gorm:"type:varchar(16)"`
	AmountDelta  int    `json:"amount_delta" gorm:"type:bigint;not null"`
	FrozenDelta  int    `json:"frozen_delta" gorm:"type:bigint;not null"`
	BalanceAfter int    `json:"balance_after" gorm:"type:bigint;not null"`
	Note         string `json:"note" gorm:"type:text"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime:false"`
}

type ReferralWithdrawalReceipt struct {
	ID              int64  `gorm:"primaryKey"`
	WithdrawalID    int64  `gorm:"not null;uniqueIndex"`
	Network         string `gorm:"type:varchar(16);not null;uniqueIndex:idx_referral_withdrawal_receipt,priority:1"`
	TransactionHash string `gorm:"type:varchar(66);not null;uniqueIndex:idx_referral_withdrawal_receipt,priority:2"`
	CreatedAt       int64  `gorm:"autoCreateTime:false"`
}

func (*ReferralWithdrawalEvent) BeforeUpdate(*gorm.DB) error   { return ErrLedgerImmutable }
func (*ReferralWithdrawalEvent) BeforeDelete(*gorm.DB) error   { return ErrLedgerImmutable }
func (*ReferralWithdrawalReceipt) BeforeUpdate(*gorm.DB) error { return ErrLedgerImmutable }
func (*ReferralWithdrawalReceipt) BeforeDelete(*gorm.DB) error { return ErrLedgerImmutable }

func ReferralWithdrawalModels() []any {
	return []any{&ReferralWithdrawalConfig{}, &ReferralWithdrawal{}, &ReferralWithdrawalEvent{}, &ReferralWithdrawalReceipt{}}
}

type ReferralWithdrawalInput struct {
	AmountQuota    int    `json:"amount_quota"`
	Network        string `json:"network"`
	Address        string `json:"address"`
	PolicyRevision int64  `json:"policy_revision"`
}

type ReferralWithdrawalAction struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Action           string `json:"action"`
	Note             string `json:"note"`
	TransactionHash  string `json:"transaction_hash"`
}

type ReferralWithdrawalSummary struct {
	Config         ReferralWithdrawalConfig `json:"config"`
	AvailableQuota int64                    `json:"available_quota"`
	ReservedQuota  int64                    `json:"reserved_quota"`
	ConsumedQuota  int                      `json:"consumed_quota"`
	Eligible       bool                     `json:"eligible"`
}

func DefaultReferralWithdrawalConfig() ReferralWithdrawalConfig {
	return ReferralWithdrawalConfig{ID: 1, MinWithdrawalQuota: 5_000_000, QuotaPerUSDT: 500_000}
}

func GetReferralWithdrawalConfig(ctx context.Context) (*ReferralWithdrawalConfig, error) {
	config := DefaultReferralWithdrawalConfig()
	err := DB.WithContext(ctx).Where("id = ?", 1).Limit(1).Find(&config).Error
	return &config, err
}

type ReferralWithdrawalPermissionCheck func(*gorm.DB, *User) error

func referralWithdrawalActor(tx *gorm.DB, identity AuthSessionIdentity, admin bool, permissions ...ReferralWithdrawalPermissionCheck) (*User, error) {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return nil, ErrUserSessionInactive
	}
	if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
		return nil, err
	}
	var user User
	if err := tx.First(&user, identity.UserID).Error; err != nil {
		return nil, err
	}
	if !common.IsValidateRole(user.Role) || admin && user.Role < common.RoleAdminUser {
		return nil, ErrReferralWithdrawalForbidden
	}
	if admin {
		if len(permissions) == 0 && user.Role != common.RoleRootUser {
			return nil, ErrReferralWithdrawalForbidden
		}
		if len(permissions) > 1 || len(permissions) == 1 && permissions[0] == nil {
			return nil, ErrReferralWithdrawalForbidden
		}
		if len(permissions) == 1 {
			if err := permissions[0](tx, &user); err != nil {
				return nil, err
			}
		}
	}
	return &user, nil
}

func SaveReferralWithdrawalConfig(ctx context.Context, identity AuthSessionIdentity, input ReferralWithdrawalConfig, permissions ...ReferralWithdrawalPermissionCheck) (*ReferralWithdrawalConfig, error) {
	input.Instructions = strings.TrimSpace(input.Instructions)
	if input.Revision < 0 || input.MinConsumedQuota < 0 || input.MinConsumedQuota > common.MaxWalletQuota || input.MinWithdrawalQuota <= 0 || input.MinWithdrawalQuota > common.MaxWalletQuota || input.QuotaPerUSDT < 1 || input.QuotaPerUSDT > 1_000_000_000_000 || input.BSCFeeBPS < 0 || input.BSCFeeBPS >= 10000 || input.PolygonFeeBPS < 0 || input.PolygonFeeBPS >= 10000 || input.TRC20FeeBPS < 0 || input.TRC20FeeBPS >= 10000 || !commerceText(input.Instructions, 0, 3000) {
		return nil, ErrReferralWithdrawalInvalid
	}
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := referralWithdrawalActor(tx, identity, true, permissions...); err != nil {
			return err
		}
		config := DefaultReferralWithdrawalConfig()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&config).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&config, 1).Error; err != nil {
			return err
		}
		if config.Revision != input.Revision {
			return ErrReferralWithdrawalConflict
		}
		input.ID, input.Revision, input.UpdatedBy, input.UpdatedAt = 1, config.Revision+1, identity.UserID, common.GetTimestamp()
		if err := tx.Save(&input).Error; err != nil {
			return err
		}
		encoded, err := common.Marshal(input)
		if err != nil {
			return err
		}
		return tx.Create(&ReferralWithdrawalEvent{ActorID: identity.UserID, RequestKey: fmt.Sprintf("settings:%d", input.Revision), Action: "settings", Note: string(encoded), CreatedAt: input.UpdatedAt}).Error
	})
	return &input, err
}

func ReadReferralWithdrawalConfig(ctx context.Context, identity AuthSessionIdentity, permissions ...ReferralWithdrawalPermissionCheck) (*ReferralWithdrawalConfig, error) {
	result := DefaultReferralWithdrawalConfig()
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := referralWithdrawalActor(tx, identity, true, permissions...); err != nil {
			return err
		}
		return tx.Where("id = ?", 1).Limit(1).Find(&result).Error
	})
	return &result, err
}

func NormalizeReferralWithdrawalInput(input ReferralWithdrawalInput) (ReferralWithdrawalInput, error) {
	input.Network = strings.ToUpper(strings.TrimSpace(input.Network))
	input.Address = strings.TrimSpace(input.Address)
	if input.AmountQuota <= 0 || input.AmountQuota > common.MaxWalletQuota || input.PolicyRevision < 0 {
		return input, ErrReferralWithdrawalInvalid
	}
	switch input.Network {
	case "BSC", "POLYGON":
		if !validReferralEVMAddress(input.Address) {
			return input, ErrReferralWithdrawalInvalid
		}
		input.Address = strings.ToLower(input.Address)
	case "TRC20":
		if !validReferralTronAddress(input.Address) {
			return input, ErrReferralWithdrawalInvalid
		}
	default:
		return input, ErrReferralWithdrawalInvalid
	}
	return input, nil
}

func validReferralEVMAddress(value string) bool {
	if !referralEVMAddress.MatchString(value) || strings.EqualFold(value, "0x"+strings.Repeat("0", 40)) {
		return false
	}
	address := value[2:]
	if address == strings.ToLower(address) || address == strings.ToUpper(address) {
		return true
	}
	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(strings.ToLower(address)))
	digest := hex.EncodeToString(hash.Sum(nil))
	for i, char := range address {
		if char >= '0' && char <= '9' {
			continue
		}
		uppercase := digest[i] >= '8'
		if uppercase != (char >= 'A' && char <= 'F') {
			return false
		}
	}
	return true
}

func validReferralTronAddress(value string) bool {
	if len(value) != 34 || value[0] != 'T' {
		return false
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	decoded := new(big.Int)
	for _, char := range value {
		digit := strings.IndexRune(alphabet, char)
		if digit < 0 {
			return false
		}
		decoded.Mul(decoded, big.NewInt(58)).Add(decoded, big.NewInt(int64(digit)))
	}
	bytes := decoded.Bytes()
	if len(bytes) != 25 || bytes[0] != 0x41 {
		return false
	}
	first := sha256.Sum256(bytes[:21])
	second := sha256.Sum256(first[:])
	return slices.Equal(bytes[21:], second[:4]) && !slices.Equal(bytes[1:21], make([]byte, 20))
}

func referralWithdrawalHash(value any) (string, error) {
	payload, err := common.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func referralWithdrawalQuote(config ReferralWithdrawalConfig, input ReferralWithdrawalInput) (*ReferralWithdrawal, error) {
	if !config.Enabled {
		return nil, ErrReferralWithdrawalDisabled
	}
	if config.Revision != input.PolicyRevision {
		return nil, ErrReferralWithdrawalConflict
	}
	if input.AmountQuota < config.MinWithdrawalQuota || config.QuotaPerUSDT <= 0 {
		return nil, ErrReferralWithdrawalIneligible
	}
	feeBPS := config.BSCFeeBPS
	if input.Network == "POLYGON" {
		feeBPS = config.PolygonFeeBPS
	} else if input.Network == "TRC20" {
		feeBPS = config.TRC20FeeBPS
	}
	grossDecimal := decimal.NewFromInt(int64(input.AmountQuota)).Mul(decimal.NewFromInt(ReferralUSDTPrecision)).Div(decimal.NewFromInt(int64(config.QuotaPerUSDT)))
	if !grossDecimal.Equal(grossDecimal.Truncate(0)) {
		return nil, ErrReferralWithdrawalInvalid
	}
	gross, err := common.WalletQuotaFromDecimalStrict(grossDecimal)
	if err != nil || gross <= 0 {
		return nil, ErrReferralWithdrawalInvalid
	}
	fee, err := common.WalletQuotaFromDecimalStrict(grossDecimal.Mul(decimal.NewFromInt(int64(feeBPS))).Div(decimal.NewFromInt(10000)).Ceil())
	if err != nil || fee < 0 || fee >= gross {
		return nil, ErrReferralWithdrawalInvalid
	}
	return &ReferralWithdrawal{AmountQuota: input.AmountQuota, Network: input.Network, Address: input.Address, QuotaPerUSDT: config.QuotaPerUSDT, GrossUSDTMicro: int64(gross), FeeUSDTMicro: int64(fee), NetUSDTMicro: int64(gross - fee), FeeBPS: feeBPS, PolicyRevision: config.Revision}, nil
}

// All reservation reads happen after the same users row lock as aff_transfer,
// reward reversal and payout, so the two destinations cannot spend one reward.
func referralWithdrawalReservedTx(tx *gorm.DB, userID int) (int64, error) {
	var rows []ReferralWithdrawal
	if err := lockForUpdate(tx).Select("amount_quota").Where("user_id = ? AND status IN ?", userID, []string{ReferralWithdrawalPending, ReferralWithdrawalApproved, ReferralWithdrawalPaying}).Find(&rows).Error; err != nil {
		return 0, err
	}
	var total int64
	for _, row := range rows {
		if row.AmountQuota <= 0 || row.AmountQuota > common.MaxWalletQuota || total > int64(common.MaxWalletQuota-row.AmountQuota) {
			return 0, ErrAgentCommissionInvariant
		}
		total += int64(row.AmountQuota)
	}
	return total, nil
}

// Account deletion shares the reservation lock. An external payment in flight
// must retain an owner row until its receipt and final debit have committed.
func ensureNoPendingReferralWithdrawalsTx(tx *gorm.DB, userID int) error {
	var user User
	if err := lockForUpdate(tx.Unscoped()).Select("id").First(&user, userID).Error; err != nil {
		return err
	}
	reserved, err := referralWithdrawalReservedTx(tx, userID)
	if err != nil {
		return err
	}
	if reserved > 0 {
		return ErrReferralWithdrawalUnsettled
	}
	return nil
}

func GetReferralWithdrawalSummary(ctx context.Context, identity AuthSessionIdentity) (*ReferralWithdrawalSummary, error) {
	var result ReferralWithdrawalSummary
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := referralWithdrawalActor(tx, identity, false)
		if err != nil {
			return err
		}
		config := DefaultReferralWithdrawalConfig()
		if err := tx.Where("id = ?", 1).Limit(1).Find(&config).Error; err != nil {
			return err
		}
		available, err := referralAvailableQuotaTx(tx, identity.UserID)
		if err != nil {
			return err
		}
		reserved, err := referralWithdrawalReservedTx(tx, identity.UserID)
		if err != nil {
			return err
		}
		result = ReferralWithdrawalSummary{Config: config, AvailableQuota: available, ReservedQuota: reserved, ConsumedQuota: user.UsedQuota, Eligible: config.Enabled && user.UsedQuota >= config.MinConsumedQuota && available >= int64(config.MinWithdrawalQuota)}
		return nil
	})
	return &result, err
}

func QuoteReferralWithdrawal(ctx context.Context, identity AuthSessionIdentity, input ReferralWithdrawalInput) (*ReferralWithdrawal, error) {
	normalized, err := NormalizeReferralWithdrawalInput(input)
	if err != nil {
		return nil, err
	}
	summary, err := GetReferralWithdrawalSummary(ctx, identity)
	if err != nil {
		return nil, err
	}
	if summary.ConsumedQuota < summary.Config.MinConsumedQuota || summary.AvailableQuota < int64(normalized.AmountQuota) {
		return nil, ErrReferralWithdrawalIneligible
	}
	return referralWithdrawalQuote(summary.Config, normalized)
}

func CreateReferralWithdrawal(ctx context.Context, identity AuthSessionIdentity, key string, input ReferralWithdrawalInput) (*ReferralWithdrawal, error) {
	if !ValidReferralTransferIdempotencyKey(key) {
		return nil, ErrReferralWithdrawalInvalid
	}
	input, err := NormalizeReferralWithdrawalInput(input)
	if err != nil {
		return nil, err
	}
	hash, err := referralWithdrawalHash(input)
	if err != nil {
		return nil, err
	}
	var result ReferralWithdrawal
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		user, err := referralWithdrawalActor(tx, identity, false)
		if err != nil {
			return err
		}
		prior := tx.Where("user_id = ? AND request_key = ?", identity.UserID, key).First(&result)
		if prior.Error == nil {
			if result.RequestHash != hash {
				return ErrReferralWithdrawalConflict
			}
			return nil
		}
		if !errors.Is(prior.Error, gorm.ErrRecordNotFound) {
			return prior.Error
		}
		config := DefaultReferralWithdrawalConfig()
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&config).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&config, 1).Error; err != nil {
			return err
		}
		quote, err := referralWithdrawalQuote(config, input)
		if err != nil {
			return err
		}
		available, err := referralAvailableQuotaTx(tx, identity.UserID)
		if err != nil {
			return err
		}
		if user.UsedQuota < config.MinConsumedQuota || available < int64(input.AmountQuota) {
			return ErrReferralWithdrawalIneligible
		}
		result = *quote
		result.UserID, result.RequestKey, result.RequestHash = identity.UserID, key, hash
		result.Status, result.Revision, result.CreatedAt = ReferralWithdrawalPending, 1, common.GetTimestamp()
		result.UpdatedAt = result.CreatedAt
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return tx.Create(&ReferralWithdrawalEvent{WithdrawalID: result.ID, ActorID: identity.UserID, UserID: identity.UserID, RequestKey: key, RequestHash: hash, Action: "created", ToStatus: result.Status, FrozenDelta: result.AmountQuota, BalanceAfter: user.AffQuota, CreatedAt: result.CreatedAt}).Error
	})
	return &result, err
}

func NormalizeReferralWithdrawalAction(input ReferralWithdrawalAction) (ReferralWithdrawalAction, error) {
	input.Note, input.TransactionHash = strings.TrimSpace(input.Note), strings.ToLower(strings.TrimSpace(input.TransactionHash))
	if input.ExpectedRevision <= 0 || !slices.Contains([]string{"approve", "reject", "cancel", "begin_payment", "paid"}, input.Action) || !commerceText(input.Note, 0, 1000) || len(input.TransactionHash) > 66 {
		return input, ErrReferralWithdrawalInvalid
	}
	if input.Action == "reject" && input.Note == "" {
		return input, ErrReferralWithdrawalInvalid
	}
	if input.Action != "paid" && input.TransactionHash != "" {
		return input, ErrReferralWithdrawalInvalid
	}
	return input, nil
}

func ReviewReferralWithdrawal(ctx context.Context, identity AuthSessionIdentity, id int64, key string, input ReferralWithdrawalAction, admin bool, permissions ...ReferralWithdrawalPermissionCheck) (*ReferralWithdrawal, error) {
	if id <= 0 || !ValidReferralTransferIdempotencyKey(key) {
		return nil, ErrReferralWithdrawalInvalid
	}
	input, err := NormalizeReferralWithdrawalAction(input)
	if err != nil {
		return nil, err
	}
	hash, err := referralWithdrawalHash(input)
	if err != nil {
		return nil, err
	}
	var result ReferralWithdrawal
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Discover ownership without locks, then use a stable actor/user order to
		// avoid two administrators reviewing each other's requests in a deadlock.
		if err := tx.Select("id", "user_id").First(&result, id).Error; err != nil {
			return err
		}
		if !admin && result.UserID != identity.UserID {
			return gorm.ErrRecordNotFound
		}
		ids := []int{identity.UserID, result.UserID}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		var users []User
		if err := lockForUpdate(tx).Where("id IN ?", ids).Order("id").Find(&users).Error; err != nil {
			return err
		}
		if _, err := referralWithdrawalActor(tx, identity, admin, permissions...); err != nil {
			return err
		}
		if admin && result.UserID == identity.UserID {
			return ErrReferralWithdrawalForbidden
		}
		if !admin && input.Action != "cancel" {
			return ErrReferralWithdrawalForbidden
		}
		if err := lockForUpdate(tx).First(&result, id).Error; err != nil {
			return err
		}
		var prior ReferralWithdrawalEvent
		priorErr := tx.Where("withdrawal_id = ? AND actor_id = ? AND request_key = ?", id, identity.UserID, key).First(&prior).Error
		if priorErr == nil {
			if prior.RequestHash != hash {
				return ErrReferralWithdrawalConflict
			}
			return nil
		}
		if !errors.Is(priorErr, gorm.ErrRecordNotFound) {
			return priorErr
		}
		if result.Revision != input.ExpectedRevision {
			return ErrReferralWithdrawalConflict
		}
		var owner User
		if err := tx.Select("id", "aff_quota").First(&owner, result.UserID).Error; err != nil {
			return err
		}
		from, balanceDelta, frozenDelta := result.Status, 0, 0
		switch input.Action {
		case "cancel":
			if admin || result.Status != ReferralWithdrawalPending {
				return ErrReferralWithdrawalConflict
			}
			result.Status, frozenDelta = ReferralWithdrawalCancelled, -result.AmountQuota
		case "reject":
			if !slices.Contains([]string{ReferralWithdrawalPending, ReferralWithdrawalApproved}, result.Status) {
				return ErrReferralWithdrawalConflict
			}
			result.Status, frozenDelta = ReferralWithdrawalRejected, -result.AmountQuota
			result.ReviewedBy = identity.UserID
		case "approve", "begin_payment":
			expected := ReferralWithdrawalPending
			if input.Action == "begin_payment" {
				expected = ReferralWithdrawalApproved
			}
			if result.Status != expected {
				return ErrReferralWithdrawalConflict
			}
			available, err := referralAvailableQuotaTx(tx, result.UserID)
			if err != nil {
				return err
			}
			if available < 0 {
				return ErrReferralWithdrawalIneligible
			}
			if input.Action == "approve" {
				result.Status, result.ReviewedBy = ReferralWithdrawalApproved, identity.UserID
			} else {
				result.Status, result.PaymentStartedBy = ReferralWithdrawalPaying, identity.UserID
			}
		case "paid":
			if result.Status != ReferralWithdrawalPaying {
				return ErrReferralWithdrawalConflict
			}
			hashText := strings.TrimPrefix(input.TransactionHash, "0x")
			if !referralHexHash.MatchString(hashText) {
				return ErrReferralWithdrawalInvalid
			}
			transaction := hashText
			if result.Network != "TRC20" {
				transaction = "0x" + hashText
			}
			receipt := ReferralWithdrawalReceipt{WithdrawalID: id, Network: result.Network, TransactionHash: transaction, CreatedAt: common.GetTimestamp()}
			created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt)
			if created.Error != nil {
				return created.Error
			}
			if created.RowsAffected != 1 {
				return ErrReferralWithdrawalReceipt
			}
			// A refund may create referral debt while a human is paying. Record the
			// completed payment exactly once, preserving that debt instead of losing
			// an external payment because its original reward was later reversed.
			if owner.AffQuota < -common.MaxWalletQuota+result.AmountQuota {
				return ErrAgentCommissionInvariant
			}
			update := tx.Model(&User{}).Where("id = ? AND aff_quota >= ?", result.UserID, -common.MaxWalletQuota+result.AmountQuota).UpdateColumn("aff_quota", gorm.Expr("aff_quota - ?", result.AmountQuota))
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return ErrAgentCommissionInvariant
			}
			owner.AffQuota -= result.AmountQuota
			result.Status, result.TransactionHash, result.PaidBy, result.PaidAt = ReferralWithdrawalPaid, transaction, identity.UserID, receipt.CreatedAt
			balanceDelta, frozenDelta = -result.AmountQuota, -result.AmountQuota
		}
		result.ReviewNote, result.UpdatedAt, result.Revision = input.Note, common.GetTimestamp(), result.Revision+1
		if err := tx.Save(&result).Error; err != nil {
			return err
		}
		return tx.Create(&ReferralWithdrawalEvent{WithdrawalID: id, ActorID: identity.UserID, UserID: result.UserID, RequestKey: key, RequestHash: hash, Action: input.Action, FromStatus: from, ToStatus: result.Status, AmountDelta: balanceDelta, FrozenDelta: frozenDelta, BalanceAfter: owner.AffQuota, Note: input.Note, CreatedAt: result.UpdatedAt}).Error
	})
	return &result, err
}

func ListReferralWithdrawals(ctx context.Context, identity AuthSessionIdentity, admin bool, status string, page, size int, permissions ...ReferralWithdrawalPermissionCheck) ([]ReferralWithdrawal, int64, error) {
	if page < 1 || page > maxWalletPage || size < 1 || size > 100 || status != "" && !slices.Contains([]string{ReferralWithdrawalPending, ReferralWithdrawalApproved, ReferralWithdrawalPaying, ReferralWithdrawalPaid, ReferralWithdrawalRejected, ReferralWithdrawalCancelled}, status) {
		return nil, 0, ErrReferralWithdrawalInvalid
	}
	rows := []ReferralWithdrawal{}
	var total int64
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := referralWithdrawalActor(tx, identity, admin, permissions...); err != nil {
			return err
		}
		query := tx.Model(&ReferralWithdrawal{})
		if !admin {
			query = query.Where("user_id = ?", identity.UserID)
		}
		if status != "" {
			query = query.Where("status = ?", status)
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		return query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error
	})
	return rows, total, err
}

func GetReferralWithdrawal(ctx context.Context, identity AuthSessionIdentity, id int64, admin bool, permissions ...ReferralWithdrawalPermissionCheck) (*ReferralWithdrawal, error) {
	if id <= 0 {
		return nil, ErrReferralWithdrawalInvalid
	}
	var result ReferralWithdrawal
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := referralWithdrawalActor(tx, identity, admin, permissions...); err != nil {
			return err
		}
		query := tx.Where("id = ?", id)
		if !admin {
			query = query.Where("user_id = ?", identity.UserID)
		}
		if err := query.First(&result).Error; err != nil {
			return err
		}
		return tx.Where("withdrawal_id = ?", id).Order("id").Find(&result.Events).Error
	})
	return &result, err
}
