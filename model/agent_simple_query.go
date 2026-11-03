package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type AgentSimpleAgentRow struct {
	AgentSimpleAccountView
	Username   string `json:"username"`
	Inactive   bool   `json:"inactive"`
	Overdue    int64  `json:"overdue_quota"`
	LastActive int64  `json:"last_activity_at"`
}

func ListAgentSimpleAgents(identity AuthSessionIdentity, offset, limit int) ([]AgentSimpleAgentRow, int64, error) {
	if offset < 0 || offset > 100_000_000 || limit <= 0 || limit > 100 {
		return nil, 0, ErrAgentSimpleInvalid
	}
	rows := make([]AgentSimpleAgentRow, 0)
	var total int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		if err := tx.Model(&AgentSimpleAccount{}).Count(&total).Error; err != nil {
			return err
		}
		var accounts []AgentSimpleAccount
		if err := tx.Order("agent_user_id").Offset(offset).Limit(limit).Find(&accounts).Error; err != nil {
			return err
		}
		for _, account := range accounts {
			var user User
			if err := tx.Unscoped().Select("username").First(&user, account.AgentUserID).Error; err != nil {
				return err
			}
			var overdue int64
			if err := tx.Model(&AgentSimpleBill{}).Where("agent_user_id = ? AND due_at <= ?", account.AgentUserID, common.GetTimestamp()).Select("COALESCE(SUM(quota - paid_quota),0)").Scan(&overdue).Error; err != nil {
				return err
			}
			var lastSale int64
			if err := tx.Model(&AgentSimpleLedger{}).Where("agent_user_id = ? AND kind IN ?", account.AgentUserID, []string{"code_issue", "payment_profit_pending"}).Select("COALESCE(MAX(created_at),0)").Scan(&lastSale).Error; err != nil {
				return err
			}
			lastActivity := max(lastSale, account.CreatedAt)
			rows = append(rows, AgentSimpleAgentRow{AgentSimpleAccountView: *agentSimpleAccountView(&account, true), Username: user.Username, Inactive: lastActivity <= common.GetTimestamp()-90*24*60*60, Overdue: overdue, LastActive: lastSale})
		}
		return nil
	})
	return rows, total, err
}

func QueryAgentSimpleHistory(identity AuthSessionIdentity, kind string, targetID, offset, limit int) (any, int64, error) {
	if targetID < 0 || offset < 0 || offset > 100_000_000 || limit <= 0 || limit > 100 {
		return nil, 0, ErrAgentSimpleInvalid
	}
	var rows any
	var total int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		actor, err := agentSimpleActorTx(tx, identity)
		if err != nil || actor.Role == common.RoleAdminUser {
			return ErrAgentSimplePermission
		}
		root := actor.Role == common.RoleRootUser
		if !root {
			if targetID > 0 && targetID != actor.Id {
				return ErrAgentSimplePermission
			}
			targetID = actor.Id
		}
		column := "agent_user_id"
		switch kind {
		case "ledger":
			rows = &[]AgentSimpleLedger{}
		case "bills":
			rows = &[]AgentSimpleBill{}
		case "reminders":
			rows = &[]AgentSimpleReminder{}
		case "withdrawals":
			rows = &[]AgentSimplePaymentWithdrawal{}
		case "exits":
			rows = &[]AgentSimpleExitRecord{}
		case "recharges":
			rows, column = &[]TopUp{}, "user_id"
		case "referrals":
			rows, column = &[]ReferralCommission{}, "user_id"
		default:
			return ErrAgentSimpleInvalid
		}
		query := tx.Model(rows)
		if targetID > 0 {
			query = query.Where(column+" = ?", targetID)
		}
		if kind == "recharges" {
			query = query.Where("agent_simple_top_up = ?", true)
		}
		if kind == "reminders" {
			audience := "agent"
			if root {
				audience = "root"
			}
			query = query.Where("audience = ?", audience)
		}
		if err := query.Count(&total).Error; err != nil {
			return err
		}
		return query.Order("id DESC").Offset(offset).Limit(limit).Find(rows).Error
	})
	return rows, total, err
}

func RevealAgentSimpleCode(identity AuthSessionIdentity, codeID int, reason string) (string, error) {
	if codeID <= 0 || strings.TrimSpace(reason) == "" || len(reason) > 255 {
		return "", ErrAgentSimpleInvalid
	}
	var key string
	err := DB.Session(&gorm.Session{Logger: DB.Logger.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if err := requireAgentSimpleRootTx(tx, identity); err != nil {
			return err
		}
		var code Redemption
		if err := tx.Where("id = ? AND agent_simple_mode <> ''", codeID).First(&code).Error; err != nil {
			return err
		}
		if err := tx.Create(&AgentSimpleCodeViewAudit{ActorUserID: identity.UserID, CodeID: codeID, Reason: reason}).Error; err != nil {
			return err
		}
		key = code.Key
		return nil
	})
	return key, err
}
