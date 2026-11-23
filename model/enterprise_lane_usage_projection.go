//go:build enterprise_lane_lab

package model

import (
	"context"
	"crypto/subtle"
	"errors"
	"math"
	"os"
	"slices"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxEnterpriseLaneUsageProjectionBatch = 256

var ErrEnterpriseLaneUsageProjection = errors.New("enterprise lane usage projection is invalid or unreconciled")

// EnterpriseLaneUsageProjection retains the exact detached usage and a unique
// receipt for the committed dashboard counter and same-DB consume log writes.
// It is not authority for billing, dispatch, token spend or refund.
type EnterpriseLaneUsageProjection struct {
	RequestID   string              `gorm:"type:varchar(64);primaryKey"`
	LaneID      string              `gorm:"type:varchar(64);not null"`
	Epoch       int64               `gorm:"not null"`
	UserID      int                 `gorm:"not null;index"`
	TokenID     int                 `gorm:"not null"`
	Actual      int                 `gorm:"type:bigint;not null"`
	Usage       EnterpriseLaneUsage `gorm:"embedded;embeddedPrefix:usage_"`
	ProjectedAt int64               `gorm:"type:bigint;not null"`
}

// ProjectEnterpriseLaneUsageBatch runs on the primary database with shared
// logs, strictly off the HTTP path. One commit owns the detached receipt, real
// consume log, aggregated counters and outbox acknowledgement. If its ACK is
// lost, the next pass sees no pending owner and cannot increment again.
func ProjectEnterpriseLaneUsageBatch(ctx context.Context, db *gorm.DB, limit int) (int, error) {
	if db == nil || db.Dialector.Name() != "postgres" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return 0, ErrEnterpriseLaneUnavailable
	}
	// Fail closed rather than write a consume log invisible to the gateway, or
	// lose its exactly-once boundary across separately committed databases.
	if os.Getenv("LOG_SQL_DSN") != "" || !common.LogConsumeEnabled {
		return 0, ErrEnterpriseLaneUsageProjection
	}
	if limit < 1 || limit > MaxEnterpriseLaneUsageProjectionBatch {
		return 0, ErrEnterpriseLaneUsageProjection
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	projected := 0
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owners []EnterpriseLaneRequest
		// This PG-only claim intentionally skips other workers' locked owners.
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("usage_projection_pending AND state = ?", EnterpriseLaneSettled).
			Order("request_id ASC").Limit(limit).Find(&owners).Error
		if err != nil || len(owners) == 0 {
			return err
		}
		if err := projectEnterpriseLaneUsageTx(tx, owners); err != nil {
			return err
		}
		projected = len(owners)
		return nil
	})
	if err != nil {
		return 0, err
	}
	return projected, nil
}

func projectEnterpriseLaneUsageTx(tx *gorm.DB, owners []EnterpriseLaneRequest) error {
	type counters struct{ used, requests int64 }
	userCounters, channelCounters := map[int]counters{}, map[int]int64{}
	requests, tokenIDs := make([]string, 0, len(owners)), make([]int, 0, len(owners))
	receipts, logs := make([]EnterpriseLaneUsageProjection, 0, len(owners)), make([]Log, 0, len(owners))
	now := common.GetTimestamp()
	for _, owner := range owners {
		if !enterpriseLaneIDValid(owner.RequestID) || !enterpriseLaneIDValid(owner.LaneID) || owner.Epoch <= 0 ||
			owner.UserID <= 0 || owner.TokenID <= 0 || owner.Held < 1 || owner.Held > common.MaxQuota ||
			owner.Actual < 0 || owner.Actual > owner.Held || owner.RefundProof != "" || !owner.Usage.valid() {
			return ErrEnterpriseLaneUsageProjection
		}
		u := owner.Usage
		requests = append(requests, owner.RequestID)
		tokenIDs = append(tokenIDs, owner.TokenID)
		receipts = append(receipts, EnterpriseLaneUsageProjection{RequestID: owner.RequestID, LaneID: owner.LaneID, Epoch: owner.Epoch,
			UserID: owner.UserID, TokenID: owner.TokenID, Actual: owner.Actual, Usage: u, ProjectedAt: now})
		logs = append(logs, Log{RequestId: owner.RequestID, UserId: owner.UserID, TokenId: owner.TokenID,
			CreatedAt: u.ObservedAt, Type: LogTypeConsume, ModelName: u.Model, Quota: owner.Actual,
			ChannelId: u.ChannelID, PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, Group: u.Group, IsStream: true})
		counter := userCounters[owner.UserID]
		counter.used += int64(owner.Actual)
		counter.requests++
		userCounters[owner.UserID] = counter
		if owner.Actual != 0 {
			channelCounters[u.ChannelID] += int64(owner.Actual)
		}
	}

	// Pending plus an existing receipt or a legacy consume log means someone
	// outside this atomic path wrote part of the answer. Do not guess which
	// counters are already applied or silently double charge reporting totals.
	var existing int64
	if err := tx.Model(&Log{}).Where("type = ? AND request_id IN ?", LogTypeConsume, requests).Count(&existing).Error; err != nil {
		return err
	}
	if existing != 0 {
		return ErrEnterpriseLaneUsageProjection
	}
	if err := tx.Create(&receipts).Error; err != nil {
		return errors.Join(ErrEnterpriseLaneUsageProjection, err)
	}
	// Keep the original token ID in the log, but never project the name of
	// a different key that happens to reuse that ID. Only fetch its digest.
	type tokenDisplay struct {
		ID, UserID        int
		Name, Fingerprint string
	}
	var tokens []tokenDisplay
	if err := tx.Unscoped().Model(&Token{}).
		Select(`id, user_id, name, encode(sha256(convert_to("key", 'UTF8')), 'hex') AS fingerprint`).
		Where("id IN ?", tokenIDs).Find(&tokens).Error; err != nil {
		return err
	}
	tokenNames := make(map[int]tokenDisplay, len(tokens))
	for _, token := range tokens {
		tokenNames[token.ID] = token
	}

	// Lock the existing reporting rows once per distinct batch dimension, in
	// common user-before-channel and ascending ID order. Each update guards the
	// live column width and never mutates money or authorization fields.
	userIDs := make([]int, 0, len(userCounters))
	for id := range userCounters {
		userIDs = append(userIDs, id)
	}
	slices.Sort(userIDs)
	userNames := make(map[int]string, len(userIDs))
	for _, id := range userIDs {
		counter := userCounters[id]
		var user User
		result := tx.Model(&user).Clauses(clause.Returning{Columns: []clause.Column{{Name: "username"}}}).
			Where("id = ? AND COALESCE(used_quota, 0) <= ? AND COALESCE(request_count, 0) <= ?", id, math.MaxInt64-counter.used, math.MaxInt32-counter.requests).
			UpdateColumns(map[string]any{
				"used_quota":    gorm.Expr("COALESCE(used_quota, 0) + ?", counter.used),
				"request_count": gorm.Expr("COALESCE(request_count, 0) + ?", counter.requests),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnterpriseLaneUsageProjection
		}
		userNames[id] = user.Username
	}
	channelIDs := make([]int, 0, len(channelCounters))
	for id := range channelCounters {
		channelIDs = append(channelIDs, id)
	}
	slices.Sort(channelIDs)
	for _, id := range channelIDs {
		used := channelCounters[id]
		result := tx.Model(&Channel{}).Where("id = ? AND COALESCE(used_quota, 0) <= ?", id, math.MaxInt64-used).
			UpdateColumn("used_quota", gorm.Expr("COALESCE(used_quota, 0) + ?", used))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrEnterpriseLaneUsageProjection
		}
	}
	for i := range logs {
		logs[i].Username = userNames[logs[i].UserId]
		if token := tokenNames[logs[i].TokenId]; token.UserID == owners[i].UserID &&
			subtle.ConstantTimeCompare([]byte(token.Fingerprint), []byte(owners[i].TokenFingerprint)) == 1 {
			logs[i].TokenName = token.Name
		}
	}
	if err := tx.Create(&logs).Error; err != nil {
		return err
	}
	result := tx.Model(&EnterpriseLaneRequest{}).Where("usage_projection_pending AND state = ? AND request_id IN ?", EnterpriseLaneSettled, requests).
		UpdateColumn("usage_projection_pending", false)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(owners)) {
		return ErrEnterpriseLaneUsageProjection
	}
	return nil
}
