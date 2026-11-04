package model

import (
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type AgentSimpleExitRequest struct {
	RequestID   string `json:"request_id"`
	AgentUserID int    `json:"agent_user_id"`
	Quota       int64  `json:"quota"`
	SettleAll   bool   `json:"settle_all"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	Reason      string `json:"reason"`
	Evidence    string `json:"evidence"`
}

type AgentSimpleExitRecord struct {
	ID          int64  `json:"id" gorm:"primaryKey"`
	RequestID   string `json:"request_id" gorm:"type:varchar(96);not null;uniqueIndex"`
	AgentUserID int    `json:"agent_user_id" gorm:"not null;index"`
	ActorUserID int    `json:"actor_user_id" gorm:"not null"`
	Quota       int64  `json:"quota" gorm:"type:bigint;not null"`
	Source      string `json:"source" gorm:"type:varchar(16);not null"`
	Target      string `json:"target" gorm:"type:varchar(16);not null"`
	NetFen      int64  `json:"net_fen" gorm:"type:bigint;not null"`
	Reason      string `json:"reason" gorm:"type:varchar(255);not null"`
	Evidence    string `json:"evidence" gorm:"type:varchar(255);not null"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime"`
}

func (*AgentSimpleExitRecord) BeforeUpdate(*gorm.DB) error { return ErrAgentSimpleImmutable }
func (*AgentSimpleExitRecord) BeforeDelete(*gorm.DB) error { return ErrAgentSimpleImmutable }

// SettleAgentSimpleExit records root's support-assisted disposition. A cash
// refund is a recorded external result; this operation never sends money.
func SettleAgentSimpleExit(identity AuthSessionIdentity, request AgentSimpleExitRequest) (*AgentSimpleExitRecord, error) {
	if validateAgentSimpleRequestID(request.RequestID) != nil || request.AgentUserID <= 0 || (!request.SettleAll && validateAgentSimpleAmount(request.Quota) != nil) || (request.SettleAll && request.Quota != 0) || (request.Source != "cash" && request.Source != "income" && request.Source != AgentSimpleSourceWallet) || (request.Target != "wallet" && request.Target != "refund") || (request.Target == "refund" && request.Source != "cash") || strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 255 || strings.TrimSpace(request.Evidence) == "" || len(request.Evidence) > 255 {
		return nil, ErrAgentSimpleInvalid
	}
	var record AgentSimpleExitRecord
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		operation, replay, err := claimAgentRedemptionOperation(tx, "simple_exit", request.RequestID, RedemptionScope{UserID: identity.UserID, Role: common.RoleRootUser}, request.AgentUserID, request)
		if err != nil {
			return err
		}
		if replay {
			return tx.Where("request_id = ?", request.RequestID).First(&record).Error
		}
		var owner User
		if err := lockForUpdate(tx).First(&owner, request.AgentUserID).Error; err != nil {
			return err
		}
		account, err := lockAgentSimpleAccountTx(tx, request.AgentUserID, false)
		if err != nil {
			return err
		}
		if account.Status == AgentSimpleStatusActive || account.CreditOutstandingQuota > 0 || account.PaymentDebtQuota > 0 {
			return ErrAgentSimpleState
		}
		if request.SettleAll {
			if request.Source == "cash" {
				request.Quota = account.CashQuota
			} else if request.Source == AgentSimpleSourceWallet {
				request.Quota = account.WalletQuota
			} else {
				request.Quota = account.RewardQuota + account.PaymentQuota
			}
			if request.Quota <= 0 {
				return ErrAgentSimpleInvalid
			}
		}
		before := *account
		if request.Source == "cash" {
			if account.CashQuota < request.Quota {
				return ErrAgentSimpleInsufficient
			}
			account.CashQuota -= request.Quota
		} else if request.Source == AgentSimpleSourceWallet {
			if account.WalletQuota < request.Quota {
				return ErrAgentSimpleInsufficient
			}
			account.WalletQuota -= request.Quota
		} else {
			if account.RewardQuota+account.PaymentQuota < request.Quota {
				return ErrAgentSimpleInsufficient
			}
			take := min(account.RewardQuota, request.Quota)
			account.RewardQuota -= take
			account.PaymentQuota -= request.Quota - take
		}
		record = AgentSimpleExitRecord{RequestID: request.RequestID, AgentUserID: owner.Id, ActorUserID: identity.UserID, Quota: request.Quota, Source: request.Source, Target: request.Target, Reason: request.Reason, Evidence: request.Evidence}
		if request.Target == "refund" {
			record.NetFen = decimal.NewFromInt(request.Quota).Mul(decimal.NewFromInt(90)).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Mul(decimal.NewFromInt(97)).Div(decimal.NewFromInt(100)).Floor().IntPart()
			if record.NetFen <= 0 {
				return ErrAgentSimpleInvalid
			}
		} else {
			if _, err := CreditQuotaTx(tx, LedgerEntry{UserId: owner.Id, Account: AccountPaid, Delta: int(request.Quota), Reason: "agent_exit_transfer", ReferenceType: "agent_exit", ReferenceId: request.RequestID, IdempotencyKey: "agent_exit:" + strconv.Itoa(owner.Id) + ":" + request.RequestID, OperatorId: identity.UserID}); err != nil {
				return err
			}
		}
		if err := saveAgentSimpleAccountTx(tx, before, account, identity.UserID, "exit_settlement", "agent_exit", request.RequestID, request.Reason, request.RequestID); err != nil {
			return err
		}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		return storeAgentOperationResultTx(tx, operation, "agent_exit", int(record.ID), "recorded")
	})
	if err == nil && request.Target == "wallet" {
		RefreshUserQuotaCache(request.AgentUserID, 0)
	}
	return &record, err
}
