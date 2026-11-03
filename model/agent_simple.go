package model

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AgentSimpleStatusActive                  = "active"
	AgentSimpleStatusPaused                  = "paused"
	AgentSimpleStatusCancelled               = "cancelled"
	AgentSimpleCodeDirect                    = "direct"
	AgentSimpleCodeCredit                    = "credit"
	AgentSimpleCodePayment                   = "payment"
	AgentSimpleSourceCash                    = "cash"
	AgentSimpleSourceReward                  = "reward"
	AgentSimpleSourceRoot                    = "root"
	AgentSimpleSourcePayment                 = "payment"
	AgentSimpleSourceCredit                  = "credit"
	AgentSimpleAllocationReserved            = "reserved"
	AgentSimpleAllocationSpent               = "spent"
	AgentSimpleAllocationReleased            = "released"
	AgentSimplePaymentPending                = "pending"
	AgentSimplePaymentPaid                   = "paid"
	AgentSimplePaymentRefunded               = "refunded"
	AgentSimplePaymentExpired                = "expired"
	AgentSimplePaymentClosed                 = "closed"
	AgentSimplePaymentRedeemed               = "redeemed"
	AgentSimpleWithdrawalMinimumQuota  int64 = 20 * 500_000
	AgentSimpleWithdrawalFeeBPS              = 200
	AgentSimpleWithdrawalPerMonthLimit       = 1
	AgentSimplePaymentMaturity               = 5 * 24 * 60 * 60
)

var (
	ErrAgentSimpleInvalid         = errors.New("invalid simplified agent request")
	ErrAgentSimplePermission      = errors.New("simplified agent permission denied")
	ErrAgentSimpleUnavailable     = errors.New("simplified agent is unavailable")
	ErrAgentSimpleInsufficient    = errors.New("insufficient agent balance or credit")
	ErrAgentSimpleConflict        = errors.New("simplified agent request conflicts with an earlier operation")
	ErrAgentSimpleState           = errors.New("simplified agent code state changed")
	ErrAgentSimpleInvariant       = errors.New("simplified agent ledger invariant failed")
	ErrAgentSimpleImmutable       = errors.New("simplified agent ledger is immutable")
	ErrAgentSimplePaymentNotFound = errors.New("simplified payment order not found")
)

type AgentSimpleAccount struct {
	AgentUserID             int    `json:"agent_user_id" gorm:"primaryKey;autoIncrement:false"`
	Status                  string `json:"status" gorm:"type:varchar(16);not null"`
	CashQuota               int64  `json:"cash_quota" gorm:"type:bigint;not null;default:0"`
	RewardQuota             int64  `json:"reward_quota" gorm:"type:bigint;not null;default:0"`
	RootQuota               int64  `json:"root_quota" gorm:"type:bigint;not null;default:0"`
	CreditLimitQuota        int64  `json:"credit_limit_quota" gorm:"type:bigint;not null;default:0"`
	CreditReservedQuota     int64  `json:"credit_reserved_quota" gorm:"type:bigint;not null;default:0"`
	CreditOutstandingQuota  int64  `json:"credit_outstanding_quota" gorm:"type:bigint;not null;default:0"`
	PaymentPendingQuota     int64  `json:"payment_pending_quota" gorm:"type:bigint;not null;default:0"`
	PaymentAvailableQuota   int64  `json:"payment_available_quota" gorm:"type:bigint;not null;default:0"`
	PaymentTotalQuota       int64  `json:"payment_total_quota" gorm:"type:bigint;not null;default:0"`
	PaymentDebtQuota        int64  `json:"payment_debt_quota" gorm:"type:bigint;not null;default:0"`
	PaymentQuota            int64  `json:"payment_quota" gorm:"type:bigint;not null;default:0"`
	PaymentReversedQuota    int64  `json:"payment_reversed_quota" gorm:"type:bigint;not null;default:0"`
	PaymentWithdrawnQuota   int64  `json:"payment_withdrawn_quota" gorm:"type:bigint;not null;default:0"`
	PaymentTransferredQuota int64  `json:"payment_transferred_quota" gorm:"type:bigint;not null;default:0"`
	PaymentOffsetQuota      int64  `json:"payment_offset_quota" gorm:"type:bigint;not null;default:0"`
	Version                 int64  `json:"version" gorm:"not null;default:1"`
	LastActivityAt          int64  `json:"last_activity_at" gorm:"not null;default:0"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt               int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

type AgentSimpleLedger struct {
	DeltaCreditLimit        int64  `json:"delta_credit_limit" gorm:"type:bigint;not null;default:0"`
	CreditLimitAfter        int64  `json:"credit_limit_after" gorm:"type:bigint;not null;default:0"`
	ID                      int64  `json:"id" gorm:"primaryKey"`
	EventKey                string `json:"-" gorm:"type:varchar(160);not null;uniqueIndex"`
	AgentUserID             int    `json:"agent_user_id" gorm:"not null;index"`
	ActorUserID             int    `json:"actor_user_id" gorm:"not null"`
	Kind                    string `json:"kind" gorm:"type:varchar(32);not null"`
	ReferenceType           string `json:"reference_type" gorm:"type:varchar(32);not null"`
	ReferenceID             string `json:"reference_id" gorm:"type:varchar(128);not null"`
	RequestID               string `json:"request_id" gorm:"type:varchar(96)"`
	DeltaCashQuota          int64  `json:"delta_cash_quota" gorm:"type:bigint;not null"`
	DeltaRewardQuota        int64  `json:"delta_reward_quota" gorm:"type:bigint;not null"`
	DeltaRootQuota          int64  `json:"delta_root_quota" gorm:"type:bigint;not null"`
	DeltaCreditReserved     int64  `json:"delta_credit_reserved" gorm:"type:bigint;not null"`
	DeltaCreditOutstanding  int64  `json:"delta_credit_outstanding" gorm:"type:bigint;not null"`
	DeltaPaymentPending     int64  `json:"delta_payment_pending" gorm:"type:bigint;not null"`
	DeltaPaymentAvailable   int64  `json:"delta_payment_available" gorm:"type:bigint;not null"`
	DeltaPaymentTotal       int64  `json:"delta_payment_total" gorm:"type:bigint;not null"`
	DeltaPaymentDebt        int64  `json:"delta_payment_debt" gorm:"type:bigint;not null"`
	DeltaPaymentQuota       int64  `json:"delta_payment_quota" gorm:"type:bigint;not null"`
	DeltaPaymentReversed    int64  `json:"delta_payment_reversed" gorm:"type:bigint;not null"`
	DeltaPaymentWithdrawn   int64  `json:"delta_payment_withdrawn" gorm:"type:bigint;not null"`
	DeltaPaymentTransferred int64  `json:"delta_payment_transferred" gorm:"type:bigint;not null"`
	DeltaPaymentOffset      int64  `json:"delta_payment_offset" gorm:"type:bigint;not null"`
	CashAfter               int64  `json:"cash_after" gorm:"type:bigint;not null"`
	RewardAfter             int64  `json:"reward_after" gorm:"type:bigint;not null"`
	RootAfter               int64  `json:"root_after" gorm:"type:bigint;not null"`
	CreditReservedAfter     int64  `json:"credit_reserved_after" gorm:"type:bigint;not null"`
	CreditOutstandingAfter  int64  `json:"credit_outstanding_after" gorm:"type:bigint;not null"`
	PaymentPendingAfter     int64  `json:"payment_pending_after" gorm:"type:bigint;not null"`
	PaymentAvailableAfter   int64  `json:"payment_available_after" gorm:"type:bigint;not null"`
	PaymentTotalAfter       int64  `json:"payment_total_after" gorm:"type:bigint;not null"`
	PaymentDebtAfter        int64  `json:"payment_debt_after" gorm:"type:bigint;not null"`
	PaymentAfter            int64  `json:"payment_after" gorm:"type:bigint;not null"`
	PaymentReversedAfter    int64  `json:"payment_reversed_after" gorm:"type:bigint;not null"`
	PaymentWithdrawnAfter   int64  `json:"payment_withdrawn_after" gorm:"type:bigint;not null"`
	PaymentTransferredAfter int64  `json:"payment_transferred_after" gorm:"type:bigint;not null"`
	PaymentOffsetAfter      int64  `json:"payment_offset_after" gorm:"type:bigint;not null"`
	Reason                  string `json:"reason" gorm:"type:varchar(255);not null"`
	CreatedAt               int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (*AgentSimpleLedger) BeforeUpdate(*gorm.DB) error { return ErrAgentSimpleImmutable }
func (*AgentSimpleLedger) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

type AgentSimpleCodeAllocation struct {
	ID           int64  `json:"id" gorm:"primaryKey"`
	AgentUserID  int    `json:"agent_user_id" gorm:"not null;index"`
	RedemptionID int    `json:"redemption_id" gorm:"not null;index"`
	Source       string `json:"source" gorm:"type:varchar(16);not null"`
	Quota        int64  `json:"quota" gorm:"type:bigint;not null"`
	State        string `json:"state" gorm:"type:varchar(16);not null;index"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentSimpleCodeAllocation) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

// AgentSimplePaymentOrder is the public payment-code order. It creates a
// normal redemption code only after the payment callback is verified; before
// then it consumes neither agent balance nor credit.
type AgentSimplePaymentOrder struct {
	RefundEvidence   string `json:"-" gorm:"type:varchar(255);not null;default:''"`
	RefundedBy       int    `json:"-" gorm:"not null;default:0"`
	ID               int64  `json:"id" gorm:"primaryKey"`
	RequestKey       string `json:"-" gorm:"type:varchar(128);not null;uniqueIndex"`
	PublicID         string `json:"public_id" gorm:"type:varchar(36);not null;uniqueIndex"`
	TokenHash        string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	TokenCipher      string `json:"-" gorm:"type:text;not null"`
	AgentUserID      int    `json:"agent_user_id" gorm:"not null;index"`
	Name             string `json:"name" gorm:"type:varchar(80);not null"`
	Quota            int64  `json:"quota" gorm:"type:bigint;not null"`
	SalePriceFen     int64  `json:"sale_price_fen" gorm:"not null"`
	CostPriceFen     int64  `json:"cost_price_fen" gorm:"not null"`
	FeeFen           int64  `json:"fee_fen" gorm:"not null"`
	ProfitQuota      int64  `json:"profit_quota" gorm:"type:bigint;not null"`
	PaymentMethod    string `json:"payment_method" gorm:"type:varchar(50);not null"`
	PaymentProvider  string `json:"payment_provider" gorm:"type:varchar(50);not null"`
	TradeNo          string `json:"trade_no" gorm:"type:varchar(255);not null;uniqueIndex"`
	PaymentReceiptID int64  `json:"-" gorm:"type:bigint;not null;default:0"`
	PaymentURL       string `json:"-" gorm:"type:text;not null"`
	PaymentParams    string `json:"-" gorm:"type:text;not null"`
	Status           string `json:"status" gorm:"type:varchar(16);not null;index"`
	ExpiresAt        int64  `json:"expires_at" gorm:"not null;index"`
	MaturesAt        int64  `json:"matures_at" gorm:"not null;index"`
	PaidAt           int64  `json:"paid_at" gorm:"not null"`
	DeliveredAt      int64  `json:"delivered_at" gorm:"not null"`
	RefundedAt       int64  `json:"refunded_at" gorm:"not null"`
	RefundReason     string `json:"refund_reason" gorm:"type:varchar(255);not null"`
	RedemptionID     int    `json:"redemption_id" gorm:"not null;index"`
	ProfitMatured    bool   `json:"profit_matured" gorm:"not null"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (*AgentSimplePaymentOrder) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

func (order *AgentSimplePaymentOrder) BeforeCreate(*gorm.DB) error {
	return validateAgentSimplePaymentOrder(order)
}

type AgentSimplePaymentFee struct {
	PaymentMethod string `json:"payment_method" gorm:"type:varchar(50);primaryKey"`
	FeeBPS        int    `json:"fee_bps" gorm:"not null"`
	Enabled       bool   `json:"enabled" gorm:"not null"`
	Version       int64  `json:"version" gorm:"not null"`
	UpdatedBy     int    `json:"updated_by" gorm:"not null"`
	UpdatedAt     int64  `json:"updated_at" gorm:"not null"`
}

type AgentSimplePaymentFeeAudit struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	RequestID     string `json:"request_id" gorm:"type:varchar(96);uniqueIndex"`
	PaymentMethod string `json:"payment_method" gorm:"type:varchar(50);not null;index"`
	OldFeeBPS     int    `json:"old_fee_bps" gorm:"not null"`
	FeeBPS        int    `json:"fee_bps" gorm:"not null"`
	Enabled       bool   `json:"enabled" gorm:"not null"`
	ActorUserID   int    `json:"actor_user_id" gorm:"not null"`
	Reason        string `json:"reason" gorm:"type:varchar(255);not null"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentSimplePaymentFeeAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentSimpleImmutable }
func (*AgentSimplePaymentFeeAudit) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

type AgentSimpleCodeRequest struct {
	RequestID string `json:"request_id"`
	Name      string `json:"name"`
	Quota     int64  `json:"quota"`
	Mode      string `json:"mode"`
	ExpiresAt int64  `json:"expires_at"`
}

type AgentSimplePaymentRequest struct {
	FeeVersion    int64  `json:"fee_version,omitempty"`
	RequestID     string `json:"request_id"`
	Name          string `json:"name"`
	Quota         int64  `json:"quota"`
	SalePriceFen  int64  `json:"sale_price_fen"`
	PaymentMethod string `json:"payment_method"`
}

type AgentSimplePaymentRefundRequest struct {
	RequestID string `json:"request_id"`
	OrderID   int64  `json:"order_id"`
	Reason    string `json:"reason"`
	Evidence  string `json:"evidence"`
}

type AgentSimplePaymentTransferRequest struct {
	RequestID string `json:"request_id"`
	Quota     int64  `json:"quota"`
	Reason    string `json:"reason"`
}

type AgentSimplePaymentWithdrawalRequest struct {
	RequestID   string `json:"request_id"`
	AgentUserID int    `json:"agent_user_id"`
	Quota       int64  `json:"quota"`
	Reason      string `json:"reason"`
	Evidence    string `json:"evidence"`
}

type AgentSimplePaymentTransferCommand struct {
	RequestID string `json:"request_id"`
	Quota     int64  `json:"quota"`
	Reason    string `json:"reason"`
}

type AgentSimplePaymentRefundCommand struct {
	RequestID string `json:"request_id"`
	OrderID   int64  `json:"order_id"`
	Reason    string `json:"reason"`
	Evidence  string `json:"evidence"`
}

type AgentSimplePaymentWithdrawal struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	RequestID     string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	AgentUserID   int    `json:"agent_user_id" gorm:"not null;index"`
	ActorUserID   int    `json:"actor_user_id" gorm:"not null"`
	Quota         int64  `json:"quota" gorm:"type:bigint;not null"`
	ReferralQuota int64  `json:"referral_quota" gorm:"type:bigint;not null;default:0"`
	PaymentQuota  int64  `json:"payment_quota" gorm:"type:bigint;not null;default:0"`
	FeeQuota      int64  `json:"fee_quota" gorm:"type:bigint;not null"`
	NetQuota      int64  `json:"net_quota" gorm:"type:bigint;not null"`
	Reason        string `json:"reason" gorm:"type:varchar(255);not null"`
	Evidence      string `json:"evidence" gorm:"type:varchar(255);not null"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime;index"`
}

func (*AgentSimplePaymentWithdrawal) BeforeUpdate(*gorm.DB) error { return ErrAgentSimpleImmutable }
func (*AgentSimplePaymentWithdrawal) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

type AgentSimpleCodeCancelRequest struct {
	RequestID string `json:"request_id"`
	CodeID    int    `json:"code_id"`
	Version   int64  `json:"version"`
	Reason    string `json:"reason"`
}
type AgentSimpleAdjustmentRequest struct {
	RequestID   string `json:"request_id"`
	AgentUserID int    `json:"agent_user_id"`
	Delta       int64  `json:"delta"`
	Reason      string `json:"reason"`
}
type AgentSimpleCreditLimitRequest struct {
	RequestID   string `json:"request_id"`
	AgentUserID int    `json:"agent_user_id"`
	LimitQuota  int64  `json:"limit_quota"`
	Reason      string `json:"reason"`
}
type AgentSimpleRepaymentRequest struct {
	RequestID   string `json:"request_id"`
	AgentUserID int    `json:"agent_user_id"`
	Quota       int64  `json:"quota"`
	Reason      string `json:"reason"`
}

type AgentSimpleStatusRequest struct {
	RequestID   string `json:"request_id"`
	AgentUserID int    `json:"agent_user_id"`
	Status      string `json:"status"`
	Reason      string `json:"reason"`
}

type AgentSimpleAccountView struct {
	OverdueQuota            int64  `json:"overdue_quota"`
	AgentUserID             int    `json:"agent_user_id"`
	Status                  string `json:"status"`
	CashQuota               int64  `json:"cash_quota"`
	RewardQuota             int64  `json:"reward_quota"`
	RootQuota               int64  `json:"root_quota"`
	CreditLimitQuota        int64  `json:"credit_limit_quota,omitempty"`
	CreditReservedQuota     int64  `json:"credit_reserved_quota"`
	CreditOutstandingQuota  int64  `json:"credit_outstanding_quota"`
	PaymentPendingQuota     int64  `json:"payment_pending_quota"`
	PaymentAvailableQuota   int64  `json:"payment_available_quota"`
	PaymentTotalQuota       int64  `json:"payment_total_quota"`
	PaymentDebtQuota        int64  `json:"payment_debt_quota"`
	PaymentQuota            int64  `json:"payment_quota"`
	PaymentReversedQuota    int64  `json:"payment_reversed_quota"`
	PaymentWithdrawnQuota   int64  `json:"payment_withdrawn_quota"`
	PaymentTransferredQuota int64  `json:"payment_transferred_quota"`
	PaymentOffsetQuota      int64  `json:"payment_offset_quota"`
	Version                 int64  `json:"version"`
	AvailableQuota          int64  `json:"available_quota"`
	AvailableCreditQuota    int64  `json:"available_credit_quota"`
	AmountDueQuota          int64  `json:"amount_due_quota"`
	ReservedQuota           int64  `json:"reserved_quota"`
}
type AgentSimpleRecord struct {
	ID          int    `json:"id"`
	AgentUserID int    `json:"agent_user_id"`
	Name        string `json:"name"`
	Key         string `json:"key,omitempty"`
	MaskedKey   string `json:"masked_key"`
	Quota       int    `json:"quota"`
	Mode        string `json:"mode"`
	Status      int    `json:"status"`
	ExpiredAt   int64  `json:"expired_at"`
	RedeemedAt  int64  `json:"redeemed_at"`
	CreatedAt   int64  `json:"created_at"`
	Version     int64  `json:"version"`
	CostQuota   int64  `json:"cost_quota"`
}
type AgentSimpleSummary struct {
	Account      AgentSimpleAccountView `json:"account"`
	Records      []AgentSimpleRecord    `json:"records"`
	TotalRecords int64                  `json:"total_records"`
}

type AgentSimplePaymentOrderView struct {
	RefundReason        string `json:"refund_reason,omitempty"`
	RefundEvidence      string `json:"refund_evidence,omitempty"`
	RefundedBy          int    `json:"refunded_by,omitempty"`
	RefundedAt          int64  `json:"refunded_at"`
	MinimumSalePriceFen int64  `json:"minimum_sale_price_fen"`
	FeeVersion          int64  `json:"fee_version"`
	PriceValid          bool   `json:"price_valid"`
	MaturesAt           int64  `json:"matures_at"`
	ProfitMatured       bool   `json:"profit_matured"`
	ID                  int64  `json:"id"`
	AgentUserID         int    `json:"agent_user_id"`
	PublicID            string `json:"public_id"`
	Name                string `json:"name"`
	Quota               int64  `json:"quota"`
	SalePriceFen        int64  `json:"sale_price_fen"`
	CostPriceFen        int64  `json:"cost_price_fen"`
	FeeFen              int64  `json:"fee_fen"`
	ProfitQuota         int64  `json:"profit_quota"`
	Status              string `json:"status"`
	CustomerURL         string `json:"customer_url,omitempty"`
	ExpiresAt           int64  `json:"expires_at"`
	PaidAt              int64  `json:"paid_at"`
	DeliveredAt         int64  `json:"delivered_at"`
	RedemptionID        int    `json:"redemption_id"`
	CreatedAt           int64  `json:"created_at"`
	RedemptionKey       string `json:"redemption_key,omitempty"`
	TradeNo             string `json:"-"`
	PaymentURL          string `json:"-"`
	PaymentParams       string `json:"-"`
}

type AgentSimplePublicPaymentView struct {
	PublicID      string `json:"public_id"`
	Name          string `json:"name"`
	Quota         int64  `json:"quota"`
	SalePriceFen  int64  `json:"sale_price_fen"`
	Status        string `json:"status"`
	ExpiresAt     int64  `json:"expires_at"`
	PaymentURL    string `json:"payment_url,omitempty"`
	PaymentParams string `json:"payment_params,omitempty"`
	RedemptionKey string `json:"redemption_key,omitempty"`
	CreatedAt     int64  `json:"created_at"`
}

func AgentSimpleModels() []any {
	return []any{&AgentSimpleAccount{}, &AgentSimpleLedger{}, &AgentSimpleCodeAllocation{}, &AgentSimplePaymentOrder{}, &AgentSimplePaymentFee{}, &AgentSimplePaymentFeeAudit{}, &AgentSimplePaymentWithdrawal{}}
}

type AgentSimplePaymentFeeRequest struct {
	RequestID     string `json:"request_id"`
	PaymentMethod string `json:"payment_method"`
	FeeBPS        int    `json:"fee_bps"`
	Enabled       bool   `json:"enabled"`
	Reason        string `json:"reason"`
}

func SetAgentSimpleStatus(identity AuthSessionIdentity, request AgentSimpleStatusRequest) (*AgentSimpleAccountView, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.AgentUserID <= 0 || (request.Status != AgentSimpleStatusActive && request.Status != AgentSimpleStatusPaused && request.Status != AgentSimpleStatusCancelled) || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 255 {
		return nil, ErrAgentSimpleInvalid
	}
	var view *AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		scope := RedemptionScope{UserID: actor.Id, Role: actor.Role}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_status", request.RequestID, scope, request.AgentUserID, request)
		if err != nil {
			return err
		}
		if replay {
			view, err = loadAgentSimpleAccountOperationTx(tx, operation)
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, request.AgentUserID, true)
		if err != nil {
			return err
		}
		var target User
		if err := lockForUpdate(tx).First(&target, request.AgentUserID).Error; err != nil || target.Role >= common.RoleAdminUser || target.Status != common.UserStatusEnabled {
			return ErrAgentSimplePermission
		}
		if request.Status == AgentSimpleStatusCancelled && (account.Status != AgentSimpleStatusPaused || account.CreditReservedQuota != 0 || account.CreditOutstandingQuota != 0 || account.PaymentDebtQuota != 0) {
			return ErrAgentSimpleConflict
		}
		profile, err := LockAgentProfileTx(tx, target.Id)
		if err != nil {
			return err
		}
		previous := AgentProfile{UserID: target.Id}
		if profile != nil {
			previous = *profile
		}
		profile = &AgentProfile{UserID: target.Id, Version: previous.Version + 1, Status: AgentProfileActive, SalesFrozen: request.Status != AgentSimpleStatusActive, UpdatedBy: actor.Id, Reason: request.Reason, CreatedAt: previous.CreatedAt}
		if request.Status == AgentSimpleStatusCancelled {
			profile.Status = AgentProfileExited
		}
		if err := tx.Save(profile).Error; err != nil {
			return err
		}
		if err := tx.Create(&AgentProfileAudit{UserID: target.Id, Version: profile.Version, ActorUserID: actor.Id, PreviousStatus: previous.Status, Status: profile.Status, PreviousSalesFrozen: previous.SalesFrozen, SalesFrozen: profile.SalesFrozen, PreviousSettlementFrozen: previous.SettlementFrozen, Reason: request.Reason}).Error; err != nil {
			return err
		}
		before := *account
		account.Status = request.Status
		if request.Status == AgentSimpleStatusCancelled {
			account.RootQuota, account.CreditLimitQuota = 0, 0
		}
		if request.Status != AgentSimpleStatusActive {
			if err := tx.Model(&AgentSimplePaymentOrder{}).Where("agent_user_id = ? AND status = ?", request.AgentUserID, AgentSimplePaymentPending).Update("status", AgentSimplePaymentClosed).Error; err != nil {
				return err
			}
		}
		if err := saveAgentSimpleAccountTx(tx, before, account, actor.Id, "status_change", "agent", strconv.Itoa(request.AgentUserID), strings.TrimSpace(request.Reason), request.RequestID); err != nil {
			return err
		}
		view = agentSimpleAccountView(account, true)
		return storeAgentSimpleAccountOperationTx(tx, operation, account)
	})
	return view, err
}

func agentSimpleTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func sealAgentSimplePaymentToken(token string) (string, error) {
	key := sha256.Sum256([]byte("wanchuan-agent-payment-link-v1:" + common.SessionSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, []byte(token), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func openAgentSimplePaymentToken(sealed string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil {
		return "", ErrAgentSimpleInvariant
	}
	key := sha256.Sum256([]byte("wanchuan-agent-payment-link-v1:" + common.SessionSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(data) < aead.NonceSize()+aead.Overhead() {
		return "", ErrAgentSimpleInvariant
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], nil)
	if err != nil {
		return "", ErrAgentSimpleInvariant
	}
	return string(plain), nil
}

func validateAgentSimplePaymentToken(token string) error {
	if len(token) < 32 || len(token) > 128 {
		return ErrAgentSimpleInvalid
	}
	return nil
}

func validateAgentSimplePaymentOrder(order *AgentSimplePaymentOrder) error {
	if order == nil || order.AgentUserID <= 0 || order.Quota <= 0 || order.SalePriceFen <= 0 || order.CostPriceFen <= 0 || order.ProfitQuota < 0 || order.ExpiresAt <= 0 || order.Status == "" {
		return ErrAgentSimpleInvariant
	}
	if order.SalePriceFen < order.CostPriceFen || order.ProfitQuota > int64(common.MaxWalletQuota) {
		return ErrAgentSimpleInvariant
	}
	if order.Status != AgentSimplePaymentPending && order.Status != AgentSimplePaymentPaid && order.Status != AgentSimplePaymentRefunded && order.Status != AgentSimplePaymentExpired && order.Status != AgentSimplePaymentClosed && order.Status != AgentSimplePaymentRedeemed {
		return ErrAgentSimpleInvariant
	}
	return nil
}

func lockAgentSimplePaymentFeeTx(tx *gorm.DB, paymentMethod string) (*AgentSimplePaymentFee, error) {
	if strings.TrimSpace(paymentMethod) == "" || len(paymentMethod) > 50 {
		return nil, ErrAgentSimpleInvalid
	}
	seed := AgentSimplePaymentFee{PaymentMethod: paymentMethod, Version: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return nil, err
	}
	var fee AgentSimplePaymentFee
	if err := lockForUpdate(tx).Where("payment_method = ?", paymentMethod).First(&fee).Error; err != nil {
		return nil, err
	}
	if fee.FeeBPS < 0 || fee.FeeBPS > 10_000 || fee.Version <= 0 {
		return nil, ErrAgentSimpleInvariant
	}
	if !fee.Enabled {
		return nil, ErrAgentSimpleUnavailable
	}
	return &fee, nil
}

func agentSimpleCostPriceFen(quota int64) (int64, error) {
	if validateAgentSimpleAmount(quota) != nil || common.QuotaPerUnit <= 0 {
		return 0, ErrAgentSimpleInvalid
	}
	cost := decimal.NewFromInt(quota).
		Mul(decimal.NewFromInt(90)).
		Div(decimal.NewFromFloat(common.QuotaPerUnit)).
		Ceil().IntPart()
	if cost < 1 {
		return 0, ErrAgentSimpleInvalid
	}
	return cost, nil
}

func agentSimplePaymentProfitQuota(profitFen int64) (int64, error) {
	if profitFen < 0 {
		return 0, ErrAgentSimpleInvalid
	}
	quota, err := common.WalletQuotaFromDecimalStrict(
		decimal.NewFromInt(profitFen).
			Div(decimal.NewFromInt(100)).
			Mul(decimal.NewFromFloat(common.QuotaPerUnit)),
	)
	return int64(quota), err
}

func QuoteAgentSimplePayment(paymentMethod string, quota, salePriceFen int64) (*AgentSimplePaymentOrderView, error) {
	if validateAgentSimpleAmount(quota) != nil || salePriceFen <= 0 || salePriceFen > int64(common.MaxWalletQuota) {
		return nil, ErrAgentSimpleInvalid
	}
	var result *AgentSimplePaymentOrderView
	err := DB.Transaction(func(tx *gorm.DB) error {
		fee, err := lockAgentSimplePaymentFeeTx(tx, paymentMethod)
		if err != nil {
			return err
		}
		result, err = agentSimplePaymentQuote(fee, quota, salePriceFen)
		return err
	})
	return result, err
}

func agentSimplePaymentQuote(fee *AgentSimplePaymentFee, quota, salePriceFen int64) (*AgentSimplePaymentOrderView, error) {
	if fee.FeeBPS >= 10_000 || fee.FeeBPS < 0 {
		return nil, ErrAgentSimpleUnavailable
	}
	costFen, err := agentSimpleCostPriceFen(quota)
	if err != nil {
		return nil, err
	}
	minimum := decimal.NewFromInt(costFen).Mul(decimal.NewFromInt(10_000)).Div(decimal.NewFromInt(int64(10_000 - fee.FeeBPS))).Ceil()
	if minimum.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return nil, ErrAgentSimpleInvalid
	}
	feeFen := decimal.NewFromInt(salePriceFen).Mul(decimal.NewFromInt(int64(fee.FeeBPS))).Div(decimal.NewFromInt(10_000)).Ceil().IntPart()
	quote := &AgentSimplePaymentOrderView{Quota: quota, SalePriceFen: salePriceFen, CostPriceFen: costFen, FeeFen: feeFen, MinimumSalePriceFen: minimum.IntPart(), FeeVersion: fee.Version, PriceValid: salePriceFen >= minimum.IntPart()}
	if !quote.PriceValid {
		return quote, ErrAgentSimpleInvalid
	}
	quote.ProfitQuota, err = agentSimplePaymentProfitQuota(salePriceFen - costFen - feeFen)
	return quote, err
}

func ListAgentSimplePaymentFees(identity AuthSessionIdentity) ([]AgentSimplePaymentFee, error) {
	var rows []AgentSimplePaymentFee
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		for _, method := range operation_setting.PayMethods {
			if isSimpleEpayMethod(method["type"]) && method["type"] != "" {
				if _, err := lockAgentSimplePaymentFeeTxIncludingDisabled(tx, method["type"]); err != nil {
					return err
				}
			}
		}
		return tx.Order("payment_method ASC").Find(&rows).Error
	})
	return rows, err
}

func UpdateAgentSimplePaymentFee(identity AuthSessionIdentity, request AgentSimplePaymentFeeRequest) (*AgentSimplePaymentFee, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || strings.TrimSpace(request.PaymentMethod) != request.PaymentMethod || len(request.PaymentMethod) == 0 || len(request.PaymentMethod) > 50 || request.FeeBPS < 0 || request.FeeBPS > 10_000 || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 255 {
		return nil, ErrAgentSimpleInvalid
	}
	var result AgentSimplePaymentFee
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		scope := RedemptionScope{UserID: actor.Id, Role: actor.Role}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_payment_fee", request.RequestID, scope, 0, request)
		if err != nil {
			return err
		}
		if replay {
			if common.UnmarshalJsonStr(operation.Result, &result) != nil || result.PaymentMethod != request.PaymentMethod {
				return ErrAgentSimpleConflict
			}
			return nil
		}
		fee, err := lockAgentSimplePaymentFeeTxIncludingDisabled(tx, request.PaymentMethod)
		if err != nil {
			return err
		}
		if fee.Version == math.MaxInt64 {
			return ErrAgentSimpleInvariant
		}
		audit := AgentSimplePaymentFeeAudit{RequestID: request.RequestID, PaymentMethod: request.PaymentMethod, OldFeeBPS: fee.FeeBPS, FeeBPS: request.FeeBPS, Enabled: request.Enabled, ActorUserID: identity.UserID, Reason: strings.TrimSpace(request.Reason)}
		if err := tx.Create(&audit).Error; err != nil {
			return err
		}
		fee.FeeBPS, fee.Enabled, fee.Version = request.FeeBPS, request.Enabled, fee.Version+1
		fee.UpdatedBy, fee.UpdatedAt = identity.UserID, common.GetTimestamp()
		if err := tx.Model(fee).Updates(map[string]any{"fee_bps": fee.FeeBPS, "enabled": fee.Enabled, "version": fee.Version, "updated_by": fee.UpdatedBy, "updated_at": fee.UpdatedAt}).Error; err != nil {
			return err
		}
		encoded, err := common.Marshal(fee)
		if err != nil {
			return err
		}
		if err := tx.Model(operation).Update("result", string(encoded)).Error; err != nil {
			return err
		}
		result = *fee
		return nil
	})
	return &result, err
}

func GetAgentSimpleAvailablePaymentMethods(identity AuthSessionIdentity, purpose string) ([]AgentSimplePaymentFee, error) {
	if identity.UserID <= 0 {
		return nil, ErrAgentSimplePermission
	}
	if purpose == "" {
		purpose = "payment"
	}
	if purpose != "payment" && purpose != "recharge" && purpose != "repayment" {
		return nil, ErrAgentSimpleInvalid
	}
	fees := make([]AgentSimplePaymentFee, 0)
	err := DB.Transaction(func(tx *gorm.DB) error {
		if purpose == "repayment" {
			if err := requireAgentSimpleHistoryTx(tx, identity, identity.UserID); err != nil {
				return err
			}
			account, err := lockAgentSimpleAccountTx(tx, identity.UserID, false)
			if err != nil || account.Status == AgentSimpleStatusCancelled {
				return ErrAgentSimpleUnavailable
			}
		} else {
			if err := requireAgentSimpleSalesTx(tx, identity, identity.UserID); err != nil {
				return err
			}
		}
		for _, configured := range operation_setting.PayMethods {
			method := configured["type"]
			if method == "" || !operation_setting.ContainsPayMethod(method) || !isSimpleEpayMethod(method) {
				continue
			}
			if purpose != "payment" {
				fees = append(fees, AgentSimplePaymentFee{PaymentMethod: method, Enabled: true})
				continue
			}
			fee, err := lockAgentSimplePaymentFeeTxIncludingDisabled(tx, method)
			if err != nil {
				return err
			}
			if fee.Enabled {
				fees = append(fees, *fee)
			}
		}
		return nil
	})
	return fees, err
}

func isSimpleEpayMethod(method string) bool {
	switch method {
	case PaymentMethodStripe, PaymentMethodCreem, PaymentMethodWaffo, PaymentMethodWaffoPancake, PaymentMethodBalance:
		return false
	default:
		return true
	}
}

func lockAgentSimplePaymentFeeTxIncludingDisabled(tx *gorm.DB, paymentMethod string) (*AgentSimplePaymentFee, error) {
	if strings.TrimSpace(paymentMethod) == "" || len(paymentMethod) > 50 {
		return nil, ErrAgentSimpleInvalid
	}
	seed := AgentSimplePaymentFee{PaymentMethod: paymentMethod, Version: 1}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return nil, err
	}
	var fee AgentSimplePaymentFee
	if err := lockForUpdate(tx).Where("payment_method = ?", paymentMethod).First(&fee).Error; err != nil {
		return nil, err
	}
	if fee.FeeBPS < 0 || fee.FeeBPS > 10_000 || fee.Version <= 0 {
		return nil, ErrAgentSimpleInvariant
	}
	return &fee, nil
}

func simplePaymentOrderView(order *AgentSimplePaymentOrder, customerURL string) *AgentSimplePaymentOrderView {
	return &AgentSimplePaymentOrderView{MaturesAt: order.MaturesAt, ProfitMatured: order.ProfitMatured, PriceValid: true, ID: order.ID, AgentUserID: order.AgentUserID, PublicID: order.PublicID, Name: order.Name, Quota: order.Quota, SalePriceFen: order.SalePriceFen, CostPriceFen: order.CostPriceFen, FeeFen: order.FeeFen, ProfitQuota: order.ProfitQuota, Status: order.Status, CustomerURL: customerURL, ExpiresAt: order.ExpiresAt, PaidAt: order.PaidAt, DeliveredAt: order.DeliveredAt, RedemptionID: order.RedemptionID, CreatedAt: order.CreatedAt, TradeNo: order.TradeNo, PaymentURL: order.PaymentURL, PaymentParams: order.PaymentParams}
}

func GetAgentSimplePublicPayment(publicID, token string) (*AgentSimplePublicPaymentView, error) {
	if strings.TrimSpace(publicID) == "" || validateAgentSimplePaymentToken(token) != nil {
		return nil, ErrAgentSimpleInvalid
	}
	var order AgentSimplePaymentOrder
	if err := DB.Where("public_id = ?", publicID).First(&order).Error; err != nil {
		return nil, err
	}
	providedHash := []byte(agentSimpleTokenHash(token))
	if subtle.ConstantTimeCompare([]byte(order.TokenHash), providedHash) != 1 {
		return nil, ErrAgentSimplePermission
	}
	if order.Status == AgentSimplePaymentPending && order.ExpiresAt <= common.GetTimestamp() {
		_ = DB.Model(&order).Where("id = ? AND status = ?", order.ID, AgentSimplePaymentPending).Update("status", AgentSimplePaymentExpired).Error
		order.Status = AgentSimplePaymentExpired
	}
	if order.Status == AgentSimplePaymentPending {
		active, err := activeAgentSalesTx(DB, order.AgentUserID)
		if err != nil {
			return nil, err
		}
		if !active {
			order.Status = AgentSimplePaymentClosed
		}
	}
	result := &AgentSimplePublicPaymentView{PublicID: order.PublicID, Name: order.Name, Quota: order.Quota, SalePriceFen: order.SalePriceFen, Status: order.Status, ExpiresAt: order.ExpiresAt, CreatedAt: order.CreatedAt}
	if order.Status == AgentSimplePaymentPending {
		result.PaymentURL, result.PaymentParams = order.PaymentURL, order.PaymentParams
	}
	if order.RedemptionID > 0 && (order.Status == AgentSimplePaymentPaid || order.Status == AgentSimplePaymentRedeemed) {
		var redemption Redemption
		if err := DB.Select("key").First(&redemption, order.RedemptionID).Error; err != nil {
			return nil, err
		}
		result.RedemptionKey = redemption.Key
	}
	return result, nil
}

// SettleAgentSimplePaymentByTradeNo is called only after the payment provider
// signature has been verified by the host webhook. It keeps customer payment
// settlement separate from the ordinary wallet top-up path.
func SettleAgentSimplePaymentByTradeNo(tradeNo, paymentMethod, callbackMoney string, facts ...AgentVerifiedPaymentFacts) (bool, error) {
	var result bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = SettleAgentSimplePaymentCallbackTx(tx, tradeNo, paymentMethod, callbackMoney, common.GetTimestamp(), facts...)
		return err
	})
	return result, err
}

func SettleAgentSimplePaymentCallbackTx(tx *gorm.DB, tradeNo, paymentMethod, callbackMoney string, paidAt int64, facts ...AgentVerifiedPaymentFacts) (bool, error) {
	if tx == nil || strings.TrimSpace(tradeNo) == "" || strings.TrimSpace(callbackMoney) == "" || paidAt <= 0 {
		return false, ErrAgentSimpleInvalid
	}
	callback, err := decimal.NewFromString(strings.TrimSpace(callbackMoney))
	if err != nil || callback.LessThanOrEqual(decimal.Zero) || !callback.Equal(callback.Round(2)) {
		return false, ErrPaymentAmountMismatch
	}
	var order AgentSimplePaymentOrder
	if err := lockForUpdate(tx).Where("trade_no = ?", tradeNo).First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrAgentSimplePaymentNotFound
		}
		return false, err
	}
	if order.PaymentProvider != PaymentProviderEpay || order.PaymentMethod != paymentMethod {
		return false, ErrPaymentMethodMismatch
	}
	expected := decimal.NewFromInt(order.SalePriceFen).Div(decimal.NewFromInt(100))
	if !callback.Equal(expected) {
		return false, ErrPaymentAmountMismatch
	}
	if len(facts) != 1 || facts[0].Provider != PaymentProviderEpay || facts[0].Currency != "CNY" || facts[0].CurrencyExponent != 2 || facts[0].PaidMinor != order.SalePriceFen {
		return false, ErrAgentPaymentReceiptConflict
	}
	f := facts[0]
	receipt, err := ClaimAgentPaymentReceiptTx(tx, &AgentPaymentReceipt{Provider: f.Provider, ProviderReference: f.ExternalReference, MerchantScope: f.MerchantScope, Environment: f.Environment, Purpose: "payment_code", LocalOrderKey: order.TradeNo, OwnerUserID: order.AgentUserID, Currency: f.Currency, CurrencyExponent: f.CurrencyExponent, PaidMinor: f.PaidMinor, PaidAt: paidAt, VerifiedAt: common.GetTimestamp()})
	if err != nil {
		return false, err
	}
	if order.PaymentReceiptID != 0 && order.PaymentReceiptID != receipt.ID {
		return false, ErrAgentPaymentReceiptConflict
	}
	if order.PaymentReceiptID == 0 {
		if err := tx.Model(&order).Update("payment_receipt_id", receipt.ID).Error; err != nil {
			return false, err
		}
	}
	if order.Status == AgentSimplePaymentPaid || order.Status == AgentSimplePaymentRedeemed || order.Status == AgentSimplePaymentRefunded {
		return true, nil
	}
	if order.Status != AgentSimplePaymentPending && order.Status != AgentSimplePaymentExpired && order.Status != AgentSimplePaymentClosed {
		return false, ErrTopUpStatusInvalid
	}
	_, err = settleAgentSimplePaymentTx(tx, order.ID, 0, paidAt)
	return false, err
}

func settleAgentSimplePaymentTx(tx *gorm.DB, orderID int64, actorID int, paidAt int64) (*AgentSimplePaymentOrder, error) {
	var order AgentSimplePaymentOrder
	if err := lockForUpdate(tx).First(&order, orderID).Error; err != nil {
		return nil, err
	}
	if order.Status == AgentSimplePaymentPaid {
		return &order, nil
	}
	if order.Status != AgentSimplePaymentPending && order.Status != AgentSimplePaymentExpired && order.Status != AgentSimplePaymentClosed {
		return nil, ErrAgentSimpleState
	}
	account, err := lockAgentSimpleAccountTx(tx, order.AgentUserID, false)
	if err != nil {
		return nil, err
	}
	// A verified payment is a durable customer obligation, including a delayed
	// callback after the local link closed. New payment entry points stay closed.
	before := *account
	if account.PaymentPendingQuota > int64(common.MaxWalletQuota)-order.ProfitQuota || account.PaymentTotalQuota > int64(common.MaxWalletQuota)-order.ProfitQuota {
		return nil, ErrAgentSimpleInvariant
	}
	account.PaymentPendingQuota += order.ProfitQuota
	account.PaymentTotalQuota += order.ProfitQuota
	account.LastActivityAt = common.GetTimestamp()
	redemption := Redemption{UserId: order.AgentUserID, Name: order.Name, Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(), Quota: int(order.Quota), BudgetAgentID: order.AgentUserID, BudgetReserved: true, BudgetVersion: 1, AgentSimpleMode: AgentSimpleCodePayment, AgentSimplePaymentID: order.ID}
	if err := tx.Create(&redemption).Error; err != nil {
		return nil, err
	}
	if err := saveAgentSimpleAccountTx(tx, before, account, actorID, "payment_profit_pending", "payment_order", strconv.FormatInt(order.ID, 10), "Customer payment code profit pending", "payment:"+strconv.FormatInt(order.ID, 10)); err != nil {
		return nil, err
	}
	previousStatus := order.Status
	order.Status, order.PaidAt, order.DeliveredAt, order.RedemptionID = AgentSimplePaymentPaid, paidAt, common.GetTimestamp(), redemption.Id
	order.MaturesAt = paidAt + AgentSimplePaymentMaturity
	if err := tx.Model(&order).Where("id = ? AND status = ?", order.ID, previousStatus).Updates(map[string]any{"status": order.Status, "paid_at": order.PaidAt, "delivered_at": order.DeliveredAt, "redemption_id": order.RedemptionID, "matures_at": order.MaturesAt}).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

func SettleAgentSimplePayment(identity AuthSessionIdentity, orderID int64, paidAt int64) error {
	if orderID <= 0 || paidAt <= 0 {
		return ErrAgentSimpleInvalid
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		if actor.Role < common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		_, err = settleAgentSimplePaymentTx(tx, orderID, actor.Id, paidAt)
		return err
	})
}

func MatureAgentSimplePaymentProfits(ctx context.Context, limit int) (int, error) {
	limit = min(max(limit, 1), 200)
	var orders []AgentSimplePaymentOrder
	if err := DB.WithContext(ctx).Where("status IN ? AND profit_matured = ? AND matures_at > 0 AND matures_at <= ?", []string{AgentSimplePaymentPaid, AgentSimplePaymentRedeemed}, false, common.GetTimestamp()).Order("id").Limit(limit).Find(&orders).Error; err != nil {
		return 0, err
	}
	count := 0
	for i := range orders {
		order := orders[i]
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var locked AgentSimplePaymentOrder
			if err := lockForUpdate(tx).First(&locked, order.ID).Error; err != nil {
				return err
			}
			if (locked.Status != AgentSimplePaymentPaid && locked.Status != AgentSimplePaymentRedeemed) || locked.ProfitMatured || locked.MaturesAt > common.GetTimestamp() {
				return nil
			}
			account, err := lockAgentSimpleAccountTx(tx, locked.AgentUserID, false)
			if err != nil {
				return err
			}
			if account.PaymentPendingQuota < locked.ProfitQuota {
				return ErrAgentSimpleInvariant
			}
			before := *account
			account.PaymentPendingQuota -= locked.ProfitQuota
			repay := min(account.PaymentDebtQuota, locked.ProfitQuota)
			account.PaymentDebtQuota -= repay
			account.PaymentAvailableQuota += locked.ProfitQuota - repay
			if err := saveAgentSimpleAccountTx(tx, before, account, 0, "payment_profit_mature", "payment_order", strconv.FormatInt(locked.ID, 10), "Payment code profit matured", "mature:"+strconv.FormatInt(locked.ID, 10)); err != nil {
				return err
			}
			return tx.Model(&locked).Where("id = ? AND profit_matured = ?", locked.ID, false).Update("profit_matured", true).Error
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func TransferAgentSimplePaymentProfit(identity AuthSessionIdentity, request AgentSimplePaymentTransferCommand) (*AgentSimpleAccountView, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || validateAgentSimpleAmount(request.Quota) != nil || strings.TrimSpace(request.Reason) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	var view *AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil || actor.Role >= common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		if err := requireAgentSimpleSalesTx(tx, identity, identity.UserID); err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_payment_transfer", request.RequestID, RedemptionScope{UserID: actor.Id, Role: actor.Role}, actor.Id, request)
		if err != nil {
			return err
		}
		if replay {
			encoded, err := loadAgentSimpleAccountOperationTx(tx, operation)
			if err != nil {
				return err
			}
			view = encoded
			return nil
		}
		account, err := lockAgentSimpleAccountTx(tx, actor.Id, false)
		if err != nil {
			return err
		}
		if err := offsetAgentSimpleIncomeTx(tx, account, actor.Id, request.RequestID); err != nil {
			return err
		}
		if account.PaymentAvailableQuota < request.Quota {
			return ErrAgentSimpleInsufficient
		}
		if account.Status != AgentSimpleStatusActive || account.CreditOutstandingQuota > 0 || account.PaymentDebtQuota > 0 {
			return ErrAgentSimpleUnavailable
		}
		before := *account
		account.PaymentAvailableQuota -= request.Quota
		account.PaymentQuota += request.Quota
		account.PaymentTransferredQuota += request.Quota
		if err := saveAgentSimpleAccountTx(tx, before, account, actor.Id, "payment_profit_transfer", "agent", strconv.Itoa(actor.Id), strings.TrimSpace(request.Reason), request.RequestID); err != nil {
			return err
		}
		view = agentSimpleAccountView(account, false)
		return storeAgentSimpleAccountOperationTx(tx, operation, account)
	})
	return view, err
}

func RefundAgentSimplePayment(identity AuthSessionIdentity, request AgentSimplePaymentRefundCommand) error {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.OrderID <= 0 || strings.TrimSpace(request.Reason) == "" || strings.TrimSpace(request.Evidence) == "" || len(request.Reason) > 255 || len(request.Evidence) > 255 {
		return ErrAgentSimpleInvalid
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		var order AgentSimplePaymentOrder
		if err := lockForUpdate(tx).First(&order, request.OrderID).Error; err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_payment_refund", request.RequestID, RedemptionScope{UserID: actor.Id, Role: actor.Role}, order.AgentUserID, request)
		if err != nil || replay {
			return err
		}
		if order.Status != AgentSimplePaymentPaid || order.RedemptionID <= 0 {
			return ErrAgentSimpleState
		}
		var code Redemption
		if err := lockForUpdate(tx).First(&code, order.RedemptionID).Error; err != nil {
			return err
		}
		if code.Status != common.RedemptionCodeStatusEnabled || code.UsedUserId != 0 || code.RedeemedTime != 0 {
			return ErrAgentSimpleState
		}
		account, err := lockAgentSimpleAccountTx(tx, order.AgentUserID, false)
		if err != nil {
			return err
		}
		before := *account
		remaining := order.ProfitQuota
		if !order.ProfitMatured {
			if account.PaymentPendingQuota < remaining {
				return ErrAgentSimpleInvariant
			}
			account.PaymentPendingQuota -= remaining
			remaining = 0
		} else {
			take := min(account.PaymentAvailableQuota, remaining)
			account.PaymentAvailableQuota -= take
			remaining -= take
		}
		account.PaymentReversedQuota += order.ProfitQuota
		account.PaymentDebtQuota += remaining
		if err := saveAgentSimpleAccountTx(tx, before, account, actor.Id, "payment_refund", "payment_order", strconv.FormatInt(order.ID, 10), strings.TrimSpace(request.Reason), request.RequestID); err != nil {
			return err
		}
		if err := tx.Model(&code).Select("status", "budget_reserved", "budget_version").Updates(map[string]any{"status": common.RedemptionCodeStatusDisabled, "budget_reserved": false, "budget_version": code.BudgetVersion + 1}).Error; err != nil {
			return err
		}
		if err := tx.Model(&order).Where("id = ? AND status = ?", order.ID, AgentSimplePaymentPaid).Updates(map[string]any{"status": AgentSimplePaymentRefunded, "refunded_at": common.GetTimestamp(), "refund_reason": strings.TrimSpace(request.Reason), "refund_evidence": strings.TrimSpace(request.Evidence), "refunded_by": actor.Id, "profit_matured": true}).Error; err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "simple_payment", int(order.ID), AgentSimplePaymentRefunded)
	})
}

func WithdrawAgentSimplePaymentProfit(identity AuthSessionIdentity, request AgentSimplePaymentWithdrawalRequest) (*AgentSimplePaymentWithdrawal, error) {
	return RecordAgentSimpleWithdrawal(identity, AgentSimpleWithdrawalRequest{RequestID: request.RequestID, UserID: request.AgentUserID, PaymentQuota: request.Quota, Reason: request.Reason, Evidence: request.Evidence})
}

func CreateAgentSimplePaymentOrder(identity AuthSessionIdentity, request AgentSimplePaymentRequest, publicID, token, tradeNo, paymentURL, paymentParams, publicBase string) (*AgentSimplePaymentOrderView, error) {
	if strings.TrimSpace(paymentURL) == "" || strings.TrimSpace(paymentParams) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	if validateAgentSimpleRequestID(request.RequestID) != nil || validateAgentSimpleAmount(request.Quota) != nil || request.SalePriceFen <= 0 || request.SalePriceFen > int64(common.MaxWalletQuota) || strings.TrimSpace(request.Name) == "" || len(request.Name) > 80 || strings.TrimSpace(request.PaymentMethod) == "" || strings.TrimSpace(publicID) == "" || validateAgentSimplePaymentToken(token) != nil || tradeNo == "" || strings.TrimSpace(publicBase) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	var result *AgentSimplePaymentOrderView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		var actor User
		if err := tx.Select("id", "role").First(&actor, identity.UserID).Error; err != nil || actor.Role >= common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		if err := requireAgentSimpleSalesTx(tx, identity, identity.UserID); err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_payment_create", request.RequestID, RedemptionScope{UserID: identity.UserID, Role: actor.Role}, identity.UserID, request)
		if err != nil {
			return err
		}
		var order AgentSimplePaymentOrder
		if replay {
			if err := lockForUpdate(tx).Where("request_key = ?", operation.OperationKey).First(&order).Error; err != nil {
				return ErrAgentSimpleConflict
			}
		} else {
			if _, err := lockAgentSimpleAccountTx(tx, identity.UserID, true); err != nil {
				return err
			}
			fee, err := lockAgentSimplePaymentFeeTx(tx, request.PaymentMethod)
			if err != nil {
				return err
			}
			if request.FeeVersion < 0 || (request.FeeVersion > 0 && request.FeeVersion != fee.Version) {
				return ErrAgentSimpleConflict
			}
			quote, err := agentSimplePaymentQuote(fee, request.Quota, request.SalePriceFen)
			if err != nil {
				return err
			}
			ciphertext, err := sealAgentSimplePaymentToken(token)
			if err != nil {
				return err
			}
			order = AgentSimplePaymentOrder{RequestKey: operation.OperationKey, PublicID: publicID, TokenHash: agentSimpleTokenHash(token), TokenCipher: ciphertext, AgentUserID: identity.UserID, Name: strings.TrimSpace(request.Name), Quota: request.Quota, SalePriceFen: request.SalePriceFen, CostPriceFen: quote.CostPriceFen, FeeFen: quote.FeeFen, ProfitQuota: quote.ProfitQuota, PaymentMethod: request.PaymentMethod, PaymentProvider: PaymentProviderEpay, TradeNo: tradeNo, PaymentURL: paymentURL, PaymentParams: paymentParams, Status: AgentSimplePaymentPending, ExpiresAt: common.GetTimestamp() + 7*24*60*60}
			if err := tx.Create(&order).Error; err != nil {
				return err
			}
			if err := storeAgentOperationResultTx(tx, operation, "simple_payment", int(order.ID), AgentSimplePaymentPending); err != nil {
				return err
			}
		}
		token, err := openAgentSimplePaymentToken(order.TokenCipher)
		if err != nil {
			return err
		}
		result = simplePaymentOrderView(&order, strings.TrimRight(publicBase, "/")+"/agent-pay/"+order.PublicID+"#token="+token)
		return nil
	})
	return result, err
}

func AttachAgentSimplePaymentGateway(identity AuthSessionIdentity, requestID, paymentURL, paymentParams, publicBase string) (*AgentSimplePaymentOrderView, error) {
	if validateAgentSimpleRequestID(requestID) != nil || strings.TrimSpace(paymentURL) == "" || strings.TrimSpace(paymentParams) == "" || strings.TrimSpace(publicBase) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	var result *AgentSimplePaymentOrderView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleSalesTx(tx, identity, identity.UserID); err != nil {
			return err
		}
		operationKey := "simple_payment_create:" + requestID
		var operation AgentRedemptionOperation
		if err := tx.Where("operation_key = ? AND actor_user_id = ?", operationKey, identity.UserID).First(&operation).Error; err != nil {
			return ErrAgentSimpleConflict
		}
		var order AgentSimplePaymentOrder
		if err := lockForUpdate(tx).Where("request_key = ? AND agent_user_id = ?", operationKey, identity.UserID).First(&order).Error; err != nil {
			return err
		}
		if order.Status != AgentSimplePaymentPending {
			return ErrAgentSimpleState
		}
		if order.PaymentURL != "" || order.PaymentParams != "" {
			if order.PaymentURL != paymentURL || order.PaymentParams != paymentParams {
				return ErrAgentSimpleConflict
			}
		} else if err := tx.Model(&order).Updates(map[string]any{"payment_url": paymentURL, "payment_params": paymentParams}).Error; err != nil {
			return err
		} else {
			order.PaymentURL, order.PaymentParams = paymentURL, paymentParams
		}
		token, err := openAgentSimplePaymentToken(order.TokenCipher)
		if err != nil {
			return err
		}
		result = simplePaymentOrderView(&order, strings.TrimRight(publicBase, "/")+"/agent-pay/"+order.PublicID+"#token="+token)
		return nil
	})
	return result, err
}

func ListAgentSimplePaymentOrders(identity AuthSessionIdentity, agentID, offset, limit int, publicBase string) ([]AgentSimplePaymentOrderView, int64, error) {
	if offset < 0 || limit <= 0 || limit > 100 || strings.TrimSpace(publicBase) == "" {
		return nil, 0, ErrAgentSimpleInvalid
	}
	var rows []AgentSimplePaymentOrder
	var total int64
	admin := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		if actor.Role == common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		admin = actor.Role == common.RoleRootUser
		if !admin {
			if agentID > 0 && agentID != actor.Id {
				return ErrAgentSimplePermission
			}
			profile, err := LockAgentProfileTx(tx, actor.Id)
			if err != nil || profile == nil || !profile.Allows(AgentCapabilityHistory) {
				return ErrAgentSimplePermission
			}
			agentID = actor.Id
		}
		query := tx.Model(&AgentSimplePaymentOrder{})
		if agentID > 0 {
			query = query.Where("agent_user_id = ?", agentID)
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		return query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	})
	if err != nil {
		return nil, 0, err
	}
	result := make([]AgentSimplePaymentOrderView, 0, len(rows))
	for i := range rows {
		customerURL := ""
		if !admin {
			token, err := openAgentSimplePaymentToken(rows[i].TokenCipher)
			if err != nil {
				return nil, 0, err
			}
			customerURL = strings.TrimRight(publicBase, "/") + "/agent-pay/" + rows[i].PublicID + "#token=" + token
		}
		view := simplePaymentOrderView(&rows[i], customerURL)
		view.RefundedAt = rows[i].RefundedAt
		if admin {
			view.RefundReason, view.RefundEvidence, view.RefundedBy = rows[i].RefundReason, rows[i].RefundEvidence, rows[i].RefundedBy
		}
		if !admin && rows[i].Status == AgentSimplePaymentPaid && rows[i].RedemptionID > 0 {
			var code Redemption
			if err := DB.Select("key").Where("id = ? AND budget_agent_id = ? AND status = ? AND used_user_id = 0", rows[i].RedemptionID, rows[i].AgentUserID, common.RedemptionCodeStatusEnabled).First(&code).Error; err == nil {
				view.RedemptionKey = code.Key
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, 0, err
			}
		}
		result = append(result, *view)
	}
	return result, total, nil
}

func ExpireAgentSimplePaymentOrders(ctx context.Context, limit int) (int, error) {
	limit = min(max(limit, 1), 200)
	now := common.GetTimestamp()
	var ids []int64
	if err := DB.WithContext(ctx).Model(&AgentSimplePaymentOrder{}).Where("status = ? AND expires_at <= ?", AgentSimplePaymentPending, now).Order("id").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	count := 0
	for _, id := range ids {
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var order AgentSimplePaymentOrder
			if err := lockForUpdate(tx).First(&order, id).Error; err != nil {
				return err
			}
			if order.Status != AgentSimplePaymentPending || order.ExpiresAt > common.GetTimestamp() {
				return nil
			}
			return tx.Model(&order).Where("id = ? AND status = ?", order.ID, AgentSimplePaymentPending).Update("status", AgentSimplePaymentExpired).Error
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func validateAgentSimpleAmount(value int64) error {
	if value <= 0 || value > int64(common.MaxWalletQuota) {
		return ErrAgentSimpleInvalid
	}
	return nil
}
func validateAgentSimpleRequestID(value string) error {
	if len(value) < 8 || len(value) > 96 {
		return ErrAgentSimpleInvalid
	}
	for _, ch := range value {
		if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' && ch != '_' && ch != ':' {
			return ErrAgentSimpleInvalid
		}
	}
	return nil
}
func validateAgentSimpleAccount(a *AgentSimpleAccount) error {
	if a == nil || a.AgentUserID <= 0 || a.Version <= 0 {
		return ErrAgentSimpleInvariant
	}
	for _, v := range []int64{a.CashQuota, a.RewardQuota, a.RootQuota, a.PaymentQuota, a.CreditLimitQuota, a.CreditReservedQuota, a.CreditOutstandingQuota, a.PaymentPendingQuota, a.PaymentAvailableQuota, a.PaymentTotalQuota, a.PaymentDebtQuota, a.PaymentReversedQuota, a.PaymentWithdrawnQuota, a.PaymentTransferredQuota, a.PaymentOffsetQuota} {
		if v < 0 || v > int64(common.MaxWalletQuota) {
			return ErrAgentSimpleInvariant
		}
	}
	if a.CashQuota > int64(common.MaxWalletQuota)-a.RewardQuota || a.RootQuota > int64(common.MaxWalletQuota)-a.CashQuota-a.RewardQuota || a.PaymentQuota > int64(common.MaxWalletQuota)-a.CashQuota-a.RewardQuota-a.RootQuota {
		return ErrAgentSimpleInvariant
	}
	if a.CreditReservedQuota > a.CreditLimitQuota || a.CreditOutstandingQuota > a.CreditLimitQuota-a.CreditReservedQuota {
		return ErrAgentSimpleInvariant
	}
	if a.PaymentAvailableQuota > a.PaymentTotalQuota || a.PaymentPendingQuota > a.PaymentTotalQuota-a.PaymentAvailableQuota || a.PaymentDebtQuota > a.PaymentTotalQuota {
		return ErrAgentSimpleInvariant
	}
	if a.PaymentTotalQuota != a.PaymentPendingQuota+a.PaymentAvailableQuota+a.PaymentWithdrawnQuota+a.PaymentTransferredQuota+a.PaymentReversedQuota+a.PaymentOffsetQuota-a.PaymentDebtQuota {
		return ErrAgentSimpleInvariant
	}
	if a.Status != AgentSimpleStatusActive && a.Status != AgentSimpleStatusPaused && a.Status != AgentSimpleStatusCancelled {
		return ErrAgentSimpleInvariant
	}
	return nil
}
func simpleAvailableQuota(a *AgentSimpleAccount) int64 {
	return a.CashQuota + a.RewardQuota + a.RootQuota + a.PaymentQuota
}
func simpleAvailableCredit(a *AgentSimpleAccount) int64 {
	return a.CreditLimitQuota - a.CreditReservedQuota - a.CreditOutstandingQuota
}

func agentSimpleAccountView(account *AgentSimpleAccount, includeCreditLimit bool) *AgentSimpleAccountView {
	view := &AgentSimpleAccountView{
		AgentUserID: account.AgentUserID, Status: account.Status, CashQuota: account.CashQuota,
		RewardQuota: account.RewardQuota, RootQuota: account.RootQuota,
		CreditReservedQuota: account.CreditReservedQuota, CreditOutstandingQuota: account.CreditOutstandingQuota,
		PaymentPendingQuota: account.PaymentPendingQuota, PaymentAvailableQuota: account.PaymentAvailableQuota,
		PaymentTotalQuota: account.PaymentTotalQuota, PaymentDebtQuota: account.PaymentDebtQuota,
		PaymentQuota:         account.PaymentQuota,
		PaymentReversedQuota: account.PaymentReversedQuota, PaymentWithdrawnQuota: account.PaymentWithdrawnQuota,
		PaymentTransferredQuota: account.PaymentTransferredQuota, PaymentOffsetQuota: account.PaymentOffsetQuota,
		Version: account.Version, AvailableQuota: simpleAvailableQuota(account),
		AvailableCreditQuota: simpleAvailableCredit(account), AmountDueQuota: account.CreditOutstandingQuota,
	}
	if includeCreditLimit {
		view.CreditLimitQuota = account.CreditLimitQuota
	}
	return view
}

func agentSimpleActorTx(tx *gorm.DB, identity AuthSessionIdentity) (User, error) {
	var actor User
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return User{}, ErrAgentSimplePermission
	}
	if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, identity.UserID).Error; err != nil {
		return User{}, ErrAgentSimplePermission
	}
	if actor.Status != common.UserStatusEnabled || ValidateAuthSessionWithTx(tx, identity) != nil {
		return User{}, ErrAgentSimplePermission
	}
	return actor, nil
}

func storeAgentSimpleAccountOperationTx(tx *gorm.DB, operation *AgentRedemptionOperation, account *AgentSimpleAccount) error {
	encoded, err := common.Marshal(agentSimpleAccountView(account, true))
	if err != nil {
		return err
	}
	return tx.Model(operation).Update("result", string(encoded)).Error
}

func loadAgentSimpleAccountOperationTx(tx *gorm.DB, operation *AgentRedemptionOperation) (*AgentSimpleAccountView, error) {
	var view AgentSimpleAccountView
	if common.UnmarshalJsonStr(operation.Result, &view) != nil {
		return nil, ErrAgentSimpleConflict
	}
	return &view, nil
}

func lockAgentSimpleAccountTx(tx *gorm.DB, agentID int, create bool) (*AgentSimpleAccount, error) {
	if agentID <= 0 {
		return nil, ErrAgentSimpleInvalid
	}
	if create {
		// Creating storage is not a grant of sales eligibility. Only the root status
		// operation may activate a newly introduced A07 account.
		seed := AgentSimpleAccount{AgentUserID: agentID, Status: AgentSimpleStatusPaused, Version: 1}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
			return nil, err
		}
	}
	var account AgentSimpleAccount
	if err := lockForUpdate(tx).Where("agent_user_id = ?", agentID).First(&account).Error; err != nil {
		return nil, err
	}
	if err := validateAgentSimpleAccount(&account); err != nil {
		return nil, err
	}
	return &account, nil
}

func requireAgentSimpleSalesTx(tx *gorm.DB, identity AuthSessionIdentity, agentID int) error {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return ErrAgentSimplePermission
	}
	var actor User
	if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, identity.UserID).Error; err != nil {
		return ErrAgentSimplePermission
	}
	if actor.Status != common.UserStatusEnabled || ValidateAuthSessionWithTx(tx, identity) != nil {
		return ErrAgentSimplePermission
	}
	if actor.Role == common.RoleRootUser {
		return nil
	}
	if actor.Role == common.RoleAdminUser {
		return ErrAgentSimplePermission
	}
	if actor.Id != agentID {
		return ErrAgentSimplePermission
	}
	active, err := activeAgentSalesTx(tx, agentID)
	if err != nil || !active {
		return ErrAgentSimpleUnavailable
	}
	return nil
}
func requireAgentSimpleRootTx(tx *gorm.DB, identity AuthSessionIdentity) error {
	if _, pat := ParseAccessTokenSessionID(identity.SessionID); pat {
		return ErrAgentSimplePermission
	}
	var actor User
	if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, identity.UserID).Error; err != nil || actor.Status != common.UserStatusEnabled || actor.Role != common.RoleRootUser || ValidateAuthSessionWithTx(tx, identity) != nil {
		return ErrAgentSimplePermission
	}
	return nil
}

func saveAgentSimpleAccountTx(tx *gorm.DB, before AgentSimpleAccount, after *AgentSimpleAccount, actorID int, kind, referenceType, referenceID, reason, requestID string) error {
	if err := validateAgentSimpleAccount(after); err != nil || strings.TrimSpace(reason) == "" || len(reason) > 255 {
		return ErrAgentSimpleInvalid
	}
	after.Version++
	after.UpdatedAt = common.GetTimestamp()
	result := tx.Model(&AgentSimpleAccount{}).Where("agent_user_id = ? AND version = ?", after.AgentUserID, before.Version).Updates(map[string]any{"status": after.Status, "cash_quota": after.CashQuota, "reward_quota": after.RewardQuota, "root_quota": after.RootQuota, "payment_quota": after.PaymentQuota, "payment_reversed_quota": after.PaymentReversedQuota, "payment_withdrawn_quota": after.PaymentWithdrawnQuota, "payment_transferred_quota": after.PaymentTransferredQuota, "payment_offset_quota": after.PaymentOffsetQuota, "credit_limit_quota": after.CreditLimitQuota, "credit_reserved_quota": after.CreditReservedQuota, "credit_outstanding_quota": after.CreditOutstandingQuota, "payment_pending_quota": after.PaymentPendingQuota, "payment_available_quota": after.PaymentAvailableQuota, "payment_total_quota": after.PaymentTotalQuota, "payment_debt_quota": after.PaymentDebtQuota, "version": after.Version, "last_activity_at": after.LastActivityAt, "updated_at": after.UpdatedAt})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAgentSimpleConflict
	}
	event := AgentSimpleLedger{DeltaCreditLimit: after.CreditLimitQuota - before.CreditLimitQuota, CreditLimitAfter: after.CreditLimitQuota, EventKey: fmt.Sprintf("simple:%d:%s:%s:%d", after.AgentUserID, kind, referenceID, after.Version), AgentUserID: after.AgentUserID, ActorUserID: actorID, Kind: kind, ReferenceType: referenceType, ReferenceID: referenceID, RequestID: requestID, Reason: reason, DeltaCashQuota: after.CashQuota - before.CashQuota, DeltaRewardQuota: after.RewardQuota - before.RewardQuota, DeltaRootQuota: after.RootQuota - before.RootQuota, DeltaCreditReserved: after.CreditReservedQuota - before.CreditReservedQuota, DeltaCreditOutstanding: after.CreditOutstandingQuota - before.CreditOutstandingQuota, DeltaPaymentPending: after.PaymentPendingQuota - before.PaymentPendingQuota, DeltaPaymentAvailable: after.PaymentAvailableQuota - before.PaymentAvailableQuota, DeltaPaymentTotal: after.PaymentTotalQuota - before.PaymentTotalQuota, DeltaPaymentDebt: after.PaymentDebtQuota - before.PaymentDebtQuota, DeltaPaymentQuota: after.PaymentQuota - before.PaymentQuota, DeltaPaymentReversed: after.PaymentReversedQuota - before.PaymentReversedQuota, DeltaPaymentWithdrawn: after.PaymentWithdrawnQuota - before.PaymentWithdrawnQuota, DeltaPaymentTransferred: after.PaymentTransferredQuota - before.PaymentTransferredQuota, DeltaPaymentOffset: after.PaymentOffsetQuota - before.PaymentOffsetQuota, CashAfter: after.CashQuota, RewardAfter: after.RewardQuota, RootAfter: after.RootQuota, CreditReservedAfter: after.CreditReservedQuota, CreditOutstandingAfter: after.CreditOutstandingQuota, PaymentPendingAfter: after.PaymentPendingQuota, PaymentAvailableAfter: after.PaymentAvailableQuota, PaymentTotalAfter: after.PaymentTotalQuota, PaymentDebtAfter: after.PaymentDebtQuota, PaymentAfter: after.PaymentQuota, PaymentReversedAfter: after.PaymentReversedQuota, PaymentWithdrawnAfter: after.PaymentWithdrawnQuota, PaymentTransferredAfter: after.PaymentTransferredQuota, PaymentOffsetAfter: after.PaymentOffsetQuota}
	return tx.Create(&event).Error
}

func allocateAgentSimpleSourcesTx(tx *gorm.DB, account *AgentSimpleAccount, redemptionID int, quota int64) error {
	remaining := quota
	for _, source := range []struct {
		name  string
		value *int64
	}{{AgentSimpleSourceReward, &account.RewardQuota}, {AgentSimpleSourceRoot, &account.RootQuota}, {AgentSimpleSourcePayment, &account.PaymentQuota}, {AgentSimpleSourceCash, &account.CashQuota}} {
		take := min(*source.value, remaining)
		if take <= 0 {
			continue
		}
		*source.value -= take
		if err := tx.Create(&AgentSimpleCodeAllocation{AgentUserID: account.AgentUserID, RedemptionID: redemptionID, Source: source.name, Quota: take, State: AgentSimpleAllocationReserved}).Error; err != nil {
			return err
		}
		remaining -= take
	}
	if remaining > 0 {
		cost := agentSimpleCodeCost(remaining)
		if simpleAvailableCredit(account) < cost {
			return ErrAgentSimpleInsufficient
		}
		account.CreditReservedQuota += cost
		if err := tx.Create(&AgentSimpleCodeAllocation{AgentUserID: account.AgentUserID, RedemptionID: redemptionID, Source: AgentSimpleSourceCredit, Quota: remaining, State: AgentSimpleAllocationReserved}).Error; err != nil {
			return err
		}
	}
	return nil
}
func agentSimpleCodeCost(quota int64) int64 { return max(int64(1), (quota*9+9)/10) }

func CreateAgentSimpleCode(identity AuthSessionIdentity, request AgentSimpleCodeRequest) (*Redemption, error) {
	if err := validateAgentSimpleRequestID(request.RequestID); err != nil || validateAgentSimpleAmount(request.Quota) != nil || (request.Mode != AgentSimpleCodeDirect && request.Mode != AgentSimpleCodeCredit) || strings.TrimSpace(request.Name) == "" || len(request.Name) > 80 || request.ExpiresAt < 0 {
		return nil, ErrAgentSimpleInvalid
	}
	if request.ExpiresAt != 0 && request.ExpiresAt <= common.GetTimestamp() {
		return nil, ErrAgentSimpleInvalid
	}
	var result Redemption
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		var actor User
		if err := tx.Select("id", "role").First(&actor, identity.UserID).Error; err != nil || actor.Role >= common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		if err := requireAgentSimpleSalesTx(tx, identity, identity.UserID); err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_create", request.RequestID, RedemptionScope{UserID: identity.UserID, Role: actor.Role}, identity.UserID, request)
		if err != nil {
			return err
		}
		if replay {
			return tx.Where("creation_operation_id = ? AND budget_agent_id = ?", operation.ID, identity.UserID).First(&result).Error
		}
		account, err := lockAgentSimpleAccountTx(tx, identity.UserID, true)
		if err != nil {
			return err
		}
		before := *account
		if account.Status != AgentSimpleStatusActive {
			return ErrAgentSimpleUnavailable
		}
		own := min(simpleAvailableQuota(account), request.Quota)
		creditFace := request.Quota - own
		cost := int64(0)
		if creditFace > 0 {
			cost = agentSimpleCodeCost(creditFace)
		}
		if simpleAvailableCredit(account) < cost {
			return ErrAgentSimpleInsufficient
		}
		result = Redemption{UserId: identity.UserID, Name: strings.TrimSpace(request.Name), Key: common.GetUUID(), Status: common.RedemptionCodeStatusEnabled, CreatedTime: common.GetTimestamp(), Quota: int(request.Quota), ExpiredTime: request.ExpiresAt, BudgetAgentID: identity.UserID, BudgetReserved: true, BudgetVersion: 1, CreationOperationID: operation.ID, AgentSimpleMode: AgentSimpleCodeDirect, AgentSimpleCredit: creditFace > 0, AgentSimpleCostQuota: cost}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		if err := allocateAgentSimpleSourcesTx(tx, account, result.Id, request.Quota); err != nil {
			return err
		}
		account.LastActivityAt = common.GetTimestamp()
		if err := saveAgentSimpleAccountTx(tx, before, account, identity.UserID, "code_issue", "redemption", strconv.Itoa(result.Id), "Issue simplified agent redemption code", request.RequestID); err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "redemption", result.Id, "issued")
	})
	return &result, err
}

func releaseAgentSimpleCodeTx(tx *gorm.DB, account *AgentSimpleAccount, code *Redemption, actorID int, reason, requestID string) error {
	if !code.BudgetReserved || code.AgentSimpleMode == AgentSimpleCodePayment {
		return ErrAgentSimpleState
	}
	before := *account
	if code.AgentSimpleCredit {
		if account.CreditReservedQuota < code.AgentSimpleCostQuota {
			return ErrAgentSimpleInvariant
		}
		account.CreditReservedQuota -= code.AgentSimpleCostQuota
	}
	var allocations []AgentSimpleCodeAllocation
	if err := lockForUpdate(tx).Where("redemption_id = ? AND state = ?", code.Id, AgentSimpleAllocationReserved).Find(&allocations).Error; err != nil {
		return err
	}
	var ownReturned int64
	for _, allocation := range allocations {
		if allocation.AgentUserID != account.AgentUserID || allocation.Quota <= 0 || allocation.Quota > int64(code.Quota)-ownReturned {
			return ErrAgentSimpleInvariant
		}
		if allocation.Source != AgentSimpleSourceCredit {
			ownReturned += allocation.Quota
		}
	}
	var offsetFace, repayment int64
	if code.AgentSimpleCredit && ownReturned > 0 && account.CreditOutstandingQuota > 0 {
		// Returned own funding pays existing discounted debt first. Releasing
		// unused credit restores capacity only and never creates a repayment.
		offsetFace = min(ownReturned, account.CreditOutstandingQuota*10/9)
		if offsetFace > 0 {
			repayment = agentSimpleCodeCost(offsetFace)
		}
	}
	total := int64(0)
	for i := range allocations {
		a := &allocations[i]
		total += a.Quota
		restored := a.Quota
		if a.Source != AgentSimpleSourceCredit {
			used := min(offsetFace, restored)
			restored -= used
			offsetFace -= used
		}
		switch a.Source {
		case AgentSimpleSourceCash:
			account.CashQuota += restored
		case AgentSimpleSourceReward:
			account.RewardQuota += restored
		case AgentSimpleSourceRoot:
			account.RootQuota += restored
		case AgentSimpleSourcePayment:
			account.PaymentQuota += restored
		case AgentSimpleSourceCredit:
			// Credit capacity was released above; no cash balance is created.
		default:
			return ErrAgentSimpleInvariant
		}
		a.State = AgentSimpleAllocationReleased
		if err := tx.Save(a).Error; err != nil {
			return err
		}
	}
	if total != int64(code.Quota) {
		return ErrAgentSimpleInvariant
	}
	if err := applyAgentSimpleRepaymentTx(tx, account, repayment); err != nil {
		return err
	}
	account.LastActivityAt = common.GetTimestamp()
	return saveAgentSimpleAccountTx(tx, before, account, actorID, "code_release", "redemption", strconv.Itoa(code.Id), reason, requestID)
}

// redeemAgentSimpleCodeTx settles the agent side of a simplified code. Direct
// codes move their source allocations to spent; credit codes turn the reserved
// capacity into the discounted weekly receivable. The customer wallet is
// credited by the caller in the same transaction.
func redeemAgentSimpleCodeTx(tx *gorm.DB, code *Redemption, actorID int) error {
	account, err := lockAgentSimpleAccountTx(tx, code.BudgetAgentID, false)
	if err != nil {
		return err
	}
	before := *account
	if code.AgentSimpleCredit {
		if account.CreditReservedQuota < code.AgentSimpleCostQuota {
			return ErrAgentSimpleInvariant
		}
		account.CreditReservedQuota -= code.AgentSimpleCostQuota
		if account.CreditOutstandingQuota > int64(common.MaxWalletQuota)-code.AgentSimpleCostQuota {
			return ErrAgentSimpleInvariant
		}
		account.CreditOutstandingQuota += code.AgentSimpleCostQuota
		if err := tx.Create(&AgentSimpleCreditDebt{AgentUserID: account.AgentUserID, CodeID: code.Id, Quota: code.AgentSimpleCostQuota}).Error; err != nil {
			return err
		}
	}
	var allocations []AgentSimpleCodeAllocation
	if err := lockForUpdate(tx).Where("redemption_id = ? AND state = ?", code.Id, AgentSimpleAllocationReserved).Find(&allocations).Error; err != nil {
		return err
	}
	total := int64(0)
	for i := range allocations {
		allocation := &allocations[i]
		total += allocation.Quota
		allocation.State = AgentSimpleAllocationSpent
		if err := tx.Model(allocation).Update("state", allocation.State).Error; err != nil {
			return err
		}
	}
	if total != int64(code.Quota) {
		return ErrAgentSimpleInvariant
	}
	account.LastActivityAt = common.GetTimestamp()
	return saveAgentSimpleAccountTx(tx, before, account, actorID, "code_redeem", "redemption", strconv.Itoa(code.Id), "Redeem simplified agent code", "redeem:"+strconv.Itoa(code.Id))
}

func creditAgentSimpleCashTx(tx *gorm.DB, agentID, actorID int, quota, paidQuota int64, kind, referenceID, reason string) error {
	if agentID <= 0 || quota <= 0 || quota > int64(common.MaxWalletQuota) || paidQuota <= 0 || paidQuota > int64(common.MaxWalletQuota) {
		return ErrAgentSimpleInvalid
	}
	account, err := lockAgentSimpleAccountTx(tx, agentID, true)
	if err != nil {
		return err
	}
	before := *account
	repayment := min(account.CreditOutstandingQuota, paidQuota)
	if err := applyAgentSimpleRepaymentTx(tx, account, repayment); err != nil {
		return err
	}
	remaining, err := agentSimpleCashRemainder(quota, paidQuota, repayment, kind)
	if err != nil {
		return err
	}
	credited := min(remaining, int64(common.MaxWalletQuota)-simpleAvailableQuota(account))
	if credited < 0 {
		return ErrAgentSimpleInvariant
	}
	account.CashQuota += credited
	if remaining > credited {
		result := tx.Model(&TopUp{}).Where("trade_no = ? AND user_id = ? AND agent_simple_top_up = ? AND status = ?", referenceID, agentID, true, common.TopUpStatusSuccess).Update("agent_simple_deferred_quota", remaining-credited)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAgentSimpleInvariant
		}
	}
	account.LastActivityAt = common.GetTimestamp()
	ledgerKind := "cash_recharge"
	if kind == "repayment" {
		ledgerKind = "repayment"
	}
	return saveAgentSimpleAccountTx(tx, before, account, actorID, ledgerKind, "topup", referenceID, reason, "topup:"+referenceID)
}

// RedeemAgentSimpleCodeTx is used by the normal redemption flow so simple
// codes never enter the legacy agent fund ledger by accident.
func RedeemAgentSimpleCodeTx(tx *gorm.DB, code *Redemption, actorID int) error {
	if code == nil || code.AgentSimpleMode == "" || !code.BudgetReserved {
		return ErrAgentSimpleInvariant
	}
	if code.AgentSimpleMode == AgentSimpleCodePayment {
		var order AgentSimplePaymentOrder
		if code.AgentSimplePaymentID <= 0 || lockForUpdate(tx).Where("id = ? AND agent_user_id = ?", code.AgentSimplePaymentID, code.BudgetAgentID).First(&order).Error != nil || order.Status != AgentSimplePaymentPaid || order.RedemptionID != code.Id {
			return ErrAgentSimpleState
		}
		result := tx.Model(&order).Where("id = ? AND status = ?", order.ID, AgentSimplePaymentPaid).Update("status", AgentSimplePaymentRedeemed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAgentSimpleConflict
		}
		code.BudgetReserved = false
		code.BudgetVersion++
		return nil
	}
	if err := redeemAgentSimpleCodeTx(tx, code, actorID); err != nil {
		return err
	}
	code.BudgetReserved = false
	code.BudgetVersion++
	return nil
}

func ExpireAgentSimpleCodeTx(tx *gorm.DB, code *Redemption, actorID int) error {
	if code == nil || code.AgentSimpleMode == "" || !code.BudgetReserved {
		return ErrAgentSimpleInvariant
	}
	account, err := lockAgentSimpleAccountTx(tx, code.BudgetAgentID, false)
	if err != nil {
		return err
	}
	if err := releaseAgentSimpleCodeTx(tx, account, code, actorID, "Expire simplified agent code", "expire:"+strconv.Itoa(code.Id)); err != nil {
		return err
	}
	code.BudgetReserved = false
	code.BudgetVersion++
	return nil
}

// ExpireAgentSimpleCodes releases only unused, expired A07 direct/credit codes.
// Each code and its original funding allocations transition atomically; legacy
// codes and customer payment-code refund obligations are owned by other flows.
func ExpireAgentSimpleCodes(ctx context.Context, limit int) (int, error) {
	limit = min(max(limit, 1), 500)
	now := common.GetTimestamp()
	predicate := "agent_simple_mode IN ? AND budget_agent_id > 0 AND user_id = budget_agent_id AND budget_reserved = ? AND customer_user_id = 0 AND delivery_id = 0 AND source_protected = ? AND agent_simple_payment_id = 0 AND status = ? AND used_user_id = 0 AND redeemed_time = 0 AND expired_time > 0 AND expired_time < ?"
	modes := []string{AgentSimpleCodeDirect, AgentSimpleCodeCredit}
	var ids []int
	if err := DB.WithContext(ctx).Model(&Redemption{}).Where(predicate, modes, true, false, common.RedemptionCodeStatusEnabled, now).Order("id").Limit(limit).Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		changed := false
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var code Redemption
			err := lockForUpdate(tx).Where("id = ?", id).Where(predicate, modes, true, false, common.RedemptionCodeStatusEnabled, now).First(&code).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := ExpireAgentSimpleCodeTx(tx, &code, 0); err != nil {
				return err
			}
			result := tx.Model(&code).Updates(map[string]any{"status": common.RedemptionCodeStatusDisabled, "budget_reserved": code.BudgetReserved, "budget_version": code.BudgetVersion})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrAgentSimpleConflict
			}
			changed = true
			return nil
		})
		if err != nil {
			return expired, err
		}
		if changed {
			expired++
		}
	}
	return expired, nil
}

func CancelAgentSimpleCode(identity AuthSessionIdentity, request AgentSimpleCodeCancelRequest) (*Redemption, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.CodeID <= 0 || strings.TrimSpace(request.Reason) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	var code Redemption
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := lockForUpdate(tx).First(&code, request.CodeID).Error; err != nil {
			return err
		}
		if code.AgentSimpleMode == "" {
			return ErrAgentSimpleState
		}
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil || actor.Role == common.RoleAdminUser || (actor.Id != code.BudgetAgentID && actor.Role != common.RoleRootUser) {
			return ErrAgentSimplePermission
		}
		scope := RedemptionScope{UserID: actor.Id, Role: actor.Role}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_cancel", request.RequestID, scope, code.BudgetAgentID, request)
		if err != nil {
			return err
		}
		if replay {
			var stored agentStoredOperationResult
			if common.UnmarshalJsonStr(operation.Result, &stored) != nil || stored.ResourceType != "redemption" || stored.ResourceID != code.Id || stored.ResourceState != "cancelled" {
				return ErrAgentSimpleConflict
			}
			return nil
		}
		if actor.Role < common.RoleAdminUser {
			if err := requireAgentSimpleSalesTx(tx, identity, code.BudgetAgentID); err != nil {
				return err
			}
		}
		if int64(code.BudgetVersion) != request.Version || !code.BudgetReserved || code.AgentSimpleMode == AgentSimpleCodePayment || code.Status != common.RedemptionCodeStatusEnabled || code.UsedUserId != 0 || code.RedeemedTime != 0 || code.CustomerUserID != 0 {
			return ErrAgentSimpleState
		}
		account, err := lockAgentSimpleAccountTx(tx, code.BudgetAgentID, false)
		if err != nil {
			return err
		}
		if err := releaseAgentSimpleCodeTx(tx, account, &code, identity.UserID, strings.TrimSpace(request.Reason), request.RequestID); err != nil {
			return err
		}
		code.Status = common.RedemptionCodeStatusDisabled
		code.BudgetReserved = false
		code.BudgetVersion++
		if err := tx.Model(&code).Select("status", "budget_reserved", "budget_version").Updates(&code).Error; err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "redemption", code.Id, "cancelled")
	})
	return &code, err
}

func GetAgentSimpleAccount(identity AuthSessionIdentity, agentID int) (*AgentSimpleAccountView, error) {
	if agentID <= 0 {
		agentID = identity.UserID
	}
	var view AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleHistoryTx(tx, identity, agentID); err != nil {
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, agentID, false)
		if err != nil {
			return err
		}
		view = *agentSimpleAccountView(account, identity.UserID != agentID)
		var reserved int64
		if err := tx.Model(&AgentSimpleCodeAllocation{}).Where("agent_user_id = ? AND state = ?", agentID, AgentSimpleAllocationReserved).Select("COALESCE(SUM(quota),0)").Scan(&reserved).Error; err != nil {
			return err
		}
		view.ReservedQuota = reserved
		if err := tx.Model(&AgentSimpleBill{}).Where("agent_user_id = ? AND due_at <= ?", agentID, common.GetTimestamp()).Select("COALESCE(SUM(quota - paid_quota),0)").Scan(&view.OverdueQuota).Error; err != nil {
			return err
		}
		return nil
	})
	return &view, err
}

func GetAgentSimpleRecords(identity AuthSessionIdentity, agentID, offset, limit int, mode string) ([]AgentSimpleRecord, int64, error) {
	if offset < 0 || limit <= 0 || limit > 100 {
		return nil, 0, ErrAgentSimpleInvalid
	}
	if mode != "" && mode != AgentSimpleCodeDirect && mode != AgentSimpleCodeCredit && mode != AgentSimpleCodePayment {
		return nil, 0, ErrAgentSimpleInvalid
	}
	var rows []Redemption
	var total int64
	mask := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var actor User
		if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, identity.UserID).Error; err != nil || actor.Status != common.UserStatusEnabled || ValidateAuthSessionWithTx(tx, identity) != nil {
			return ErrAgentSimplePermission
		}
		if actor.Role == common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		admin := actor.Role == common.RoleRootUser
		if !admin && (agentID <= 0 || agentID != actor.Id) {
			return ErrAgentSimplePermission
		}
		mask = admin
		query := tx.Model(&Redemption{}).Where("agent_simple_mode <> ''")
		if agentID > 0 {
			query = query.Where("budget_agent_id = ?", agentID)
		}
		if mode != "" {
			query = query.Where("agent_simple_mode = ?", mode)
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		return query.Order("id DESC").Offset(offset).Limit(limit).Find(&rows).Error
	})
	if err != nil {
		return nil, 0, err
	}
	result := make([]AgentSimpleRecord, 0, len(rows))
	for _, row := range rows {
		key := row.Key
		masked := key
		if len(key) >= 16 {
			masked = key[:8] + strings.Repeat("*", len(key)-16) + key[len(key)-8:]
		}
		if mask || row.Status != common.RedemptionCodeStatusEnabled || (row.ExpiredTime != 0 && row.ExpiredTime < common.GetTimestamp()) {
			key = ""
		}
		result = append(result, AgentSimpleRecord{ID: row.Id, AgentUserID: row.BudgetAgentID, Name: row.Name, Key: key, MaskedKey: masked, Quota: row.Quota, CostQuota: row.AgentSimpleCostQuota, Mode: row.AgentSimpleMode, Status: row.Status, ExpiredAt: row.ExpiredTime, RedeemedAt: row.RedeemedTime, CreatedAt: row.CreatedTime, Version: int64(row.BudgetVersion)})
	}
	return result, total, nil
}

func AdjustAgentSimpleBalance(identity AuthSessionIdentity, request AgentSimpleAdjustmentRequest) (*AgentSimpleAccountView, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.AgentUserID <= 0 || request.Delta == 0 || request.Delta > int64(common.MaxWalletQuota) || request.Delta < -int64(common.MaxWalletQuota) || strings.TrimSpace(request.Reason) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	var view *AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_adjust", request.RequestID, RedemptionScope{UserID: actor.Id, Role: actor.Role}, request.AgentUserID, request)
		if err != nil {
			return err
		}
		if replay {
			view, err = loadAgentSimpleAccountOperationTx(tx, operation)
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, request.AgentUserID, true)
		if err != nil {
			return err
		}
		before := *account
		if request.Delta < 0 && account.RootQuota < -request.Delta {
			return ErrAgentSimpleInsufficient
		}
		if request.Delta > 0 && simpleAvailableQuota(account) > int64(common.MaxWalletQuota)-request.Delta {
			return ErrWalletQuotaLimitExceeded
		}
		account.RootQuota += request.Delta
		if err := saveAgentSimpleAccountTx(tx, before, account, identity.UserID, "root_adjustment", "agent", strconv.Itoa(request.AgentUserID), strings.TrimSpace(request.Reason), request.RequestID); err != nil {
			return err
		}
		view = agentSimpleAccountView(account, true)
		return storeAgentSimpleAccountOperationTx(tx, operation, account)
	})
	return view, err
}

func SetAgentSimpleCreditLimit(identity AuthSessionIdentity, request AgentSimpleCreditLimitRequest) (*AgentSimpleAccountView, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.AgentUserID <= 0 || request.LimitQuota < 0 || request.LimitQuota > int64(common.MaxWalletQuota) || strings.TrimSpace(request.Reason) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	var view *AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_credit_limit", request.RequestID, RedemptionScope{UserID: actor.Id, Role: actor.Role}, request.AgentUserID, request)
		if err != nil {
			return err
		}
		if replay {
			view, err = loadAgentSimpleAccountOperationTx(tx, operation)
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, request.AgentUserID, true)
		if err != nil {
			return err
		}
		if request.LimitQuota < account.CreditReservedQuota || request.LimitQuota-account.CreditReservedQuota < account.CreditOutstandingQuota {
			return ErrAgentSimpleConflict
		}
		before := *account
		account.CreditLimitQuota = request.LimitQuota
		if err := saveAgentSimpleAccountTx(tx, before, account, identity.UserID, "credit_limit", "agent", strconv.Itoa(request.AgentUserID), strings.TrimSpace(request.Reason), request.RequestID); err != nil {
			return err
		}
		view = agentSimpleAccountView(account, true)
		return storeAgentSimpleAccountOperationTx(tx, operation, account)
	})
	return view, err
}

func RepayAgentSimpleCredit(identity AuthSessionIdentity, request AgentSimpleRepaymentRequest) (*AgentSimpleAccountView, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.AgentUserID <= 0 || validateAgentSimpleAmount(request.Quota) != nil || strings.TrimSpace(request.Reason) == "" {
		return nil, ErrAgentSimpleInvalid
	}
	var view *AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_repayment", request.RequestID, RedemptionScope{UserID: actor.Id, Role: actor.Role}, request.AgentUserID, request)
		if err != nil {
			return err
		}
		if replay {
			view, err = loadAgentSimpleAccountOperationTx(tx, operation)
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, request.AgentUserID, false)
		if err != nil {
			return err
		}
		if err := validateAgentSimpleRepayment(account.CreditOutstandingQuota, request.Quota); err != nil {
			return ErrAgentSimpleInvalid
		}
		before := *account
		repaid := min(request.Quota, account.CreditOutstandingQuota)
		if err := applyAgentSimpleRepaymentTx(tx, account, repaid); err != nil {
			return err
		}
		change, err := AgentMoneyRatio(request.Quota-repaid, 10, 9, false)
		if err != nil || simpleAvailableQuota(account) > int64(common.MaxWalletQuota)-change {
			return ErrWalletQuotaLimitExceeded
		}
		account.CashQuota += change
		if err := saveAgentSimpleAccountTx(tx, before, account, identity.UserID, "repayment", "agent", strconv.Itoa(request.AgentUserID), strings.TrimSpace(request.Reason), request.RequestID); err != nil {
			return err
		}
		view = agentSimpleAccountView(account, true)
		return storeAgentSimpleAccountOperationTx(tx, operation, account)
	})
	return view, err
}
