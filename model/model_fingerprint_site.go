package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const SystemTaskTypeModelFingerprintSite = "model_fingerprint_site"

var ErrFingerprintSiteConflict = errors.New("Fingerprint site configuration changed")
var ErrFingerprintSiteNotDue = errors.New("Fingerprint site target already admitted in this period")
var ErrFingerprintSiteDisabled = errors.New("Fingerprint site automatic testing is disabled")

// Legacy quota/output fields remain readable for compatibility; new settings
// normalize them to zero and execution never treats them as limits.
type ModelFingerprintSiteTarget struct {
	ID              string `json:"id"`
	Group           string `json:"group"`
	Model           string `json:"model"`
	ChannelID       int    `json:"channel_id"`
	Enabled         bool   `json:"enabled"`
	PeriodSecs      int64  `json:"period_secs"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	MaxRunQuota     int64  `json:"max_run_quota"`
}
type ModelFingerprintSiteConfig struct {
	Enabled           bool                         `json:"enabled" gorm:"-"`
	ID                int                          `json:"-" gorm:"primaryKey"`
	Version           int64                        `json:"version"`
	AutomaticEnabled  bool                         `json:"automatic_enabled"`
	DefaultPeriodSecs int64                        `json:"default_period_secs"`
	DailyBudgetQuota  int64                        `json:"daily_budget_quota"`
	Targets           []ModelFingerprintSiteTarget `json:"targets" gorm:"-"`
	TargetsJSON       string                       `json:"-" gorm:"type:text"`
	BudgetDay         string                       `json:"budget_day" gorm:"type:varchar(10)"`
	ReservedQuota     int64                        `json:"reserved_quota"`
	UpdatedAt         int64                        `json:"updated_at" gorm:"autoUpdateTime:false"`
	UpdatedBy         int                          `json:"updated_by"`
}

// EnabledTargets is the shared allowlist for catalogue display and admission.
// Historical observations remain independent of current target selection.
func (c ModelFingerprintSiteConfig) EnabledTargets() []ModelFingerprintSiteTarget {
	targets := make([]ModelFingerprintSiteTarget, 0, len(c.Targets))
	if !c.Enabled {
		return targets
	}
	for _, target := range c.Targets {
		if target.Enabled {
			targets = append(targets, target)
		}
	}
	return targets
}

type ModelFingerprintSiteAudit struct {
	ID        int64 `gorm:"primaryKey"`
	Version   int64 `gorm:"uniqueIndex"`
	ActorID   int
	CreatedAt int64
	// Only configuration identifiers and limits; never credentials or channel secrets.
	Configuration string `gorm:"type:text"`
}
type ModelFingerprintSiteRun struct {
	HistoryPrunedAt    int64  `json:"-" gorm:"not null;default:0;index"`
	ID                 string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TargetID           string `json:"target_id" gorm:"type:varchar(36);uniqueIndex:idx_fp_site_window;index"`
	ConfigVersion      int64  `json:"config_version" gorm:"uniqueIndex:idx_fp_site_window"`
	Window             int64  `json:"-" gorm:"column:period_window;uniqueIndex:idx_fp_site_window"`
	TaskID             string `json:"-" gorm:"type:varchar(64);index"`
	Group              string `json:"group" gorm:"type:varchar(128)"`
	Model              string `json:"model" gorm:"type:varchar(255)"`
	ChannelID          int    `json:"-"`
	ChannelKeyIndex    int    `json:"-"`
	MappedModel        string `json:"-" gorm:"type:varchar(255)"`
	BudgetDay          string `json:"-" gorm:"type:varchar(10)"`
	ReservedQuota      int64  `json:"-"`
	ActualQuota        int64  `json:"-"`
	Status             string `json:"status" gorm:"type:varchar(32);index"`
	UsageState         string `json:"usage_state" gorm:"type:varchar(32)"`
	BenchmarkID        string `json:"benchmark_id" gorm:"type:varchar(128)"`
	FingerprintVersion string `json:"fingerprint_version" gorm:"type:varchar(128)"`
	Assessment         string `json:"assessment" gorm:"type:varchar(128)"`
	FingerprintResult  string `json:"-" gorm:"type:text"`
	CreatedAt          int64  `json:"created_at"`
	FinishedAt         int64  `json:"finished_at"`
}
type ModelFingerprintSiteProbe struct {
	Status           string `gorm:"type:varchar(32)"`
	CompletedAt      int64
	RequestID        string `gorm:"type:varchar(36)"`
	PromptTokens     int
	CompletionTokens int
	ResponseModel    string `gorm:"type:varchar(255)"`
	ErrorCode        string `gorm:"type:varchar(64)"`
	ID               int64  `gorm:"primaryKey"`
	RunID            string `gorm:"type:varchar(36);uniqueIndex:idx_fp_site_probe"`
	ProbeID          string `gorm:"type:varchar(128);uniqueIndex:idx_fp_site_probe"`
	DispatchedAt     int64
}
type ModelFingerprintSiteResult struct {
	Status             string
	BenchmarkID        string
	FingerprintVersion string
	Assessment         string
	FingerprintResult  string
}
type ModelFingerprintSitePublicRun struct {
	Source             string `json:"source"`
	CompletedAt        int64  `json:"completed_at"`
	ID                 string `json:"id"`
	TargetID           string `json:"target_id"`
	Group              string `json:"group"`
	Model              string `json:"model"`
	Status             string `json:"status"`
	UsageState         string `json:"usage_state"`
	BenchmarkID        string `json:"benchmark_id"`
	FingerprintVersion string `json:"fingerprint_version"`
	Assessment         string `json:"assessment"`
	FingerprintResult  any    `json:"fingerprint_result"`
	CreatedAt          int64  `json:"created_at"`
	FinishedAt         int64  `json:"finished_at"`
}

func (r ModelFingerprintSiteRun) Public() ModelFingerprintSitePublicRun {
	var result *modelfingerprint.Result
	if r.FingerprintResult != "" {
		var parsed modelfingerprint.Result
		if common.UnmarshalJsonStr(r.FingerprintResult, &parsed) == nil {
			result = &parsed
		}
	}
	return ModelFingerprintSitePublicRun{Source: "site", CompletedAt: r.FinishedAt, ID: r.ID, TargetID: r.TargetID, Group: r.Group, Model: r.Model, Status: r.Status, UsageState: r.UsageState, BenchmarkID: r.BenchmarkID, FingerprintVersion: r.FingerprintVersion, Assessment: r.Assessment, FingerprintResult: result, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt}
}
func GetModelFingerprintSiteConfig(ctx context.Context) (*ModelFingerprintSiteConfig, error) {
	c := &ModelFingerprintSiteConfig{ID: 1, DefaultPeriodSecs: 3600, Targets: []ModelFingerprintSiteTarget{}}
	enabled, err := ModelTestEnabled(ctx)
	if err != nil {
		return nil, err
	}
	c.Enabled = enabled
	err = DB.WithContext(ctx).First(c, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if c.TargetsJSON != "" {
		err = common.UnmarshalJsonStr(c.TargetsJSON, &c.Targets)
	}
	c.DailyBudgetQuota = 0
	for i := range c.Targets {
		c.Targets[i].ChannelID = 0
		c.Targets[i].MaxOutputTokens = 0
		c.Targets[i].MaxRunQuota = 0
	}
	return c, err
}
func ValidateModelFingerprintSiteConfig(c *ModelFingerprintSiteConfig) error {
	if c.DefaultPeriodSecs < 60 || c.DefaultPeriodSecs > 604800 {
		return errors.New("Invalid fingerprint site interval")
	}
	if len(c.Targets) > 100 {
		return errors.New("Too many fingerprint site targets")
	}
	c.DailyBudgetQuota = 0
	seen := map[string]bool{}
	routes := map[string]bool{}
	enabledTargets := 0
	for i := range c.Targets {
		t := &c.Targets[i]
		if t.ID == "" {
			t.ID = uuid.NewString()
		}
		parsedID, err := uuid.Parse(t.ID)
		if err != nil {
			return errors.New("Invalid fingerprint site target ID")
		}
		t.ID = parsedID.String()
		if seen[t.ID] {
			return errors.New("Duplicate fingerprint site target ID")
		}
		seen[t.ID] = true
		t.Group = strings.TrimSpace(t.Group)
		t.Model = strings.TrimSpace(t.Model)
		if t.Group == "" || len(t.Group) > 128 || t.Model == "" || len(t.Model) > 255 || t.PeriodSecs < 0 || (t.PeriodSecs > 0 && t.PeriodSecs < 60) || t.PeriodSecs > 604800 {
			return errors.New("Invalid fingerprint site target")
		}
		route := t.Group + "\x00" + t.Model
		if routes[route] {
			return errors.New("Duplicate fingerprint site group and model")
		}
		routes[route] = true
		t.ChannelID, t.MaxOutputTokens, t.MaxRunQuota = 0, 0, 0
		if t.Enabled {
			enabledTargets++
		}
	}
	if c.AutomaticEnabled && enabledTargets == 0 {
		return errors.New("Automatic fingerprint testing requires an enabled target")
	}
	return nil
}
func UpdateModelFingerprintSiteConfig(ctx context.Context, expectedVersion int64, actorID int, c ModelFingerprintSiteConfig) (*ModelFingerprintSiteConfig, error) {
	if actorID <= 0 || expectedVersion < 0 {
		return nil, ErrFingerprintSiteConflict
	}
	if err := ValidateModelFingerprintSiteConfig(&c); err != nil {
		return nil, err
	}
	data, err := common.Marshal(c.Targets)
	if err != nil {
		return nil, err
	}
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Share the namespace fence with renames so a stale settings form cannot
		// reintroduce a retired group after the controller's catalogue read.
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		// A durable gate also serializes simultaneous first-time configuration saves.
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ModelTestGate{ID: 1}).Error; err != nil {
			return err
		}
		var gate ModelTestGate
		if err := lockForUpdate(tx).First(&gate, 1).Error; err != nil {
			return err
		}
		var old ModelFingerprintSiteConfig
		err := lockForUpdate(tx).First(&old, 1).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if expectedVersion != 0 {
				return ErrFingerprintSiteConflict
			}
			old = ModelFingerprintSiteConfig{ID: 1}
			if err = tx.Create(&old).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if old.Version != expectedVersion {
			return ErrFingerprintSiteConflict
		}
		for _, target := range c.EnabledTargets() {
			if err := validateGroupNamesForWriteTx(tx, target.Group); err != nil {
				return err
			}
		}
		c.ID = 1
		c.Version = old.Version + 1
		c.TargetsJSON = string(data)
		c.BudgetDay = old.BudgetDay
		c.ReservedQuota = old.ReservedQuota
		c.UpdatedBy = actorID
		c.UpdatedAt = time.Now().Unix()
		res := tx.Model(&ModelFingerprintSiteConfig{}).Where("id = ? AND version = ?", 1, expectedVersion).Select("version", "automatic_enabled", "default_period_secs", "daily_budget_quota", "targets_json", "updated_at", "updated_by").Updates(&c)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrFingerprintSiteConflict
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&Option{Key: ModelTestEnabledKey, Value: strconv.FormatBool(c.Enabled)}).Error; err != nil {
			return err
		}
		audit, err := common.Marshal(c)
		if err != nil {
			return err
		}
		return tx.Create(&ModelFingerprintSiteAudit{Version: c.Version, ActorID: actorID, CreatedAt: c.UpdatedAt, Configuration: string(audit)}).Error
	})
	return &c, err
}

// Lock config before checking the lease consistently across admission/dispatch/settlement.
func fingerprintSiteAuthorization(tx *gorm.DB, taskID, runnerID string, version int64, now int64) (*ModelFingerprintSiteConfig, error) {
	var c ModelFingerprintSiteConfig
	if err := lockForUpdate(tx).First(&c, 1).Error; err != nil {
		return nil, err
	}
	if !c.AutomaticEnabled {
		return nil, ErrFingerprintSiteDisabled
	}
	if c.Version != version {
		return nil, ErrFingerprintSiteConflict
	}
	enabled, err := modelTestEnabled(tx)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrModelTestsDisabled
	}
	var lock SystemTaskLock
	if err = lockForUpdate(tx).Where("type = ? AND task_id = ? AND locked_by = ? AND locked_until > ?", SystemTaskTypeModelFingerprintSite, taskID, runnerID, now).Take(&lock).Error; err != nil {
		return nil, ErrSystemTaskLockLost
	}
	var task SystemTask
	if err = tx.Where("task_id = ? AND type = ? AND status = ? AND locked_by = ?", taskID, SystemTaskTypeModelFingerprintSite, SystemTaskStatusRunning, runnerID).Take(&task).Error; err != nil {
		return nil, ErrSystemTaskLockLost
	}
	if err = common.UnmarshalJsonStr(c.TargetsJSON, &c.Targets); err != nil {
		return nil, err
	}
	return &c, nil
}
func ReserveModelFingerprintSiteRun(ctx context.Context, taskID, runnerID, targetID string, version, now int64) (*ModelFingerprintSiteRun, error) {
	var run ModelFingerprintSiteRun
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c, err := fingerprintSiteAuthorization(tx, taskID, runnerID, version, now)
		if err != nil {
			return err
		}
		var target *ModelFingerprintSiteTarget
		for i := range c.Targets {
			if c.Targets[i].ID == targetID {
				target = &c.Targets[i]
				break
			}
		}
		if target == nil || !target.Enabled {
			return ErrFingerprintSiteDisabled
		}
		period := target.PeriodSecs
		if period == 0 {
			period = c.DefaultPeriodSecs
		}
		if period < 60 || period > 604800 || now <= 0 {
			return ErrFingerprintSiteDisabled
		}
		// Calendar buckets alone permit back-to-back runs across a boundary. The
		// actual elapsed interval is authoritative, including across config edits.
		// The config lock serializes this check with every admission for this target.
		var latest ModelFingerprintSiteRun
		previous := tx.Select("created_at").Where("target_id = ?", targetID).Order("created_at DESC").Limit(1).Find(&latest)
		if previous.Error != nil {
			return previous.Error
		}
		if previous.RowsAffected > 0 && latest.CreatedAt > now-period {
			return ErrFingerprintSiteNotDue
		}
		window := now / period
		var count int64
		if err = tx.Model(&ModelFingerprintSiteRun{}).Where("target_id = ? AND config_version = ? AND period_window = ?", targetID, version, window).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return ErrFingerprintSiteNotDue
		}
		var active int64
		if err = tx.Model(&ModelFingerprintSiteRun{}).Where("status = ?", "running").Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrFingerprintSiteNotDue
		}
		run = ModelFingerprintSiteRun{ID: uuid.NewString(), TargetID: targetID, ConfigVersion: version, Window: window, TaskID: taskID, Group: target.Group, Model: target.Model, Status: "running", UsageState: "pending", CreatedAt: now}
		return tx.Create(&run).Error
	})
	return &run, err
}

// Persist dispatch BEFORE the network request. A repeated/uncertain probe must never be replayed.
func AuthorizeModelFingerprintSiteProbe(ctx context.Context, runID, taskID, runnerID, probeID string, requestIDs ...string) error {
	requestID := uuid.NewString()
	if len(requestIDs) > 1 {
		return errors.New("Invalid fingerprint request ID")
	}
	if len(requestIDs) == 1 {
		parsed, err := uuid.Parse(requestIDs[0])
		if err != nil {
			return errors.New("Invalid fingerprint request ID")
		}
		requestID = parsed.String()
	}
	if probeID == "" || len(probeID) > 128 {
		return errors.New("Invalid fingerprint probe ID")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run ModelFingerprintSiteRun
		if err := tx.First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		if run.TaskID != taskID || run.Status != "running" {
			return ErrFingerprintSiteConflict
		}
		c, err := fingerprintSiteAuthorization(tx, taskID, runnerID, run.ConfigVersion, time.Now().Unix())
		if err != nil {
			return err
		}
		// Re-read under the config lock so concurrent settlement cannot race dispatch.
		if err := lockForUpdate(tx).First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		if run.Status != "running" {
			return ErrFingerprintSiteConflict
		}
		found := false
		for _, target := range c.Targets {
			if target.ID == run.TargetID && target.Enabled && target.Group == run.Group && target.Model == run.Model {
				found = true
			}
		}
		if !found {
			return ErrFingerprintSiteDisabled
		}
		return tx.Create(&ModelFingerprintSiteProbe{RunID: runID, ProbeID: probeID, Status: "dispatching", RequestID: requestID, DispatchedAt: time.Now().Unix()}).Error
	})
}

// An uncertain upstream outcome stops automatic dispatch. No quota reservation or
// user-wallet settlement happens here; aggregate token usage is journaled per probe.
func FinishModelFingerprintSiteRun(ctx context.Context, runID string, result ModelFingerprintSiteResult, usageKnown bool) error {
	if result.Status != "succeeded" && result.Status != "failed" && result.Status != "unknown" {
		return errors.New("Invalid fingerprint result status")
	}
	if result.FingerprintResult != "" {
		var v modelfingerprint.Result
		if err := common.UnmarshalJsonStr(result.FingerprintResult, &v); err != nil {
			return err
		}
		safe, err := common.Marshal(v)
		if err != nil {
			return err
		}
		result.FingerprintResult = string(safe)
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockModelFingerprintRetention(tx); err != nil {
			return err
		}
		var c ModelFingerprintSiteConfig
		if err := lockForUpdate(tx).First(&c, 1).Error; err != nil {
			return err
		}
		var run ModelFingerprintSiteRun
		if err := lockForUpdate(tx).First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		if run.Status != "running" {
			return ErrFingerprintSiteConflict
		}
		usageState := "known"
		if !usageKnown {
			usageState = "unknown"
			result.Status = "unknown"
			c.AutomaticEnabled = false
			c.Version++
			c.UpdatedAt = time.Now().Unix()
			c.UpdatedBy = 0
			if err := tx.Model(&ModelFingerprintSiteConfig{}).Where("id = ?", 1).Updates(map[string]any{"automatic_enabled": false, "version": c.Version, "updated_at": c.UpdatedAt, "updated_by": 0}).Error; err != nil {
				return err
			}
			// Version advancement prevents a stale admin form from silently undoing the safety pause.
			audit, err := common.Marshal(map[string]any{"reason": "usage_unknown", "run_id": runID, "automatic_enabled": false})
			if err != nil {
				return err
			}
			if err := tx.Create(&ModelFingerprintSiteAudit{Version: c.Version, CreatedAt: c.UpdatedAt, Configuration: string(audit)}).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&ModelFingerprintSiteRun{}).Where("id = ? AND status = ?", runID, "running").Updates(map[string]any{"status": result.Status, "usage_state": usageState, "benchmark_id": result.BenchmarkID, "fingerprint_version": result.FingerprintVersion, "assessment": result.Assessment, "fingerprint_result": result.FingerprintResult, "finished_at": time.Now().Unix()}).Error; err != nil {
			return err
		}
		return pruneModelFingerprintReports(tx, time.Now().Unix(), true, run.Group, run.Model)
	})
}

// Per-target limits intentionally avoid starving less active targets with a global LIMIT.
func ListModelFingerprintSitePublicRuns(ctx context.Context) ([]ModelFingerprintSitePublicRun, error) {
	c, err := GetModelFingerprintSiteConfig(ctx)
	if err != nil {
		return nil, err
	}
	result := []ModelFingerprintSitePublicRun{}
	for _, target := range c.Targets {
		var effective []ModelFingerprintSiteRun
		if err := DB.WithContext(ctx).Where("target_id = ? AND status = ? AND assessment = ? AND history_pruned_at = ?", target.ID, "succeeded", "scored", 0).Order("created_at DESC").Order("id DESC").Limit(60).Find(&effective).Error; err != nil {
			return nil, err
		}
		for _, run := range effective {
			result = append(result, run.Public())
		}
		var failures []ModelFingerprintSiteRun
		if err := DB.WithContext(ctx).Where("target_id = ? AND NOT (status = ? AND assessment = ?) AND history_pruned_at = ?", target.ID, "succeeded", "scored", 0).Order("created_at DESC").Order("id DESC").Limit(5).Find(&failures).Error; err != nil {
			return nil, err
		}
		for _, run := range failures {
			result = append(result, run.Public())
		}
	}
	return result, nil
}

// CompleteModelFingerprintSiteProbe records only validated aggregate usage, never response bodies.
// A failed or interrupted request stays dispatching and is never automatically replayed.
func CompleteModelFingerprintSiteProbe(ctx context.Context, runID, probeID string, prompt, completion int, responseModel string) error {
	if prompt < 0 || completion < 0 || int64(prompt) > 1<<31-1 || int64(completion) > 1<<31-1 || len(responseModel) > 255 {
		return errors.New("Invalid fingerprint probe usage")
	}
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run ModelFingerprintSiteRun
		if err := lockForUpdate(tx).First(&run, "id = ?", runID).Error; err != nil {
			return err
		}
		if run.Status != "running" {
			return ErrFingerprintSiteConflict
		}
		result := tx.Model(&ModelFingerprintSiteProbe{}).Where("run_id = ? AND probe_id = ? AND status = ?", runID, probeID, "dispatching").Updates(map[string]any{"status": "completed", "completed_at": time.Now().Unix(), "prompt_tokens": prompt, "completion_tokens": completion, "response_model": responseModel})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrFingerprintSiteConflict
		}
		return nil
	})
}
