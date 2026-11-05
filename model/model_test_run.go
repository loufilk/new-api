package model

import (
	"context"
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelTestRun is a user-owned, explicitly requested playground invocation.
// It is not an assertion about model identity and never schedules paid work.
type ModelTestRun struct {
	HistoryPrunedAt   int64     `json:"-" gorm:"not null;default:0;index"`
	Source            string    `json:"source" gorm:"type:varchar(16);not null;default:user;index"`
	TokenID           int       `json:"token_id"`
	ConfirmationID    *string   `json:"-" gorm:"type:varchar(64);uniqueIndex"`
	FingerprintResult JSONValue `json:"fingerprint_result,omitempty" gorm:"type:text"`
	SamplesJSON       string    `json:"-" gorm:"type:text"`
	ChannelID         int       `json:"-"`
	ChannelKeyIndex   int       `json:"-"`
	ID                string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	UserID            int       `json:"-" gorm:"index:idx_model_test_owner_time,priority:1"`
	Model             string    `json:"model" gorm:"type:varchar(255)"`
	Group             string    `json:"group" gorm:"type:varchar(64)"`
	BenchmarkID       string    `json:"benchmark_id" gorm:"type:varchar(40)"`
	Status            string    `json:"status" gorm:"type:varchar(20);index"`
	Assessment        string    `json:"assessment" gorm:"type:varchar(20)"`
	CreatedAt         int64     `json:"created_at" gorm:"index:idx_model_test_owner_time,priority:2"`
	CompletedAt       int64     `json:"completed_at"`
	ExpiresAt         int64     `json:"-"`
	MaxOutputTokens   int       `json:"max_output_tokens"`
	EstimatedQuota    *int      `json:"estimated_quota"`
	RequestID         string    `json:"request_id" gorm:"type:varchar(128)"`
	ErrorCode         string    `json:"error_code" gorm:"type:varchar(50)"`
	CancelRequested   bool      `json:"cancel_requested"`
	ArtworkHTML       string    `json:"-" gorm:"type:text"`
}

// One database row serializes the global slot check across application nodes.
type ModelTestGate struct {
	ID int `gorm:"primaryKey"`
}

var ErrModelTestLimit = errors.New("Model test rate or concurrency limit reached")
var ErrModelTestConflict = errors.New("Model test request identifier already used")

// The user row is the cross-node serialization point. Every application node
// shares these limits; a process-local semaphore is not an authorization limit.
func CreateModelTestRun(run *ModelTestRun) (replayed bool, err error) {
	if run.Source == "" {
		run.Source = "user"
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ModelTestGate{ID: 1}).Error; err != nil {
			return err
		}
		var gate ModelTestGate
		if err := lockForUpdate(tx).First(&gate, 1).Error; err != nil {
			return err
		}
		var owner User
		if err := lockForUpdate(tx).Select("id", "status").First(&owner, run.UserID).Error; err != nil {
			return err
		}
		if owner.Status != common.UserStatusEnabled {
			return errors.New("User is not enabled")
		}
		var existing ModelTestRun
		err := tx.Where("id = ?", run.ID).First(&existing).Error
		if err == nil {
			if existing.UserID != run.UserID || existing.Model != run.Model || existing.Group != run.Group || existing.MaxOutputTokens != run.MaxOutputTokens || existing.BenchmarkID != run.BenchmarkID || existing.TokenID != run.TokenID || existing.Source != run.Source {
				return ErrModelTestConflict
			}
			*run, replayed = existing, true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if run.ConfirmationID != nil {
			var count int64
			if err := tx.Model(&ModelTestRun{}).Where("confirmation_id = ?", *run.ConfirmationID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return ErrModelTestConflict
			}
		}
		// Recheck after the cross-node queue/owner locks. A valid quote acquired
		// before a disable is not authority to create work after waiting here.
		enabled, settingsErr := modelTestEnabled(tx)
		if settingsErr != nil {
			return ErrModelTestSettingsUnavailable
		}
		if !enabled {
			return ErrModelTestsDisabled
		}
		now := time.Now().Unix()
		// A crashed node is never retried automatically: the outcome and charge
		// may be unknown. Keep its audit row, unblock only after the bounded lease.
		if err := tx.Model(&ModelTestRun{}).Where("user_id = ? AND status = ? AND expires_at < ?", run.UserID, "running", now).Updates(map[string]any{"status": "unknown", "error_code": "worker_interrupted", "completed_at": now}).Error; err != nil {
			return err
		}
		var active, recent int64
		if err := tx.Model(&ModelTestRun{}).Where("user_id = ? AND status = ?", run.UserID, "running").Count(&active).Error; err != nil {
			return err
		}
		if err := tx.Model(&ModelTestRun{}).Where("user_id = ? AND created_at >= ?", run.UserID, now-3600).Count(&recent).Error; err != nil {
			return err
		}
		if active != 0 || recent >= 3 {
			return ErrModelTestLimit
		}
		var globalActive int64
		if err := tx.Model(&ModelTestRun{}).Where("status = ? AND expires_at >= ?", "running", now).Count(&globalActive).Error; err != nil {
			return err
		}
		if globalActive >= 4 {
			return ErrModelTestLimit
		}
		return tx.Create(run).Error
	})
	return
}

func GetModelTestRun(userID int, id string) (*ModelTestRun, error) {
	var run ModelTestRun
	err := DB.Where("id = ? AND user_id = ? AND source = ?", id, userID, "user").Where("status = ? OR created_at >= ?", "running", time.Now().Unix()-ModelTestTransientHistorySeconds).First(&run).Error
	return &run, err
}

func ListModelTestRuns(userID int) ([]ModelTestRun, error) {
	runs := make([]ModelTestRun, 0)
	err := DB.Select("source", "token_id", "id", "user_id", "model", "group", "benchmark_id", "status", "assessment", "created_at", "completed_at", "expires_at", "max_output_tokens", "estimated_quota", "request_id", "error_code", "cancel_requested").Where("user_id = ? AND source = ? AND created_at >= ?", userID, "user", time.Now().Unix()-ModelTestTransientHistorySeconds).Order("created_at DESC, id DESC").Limit(60).Find(&runs).Error
	now := time.Now().Unix()
	for i := range runs {
		if runs[i].Status == "running" && runs[i].ExpiresAt < now {
			runs[i].Status, runs[i].ErrorCode = "unknown", "worker_interrupted"
		}
	}
	return runs, err
}

func FinishModelTestRun(run *ModelTestRun) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockModelFingerprintRetention(tx); err != nil {
			return err
		}
		if err := tx.Model(&ModelTestRun{}).Where("id = ? AND user_id = ? AND status = ?", run.ID, run.UserID, "running").Updates(map[string]any{"status": run.Status, "assessment": run.Assessment, "completed_at": run.CompletedAt, "request_id": run.RequestID, "error_code": run.ErrorCode, "artwork_html": run.ArtworkHTML, "fingerprint_result": run.FingerprintResult, "samples_json": run.SamplesJSON, "channel_id": run.ChannelID, "channel_key_index": run.ChannelKeyIndex}).Error; err != nil {
			return err
		}
		return pruneModelFingerprintReports(tx, time.Now().Unix(), false, run.Group, run.Model)
	})
}

func CancelModelTestRun(userID int, id string) (*ModelTestRun, error) {
	if err := DB.Model(&ModelTestRun{}).Where("id = ? AND user_id = ? AND source = ? AND status = ?", id, userID, "user", "running").Update("cancel_requested", true).Error; err != nil {
		return nil, err
	}
	return GetModelTestRun(userID, id)
}

// Persist sample intent before dispatch. A failed write or cancellation never starts a paid call.
func SaveModelTestSamples(run *ModelTestRun) error {
	result := DB.Model(&ModelTestRun{}).Where("id = ? AND user_id = ? AND status = ? AND cancel_requested = ?", run.ID, run.UserID, "running", false).Updates(map[string]any{"samples_json": run.SamplesJSON, "channel_id": run.ChannelID, "channel_key_index": run.ChannelKeyIndex})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("model test no longer running")
	}
	return nil
}

// Personal history no longer stores a second view of shared observations.
// Keep the legacy response field empty for older clients during the transition.
func ListEffectiveModelTestRuns(userID int) ([]ModelTestRun, error) {
	return []ModelTestRun{}, nil
}

// ModelTestPublicRun is deliberately independent of the owner-only audit model.
// Never embed ModelTestRun here: newly added private fields must stay private.
type ModelTestPublicRun struct {
	ID                string `json:"id"`
	Source            string `json:"source"`
	Model             string `json:"model"`
	Group             string `json:"group"`
	BenchmarkID       string `json:"benchmark_id"`
	Status            string `json:"status"`
	Assessment        string `json:"assessment"`
	FingerprintResult any    `json:"fingerprint_result"`
	CreatedAt         int64  `json:"created_at"`
	CompletedAt       int64  `json:"completed_at"`
}

// PublicFingerprint shares only the same typed report whitelist as site probes.
func (r ModelTestRun) PublicFingerprint() ModelTestPublicRun {
	report := (ModelFingerprintSiteRun{FingerprintResult: string(r.FingerprintResult)}).Public().FingerprintResult
	return ModelTestPublicRun{ID: r.ID, Source: "user", Model: r.Model, Group: r.Group, BenchmarkID: r.BenchmarkID, Status: r.Status, Assessment: r.Assessment, FingerprintResult: report, CreatedAt: r.CreatedAt, CompletedAt: r.CompletedAt}
}

// ListPublicModelTestRuns shares terminal fingerprint observations across users.
// Per-model/group limits keep noisy failures from displacing scored observations.
// This does not grant access to owner-only detail, samples or cancellation APIs.
func ListPublicModelTestRuns(ctx context.Context) ([]ModelTestPublicRun, error) {
	terminal := []string{"completed", "failed", "unknown", "cancelled", "intercepted", "timeout"}
	base := DB.WithContext(ctx).Model(&ModelTestRun{}).Where("source = ? AND benchmark_id = ? AND status IN ? AND history_pruned_at = ?", "user", "local-fingerprint-v1", terminal, 0)
	var pairs []struct {
		Model string
		Group string
	}
	if err := base.Session(&gorm.Session{}).Select("model", commonGroupCol).Distinct().Order("model").Order(commonGroupCol).Find(&pairs).Error; err != nil {
		return nil, err
	}
	result := make([]ModelTestPublicRun, 0)
	for _, pair := range pairs {
		for _, scored := range []bool{true, false} {
			query := base.Session(&gorm.Session{}).Where("model = ? AND "+commonGroupCol+" = ?", pair.Model, pair.Group)
			if scored {
				query = query.Where("status = ? AND assessment = ?", "completed", "scored").Limit(60)
			} else {
				query = query.Where("NOT (status = ? AND assessment = ?) OR assessment IS NULL", "completed", "scored").Limit(5)
			}
			var runs []ModelTestRun
			if err := query.Select("id", "model", "group", "benchmark_id", "status", "assessment", "fingerprint_result", "created_at", "completed_at").Order("created_at DESC, id DESC").Find(&runs).Error; err != nil {
				return nil, err
			}
			for _, run := range runs {
				result = append(result, run.PublicFingerprint())
			}
		}
	}
	return result, nil
}
