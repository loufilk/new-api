package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type AgentCreditView struct {
	Audits         []AgentCreditAudit          `json:"audits"`
	CurrentLimit   *AgentCreditLimitVersion    `json:"current_limit"`
	AvailableMinor int64                       `json:"available_minor,string"`
	OverdueMinor   int64                       `json:"overdue_minor,string"`
	DrawAllowed    bool                        `json:"draw_allowed"`
	Account        *AgentCreditAccount         `json:"account"`
	Limits         []AgentCreditLimitVersion   `json:"limits"`
	LimitRequests  []AgentCreditLimitRequest   `json:"limit_requests"`
	Draws          []AgentCreditDraw           `json:"draws"`
	Receivables    []AgentReceivable           `json:"receivables"`
	Invoices       []AgentReceivableInvoice    `json:"invoices"`
	Repayments     []AgentCreditRepayment      `json:"repayments"`
	Adjustments    []AgentReceivableAdjustment `json:"adjustments"`
	Ledger         []AgentCreditLedger         `json:"ledger"`
	Offset         int                         `json:"offset"`
	Limit          int                         `json:"limit"`
	HasMore        bool                        `json:"has_more"`
	NextOffset     int                         `json:"next_offset"`
}

func GetAgentCreditView(id AuthSessionIdentity, owner, offset, limit int) (*AgentCreditView, error) {
	if owner == 0 {
		owner = id.UserID
	}
	offset = max(0, offset)
	limit = min(100, max(1, limit))
	out := &AgentCreditView{Audits: []AgentCreditAudit{}, Limits: []AgentCreditLimitVersion{}, LimitRequests: []AgentCreditLimitRequest{}, Draws: []AgentCreditDraw{}, Receivables: []AgentReceivable{}, Invoices: []AgentReceivableInvoice{}, Repayments: []AgentCreditRepayment{}, Adjustments: []AgentReceivableAdjustment{}, Ledger: []AgentCreditLedger{}, Offset: offset, Limit: limit}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		policy, err := LockCurrentAgentPolicyTx(tx)
		if err != nil {
			return err
		}
		if err := requireAgentFinanceReaderTx(tx, id, owner, owner != id.UserID); err != nil {
			return err
		}
		a, err := agentCreditReadAccountTx(tx, owner)
		if err != nil {
			return err
		}
		out.Account = a
		if a != nil {
			var limit AgentCreditLimitVersion
			if err := tx.First(&limit, a.LimitVersionID).Error; err != nil {
				return err
			}
			if limit.AgentUserID != owner {
				return ErrAgentCreditInvariant
			}
			out.CurrentLimit = &limit
			out.AvailableMinor = max(int64(0), limit.AmountMinor-a.ReservedMinor-a.OutstandingMinor-a.WrittenOffMinor)
			if err := tx.Model(&AgentReceivable{}).Select("COALESCE(SUM(principal_minor - paid_minor - offset_minor - written_off_minor - corrected_minor), 0)").Where("agent_user_id = ? AND due_at <= ? AND principal_minor > paid_minor + offset_minor + written_off_minor + corrected_minor", owner, common.GetTimestamp()).Scan(&out.OverdueMinor).Error; err != nil {
				return err
			}
			profile, err := readAgentProfile(tx, owner, false)
			if err != nil {
				return err
			}
			riskErr := requireAgentCreditRiskTx(tx, a, 0)
			if riskErr != nil && !errors.Is(riskErr, ErrAgentCreditRisk) {
				return riskErr
			}
			governanceErr := RequireAgentGovernanceCreditTx(tx, owner)
			if governanceErr != nil && !errors.Is(governanceErr, ErrAgentGovernanceRequired) && !errors.Is(governanceErr, ErrAgentFeatureDisabled) {
				return governanceErr
			}
			out.DrawAllowed = policy.CreditEnabled && ValidateAgentCreditPolicy(policy) == nil && profile.Allows(AgentCapabilitySales) && riskErr == nil && governanceErr == nil && out.AvailableMinor > 0 && limit.AmountMinor <= policy.CreditMaximumLimitMinor && limit.TermSeconds <= policy.CreditMaximumTermSeconds
		}
		for _, rows := range []any{&out.Limits, &out.LimitRequests, &out.Draws, &out.Receivables, &out.Repayments, &out.Adjustments, &out.Ledger} {
			if err := tx.Where("agent_user_id = ?", owner).Order("id DESC").Offset(offset).Limit(limit + 1).Find(rows).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("receivable_id IN (?)", tx.Model(&AgentReceivable{}).Select("id").Where("agent_user_id = ?", owner)).Order("id DESC").Offset(offset).Limit(limit + 1).Find(&out.Invoices).Error; err != nil {
			return err
		}
		if err := tx.Where("agent_user_id = ? AND action LIKE ?", owner, "credit.%").Order("id DESC").Offset(offset).Limit(limit + 1).Find(&out.Audits).Error; err != nil {
			return err
		}
		out.Audits = agentFinancePage(out.Audits, limit, &out.HasMore)
		out.Limits = agentFinancePage(out.Limits, limit, &out.HasMore)
		out.LimitRequests = agentFinancePage(out.LimitRequests, limit, &out.HasMore)
		out.Draws = agentFinancePage(out.Draws, limit, &out.HasMore)
		out.Receivables = agentFinancePage(out.Receivables, limit, &out.HasMore)
		out.Invoices = agentFinancePage(out.Invoices, limit, &out.HasMore)
		out.Repayments = agentFinancePage(out.Repayments, limit, &out.HasMore)
		out.Adjustments = agentFinancePage(out.Adjustments, limit, &out.HasMore)
		out.Ledger = agentFinancePage(out.Ledger, limit, &out.HasMore)
		for i := range out.Repayments {
			if err := common.UnmarshalJsonStr(out.Repayments[i].AllocationSnapshot, &out.Repayments[i].Allocations); err != nil {
				return ErrAgentCreditInvariant
			}
		}
		if out.HasMore {
			out.NextOffset = offset + limit
		}
		return nil
	})
	return out, err
}
func agentFinancePage[T any](rows []T, limit int, more *bool) []T {
	if len(rows) > limit {
		*more = true
		return rows[:limit]
	}
	return rows
}

type AgentDepositView struct {
	Audits     []AgentCreditAudit             `json:"audits"`
	Account    *AgentDepositAccount           `json:"account"`
	Receipts   []AgentDepositReceipt          `json:"receipts"`
	Requests   []AgentDepositOperationRequest `json:"requests"`
	Ledger     []AgentDepositLedger           `json:"ledger"`
	Offset     int                            `json:"offset"`
	Limit      int                            `json:"limit"`
	HasMore    bool                           `json:"has_more"`
	NextOffset int                            `json:"next_offset"`
}

func GetAgentDepositView(id AuthSessionIdentity, owner, offset, limit int) (*AgentDepositView, error) {
	if owner == 0 {
		owner = id.UserID
	}
	offset = max(0, offset)
	limit = min(100, max(1, limit))
	out := &AgentDepositView{Audits: []AgentCreditAudit{}, Receipts: []AgentDepositReceipt{}, Requests: []AgentDepositOperationRequest{}, Ledger: []AgentDepositLedger{}, Offset: offset, Limit: limit}
	err := agentFinancialDB().Transaction(func(tx *gorm.DB) error {
		if _, err := LockCurrentAgentPolicyTx(tx); err != nil {
			return err
		}
		if err := requireAgentFinanceReaderTx(tx, id, owner, owner != id.UserID); err != nil {
			return err
		}
		var a AgentDepositAccount
		err := tx.Where("agent_user_id = ?", owner).First(&a).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if err := validateAgentDepositAccount(&a); err != nil {
				return err
			}
			out.Account = &a
		}
		for _, rows := range []any{&out.Receipts, &out.Requests, &out.Ledger} {
			if err := tx.Where("agent_user_id = ?", owner).Order("id DESC").Offset(offset).Limit(limit + 1).Find(rows).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("agent_user_id = ? AND action LIKE ?", owner, "deposit.%").Order("id DESC").Offset(offset).Limit(limit + 1).Find(&out.Audits).Error; err != nil {
			return err
		}
		out.Audits = agentFinancePage(out.Audits, limit, &out.HasMore)
		out.Receipts = agentFinancePage(out.Receipts, limit, &out.HasMore)
		out.Requests = agentFinancePage(out.Requests, limit, &out.HasMore)
		out.Ledger = agentFinancePage(out.Ledger, limit, &out.HasMore)
		for i := range out.Requests {
			if err := common.UnmarshalJsonStr(out.Requests[i].AllocationSnapshot, &out.Requests[i].Allocations); err != nil {
				return ErrAgentDepositInvariant
			}
		}
		if out.HasMore {
			out.NextOffset = offset + limit
		}
		return nil
	})
	return out, err
}
