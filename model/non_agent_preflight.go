package model

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// ErrNonAgentCutoverBlocked never means that a balance or debt was cleared.
// The original agent-enabled build must reconcile the reported obligations.
var ErrNonAgentCutoverBlocked = errors.New("non-agent cutover blocked: unresolved agent obligations require the original agent-enabled version")

// NonAgentCutoverBlock contains only schema names and counts, not payment or
// recipient information. Legacy business tables are never migrated or dropped.
type NonAgentCutoverBlock struct {
	Table string
	Rows  int64
}

type nonAgentRetirementRule struct {
	predicate string
	args      []any
}

// NonAgentCutoverPreflight is deliberately read-only and runs before any startup
// migration on every node. Stop ALL old writers before running this check and
// keep them stopped throughout cutover; mixed-version operation is unsupported.
// Missing schemas are normal for a new site. Unknown/nonempty business tables,
// unreadable schemas, and unknown states fail closed rather than imply zero.
func NonAgentCutoverPreflight(db *gorm.DB) ([]NonAgentCutoverBlock, error) {
	return nonAgentCutoverPreflight(db, false)
}

func nonAgentCutoverPreflight(db *gorm.DB, a07 bool) ([]NonAgentCutoverBlock, error) {
	if db == nil {
		return nil, errors.New("non-agent cutover preflight: database unavailable")
	}
	rules := map[string]nonAgentRetirementRule{
		"agent_payment_receipts":    {predicate: "purpose IS NULL OR purpose <> ?", args: []any{"topup"}},
		"agent_redemption_accounts": {predicate: "available_quota IS NULL OR reserved_quota IS NULL OR allocated_quota IS NULL OR spent_quota IS NULL OR available_quota <> 0 OR reserved_quota <> 0 OR allocated_quota < 0 OR spent_quota < 0 OR allocated_quota <> spent_quota"},
		"agent_commission_accounts": {predicate: "pending_minor IS NULL OR available_minor IS NULL OR reserved_minor IS NULL OR held_minor IS NULL OR debt_minor IS NULL OR pending_minor <> 0 OR available_minor <> 0 OR reserved_minor <> 0 OR held_minor <> 0 OR debt_minor <> 0"},
		// A paid/converted award remains subject to later refund clawback. Merely
		// draining cash balances cannot authorize abandoning that exposure.
		"agent_commission_awards":          {predicate: "reward_minor IS NULL OR reversed_minor IS NULL OR reward_minor < 0 OR reversed_minor < 0 OR reward_minor <> reversed_minor"},
		"agent_commission_debts":           {predicate: "amount_minor IS NULL OR recovered_minor IS NULL OR amount_minor < 0 OR recovered_minor < 0 OR amount_minor <> recovered_minor"},
		"agent_commission_outboxes":        {predicate: "status IS NULL OR status <> ?", args: []any{"done"}},
		"agent_credit_accounts":            {predicate: "reserved_minor IS NULL OR outstanding_minor IS NULL OR written_off_minor IS NULL OR reserved_minor <> 0 OR outstanding_minor <> 0 OR written_off_minor <> 0"},
		"agent_receivables":                {predicate: "state IS NULL OR state <> 'settled' OR principal_minor IS NULL OR paid_minor IS NULL OR offset_minor IS NULL OR corrected_minor IS NULL OR written_off_minor IS NULL OR principal_minor <= 0 OR paid_minor < 0 OR offset_minor < 0 OR corrected_minor < 0 OR principal_minor <> paid_minor + offset_minor + corrected_minor OR written_off_minor <> 0"},
		"agent_deposit_accounts":           {predicate: "available_minor IS NULL OR held_minor IS NULL OR received_minor IS NULL OR offset_minor IS NULL OR refunded_minor IS NULL OR available_minor <> 0 OR held_minor <> 0 OR received_minor < 0 OR offset_minor < 0 OR refunded_minor < 0 OR received_minor <> offset_minor + refunded_minor"},
		"agent_deposit_receipts":           {predicate: "state IS NULL OR state <> ? OR available_minor IS NULL OR held_minor IS NULL OR amount_minor IS NULL OR offset_minor IS NULL OR refunded_minor IS NULL OR available_minor <> 0 OR held_minor <> 0 OR amount_minor < 0 OR offset_minor < 0 OR refunded_minor < 0 OR amount_minor <> offset_minor + refunded_minor", args: []any{"confirmed"}},
		"agent_purchase_orders":            {predicate: "status IS NULL OR status NOT IN ?", args: []any{[]string{"credited", "rejected"}}},
		"agent_purchase_refunds":           {predicate: "state IS NULL OR state NOT IN ?", args: []any{[]string{"succeeded", "failed", "rejected"}}},
		"agent_commission_withdrawals":     {predicate: "status IS NULL OR status NOT IN ?", args: []any{[]string{"succeeded", "failed", "rejected"}}},
		"agent_deliveries":                 {predicate: "state IS NULL OR state NOT IN ?", args: []any{[]string{"redeemed", "cancelled"}}},
		"agent_fund_reclassifications":     {predicate: "state IS NULL OR state NOT IN ?", args: []any{[]string{"confirmed", "rejected"}}},
		"agent_credit_limit_requests":      {predicate: "state IS NULL OR state NOT IN ?", args: []any{[]string{"approved", "rejected"}}},
		"agent_credit_repayments":          {predicate: "state IS NULL OR state <> ?", args: []any{"confirmed"}},
		"agent_receivable_adjustments":     {predicate: "state IS NULL OR state NOT IN ?", args: []any{[]string{"approved", "rejected"}}},
		"agent_deposit_operation_requests": {predicate: "state IS NULL OR state NOT IN ?", args: []any{[]string{"succeeded", "failed", "rejected"}}},
		"agent_campaign_budgets":           {predicate: "reserved_quota IS NULL OR spent_quota IS NULL OR limit_quota IS NULL OR reserved_quota <> 0 OR spent_quota < 0 OR limit_quota < 0 OR spent_quota > limit_quota"},
		"agent_campaign_claims":            {predicate: "status IS NULL OR status NOT IN ?", args: []any{[]string{"granted", "rejected", "revoked", "expired"}}},
		"agent_statement_periods":          {predicate: "state IS NULL OR state <> ?", args: []any{"closed"}},
		"agent_statement_reviews":          {predicate: "state IS NULL OR state NOT IN ?", args: []any{[]string{"confirmed", "excluded"}}},
		"agent_statement_exceptions":       {predicate: "state IS NULL OR state <> ?", args: []any{"resolved"}},
	}
	// Funding histories remain blocked except a fully reclaimed, never-used
	// non-cash promotion verified below. Economics, credit draws and outbound
	// payment receipts remain outside this allowlist: without their reconciler,
	// this build cannot prove the absence of an orphaned source or late clawback.
	// Such histories require a separately reviewed retirement migration, not a
	// force-start flag or deletion of rows.
	// These are immutable evidence, configuration, identities, or committed
	// operation receipts. Their existence alone is not an outstanding liability.
	archives := strings.Fields(`
agent_policies agent_policy_states agent_policy_changes
agent_profiles agent_profile_audits agent_profile_operations
agent_invitations agent_invitation_policies agent_invitation_policy_audits agent_invitation_policy_operations
agent_redemption_ledgers agent_redemption_operations
agent_fund_ledgers agent_fund_economic_ledgers
agent_purchase_quotes agent_purchase_audits
agent_commission_caps agent_commission_ledgers agent_commission_inboxes agent_commission_refund_facts
agent_commission_operations agent_commission_recipients agent_commission_quotes agent_commission_conversions
agent_commission_allocations agent_commission_withdrawal_audits agent_commission_payout_evidences
agent_credit_audits agent_credit_limit_versions agent_credit_ledgers agent_credit_operations
agent_receivable_invoices agent_credit_repayment_allocations agent_deposit_ledgers
agent_campaigns agent_campaign_publications agent_campaign_budget_entries
agent_campaign_dedupes agent_campaign_privacy_keys agent_campaign_operations
agent_agreements agent_agreement_approvals agent_agreement_audits
agent_statement_imports agent_statement_lines agent_statement_matches agent_statement_confirmed_matches
agent_statement_audits agent_statement_evidences agent_statement_evidence_decisions
agent_governances agent_governance_submissions agent_governance_audits agent_governance_risk_usages`)
	if a07 {
		// These models belong to the new live A07 ledger, not retired legacy liabilities.
		for _, schema := range append(AgentSimpleModels(), &AgentSimpleBill{}, &AgentSimpleCreditDebt{}, &AgentSimpleReminder{}, &AgentSimpleCodeViewAudit{}, &AgentSimpleExitRecord{}, &AgentSimpleReminderReceipt{}, &AgentSimpleMigration{}) {
			statement := &gorm.Statement{DB: db}
			if err := statement.Parse(schema); err != nil {
				return nil, err
			}
			archives = append(archives, statement.Schema.Table)
		}
		rules["agent_payment_receipts"] = nonAgentRetirementRule{predicate: "purpose IS NULL OR purpose NOT IN ?", args: []any{[]string{"topup", "payment_code", "agent_recharge"}}}
		rules["agent_commission_outboxes"] = nonAgentRetirementRule{predicate: "(status IS NULL OR status <> ?) AND (route IS NULL OR route <> ?)", args: []any{"done", "referral"}}
	}
	var blocks []NonAgentCutoverBlock
	err := db.Transaction(func(tx *gorm.DB) error {
		var tables []string
		var err error
		if tx.Dialector.Name() == "postgres" {
			// information_schema hides tables without SELECT privilege and GORM's
			// GetTables only sees current_schema. Catalog visibility follows the
			// effective search_path and still exposes a table we must fail to read.
			err = tx.Raw("SELECT relname FROM pg_catalog.pg_class WHERE pg_catalog.pg_table_is_visible(oid) AND relkind IN ('r','p','v','m','f')").Scan(&tables).Error
		} else {
			tables, err = tx.Migrator().GetTables()
		}
		if err != nil {
			return errors.New("non-agent cutover preflight: unable to enumerate database tables")
		}
		slices.Sort(tables)
		retiredPromotions, err := retiredUnusedPromotions(tx, tables)
		if err != nil {
			return errors.New("non-agent cutover preflight: cannot verify retired promotional sources")
		}
		identifier := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
		for _, table := range tables {
			if !strings.HasPrefix(table, "agent_") || slices.Contains(archives, table) {
				continue
			}
			if !identifier.MatchString(table) {
				return errors.New("non-agent cutover preflight: unrecognized legacy table name")
			}
			query := tx.Table(table)
			if len(retiredPromotions) > 0 {
				switch table {
				case "agent_fund_batches":
					query = query.Where("id NOT IN ?", retiredPromotions)
				case "agent_fund_slices":
					query = query.Where("batch_id NOT IN ?", retiredPromotions)
				}
			}
			if rule, ok := rules[table]; ok {
				query = query.Where(rule.predicate, rule.args...)
			}
			var count int64
			if err := query.Count(&count).Error; err != nil {
				return fmt.Errorf("non-agent cutover preflight: cannot inspect %s; retain the original version", table)
			}
			if count != 0 {
				blocks = append(blocks, NonAgentCutoverBlock{Table: table, Rows: count})
			}
		}
		// Campaign grants share the ordinary bonus-source ledger. Keep ordinary
		// sources and restoration logic, but do not retire live campaign rights
		// merely because an agent's own cash account happens to be empty.
		if slices.Contains(tables, "bonus_source_lots") {
			var candidates int64
			candidate := "l.kind IS NULL OR l.campaign_id IS NULL OR l.claim_id IS NULL OR l.kind LIKE 'campaign%' OR l.campaign_id <> 0 OR l.claim_id <> 0"
			if err := tx.Table("bonus_source_lots AS l").Where(candidate).Count(&candidates).Error; err != nil {
				return errors.New("non-agent cutover preflight: cannot inspect bonus-source provenance")
			}
			if candidates > 0 {
				if !slices.Contains(tables, "bonus_source_states") {
					return errors.New("non-agent cutover preflight: campaign bonus-source states are unavailable")
				}
				var count int64
				unresolved := "l.kind IS NULL OR l.campaign_id IS NULL OR l.claim_id IS NULL OR l.user_id IS NULL OR l.grant_quota IS NULL OR l.grant_quota < 0 OR s.lot_id IS NULL OR s.user_id IS NULL OR s.user_id <> l.user_id OR s.available IS NULL OR s.outstanding IS NULL OR s.expired IS NULL OR s.revoked IS NULL OR s.legacy_restored IS NULL OR s.available <> 0 OR s.outstanding <> 0 OR s.expired < 0 OR s.revoked < 0 OR s.legacy_restored <> 0 OR l.grant_quota <> s.expired + s.revoked"
				if err := tx.Table("bonus_source_lots AS l").Joins("LEFT JOIN bonus_source_states AS s ON s.lot_id = l.id").Where("(" + candidate + ") AND (" + unresolved + ")").Count(&count).Error; err != nil {
					return errors.New("non-agent cutover preflight: cannot inspect campaign bonus-source obligations")
				}
				if count > 0 {
					blocks = append(blocks, NonAgentCutoverBlock{Table: "bonus_source_lots", Rows: count})
				}
			}
		}
		// Old releases predate some marker columns. Inspect only columns which
		// exist, but never ignore an inspection error or an unknown agent table.
		if slices.Contains(tables, "redemptions") {
			columns, err := tx.Migrator().ColumnTypes("redemptions")
			if err != nil {
				return errors.New("non-agent cutover preflight: cannot inspect redemptions")
			}
			var markers, unknownMarkers []string
			hasReserved := false
			for _, column := range columns {
				switch column.Name() {
				case "budget_agent_id", "customer_user_id", "delivery_id":
					markers = append(markers, column.Name()+" <> 0")
					unknownMarkers = append(unknownMarkers, column.Name()+" IS NULL")
				case "budget_reserved":
					hasReserved = true
					markers = append(markers, "budget_reserved IS NULL OR budget_reserved = TRUE")
				case "source_protected":
					markers = append(markers, "source_protected = TRUE")
					unknownMarkers = append(unknownMarkers, "source_protected IS NULL")
				}
			}
			if len(markers) > 0 {
				var count int64
				predicate := "(" + strings.Join(markers, " OR ") + ") AND (status IS NULL OR status NOT IN ?)"
				if len(unknownMarkers) > 0 {
					predicate = "(" + predicate + ") OR " + strings.Join(unknownMarkers, " OR ")
				}
				if hasReserved {
					predicate = "(" + predicate + ") OR budget_reserved IS NULL OR budget_reserved = TRUE"
				}
				query := tx.Table("redemptions").Where(predicate, []int{common.RedemptionCodeStatusDisabled, common.RedemptionCodeStatusUsed})
				if a07 {
					// A completed migration distinguishes new live code obligations
					// from retired legacy markers. Unknown or NULL provenance never
					// qualifies for this exception, including on used/disabled codes.
					validA07 := strings.Join([]string{
						"agent_simple_mode IN ('direct', 'credit', 'payment')",
						"budget_agent_id > 0 AND user_id = budget_agent_id",
						"customer_user_id = 0 AND delivery_id = 0 AND source_protected = FALSE",
						"budget_reserved IS NOT NULL AND agent_simple_credit IS NOT NULL AND budget_version > 0",
						"quota > 0 AND quota <= ? AND agent_simple_cost_quota >= 0 AND agent_simple_cost_quota <= ?",
						"((status = ? AND budget_reserved = TRUE AND used_user_id = 0 AND redeemed_time = 0) OR (status = ? AND budget_reserved = FALSE AND used_user_id = 0 AND redeemed_time = 0) OR (status = ? AND budget_reserved = FALSE AND used_user_id > 0 AND redeemed_time > 0))",
						"EXISTS (SELECT 1 FROM agent_simple_accounts AS a WHERE a.agent_user_id = redemptions.budget_agent_id)",
						"((agent_simple_mode IN ('direct', 'credit') AND agent_simple_payment_id = 0 AND creation_operation_id > 0) OR (agent_simple_mode = 'payment' AND agent_simple_payment_id > 0 AND agent_simple_credit = FALSE AND agent_simple_cost_quota = 0 AND EXISTS (SELECT 1 FROM agent_simple_payment_orders AS p WHERE p.id = redemptions.agent_simple_payment_id AND p.redemption_id = redemptions.id AND p.agent_user_id = redemptions.budget_agent_id)))",
					}, " AND ")
					query = tx.Table("redemptions").Where("(("+predicate+") OR (agent_simple_mode IS NOT NULL AND agent_simple_mode <> ''))", []int{common.RedemptionCodeStatusDisabled, common.RedemptionCodeStatusUsed}).
						Where("COALESCE(("+validA07+"), FALSE) = FALSE", common.MaxWalletQuota, common.MaxWalletQuota, common.RedemptionCodeStatusEnabled, common.RedemptionCodeStatusDisabled, common.RedemptionCodeStatusUsed)
				}
				if err := query.Count(&count).Error; err != nil {
					return errors.New("non-agent cutover preflight: cannot inspect legacy redemption obligations")
				}
				if count > 0 {
					blocks = append(blocks, NonAgentCutoverBlock{Table: "redemptions", Rows: count})
				}
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return blocks, err
	}
	if len(blocks) > 0 {
		return blocks, fmt.Errorf("%w (%v)", ErrNonAgentCutoverBlocked, blocks)
	}
	return blocks, nil
}
