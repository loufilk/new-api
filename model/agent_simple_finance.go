package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var agentSimpleLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

type AgentSimpleBill struct {
	ID          int64 `json:"id" gorm:"primaryKey"`
	AgentUserID int   `json:"agent_user_id" gorm:"not null;uniqueIndex:idx_simple_bill_week,priority:1"`
	WeekStart   int64 `json:"week_start" gorm:"not null;uniqueIndex:idx_simple_bill_week,priority:2"`
	Quota       int64 `json:"quota" gorm:"type:bigint;not null"`
	PaidQuota   int64 `json:"paid_quota" gorm:"type:bigint;not null"`
	DueAt       int64 `json:"due_at" gorm:"not null;index"`
	CreatedAt   int64 `json:"created_at" gorm:"autoCreateTime"`
}

type AgentSimpleCreditDebt struct {
	ID          int64 `json:"id" gorm:"primaryKey"`
	AgentUserID int   `json:"agent_user_id" gorm:"not null;index"`
	CodeID      int   `json:"code_id" gorm:"not null;uniqueIndex"`
	Quota       int64 `json:"quota" gorm:"type:bigint;not null"`
	PaidQuota   int64 `json:"paid_quota" gorm:"type:bigint;not null"`
	BillID      int64 `json:"bill_id" gorm:"not null;default:0;index"`
	CreatedAt   int64 `json:"created_at" gorm:"autoCreateTime"`
}

type AgentSimpleReminder struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	AgentUserID int    `json:"agent_user_id" gorm:"not null;uniqueIndex:idx_simple_reminder_day,priority:1"`
	Day         string `json:"day" gorm:"type:char(10);not null;uniqueIndex:idx_simple_reminder_day,priority:2"`
	Audience    string `json:"audience" gorm:"type:varchar(8);not null;uniqueIndex:idx_simple_reminder_day,priority:3"`
	Quota       int64  `json:"quota" gorm:"type:bigint;not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentSimpleReminder) BeforeUpdate(*gorm.DB) error { return ErrAgentSimpleImmutable }
func (*AgentSimpleReminder) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

type AgentSimpleWithdrawalRequest struct {
	RequestID     string `json:"request_id"`
	UserID        int    `json:"user_id"`
	ReferralQuota int64  `json:"referral_quota"`
	PaymentQuota  int64  `json:"payment_quota"`
	Reason        string `json:"reason"`
	Evidence      string `json:"evidence"`
}

type AgentSimpleTransferRequest struct {
	RequestID string `json:"request_id"`
	Quota     int64  `json:"quota"`
}

type AgentSimpleCodeViewAudit struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	ActorUserID int    `json:"actor_user_id" gorm:"not null"`
	CodeID      int    `json:"code_id" gorm:"not null;index"`
	Reason      string `json:"reason" gorm:"type:varchar(255);not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentSimpleCodeViewAudit) BeforeUpdate(*gorm.DB) error { return ErrAgentSimpleImmutable }
func (*AgentSimpleCodeViewAudit) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

// applyAgentSimpleRepaymentTx allocates an already funded repayment to the
// oldest bills. Any remainder repays credit redeemed since the latest bill.
func applyAgentSimpleRepaymentTx(tx *gorm.DB, account *AgentSimpleAccount, quota int64) error {
	if quota < 0 || quota > account.CreditOutstandingQuota {
		return ErrAgentSimpleInvariant
	}
	remaining := quota
	var debts []AgentSimpleCreditDebt
	if err := lockForUpdate(tx).Where("agent_user_id = ? AND paid_quota < quota", account.AgentUserID).Order("created_at, id").Find(&debts).Error; err != nil {
		return err
	}
	for _, debt := range debts {
		take := min(remaining, debt.Quota-debt.PaidQuota)
		if take == 0 {
			break
		}
		if err := tx.Model(&debt).Update("paid_quota", debt.PaidQuota+take).Error; err != nil {
			return err
		}
		if debt.BillID > 0 {
			result := tx.Model(&AgentSimpleBill{}).Where("id = ? AND agent_user_id = ? AND quota - paid_quota >= ?", debt.BillID, account.AgentUserID, take).UpdateColumn("paid_quota", gorm.Expr("paid_quota + ?", take))
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrAgentSimpleInvariant
			}
		}
		remaining -= take
	}
	if remaining != 0 {
		return ErrAgentSimpleInvariant
	}
	account.CreditOutstandingQuota -= quota
	return nil
}

func offsetAgentSimpleIncomeTx(tx *gorm.DB, account *AgentSimpleAccount, actorID int, reference string) error {
	var owner User
	if err := lockForUpdate(tx).First(&owner, account.AgentUserID).Error; err != nil {
		return err
	}
	before := *account
	paymentDebt := min(account.PaymentDebtQuota, account.PaymentAvailableQuota)
	account.PaymentDebtQuota -= paymentDebt
	account.PaymentAvailableQuota -= paymentDebt
	payment := min(account.CreditOutstandingQuota, account.PaymentAvailableQuota)
	if err := applyAgentSimpleRepaymentTx(tx, account, payment); err != nil {
		return err
	}
	account.PaymentAvailableQuota -= payment
	account.PaymentOffsetQuota += payment
	available, err := referralAvailableQuotaTx(tx, owner.Id)
	if err != nil {
		return err
	}
	referral := min(account.CreditOutstandingQuota, max(int64(0), available))
	if err := applyAgentSimpleRepaymentTx(tx, account, referral); err != nil {
		return err
	}
	if referral > 0 {
		if err := tx.Model(&owner).UpdateColumn("aff_quota", gorm.Expr("aff_quota - ?", referral)).Error; err != nil {
			return err
		}
	}
	if referral+payment+paymentDebt == 0 {
		return nil
	}
	return saveAgentSimpleAccountTx(tx, before, account, actorID, "income_debt_offset", "agent", reference, "Apply released payment profit then referral reward to debt", reference)
}

func RecordAgentSimpleWithdrawal(identity AuthSessionIdentity, request AgentSimpleWithdrawalRequest) (*AgentSimplePaymentWithdrawal, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.UserID <= 0 || request.ReferralQuota < 0 || request.PaymentQuota < 0 || request.PaymentQuota > int64(common.MaxWalletQuota) || request.ReferralQuota > int64(common.MaxWalletQuota)-request.PaymentQuota || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 255 || strings.TrimSpace(request.Evidence) == "" || len(request.Evidence) > 255 {
		return nil, ErrAgentSimpleInvalid
	}
	total := request.ReferralQuota + request.PaymentQuota
	if total < AgentSimpleWithdrawalMinimumQuota {
		return nil, ErrAgentSimpleInvalid
	}
	var result AgentSimplePaymentWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_withdrawal", request.RequestID, RedemptionScope{UserID: identity.UserID, Role: common.RoleRootUser}, request.UserID, request)
		if err != nil {
			return err
		}
		if replay {
			return tx.Where("request_id = ?", request.RequestID).First(&result).Error
		}
		var user User
		if err := lockForUpdate(tx).First(&user, request.UserID).Error; err != nil {
			return err
		}
		var account AgentSimpleAccount
		accountErr := lockForUpdate(tx).First(&account, request.UserID).Error
		if accountErr != nil && !errors.Is(accountErr, gorm.ErrRecordNotFound) {
			return accountErr
		}
		if accountErr == nil {
			if err := offsetAgentSimpleIncomeTx(tx, &account, identity.UserID, request.RequestID); err != nil {
				return err
			}
			if account.CreditOutstandingQuota > 0 || (request.PaymentQuota > 0 && account.PaymentDebtQuota > 0) || account.PaymentAvailableQuota < request.PaymentQuota {
				return ErrAgentSimpleInsufficient
			}
		} else if request.PaymentQuota > 0 {
			return ErrAgentSimpleInsufficient
		}
		available, err := referralAvailableQuotaTx(tx, request.UserID)
		if err != nil {
			return err
		}
		if request.ReferralQuota > 0 && available < request.ReferralQuota {
			return ErrAgentSimpleInsufficient
		}
		now := time.Now().In(agentSimpleLocation)
		month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, agentSimpleLocation)
		var count int64
		if err := tx.Model(&AgentSimplePaymentWithdrawal{}).Where("agent_user_id = ? AND created_at >= ? AND created_at < ?", user.Id, month.Unix(), month.AddDate(0, 1, 0).Unix()).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrAgentSimpleConflict
		}
		if request.PaymentQuota > 0 {
			before := account
			account.PaymentAvailableQuota -= request.PaymentQuota
			account.PaymentWithdrawnQuota += request.PaymentQuota
			if err := saveAgentSimpleAccountTx(tx, before, &account, identity.UserID, "payment_withdrawal", "withdrawal", request.RequestID, request.Reason, request.RequestID); err != nil {
				return err
			}
		}
		if request.ReferralQuota > 0 {
			if err := tx.Model(&user).UpdateColumn("aff_quota", gorm.Expr("aff_quota - ?", request.ReferralQuota)).Error; err != nil {
				return err
			}
		}
		fee := (total*AgentSimpleWithdrawalFeeBPS + 9_999) / 10_000
		result = AgentSimplePaymentWithdrawal{RequestID: request.RequestID, AgentUserID: user.Id, ActorUserID: identity.UserID, Quota: total, ReferralQuota: request.ReferralQuota, PaymentQuota: request.PaymentQuota, FeeQuota: fee, NetQuota: total - fee, Reason: request.Reason, Evidence: request.Evidence}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "simple_withdrawal", int(result.ID), "recorded")
	})
	return &result, err
}

func TransferReferralToAgentBalance(identity AuthSessionIdentity, request AgentSimpleTransferRequest) (*AgentSimpleAccountView, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || validateAgentSimpleAmount(request.Quota) != nil {
		return nil, ErrAgentSimpleInvalid
	}
	var view *AgentSimpleAccountView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleSalesTx(tx, identity, identity.UserID); err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_referral_transfer", request.RequestID, RedemptionScope{UserID: identity.UserID}, identity.UserID, request)
		if err != nil {
			return err
		}
		if replay {
			view, err = loadAgentSimpleAccountOperationTx(tx, operation)
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, identity.UserID, true)
		if err != nil {
			return err
		}
		available, err := referralAvailableQuotaTx(tx, identity.UserID)
		if err != nil || available < request.Quota || account.Status != AgentSimpleStatusActive {
			return ErrAgentSimpleInsufficient
		}
		before := *account
		account.RewardQuota += request.Quota
		if err := tx.Model(&User{}).Where("id = ?", identity.UserID).UpdateColumn("aff_quota", gorm.Expr("aff_quota - ?", request.Quota)).Error; err != nil {
			return err
		}
		if err := saveAgentSimpleAccountTx(tx, before, account, identity.UserID, "referral_transfer", "referral", request.RequestID, "Transfer released referral reward to agent balance", request.RequestID); err != nil {
			return err
		}
		view = agentSimpleAccountView(account, false)
		return storeAgentSimpleAccountOperationTx(tx, operation, account)
	})
	return view, err
}

func RunAgentSimpleWeeklySettlement(ctx context.Context, limit int) (int, error) {
	return runAgentSimpleWeeklySettlementAt(ctx, limit, time.Now().In(agentSimpleLocation))
}

func runAgentSimpleWeeklySettlementAt(ctx context.Context, limit int, now time.Time) (int, error) {
	now = now.In(agentSimpleLocation)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, agentSimpleLocation)
	week := day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	var ids []int
	query := DB.WithContext(ctx).Model(&AgentSimpleAccount{}).Where("credit_outstanding_quota > 0").
		Where(`NOT EXISTS (SELECT 1 FROM agent_simple_bills b WHERE b.agent_user_id = agent_simple_accounts.agent_user_id AND b.week_start = ?)
		OR (EXISTS (SELECT 1 FROM agent_simple_bills b WHERE b.agent_user_id = agent_simple_accounts.agent_user_id AND b.due_at <= ? AND b.paid_quota < b.quota)
		AND NOT EXISTS (SELECT 1 FROM agent_simple_reminders r WHERE r.agent_user_id = agent_simple_accounts.agent_user_id AND r.day = ? AND r.audience = ?))`, week.Unix(), now.Unix(), day.Format("2006-01-02"), "root")
	if err := query.Order("agent_user_id").Limit(min(max(limit, 1), 500)).Pluck("agent_user_id", &ids).Error; err != nil {
		return 0, err
	}
	for _, id := range ids {
		err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var owner User
			if err := lockForUpdate(tx).First(&owner, id).Error; err != nil {
				return err
			}
			account, err := lockAgentSimpleAccountTx(tx, id, false)
			if err != nil {
				return err
			}
			var existing AgentSimpleBill
			lookup := tx.Where("agent_user_id = ? AND week_start = ?", id, week.Unix()).First(&existing).Error
			if errors.Is(lookup, gorm.ErrRecordNotFound) {
				if err := offsetAgentSimpleIncomeTx(tx, account, 0, "weekly:"+strconv.FormatInt(week.Unix(), 10)); err != nil {
					return err
				}
				var newDebt int64
				if err := tx.Model(&AgentSimpleCreditDebt{}).Where("agent_user_id = ? AND bill_id = 0 AND created_at < ?", id, week.Unix()).Select("COALESCE(SUM(quota - paid_quota),0)").Scan(&newDebt).Error; err != nil {
					return err
				}
				if newDebt > account.CreditOutstandingQuota {
					return ErrAgentSimpleInvariant
				}
				bill := AgentSimpleBill{AgentUserID: id, WeekStart: week.Unix(), Quota: newDebt, DueAt: now.AddDate(0, 0, 3).Unix()}
				if err := tx.Create(&bill).Error; err != nil {
					return err
				}
				if err := tx.Model(&AgentSimpleCreditDebt{}).Where("agent_user_id = ? AND bill_id = 0 AND created_at < ?", id, week.Unix()).Update("bill_id", bill.ID).Error; err != nil {
					return err
				}
			} else if lookup != nil {
				return lookup
			}
			var overdue int64
			if err := tx.Model(&AgentSimpleBill{}).Where("agent_user_id = ? AND due_at <= ?", id, now.Unix()).Select("COALESCE(SUM(quota - paid_quota),0)").Scan(&overdue).Error; err != nil {
				return err
			}
			if overdue > 0 {
				for _, audience := range []string{"agent", "root"} {
					reminder := AgentSimpleReminder{AgentUserID: id, Day: day.Format("2006-01-02"), Audience: audience, Quota: overdue}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&reminder).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
