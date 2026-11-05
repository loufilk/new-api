package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ModelTestRun is a user-owned, explicitly requested playground invocation.
// It is not an assertion about model identity and never schedules paid work.
type ModelTestRun struct {
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
	err := DB.Where("id = ? AND user_id = ? AND source = ?", id, userID, "user").First(&run).Error
	return &run, err
}

func ListModelTestRuns(userID int) ([]ModelTestRun, error) {
	runs := make([]ModelTestRun, 0)
	err := DB.Select("source", "token_id", "fingerprint_result", "id", "user_id", "model", "group", "benchmark_id", "status", "assessment", "created_at", "completed_at", "expires_at", "max_output_tokens", "estimated_quota", "request_id", "error_code", "cancel_requested").Where("user_id = ? AND source = ?", userID, "user").Order("created_at DESC, id DESC").Limit(60).Find(&runs).Error
	now := time.Now().Unix()
	for i := range runs {
		if runs[i].Status == "running" && runs[i].ExpiresAt < now {
			runs[i].Status, runs[i].ErrorCode = "unknown", "worker_interrupted"
		}
	}
	return runs, err
}

func FinishModelTestRun(run *ModelTestRun) error {
	return DB.Model(&ModelTestRun{}).Where("id = ? AND user_id = ? AND status = ?", run.ID, run.UserID, "running").Updates(map[string]any{"status": run.Status, "assessment": run.Assessment, "completed_at": run.CompletedAt, "request_id": run.RequestID, "error_code": run.ErrorCode, "artwork_html": run.ArtworkHTML, "fingerprint_result": run.FingerprintResult, "samples_json": run.SamplesJSON, "channel_id": run.ChannelID, "channel_key_index": run.ChannelKeyIndex}).Error
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

// ListEffectiveModelTestRuns returns the most recent sixty scored observations
// per owned model/group, independent of intervening failed attempts.
func ListEffectiveModelTestRuns(userID int) ([]ModelTestRun, error) {
	var pairs []struct {
		Model string
		Group string
	}
	if err := DB.Model(&ModelTestRun{}).Select("model", commonGroupCol).Where("user_id = ? AND source = ? AND status = ? AND assessment = ?", userID, "user", "completed", "scored").Distinct().Find(&pairs).Error; err != nil {
		return nil, err
	}
	result := make([]ModelTestRun, 0)
	for _, pair := range pairs {
		var runs []ModelTestRun
		if err := DB.Omit("artwork_html", "samples_json").Where("user_id = ? AND source = ? AND status = ? AND assessment = ? AND model = ? AND "+commonGroupCol+" = ?", userID, "user", "completed", "scored", pair.Model, pair.Group).Order("created_at DESC, id DESC").Limit(60).Find(&runs).Error; err != nil {
			return nil, err
		}
		result = append(result, runs...)
	}
	return result, nil
}
