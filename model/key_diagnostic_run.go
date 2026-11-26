package model

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	KeyDiagnosticQueued  = "queued"
	KeyDiagnosticRunning = "running"
)

// Reuse the existing, already-private diagnostics row and admission gate. ID
// in PostgreSQL is namespaced by owner; the browser's UUID is only a replay
// key. The private versioned envelope contains no IP, session or credentials.
var keyDiagnosticIDNamespace = uuid.MustParse("3ff4e24e-733b-43e4-8310-f30fdd546e93")

type KeyDiagnosticDetails struct {
	Version     int    `json:"version"`
	ClientID    string `json:"client_id"`
	ElapsedMS   int64  `json:"elapsed_ms,omitempty"`
	HTTPStatus  int    `json:"http_status,omitempty"`
	CandyResult string `json:"candy_result,omitempty"`
}

func KeyDiagnosticRowID(owner int, clientID string) string {
	return uuid.NewSHA1(keyDiagnosticIDNamespace, []byte(strconv.Itoa(owner)+":"+clientID)).String()
}

func ReadKeyDiagnosticDetails(run ModelTestRun) KeyDiagnosticDetails {
	var details KeyDiagnosticDetails
	if common.Unmarshal([]byte(run.SamplesJSON), &details) == nil && details.Version == 1 &&
		details.ClientID != "" && KeyDiagnosticRowID(run.UserID, details.ClientID) == run.ID {
		return details
	}
	return KeyDiagnosticDetails{ClientID: run.ID}
}

func keyDiagnosticQuery(tx *gorm.DB, owner int) *gorm.DB {
	return tx.Where("user_id = ? AND source = ?", owner, KeyDiagnosticSource)
}

func getKeyDiagnosticByUserID(tx *gorm.DB, owner int, clientID string) (*ModelTestRun, error) {
	var row ModelTestRun
	if err := keyDiagnosticQuery(tx, owner).Where("id = ?", KeyDiagnosticRowID(owner, clientID)).First(&row).Error; err == nil {
		// A prior client was allowed to pick a raw ID globally. It cannot
		// claim a new row's namespaced ID, even if it chose that precise UUID.
		details := ReadKeyDiagnosticDetails(row)
		if details.Version != 1 || details.ClientID != clientID {
			return nil, ErrModelTestConflict
		}
		return &row, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	// Read old owned receipts only; they have no new dispatch intent.
	if err := keyDiagnosticQuery(tx, owner).Where("id = ?", clientID).First(&row).Error; err != nil {
		return nil, err
	}
	if ReadKeyDiagnosticDetails(row).Version != 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &row, nil
}

func GetKeyDiagnosticRun(ctx context.Context, owner int, clientID string) (*ModelTestRun, error) {
	return getKeyDiagnosticByUserID(DB.WithContext(ctx), owner, clientID)
}

func ListKeyDiagnosticRuns(ctx context.Context, owner int) ([]ModelTestRun, error) {
	runs := make([]ModelTestRun, 0)
	err := keyDiagnosticQuery(DB.WithContext(ctx).Model(&ModelTestRun{}), owner).
		Select("id", "user_id", "token_id", "model", "benchmark_id", "status", "created_at", "completed_at", "expires_at", "request_id", "error_code", "samples_json").
		Order("created_at DESC, id DESC").Limit(30).Find(&runs).Error
	return runs, err
}

func KeyDiagnosticSameInput(run *ModelTestRun, tokenID int, modelName, question string, limit int) bool {
	// A Key may since have been disabled, removed, or assigned to another
	// routing group. Replaying the same consent still returns its receipt.
	return run.TokenID == tokenID && run.Model == modelName &&
		run.BenchmarkID == question && run.MaxOutputTokens == limit
}

func CreateKeyDiagnosticRun(ctx context.Context, run *ModelTestRun, clientID string) (replayed bool, err error) {
	run.Source, run.Status = KeyDiagnosticSource, KeyDiagnosticQueued
	run.ID = KeyDiagnosticRowID(run.UserID, clientID)
	frozen, err := common.Marshal(KeyDiagnosticDetails{Version: 1, ClientID: clientID})
	if err != nil {
		return false, err
	}
	run.SamplesJSON = string(frozen)
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockModelFingerprintRetention(tx); err != nil {
			return err
		}
		var owner User
		if err := lockForUpdate(tx).Select("id", "status").First(&owner, run.UserID).Error; err != nil {
			return err
		}
		if owner.Status != common.UserStatusEnabled {
			return ErrKeyDiagnosticNotAuthorized
		}
		existing, err := getKeyDiagnosticByUserID(tx, run.UserID, clientID)
		if err == nil {
			if !KeyDiagnosticSameInput(existing, run.TokenID, run.Model, run.BenchmarkID, run.MaxOutputTokens) {
				return ErrModelTestConflict
			}
			*run, replayed = *existing, true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().Unix()
		active := func() *gorm.DB {
			return tx.Model(&ModelTestRun{}).Where("expires_at > ? AND (status = ? OR (source = ? AND status = ?))",
				now, KeyDiagnosticRunning, KeyDiagnosticSource, KeyDiagnosticQueued)
		}
		var ownerActive, globalActive int64
		if err := active().Where("user_id = ?", run.UserID).Count(&ownerActive).Error; err != nil {
			return err
		}
		if ownerActive != 0 {
			return &KeyDiagnosticLimit{Reason: "user_active"}
		}
		// Only the three newest rows matter. Preserve the original inclusive
		// one-hour limit across all sources. The third newest determines when
		// a slot opens, even with more historical rows than the current cap.
		var thirdNewest []int64
		if err := tx.Model(&ModelTestRun{}).Where("user_id = ? AND created_at >= ?", run.UserID, now-3600).
			Order("created_at DESC").Offset(2).Limit(1).Pluck("created_at", &thirdNewest).Error; err != nil {
			return err
		}
		if len(thirdNewest) != 0 {
			return &KeyDiagnosticLimit{Reason: "hourly", RetryAfterSeconds: thirdNewest[0] + 3601 - now}
		}
		if err := active().Count(&globalActive).Error; err != nil {
			return err
		}
		if globalActive >= 4 {
			return &KeyDiagnosticLimit{Reason: "capacity"}
		}
		return tx.Create(run).Error
	})
	return
}

// KeyDiagnosticLimit describes a pre-insert admission refusal, not a
// running task or an upstream 429. Concurrent work has no fixed end time.
type KeyDiagnosticLimit struct {
	Reason            string
	RetryAfterSeconds int64
}

func (*KeyDiagnosticLimit) Error() string { return ErrModelTestLimit.Error() }
func (*KeyDiagnosticLimit) Unwrap() error { return ErrModelTestLimit }

var ErrKeyDiagnosticNotAuthorized = errors.New("key diagnostic authority is no longer current")

// Claim is one-way. Only the process receiving a successful committed CAS
// may enter the relay. No retry/resume after claim, even on ambiguous ACK.
func ClaimKeyDiagnosticRun(ctx context.Context, row ModelTestRun, identity AuthSessionIdentity, leaseUntil int64) (claimed bool, err error) {
	if row.UserID != identity.UserID || ReadKeyDiagnosticDetails(row).Version != 1 {
		return false, ErrKeyDiagnosticNotAuthorized
	}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := ValidateAuthSessionWithTx(tx, identity); err != nil {
			if errors.Is(err, ErrUserSessionInactive) || errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrKeyDiagnosticNotAuthorized
			}
			return err
		}
		res := keyDiagnosticQuery(tx.Model(&ModelTestRun{}), row.UserID).
			Where("id = ? AND request_id = ? AND status = ? AND expires_at > ?", row.ID, row.RequestID, KeyDiagnosticQueued, time.Now().Unix()).
			Updates(map[string]any{"status": KeyDiagnosticRunning, "expires_at": leaseUntil})
		claimed = res.RowsAffected == 1
		return res.Error
	})
	return claimed && err == nil, err
}

func FinishSavedKeyDiagnostic(ctx context.Context, run ModelTestRun, fromStatus string, details KeyDiagnosticDetails) error {
	metadata, err := common.Marshal(details)
	if err != nil {
		return err
	}
	// Late result may settle an expired visible running projection, but can
	// never replace a committed terminal result or create another attempt.
	res := keyDiagnosticQuery(DB.WithContext(ctx).Model(&ModelTestRun{}), run.UserID).
		Where("id = ? AND request_id = ? AND status = ?", run.ID, run.RequestID, fromStatus).
		Updates(map[string]any{"status": run.Status, "error_code": run.ErrorCode,
			"completed_at": run.CompletedAt, "artwork_html": run.ArtworkHTML, "samples_json": string(metadata)})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrModelTestConflict
	}
	return nil
}

type KeyDiagnosticUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type KeyDiagnosticBill struct {
	Status string              `json:"status"`
	Scope  string              `json:"scope"`
	Quota  *int                `json:"quota,omitempty"`
	Usage  *KeyDiagnosticUsage `json:"-"`
}

func ReadKeyDiagnosticBills(ctx context.Context, owner int, runs []ModelTestRun) map[string]KeyDiagnosticBill {
	reqs := make([]string, 0, len(runs))
	tokens := make(map[string]int, len(runs))
	for _, run := range runs {
		if run.UserID == owner && run.TokenID > 0 && run.RequestID != "" {
			reqs = append(reqs, run.RequestID)
			tokens[run.RequestID] = run.TokenID
		}
	}
	bills := make(map[string]KeyDiagnosticBill, len(reqs))
	if len(reqs) == 0 {
		return bills
	}
	var rows []BillingFinalization
	if err := DB.WithContext(ctx).Select("request_id", "token_id", "state", "actual_quota").
		Where("request_id IN ? AND user_id = ? AND state IN ?", reqs, owner, []string{BillingSettled, BillingRefunded}).Find(&rows).Error; err != nil {
		return bills
	}
	for _, row := range rows {
		if row.TokenID != tokens[row.RequestID] || row.ActualQuota < 0 || row.ActualQuota > common.MaxQuota || (row.State == BillingRefunded && row.ActualQuota != 0) {
			continue
		}
		qty := row.ActualQuota
		bills[row.RequestID] = KeyDiagnosticBill{Status: row.State, Quota: &qty}
	}
	appendVerifiedKeyDiagnosticUsage(ctx, owner, tokens, bills)
	return bills
}

func appendVerifiedKeyDiagnosticUsage(ctx context.Context, owner int, tokens map[string]int, bills map[string]KeyDiagnosticBill) {
	if LOG_DB == nil {
		return
	}
	ids := make([]string, 0, len(bills))
	for req, bill := range bills {
		if bill.Status == BillingSettled {
			ids = append(ids, req)
		}
	}
	if len(ids) == 0 {
		return
	}
	// A separately configured projection may be delayed, duplicated or
	// unavailable. It never supplies the charge itself. Truncating a
	// potentially non-unique page cannot prove uniqueness for any row.
	limit := 2 * len(ids)
	var logs []Log
	if err := LOG_DB.WithContext(ctx).Select("request_id", "token_id", "quota", "prompt_tokens", "completion_tokens", "other").
		Where("request_id IN ? AND user_id = ? AND type = ?", ids, owner, LogTypeConsume).Limit(limit).Find(&logs).Error; err != nil || len(logs) >= limit {
		return
	}
	count := make(map[string]int, len(logs))
	for _, log := range logs {
		count[log.RequestId]++
	}
	for _, log := range logs {
		bill := bills[log.RequestId]
		if count[log.RequestId] != 1 || bill.Quota == nil || log.Quota != *bill.Quota ||
			log.TokenId != tokens[log.RequestId] || log.PromptTokens < 0 || log.CompletionTokens < 0 ||
			(log.PromptTokens == 0 && log.CompletionTokens == 0) || !keyDiagnosticMeasuredUsage(log) {
			continue
		}
		bill.Usage = &KeyDiagnosticUsage{log.PromptTokens, log.CompletionTokens}
		bills[log.RequestId] = bill
	}
}

func keyDiagnosticMeasuredUsage(log Log) bool {
	var meta struct {
		Admin struct {
			Path string `json:"usage_billing_path"`
		} `json:"admin_info"`
	}
	if len(log.Other) > 64<<10 || common.UnmarshalJsonStr(log.Other, &meta) != nil {
		return false
	}
	switch meta.Admin.Path {
	case "billing-usage-openai", "billing-usage-anthropic", "billing-usage-gemini":
		return true
	case "upstream":
		// Legacy compatible OpenAI text responses don't carry a typed
		// BillingUsage. This projection requires both measured aggregates;
		// the non-stream adaptor marks its missing-prompt fallback local.
		return log.PromptTokens > 0 && log.CompletionTokens > 0
	default:
		return false
	}
}
