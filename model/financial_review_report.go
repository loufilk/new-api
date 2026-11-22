package model

import (
	"context"
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// FinancialReviewEntity selects one independent, bounded scan. The report is
// evidence for an operator, never authorization to refund or replay a request.
type FinancialReviewEntity string

const (
	FinancialReviewBilling      FinancialReviewEntity = "billing_finalizations"
	FinancialReviewSubscription FinancialReviewEntity = "subscription_receipts"
	FinancialReviewTask         FinancialReviewEntity = "tasks"
	FinancialReviewMidjourney   FinancialReviewEntity = "midjourneys"
	FinancialReviewMaxPageSize                        = 200
)

var (
	ErrFinancialReviewOptions  = errors.New("invalid financial review options")
	ErrFinancialReviewDatabase = errors.New("financial review database dialect is unsupported")
	// Do not wrap driver errors: they may contain connection details. Operators
	// must diagnose database access separately, without exporting credentials.
	ErrFinancialReviewQuery = errors.New("financial review SELECT failed; database access requires separate diagnosis")
)

type FinancialReviewReportOptions struct {
	Entity FinancialReviewEntity
	Offset int
	Limit  int // Required: 1..FinancialReviewMaxPageSize. No unbounded default.
}

type FinancialReviewSchema struct {
	// available, partial, partial_unscannable, or missing_or_not_visible.
	// A missing schema is not an empty, successfully reconciled history.
	Status         string   `json:"status"`
	MissingColumns []string `json:"missing_columns,omitempty"`
}

// FinancialReviewItem deliberately contains no token material, provider IDs or
// payloads, raw private_data, free-form error/review reasons, or database details.
// Missing amount columns are absent from Amounts rather than fabricated zeros.
type FinancialReviewItem struct {
	RowID              int64            `json:"row_id,omitempty"`
	RowOffset          int              `json:"row_offset"`
	RequestID          string           `json:"request_id,omitempty"`
	UserID             int              `json:"user_id,omitempty"`
	State              string           `json:"state"`
	BillingSource      string           `json:"billing_source,omitempty"`
	SubscriptionID     int64            `json:"subscription_id,omitempty"`
	SubscriptionPeriod *int64           `json:"subscription_period,omitempty"`
	Amounts            map[string]int64 `json:"amounts,omitempty"`
	Issues             []string         `json:"issues"`
	FailureCode        string           `json:"failure_code,omitempty"`
}

type FinancialReviewReportPage struct {
	Entity     FinancialReviewEntity `json:"entity"`
	Schema     FinancialReviewSchema `json:"schema"`
	Offset     int                   `json:"offset"`
	Scanned    int                   `json:"scanned"`
	NextOffset int                   `json:"next_offset"`
	HasMore    bool                  `json:"has_more"`
	Items      []FinancialReviewItem `json:"items"`
}

// ReadFinancialReviewReport issues SELECT statements only against the explicit
// handle. It does not initialize the application, migrate, start a transaction,
// lock rows, consult global DB, repair history, or schedule recovery. A caller
// should supply read-only credentials/a read-only snapshot for defense in depth.
//
// Pagination scans rows, not findings: an empty Items page can still have
// HasMore=true. Offset pagination is deterministic only on an unchanged snapshot;
// concurrent inserts/deletes can otherwise cause skips/duplicates across pages.
func ReadFinancialReviewReport(ctx context.Context, db *gorm.DB, opts FinancialReviewReportOptions) (*FinancialReviewReportPage, error) {
	if ctx == nil || db == nil || db.Config == nil || db.Dialector == nil || db.DryRun || opts.Limit < 1 || opts.Limit > FinancialReviewMaxPageSize || opts.Offset < 0 || opts.Offset > math.MaxInt-opts.Limit {
		return nil, ErrFinancialReviewOptions
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var model any
	var key string
	var wanted []string
	switch opts.Entity {
	case FinancialReviewBilling:
		model, key = &BillingFinalization{}, "request_id"
		wanted = []string{"request_id", "user_id", "source", "source_held", "token_held", "pre_consumed", "subscription_id", "subscription_period", "state", "actual_quota", "last_error_code"}
	case FinancialReviewSubscription:
		model, key = &SubscriptionPreConsumeRecord{}, "id"
		wanted = []string{"id", "request_id", "user_id", "user_subscription_id", "pre_consumed", "quota_period", "reserved_amount", "settled_amount", "status"}
	case FinancialReviewTask:
		model, key = &Task{}, "id"
		wanted = []string{"id", "user_id", "quota", "status", "settlement_pending", "private_data"}
	case FinancialReviewMidjourney:
		model, key = &Midjourney{}, "id"
		wanted = []string{"id", "user_id", "quota", "status", "token_id", "token_fingerprint", "billing_request_id", "billing_original_quota", "billing_accounted", "wallet_debit"}
	default:
		return nil, ErrFinancialReviewOptions
	}
	// Reset caller query clauses (including any lock) without losing its actual
	// connection/transaction or naming strategy. SkipHooks prevents model hooks.
	// Do not let a caller's SQL logger export unsanitized driver errors.
	reader := db.Session(&gorm.Session{NewDB: true, SkipHooks: true, Context: ctx, Logger: logger.Discard})
	statement := &gorm.Statement{DB: reader}
	if err := statement.Parse(model); err != nil {
		return nil, ErrFinancialReviewQuery
	}
	columns, err := financialReviewColumns(ctx, reader, statement.Table)
	if err != nil {
		return nil, err
	}
	page := &FinancialReviewReportPage{Entity: opts.Entity, Offset: opts.Offset, NextOffset: opts.Offset, Items: []FinancialReviewItem{}}
	page.Schema.Status = "available"
	selected := make([]string, 0, len(wanted))
	for _, name := range wanted {
		if columns[name] {
			selected = append(selected, name)
		} else {
			page.Schema.MissingColumns = append(page.Schema.MissingColumns, name)
		}
	}
	if len(columns) == 0 {
		page.Schema.Status = "missing_or_not_visible"
		return page, nil
	}
	if len(page.Schema.MissingColumns) != 0 {
		page.Schema.Status = "partial"
	}
	if !columns[key] {
		page.Schema.Status = "partial_unscannable"
		return page, nil
	}
	var rows []financialReviewRow
	err = reader.Table(statement.Table).Select(selected).
		Order(clause.OrderByColumn{Column: clause.Column{Name: key}}).
		Offset(opts.Offset).Limit(opts.Limit + 1).Find(&rows).Error
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrFinancialReviewQuery
	}
	page.HasMore = len(rows) > opts.Limit
	if page.HasMore {
		rows = rows[:opts.Limit]
	}
	page.Scanned = len(rows)
	page.NextOffset += page.Scanned
	for i := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := rows[i].review(opts.Entity, columns)
		if len(item.Issues) != 0 {
			item.RowOffset = opts.Offset + i
			page.Items = append(page.Items, item)
		}
	}
	return page, nil
}

// Schema inspection intentionally avoids Migrator: some dialects use SHOW or
// PRAGMA statements there. These three branches are SELECT-only, including the
// SQLite table-valued pragma function. Use the supplied handle's dialect rather
// than application-global database settings, which need not describe this DB.
func financialReviewColumns(ctx context.Context, db *gorm.DB, table string) (map[string]bool, error) {
	var names []string
	var result *gorm.DB
	schemaName, tableName, qualified := strings.Cut(table, ".")
	if !qualified {
		tableName = table
	}
	switch db.Dialector.Name() {
	case "sqlite":
		if !qualified {
			schemaName = "main"
		}
		result = db.Raw("SELECT name FROM pragma_table_info(?, ?)", tableName, schemaName).Scan(&names)
	case "mysql":
		if qualified {
			result = db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema = ? AND table_name = ?", schemaName, tableName).Scan(&names)
		} else {
			result = db.Raw("SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ?", tableName).Scan(&names)
		}
	case "postgres":
		// Resolve the same search_path as the later table SELECT, not merely
		// current_schema(): the table can exist in a later visible schema.
		lookup := `"` + strings.ReplaceAll(tableName, `"`, `""`) + `"`
		if qualified {
			lookup = `"` + strings.ReplaceAll(schemaName, `"`, `""`) + `".` + lookup
		}
		result = db.Raw("SELECT attname FROM pg_catalog.pg_attribute WHERE attrelid = to_regclass(?) AND attnum > 0 AND NOT attisdropped", lookup).Scan(&names)
	default:
		return nil, ErrFinancialReviewDatabase
	}
	if result.Error != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrFinancialReviewQuery
	}
	columns := make(map[string]bool, len(names))
	for _, name := range names {
		columns[name] = true
	}
	return columns, nil
}

// This private projection is never returned or logged. In particular, scanning
// private_data into a string avoids application Scanner hooks and rejects a bad
// historical JSON row locally instead of losing the whole report page.
type financialReviewRow struct {
	ID, UserSubscriptionID, SubscriptionID, SubscriptionPeriod, QuotaPeriod int64
	RequestID, Source, State, Status, PrivateData, LastErrorCode            string
	UserID, TokenID                                                         int
	SourceHeld, TokenHeld, PreConsumed, ActualQuota                         int64
	ReservedAmount, SettledAmount, Quota, BillingOriginalQuota              int64
	SettlementPending, BillingAccounted                                     bool
	TokenFingerprint, BillingRequestID, WalletDebit                         string
}

func (row *financialReviewRow) review(entity FinancialReviewEntity, columns map[string]bool) FinancialReviewItem {
	item := FinancialReviewItem{RowID: row.ID, UserID: row.UserID, State: "unknown", Amounts: map[string]int64{}}
	switch entity {
	case FinancialReviewBilling:
		item.RequestID = financialReviewRequestID(row.RequestID)
		item.BillingSource = financialReviewSource(row.Source)
		item.SubscriptionID = row.SubscriptionID
		if columns["subscription_period"] {
			item.SubscriptionPeriod = &row.SubscriptionPeriod
		}
		for name, amount := range map[string]int64{"source_held": row.SourceHeld, "token_held": row.TokenHeld, "pre_consumed": row.PreConsumed, "actual_quota": row.ActualQuota} {
			if columns[name] {
				item.Amounts[name] = amount
			}
		}
		switch row.State {
		case BillingRefundPending:
			item.Issues = append(item.Issues, "legacy_refund_pending")
		case BillingHeld:
			item.Issues = append(item.Issues, "held_not_refund_authorization")
			if row.LastErrorCode == "initial_refund_unconfirmed" {
				item.Issues = append(item.Issues, "initial_reservation_refund_requires_review")
			}
		case BillingDeliveryUnknown, "held_unknown", "unknown":
			item.Issues = append(item.Issues, "delivery_evidence_unknown")
		case BillingRefundReview:
			item.Issues = append(item.Issues, "refund_requires_review")
		case BillingSettlementReview:
			item.Issues = append(item.Issues, "settlement_requires_review")
			if columns["actual_quota"] && columns["pre_consumed"] && row.ActualQuota >= 0 && row.ActualQuota <= common.MaxQuota && row.PreConsumed >= 0 && row.PreConsumed <= common.MaxQuota {
				item.Amounts["settlement_delta"] = row.ActualQuota - row.PreConsumed
			} else {
				item.Issues = append(item.Issues, "settlement_amount_evidence_invalid")
			}
			// Never export arbitrary historical free text as an error code.
			switch row.LastErrorCode {
			case "insufficient_balance", "transient_database", "settlement_unconfirmed", "deadline_exceeded", "canceled", "missing_record", "terminal_owner_conflict", "ledger_evidence", "receipt_invalid", "identity_mismatch", "source_evidence", "owner_missing", "actual_mismatch", "recovery_error":
				item.FailureCode = row.LastErrorCode
			default:
				item.FailureCode = "unclassified"
			}
		case BillingRefundReady:
			item.Issues = append(item.Issues, "versioned_refund_pending")
		}
		if len(item.Issues) != 0 {
			item.State = row.State // Only the fixed, allowlisted states above.
		}
		if !columns["state"] {
			item.Issues = append(item.Issues, "billing_state_unavailable")
		}
		if row.Source == "subscription" && (!columns["subscription_period"] || row.SubscriptionPeriod <= 0) {
			item.Issues = append(item.Issues, "subscription_period_unknown")
		}
		if len(item.Issues) != 0 && (item.RequestID == "" || row.UserID <= 0) {
			item.Issues = append(item.Issues, "billing_identity_incomplete")
		}
	case FinancialReviewSubscription:
		item.RequestID = financialReviewRequestID(row.RequestID)
		item.BillingSource = "subscription"
		item.SubscriptionID = row.UserSubscriptionID
		if columns["quota_period"] {
			item.SubscriptionPeriod = &row.QuotaPeriod
		}
		switch row.Status {
		case "consumed", "settled", "refunded":
			item.State = row.Status
		}
		for name, amount := range map[string]int64{"pre_consumed": row.PreConsumed, "reserved_amount": row.ReservedAmount, "settled_amount": row.SettledAmount} {
			if columns[name] {
				item.Amounts[name] = amount
			}
		}
		if !columns["quota_period"] || row.QuotaPeriod <= 0 {
			item.Issues = append(item.Issues, "subscription_period_unknown")
		}
		if !columns["reserved_amount"] || row.ReservedAmount < row.PreConsumed || row.ReservedAmount > common.MaxQuota {
			item.Issues = append(item.Issues, "subscription_reservation_evidence_incomplete")
		}
		if item.RequestID == "" || row.UserID <= 0 || row.UserSubscriptionID <= 0 {
			item.Issues = append(item.Issues, "subscription_identity_incomplete")
		}
	case FinancialReviewTask:
		item = row.reviewTask(columns, item)
	case FinancialReviewMidjourney:
		item = row.reviewMidjourney(columns, item)
	}
	return item
}

// Decode only the financial allowlist. Credentials, provider payload, execution
// plugin metadata and free-form reasons are deliberately not represented here.
type financialReviewTaskIdentity struct {
	RequestID string `json:"request_id"`
	Execution *struct {
		RequestID string `json:"request_id"`
	} `json:"execution"`
	BillingSource              string        `json:"billing_source"`
	SubscriptionID             int64         `json:"subscription_id"`
	SubscriptionQuotaPeriod    int64         `json:"subscription_quota_period"`
	SubscriptionReservedAmount int64         `json:"subscription_reserved_amount"`
	TokenID                    int           `json:"token_id"`
	TokenFingerprint           string        `json:"token_fingerprint"`
	WalletDebit                []LedgerShare `json:"wallet_debit"`
	PendingSettlement          *struct{}     `json:"pending_settlement"`
	FinancialVersion           int           `json:"financial_version"`
	FinancialIntent            *struct{}     `json:"financial_intent"`
	FinancialReviewReason      string        `json:"financial_review_reason"`
}

func (row *financialReviewRow) reviewTask(columns map[string]bool, item FinancialReviewItem) FinancialReviewItem {
	item.State = financialReviewTaskState(row.Status)
	if columns["quota"] {
		item.Amounts["quota"] = row.Quota
	}
	var identity financialReviewTaskIdentity
	if !columns["private_data"] || row.PrivateData == "" || row.PrivateData == "null" {
		item.Issues = append(item.Issues, "task_private_identity_unavailable")
	} else if len(row.PrivateData) > 1<<20 {
		item.Issues = append(item.Issues, "task_private_identity_oversized")
	} else if err := common.UnmarshalJsonStr(row.PrivateData, &identity); err != nil {
		item.Issues = append(item.Issues, "task_private_identity_invalid")
	}
	requestID := identity.RequestID
	if identity.Execution != nil && identity.Execution.RequestID != "" {
		requestID = identity.Execution.RequestID
		if identity.RequestID != "" && identity.RequestID != requestID {
			item.Issues = append(item.Issues, "task_request_identity_conflict")
		}
	}
	item.RequestID = financialReviewRequestID(requestID)
	item.BillingSource = financialReviewSource(identity.BillingSource)
	item.SubscriptionID = identity.SubscriptionID
	if identity.BillingSource == "subscription" {
		item.SubscriptionPeriod = &identity.SubscriptionQuotaPeriod
		item.Amounts["original_reserved_amount"] = identity.SubscriptionReservedAmount
		if identity.SubscriptionQuotaPeriod <= 0 {
			item.Issues = append(item.Issues, "subscription_period_unknown")
		}
		if identity.SubscriptionID <= 0 || identity.SubscriptionReservedAmount <= 0 {
			item.Issues = append(item.Issues, "task_subscription_identity_incomplete")
		}
	}
	// Match the legacy guard: a historical PendingSettlement is never a v2
	// intent, even if an inconsistent row also happens to carry version=2.
	if identity.PendingSettlement != nil || row.SettlementPending && identity.FinancialVersion != 2 {
		item.Issues = append(item.Issues, "legacy_task_settlement_pending")
	} else if row.SettlementPending || identity.FinancialIntent != nil {
		item.Issues = append(item.Issues, "versioned_task_settlement_pending")
	}
	if identity.FinancialReviewReason != "" {
		item.Issues = append(item.Issues, "task_financial_review_required")
	}
	if identity.FinancialVersion != 2 || item.RequestID == "" || row.UserID <= 0 || item.BillingSource == "unknown" || identity.TokenID < 0 || identity.TokenID > 0 && len(identity.TokenFingerprint) != 64 {
		item.Issues = append(item.Issues, "task_billing_identity_incomplete")
	}
	if identity.BillingSource == "wallet" && (row.Quota < 0 || row.Quota > common.MaxQuota || ValidateTaskWalletReceipt(identity.WalletDebit, int(row.Quota)) != nil) {
		item.Issues = append(item.Issues, "task_wallet_evidence_incomplete")
	}
	return item
}

func (row *financialReviewRow) reviewMidjourney(columns map[string]bool, item FinancialReviewItem) FinancialReviewItem {
	item.State = financialReviewTaskState(row.Status)
	item.BillingSource = "wallet"
	item.RequestID = financialReviewRequestID(row.BillingRequestID)
	if columns["quota"] {
		item.Amounts["quota"] = row.Quota
	}
	if columns["billing_original_quota"] {
		item.Amounts["original_quota"] = row.BillingOriginalQuota
	}
	identity := Midjourney{UserId: row.UserID, BillingRequestID: row.BillingRequestID, TokenId: row.TokenID, TokenFingerprint: row.TokenFingerprint}
	if row.BillingOriginalQuota >= 0 && row.BillingOriginalQuota <= common.MaxQuota {
		identity.BillingOriginalQuota = int(row.BillingOriginalQuota)
	} else {
		item.Issues = append(item.Issues, "midjourney_original_quota_invalid")
	}
	if !identity.HasCurrentBillingIdentity() || item.RequestID == "" || !columns["billing_original_quota"] || !columns["token_id"] {
		item.Issues = append(item.Issues, "midjourney_billing_identity_incomplete")
	}
	if row.Quota != 0 && (!row.BillingAccounted || row.Quota != row.BillingOriginalQuota) {
		item.Issues = append(item.Issues, "midjourney_charge_evidence_incomplete")
	}
	var shares []LedgerShare
	if row.WalletDebit == "" || row.WalletDebit == "null" || len(row.WalletDebit) > 1<<20 || common.UnmarshalJsonStr(row.WalletDebit, &shares) != nil || row.Quota < 0 || row.Quota > common.MaxQuota || ValidateTaskWalletReceipt(shares, int(row.Quota)) != nil {
		item.Issues = append(item.Issues, "midjourney_wallet_evidence_incomplete")
	}
	return item
}

func financialReviewRequestID(value string) string {
	if value == "" || len(value) > 64 {
		return ""
	}
	for _, c := range value {
		if c < '!' || c > '~' {
			return ""
		}
	}
	return value
}

func financialReviewSource(value string) string {
	if value == "wallet" || value == "subscription" {
		return value
	}
	return "unknown"
}

func financialReviewTaskState(value string) string {
	switch TaskStatus(value) {
	case TaskStatusNotStart, TaskStatusSubmitted, TaskStatusQueued, TaskStatusInProgress, TaskStatusFailure, TaskStatusSuccess, TaskStatusUnknown:
		return value
	default:
		return "unknown"
	}
}
