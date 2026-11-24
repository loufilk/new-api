//go:build lane_billing_integration_dev

package model

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	laneUsagePending   = "pending"
	laneUsageProjected = "projected"
	laneUsageConflict  = "conflict"
	laneUsageLeaseSec  = 30
)

var ErrLaneBillingUsageProjectionConflict = errors.New("synthetic lane usage projection evidence conflicts")

// The authoritative spend transaction creates this usage-only payload. Names,
// raw keys, token fingerprints, wire hashes, provider IDs, content and client
// IP are neither needed for attribution nor exported to the log database.
// Current username/token names are not substituted for the original IDs.
type laneBillingUsagePayloadDev struct {
	RequestID    string `gorm:"type:varchar(64);primaryKey;index:idx_lane_dev_usage_due,priority:3"`
	UserID       int    `gorm:"not null"`
	TokenID      int    `gorm:"not null"`
	KeyEpoch     int64  `gorm:"not null"`
	LaneID       string `gorm:"type:varchar(64);not null"`
	LaneEpoch    int64  `gorm:"not null"`
	ChannelID    int    `gorm:"not null"`
	Group        string `gorm:"type:varchar(128);not null"`
	Model        string `gorm:"type:varchar(128);not null"`
	Prompt       int    `gorm:"not null"`
	Completion   int    `gorm:"not null"`
	CacheRead    int    `gorm:"not null"`
	CacheWrite   int    `gorm:"not null"`
	CacheWrite12 int    `gorm:"not null"`
	ActualQuota  int    `gorm:"not null"`
	PriceSHA256  string `gorm:"type:varchar(64);not null"`
	RouteSHA256  string `gorm:"type:varchar(64);not null"`
	UsageSHA256  string `gorm:"type:varchar(64);not null"`
	SettledAt    int64  `gorm:"not null"`
}

type laneBillingUsageOutboxDev struct {
	Payload     laneBillingUsagePayloadDev `gorm:"embedded"`
	PayloadSHA  string                     `gorm:"type:varchar(64);not null"`
	State       string                     `gorm:"type:varchar(20);not null;index:idx_lane_dev_usage_due,priority:1"`
	DueAt       int64                      `gorm:"not null;index:idx_lane_dev_usage_due,priority:2"`
	Attempt     int64                      `gorm:"not null"`
	Destination string                     `gorm:"type:varchar(64);not null"`
	ProjectedAt int64                      `gorm:"not null"`
}

func (laneBillingUsageOutboxDev) TableName() string { return "lane_billing_usage_outbox_dev_v1" }

type laneUsageDestinationDev struct {
	ID          int    `gorm:"primaryKey;autoIncrement:false"`
	Destination string `gorm:"type:varchar(64);not null"`
}

func (laneUsageDestinationDev) TableName() string { return "lane_billing_usage_destination_dev_v1" }

type laneUsageLogReceiptDev struct {
	RequestID  string `gorm:"type:varchar(64);primaryKey"`
	PayloadSHA string `gorm:"type:varchar(64);not null"`
	LogID      int    `gorm:"not null;uniqueIndex:,where:log_id > 0"`
}

func (laneUsageLogReceiptDev) TableName() string { return "lane_billing_usage_log_receipts_dev_v1" }

func laneBillingUsagePayload(r *laneBillingChildReceiptV1, m LaneBillingMeasuredDev, at int64) laneBillingUsagePayloadDev {
	return laneBillingUsagePayloadDev{RequestID: r.RequestID, UserID: r.UserID, TokenID: r.TokenID, KeyEpoch: r.KeyEpoch,
		LaneID: r.LaneID, LaneEpoch: r.Epoch, ChannelID: r.ChannelID, Group: m.Group, Model: m.Model,
		Prompt: m.PromptTokens, Completion: m.CompletionTokens, CacheRead: m.CacheReadTokens, CacheWrite: m.CacheWriteTokens,
		CacheWrite12: m.CacheWrite12hTokens, ActualQuota: r.Actual, PriceSHA256: r.FrozenPriceSHA256,
		RouteSHA256: r.RouteSHA256, UsageSHA256: r.UsageSHA256, SettledAt: at}
}

func (p laneBillingUsagePayloadDev) digest() string {
	encoded, err := common.Marshal(p)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte("newapi/lane/dev-usage-projection/v1\x00"), encoded...))
	return hex.EncodeToString(digest[:])
}

func laneUsageLabelValid(value string) bool {
	return value != "" && len(value) <= 128 && utf8.ValidString(value) && strings.IndexFunc(value, func(c rune) bool { return c < 32 || c == 127 }) < 0
}

func (p laneBillingUsagePayloadDev) valid() bool {
	return laneBillingOwnerIDValid(p.RequestID) && laneBillingOwnerIDValid(p.LaneID) && p.UserID > 0 && p.TokenID > 0 && p.KeyEpoch > 0 && p.LaneEpoch > 0 &&
		p.ChannelID > 0 && laneUsageLabelValid(p.Group) && laneUsageLabelValid(p.Model) && p.Prompt > 0 && p.Prompt <= common.MaxQuota &&
		p.Completion >= 0 && p.Completion <= common.MaxQuota-p.Prompt && p.CacheRead >= 0 && p.CacheRead <= p.Prompt && p.CacheWrite >= 0 && p.CacheWrite <= p.Prompt &&
		p.CacheWrite12 >= 0 && p.CacheWrite12 <= p.CacheWrite && p.ActualQuota > 0 && p.ActualQuota <= common.MaxQuota && p.SettledAt > 0 &&
		laneChildSHAValid(p.PriceSHA256) && laneChildSHAValid(p.RouteSHA256) && laneChildSHAValid(p.UsageSHA256)
}

func createLaneBillingUsageOutboxTx(tx *gorm.DB, r *laneBillingChildReceiptV1, m LaneBillingMeasuredDev) error {
	p := laneBillingUsagePayload(r, m, common.GetTimestamp())
	if r.State != laneBillingChildObserved || !p.valid() {
		return ErrLaneBillingOwnerEvidence
	}
	o := laneBillingUsageOutboxDev{Payload: p, State: laneUsagePending, PayloadSHA: p.digest()}
	if !laneChildSHAValid(o.PayloadSHA) {
		return ErrLaneBillingOwnerEvidence
	}
	return tx.Create(&o).Error
}

func verifyLaneBillingUsageOutboxTx(tx *gorm.DB, r *laneBillingChildReceiptV1, m LaneBillingMeasuredDev) error {
	var o laneBillingUsageOutboxDev
	if err := tx.Where("request_id = ?", r.RequestID).Take(&o).Error; err != nil {
		return err
	}
	if !o.Payload.valid() || o.Payload != laneBillingUsagePayload(r, m, o.Payload.SettledAt) || o.PayloadSHA != o.Payload.digest() {
		return ErrLaneBillingOwnerEvidence
	}
	return nil
}

// InstallLaneBillingUsageLogDevSchema is explicitly for a sacrificial log
// schema only. Its partial uniqueness also prevents an unrelated writer from
// inserting a second consume log while our exact receipt is being written.
// No ordinary startup or log migration calls this function.
func InstallLaneBillingUsageLogDevSchema(ctx context.Context, db *gorm.DB) (string, error) {
	var destination laneUsageDestinationDev
	err := withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		if !tx.Migrator().HasTable(&Log{}) {
			return ErrLaneBillingOwnerUnavailable
		}
		if err := tx.AutoMigrate(&laneUsageDestinationDev{}, &laneUsageLogReceiptDev{}); err != nil {
			return err
		}
		if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS lane_billing_dev_consume_request_uq ON logs (request_id) WHERE type = 2 AND request_id <> ''`).Error; err != nil {
			return err
		}
		var random [32]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		candidate := laneUsageDestinationDev{ID: 1, Destination: hex.EncodeToString(random[:])}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; err != nil {
			return err
		}
		return tx.First(&destination, 1).Error
	})
	if err != nil {
		return "", err
	}
	if !laneChildSHAValid(destination.Destination) {
		return "", ErrLaneBillingOwnerEvidence
	}
	return destination.Destination, nil
}

// ProjectLaneBillingUsageDev is an explicit, bounded projection pass. No
// schedule/workers are installed. For both shared and split log DBs, commit
// the destination log + receipt first, then main counters + acknowledgement.
// A lost ACK on either side is safe on exact retry. Never touch wallet or Key
// quota, the spend, dispatch, or refund state, and do not emit quota exports.
// No-log settings are deliberately unsupported in this synthetic adapter;
// they must not claim ordinary usage/counter parity on an activated route.
func ProjectLaneBillingUsageDev(ctx context.Context, mainDB, logDB *gorm.DB, destination string, limit int) (int, error) {
	if ctx == nil || logDB == nil || logDB.Config == nil || logDB.Dialector == nil || logDB.Dialector.Name() != "postgres" || logDB.DryRun ||
		!common.LogConsumeEnabled || !laneChildSHAValid(destination) || limit < 1 || limit > 32 {
		return 0, ErrLaneBillingOwnerUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var candidates []string
	err := withLaneBillingFiniteKeyDevTx(ctx, mainDB, func(tx *gorm.DB) error {
		return tx.Model(&laneBillingUsageOutboxDev{}).Where("state = ? AND due_at <= EXTRACT(EPOCH FROM statement_timestamp())::bigint", laneUsagePending).
			Order("due_at, request_id").Limit(limit).Pluck("request_id", &candidates).Error
	})
	if err != nil || len(candidates) == 0 {
		return 0, err
	}
	// Never pin an unbound row to a guessed destination. If the real target
	// cannot be contacted, record delayed retry without binding a new ID.
	targetErr := withLaneBillingFiniteKeyDevTx(ctx, logDB, func(tx *gorm.DB) error {
		return verifyLaneUsageLogDestinationTx(tx, destination)
	})
	if errors.Is(targetErr, ErrLaneBillingOwnerUnavailable) {
		return 0, targetErr // invalid scope, install or identity: no lease mutation
	}
	completed := 0
	for _, requestID := range candidates {
		claim, err := claimLaneUsageProjection(ctx, mainDB, requestID, destination, targetErr == nil)
		if err != nil {
			return completed, err
		}
		if claim == nil { // other worker or already finalized
			continue
		}
		if targetErr != nil {
			return completed, targetErr
		}
		proof, err := projectLaneUsageLog(ctx, logDB, *claim)
		if errors.Is(err, ErrLaneBillingOwnerEvidence) {
			_, finishErr := finishLaneUsageProjection(ctx, mainDB, *claim, nil, true)
			return completed, errors.Join(ErrLaneBillingUsageProjectionConflict, finishErr)
		}
		if err != nil {
			// The durable lease already set a delayed retry on the main DB.
			return completed, err
		}
		applied, err := finishLaneUsageProjection(ctx, mainDB, *claim, proof, false)
		if errors.Is(err, ErrLaneBillingOwnerEvidence) {
			_, finishErr := finishLaneUsageProjection(ctx, mainDB, *claim, nil, true)
			return completed, errors.Join(ErrLaneBillingUsageProjectionConflict, finishErr)
		}
		if err != nil {
			return completed, err
		}
		if applied {
			completed++
		}
	}
	return completed, nil
}

func claimLaneUsageProjection(ctx context.Context, db *gorm.DB, requestID, destination string, targetVerified bool) (*laneBillingUsageOutboxDev, error) {
	var claimed *laneBillingUsageOutboxDev
	var conflict bool
	err := withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		var row laneBillingUsageOutboxDev
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("request_id = ? AND state = ? AND due_at <= EXTRACT(EPOCH FROM statement_timestamp())::bigint", requestID, laneUsagePending).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if row.Destination != "" && row.Destination != destination {
			return ErrLaneBillingOwnerEvidence // configuration mismatch; do not switch target or paint a false terminal conflict
		}
		if err := verifyLaneUsageTerminalTx(tx, row); err != nil {
			if !errors.Is(err, ErrLaneBillingOwnerEvidence) {
				return err
			}
			conflict = true
			return tx.Model(&laneBillingUsageOutboxDev{}).Where("request_id = ?", requestID).UpdateColumn("state", laneUsageConflict).Error
		}
		if row.Attempt < 0 || row.Attempt == math.MaxInt64 {
			return ErrLaneBillingOwnerEvidence
		}
		row.Attempt++
		if targetVerified {
			row.Destination = destination
		}
		update := tx.Model(&laneBillingUsageOutboxDev{}).Where("request_id = ?", requestID).UpdateColumns(map[string]any{
			"attempt": row.Attempt, "destination": row.Destination, "due_at": gorm.Expr("EXTRACT(EPOCH FROM clock_timestamp())::bigint + ?", laneUsageLeaseSec),
		})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		claimed = &row
		return nil
	})
	if err == nil && conflict {
		return nil, ErrLaneBillingUsageProjectionConflict
	}
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// The projector locks only the outbox in this check. It reads immutable
// terminal owner/child/measurement facts without reversing the spend's
// canonical-owner -> child -> outbox lock order.
func verifyLaneUsageTerminalTx(tx *gorm.DB, o laneBillingUsageOutboxDev) error {
	p := o.Payload
	if !p.valid() || o.PayloadSHA != p.digest() {
		return ErrLaneBillingOwnerEvidence
	}
	var child laneBillingChildReceiptV1
	var owner BillingFinalization
	var measured LaneBillingMeasuredDev
	for _, dest := range []any{&child, &owner, &measured} {
		if err := tx.Where("request_id = ?", p.RequestID).Take(dest).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrLaneBillingOwnerEvidence
			}
			return err
		}
	}
	canonical := BillingFinalization{RequestID: p.RequestID, UserID: child.UserID, TokenID: child.TokenID, Source: laneBillingOwnerSource, TokenFingerprint: child.TokenFingerprint}
	if owner.State != laneBillingOwnerState || !sameBillingIdentity(owner, canonical) || owner.ActualQuota != 0 || owner.TaskID != 0 || owner.TaskPublicID != "" ||
		owner.SettlementRecovery != "" || owner.SettlementLogRetryAt != 0 || owner.NextRetryAt != 0 ||
		!child.valid() || !child.binding().valid() || child.State != laneBillingChildSettled || !measured.Ready ||
		measured.ChannelID != child.ChannelID || measured.ActualQuota != child.Actual || measured.UsageSHA256 != child.UsageSHA256 || laneBillingUsagePayload(&child, measured, p.SettledAt) != p {
		return ErrLaneBillingOwnerEvidence
	}
	if err := requireLaneBillingEnrolledChildProofTx(tx, p.RequestID); err != nil {
		return err
	}
	for _, target := range []struct {
		id    int
		model any
	}{{p.UserID, &User{}}, {p.ChannelID, &Channel{}}} {
		var id int
		row := tx.Model(target.model).Select("id").Where("id = ?", target.id).Scan(&id)
		if row.Error != nil {
			return row.Error
		}
		if row.RowsAffected != 1 || id != target.id {
			return ErrLaneBillingOwnerEvidence
		}
	}
	return nil
}

// A proof is returned only after a target transaction commits (or verifies
// the exact previous target commit). The main finalizer cannot apply counters
// from a claim or a request ID alone.
type laneUsageLogProjectionProof struct {
	RequestID, PayloadSHA, Destination string
	LogID                              int
}

type laneUsageLogDetailsDev struct {
	PaidCost     int `json:"paid_cost"`
	CacheRead    int `json:"cache_tokens"`
	CacheWrite   int `json:"cache_write_tokens"`
	CacheWrite12 int `json:"cache_creation_tokens_12h"`
	Admin        struct {
		Projection  string `json:"billing_projection"`
		PriceSHA256 string `json:"billing_price_snapshot_sha256"`
	} `json:"admin_info"`
}

func verifyLaneUsageLogDestinationTx(tx *gorm.DB, expected string) error {
	var target laneUsageDestinationDev
	if err := tx.First(&target, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrLaneBillingOwnerUnavailable
		}
		return err
	}
	if !laneChildSHAValid(expected) || target.Destination != expected {
		return ErrLaneBillingOwnerUnavailable
	}
	return nil
}

func projectLaneUsageLog(ctx context.Context, db *gorm.DB, claim laneBillingUsageOutboxDev) (*laneUsageLogProjectionProof, error) {
	var proof laneUsageLogProjectionProof
	err := withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		if err := verifyLaneUsageLogDestinationTx(tx, claim.Destination); err != nil {
			return err
		}
		p := claim.Payload
		receipt := laneUsageLogReceiptDev{RequestID: p.RequestID, PayloadSHA: claim.PayloadSHA}
		inserted := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "request_id"}}, DoNothing: true}).Create(&receipt)
		if inserted.Error != nil {
			return inserted.Error
		}
		var rows []Log
		if err := tx.Where("request_id = ? AND type = ?", p.RequestID, LogTypeConsume).Limit(2).Find(&rows).Error; err != nil {
			return err
		}
		// Stable struct encoding makes exact log replay independent of map
		// iteration. Privileged values stay in the ordinary admin scope.
		other := laneUsageLogDetailsDev{PaidCost: p.ActualQuota, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite, CacheWrite12: p.CacheWrite12}
		other.Admin.Projection, other.Admin.PriceSHA256 = "synthetic_lane_v1", p.PriceSHA256
		encoded, err := common.Marshal(other)
		if err != nil {
			return err
		}
		want := Log{UserId: p.UserID, TokenId: p.TokenID, ChannelId: p.ChannelID, ModelName: p.Model, Group: p.Group, RequestId: p.RequestID,
			Type: LogTypeConsume, CreatedAt: p.SettledAt, IsStream: true, Quota: p.ActualQuota, PromptTokens: p.Prompt, CompletionTokens: p.Completion, Other: string(encoded)}
		if inserted.RowsAffected == 0 {
			if err := lockForUpdate(tx).Where("request_id = ?", p.RequestID).Take(&receipt).Error; err != nil {
				return err
			}
			if receipt.PayloadSHA != claim.PayloadSHA || receipt.LogID <= 0 || len(rows) != 1 {
				return ErrLaneBillingOwnerEvidence
			}
			want.Id = receipt.LogID
			if rows[0] != want {
				return ErrLaneBillingOwnerEvidence
			}
			proof = laneUsageLogProjectionProof{p.RequestID, claim.PayloadSHA, claim.Destination, want.Id}
			return nil
		}
		if len(rows) != 0 { // never adopt another writer's log even if it looks the same
			return ErrLaneBillingOwnerEvidence
		}
		write := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&want)
		if write.Error != nil {
			return write.Error
		}
		if write.RowsAffected != 1 || want.Id <= 0 {
			return ErrLaneBillingOwnerEvidence
		}
		result := tx.Model(&laneUsageLogReceiptDev{}).Where("request_id = ? AND log_id = 0", p.RequestID).UpdateColumn("log_id", want.Id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		proof = laneUsageLogProjectionProof{p.RequestID, claim.PayloadSHA, claim.Destination, want.Id}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &proof, nil
}

func finishLaneUsageProjection(ctx context.Context, db *gorm.DB, claim laneBillingUsageOutboxDev, proof *laneUsageLogProjectionProof, conflict bool) (bool, error) {
	if !conflict && (proof == nil || proof.RequestID != claim.Payload.RequestID || proof.PayloadSHA != claim.PayloadSHA || proof.Destination != claim.Destination || proof.LogID <= 0) {
		return false, ErrLaneBillingOwnerEvidence
	}
	completed := false
	err := withLaneBillingFiniteKeyDevTx(ctx, db, func(tx *gorm.DB) error {
		var row laneBillingUsageOutboxDev
		if err := lockForUpdate(tx).Where("request_id = ?", claim.Payload.RequestID).Take(&row).Error; err != nil {
			return err
		}
		if row.Destination != claim.Destination || row.Payload != claim.Payload || row.PayloadSHA != claim.PayloadSHA {
			return ErrLaneBillingOwnerEvidence
		}
		if row.Attempt != claim.Attempt || row.State != laneUsagePending {
			return nil // a superseded worker must not acknowledge another lease
		}
		if conflict {
			return tx.Model(&laneBillingUsageOutboxDev{}).Where("request_id = ?", row.Payload.RequestID).UpdateColumn("state", laneUsageConflict).Error
		}
		if err := verifyLaneUsageTerminalTx(tx, row); err != nil {
			return err
		}
		p := row.Payload
		// User then channel, only reporting fields. Require both exact rows
		// and safe widths; rollback all stats and ack on any failure.
		user := tx.Model(&User{}).Where("id = ? AND used_quota BETWEEN 0 AND ? AND request_count >= 0 AND request_count < ?", p.UserID, math.MaxInt64-int64(p.ActualQuota), math.MaxInt32).
			UpdateColumns(map[string]any{"used_quota": gorm.Expr("used_quota + ?", p.ActualQuota), "request_count": gorm.Expr("request_count + 1")})
		if user.Error != nil {
			return user.Error
		}
		if user.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		channel := tx.Model(&Channel{}).Where("id = ? AND used_quota BETWEEN 0 AND ?", p.ChannelID, math.MaxInt64-int64(p.ActualQuota)).
			UpdateColumn("used_quota", gorm.Expr("used_quota + ?", p.ActualQuota))
		if channel.Error != nil {
			return channel.Error
		}
		if channel.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		update := tx.Model(&laneBillingUsageOutboxDev{}).Where("request_id = ? AND attempt = ? AND state = ?", p.RequestID, row.Attempt, laneUsagePending).
			UpdateColumns(map[string]any{"state": laneUsageProjected, "projected_at": gorm.Expr("EXTRACT(EPOCH FROM clock_timestamp())::bigint")})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return ErrLaneBillingOwnerEvidence
		}
		completed = true
		return nil
	})
	return err == nil && completed, err
}
