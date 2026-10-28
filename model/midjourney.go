package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

type Midjourney struct {
	Id          int    `json:"id"`
	Code        int    `json:"code"`
	UserId      int    `json:"user_id" gorm:"index"`
	Action      string `json:"action" gorm:"type:varchar(40);index"`
	MjId        string `json:"mj_id" gorm:"index"`
	Prompt      string `json:"prompt"`
	PromptEn    string `json:"prompt_en"`
	Description string `json:"description"`
	State       string `json:"state"`
	SubmitTime  int64  `json:"submit_time" gorm:"index"`
	StartTime   int64  `json:"start_time" gorm:"index"`
	FinishTime  int64  `json:"finish_time" gorm:"index"`
	ImageUrl    string `json:"image_url"`
	VideoUrl    string `json:"video_url"`
	VideoUrls   string `json:"video_urls"`
	Status      string `json:"status" gorm:"type:varchar(20);index"`
	Progress    string `json:"progress" gorm:"type:varchar(30);index"`
	FailReason  string `json:"fail_reason"`
	ChannelId   int    `json:"channel_id"`
	Quota       int    `json:"quota"`
	Buttons     string `json:"buttons"`
	Properties  string `json:"properties"`

	TokenId              int    `json:"-" gorm:"default:0"`
	TokenFingerprint     string `json:"-" gorm:"type:varchar(64)"`
	BillingAccounted     bool   `json:"-"`
	BillingRequestID     string `json:"-" gorm:"type:varchar(64)"`
	BillingOriginalQuota int    `json:"-" gorm:"type:bigint"`
	BillingChannelId     int    `json:"-" gorm:"default:0"`
	// NULL is an unknown legacy charge, never proof of a paid-only debit. An empty array marks a new task that
	// has not been charged; nonempty receipts commit atomically with its debit.
	WalletDebit []LedgerShare `json:"-" gorm:"serializer:json;type:text"`
}

// HasCurrentBillingIdentity identifies only explicitly captured v2 provenance.
// Empty legacy values never authorize reconstruction from current credentials.
func (m *Midjourney) HasCurrentBillingIdentity() bool {
	return m != nil && m.UserId > 0 && m.BillingRequestID != "" && len(m.BillingRequestID) <= 64 && strings.TrimSpace(m.BillingRequestID) == m.BillingRequestID && m.BillingOriginalQuota >= 0 && m.BillingOriginalQuota <= common.MaxQuota && m.TokenId >= 0 && len(m.TokenFingerprint) == 64 && (m.TokenId > 0 || m.TokenFingerprint == BillingTokenFingerprint("no-token-v2"))
}

// TaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type TaskQueryParams struct {
	ChannelID      string
	MjID           string
	StartTimestamp string
	EndTimestamp   string
}

func GetAllUserTask(userId int, startIdx int, num int, queryParams TaskQueryParams) []*Midjourney {
	var tasks []*Midjourney
	var err error

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func GetAllTasks(startIdx int, num int, queryParams TaskQueryParams) []*Midjourney {
	var tasks []*Midjourney
	var err error

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

// Unlike generic task plugins, the MJ upstream may legitimately return UNKNOWN.
// Exclude only our explicit local-metadata review markers from automatic scans.
func excludeMidjourneyPollingReview(db *gorm.DB) *gorm.DB {
	return db.Where("status <> ? OR fail_reason IS NULL OR fail_reason NOT IN ?", "UNKNOWN", []string{
		"Missing upstream task identity; delivery requires operator review",
		"Missing channel metadata; upstream delivery requires operator review",
		"Ambiguous upstream task identity; delivery requires operator review",
		MidjourneyTimeoutReviewReason,
	})
}

func GetAllUnFinishTasks() []*Midjourney {
	var tasks []*Midjourney
	var err error
	// get all tasks progress is not 100%
	err = excludeMidjourneyPollingReview(DB).Where("progress != ?", "100%").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return tasks
}

// HasUnfinishedMidjourneyTasks reports whether at least one Midjourney task is
// still in progress. It is a cheap existence check (LIMIT 1) used to decide
// whether the midjourney_poll system task needs to run; when no task is pending
// the scheduler skips creating a row entirely.
func HasUnfinishedMidjourneyTasks() bool {
	var id int
	err := excludeMidjourneyPollingReview(DB.Model(&Midjourney{})).
		Where("progress != ?", "100%").
		Limit(1).
		Pluck("id", &id).Error
	return err == nil && id != 0
}

func GetByOnlyMJId(mjId string) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("mj_id = ?", mjId).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetByMJId(userId int, mjId string) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("user_id = ? and mj_id = ?", userId, mjId).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetByMJIds(userId int, mjIds []string) []*Midjourney {
	var mj []*Midjourney
	var err error
	err = DB.Where("user_id = ? and mj_id in (?)", userId, mjIds).Find(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetMjByuId(id int) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("id = ?", id).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func UpdateProgress(id int, progress string) error {
	return DB.Model(&Midjourney{}).Where("id = ?", id).Update("progress", progress).Error
}

func (midjourney *Midjourney) Insert() error {
	var err error
	err = DB.Create(midjourney).Error
	return err
}

// Update is a compatibility observation write. It cannot re-create a deleted
// row, overwrite terminal status, or write financial fields from a stale copy.
func (midjourney *Midjourney) Update() error {
	if midjourney == nil || midjourney.Id <= 0 {
		return fmt.Errorf("invalid Midjourney observation identity")
	}
	return DB.Model(midjourney).Where("status NOT IN ?", []string{"SUCCESS", "FAILURE"}).
		Select(midjourneyObservationColumns).Updates(midjourney).Error
}

var midjourneyObservationColumns = []string{
	"code", "prompt_en", "description", "state", "submit_time", "start_time", "finish_time",
	"image_url", "video_url", "video_urls", "status", "progress", "fail_reason", "buttons", "properties",
}

func (midjourney *Midjourney) UpdateBillingState() error {
	return midjourney.UpdateBillingStateTx(DB)
}

func (midjourney *Midjourney) UpdateBillingStateTx(tx *gorm.DB) error {
	result := tx.Model(midjourney).
		Select("quota", "token_id", "token_fingerprint", "billing_accounted", "billing_request_id", "billing_original_quota", "billing_channel_id", "wallet_debit").
		Updates(midjourney)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("Midjourney billing state changed for task %s", midjourney.MjId)
	}
	return nil
}

func (midjourney *Midjourney) GetBillingChannelId() int {
	if midjourney.BillingChannelId > 0 {
		return midjourney.BillingChannelId
	}
	return midjourney.ChannelId
}

// UpdateWithStatus uses a terminal-safe CAS and an observation-only allowlist.
// No source/token/accounting identity can be overwritten by a poll or callback.
func (midjourney *Midjourney) UpdateWithStatus(fromStatus string) (bool, error) {
	if midjourney == nil || midjourney.Id <= 0 {
		return false, fmt.Errorf("invalid Midjourney observation identity")
	}
	if fromStatus == "SUCCESS" || fromStatus == "FAILURE" {
		return false, nil
	}
	result := DB.Model(midjourney).Where("status = ?", fromStatus).
		Select(midjourneyObservationColumns).Updates(midjourney)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func MjBulkUpdate(mjIds []string, params map[string]any) error {
	return DB.Model(&Midjourney{}).
		Where("mj_id in (?)", mjIds).
		Updates(params).Error
}

// FailUnfinishedMidjourneyTasksByIDs never overwrites a terminal observation
// published after the polling snapshot was loaded. Local IDs also isolate tasks
// from unrelated channels that happen to reuse an upstream ID.
func FailUnfinishedMidjourneyTasksByIDs(taskIDs []int, reason string) error {
	updates := map[string]any{"status": "FAILURE", "progress": "100%"}
	if reason != "" {
		updates["fail_reason"] = reason
	}
	return DB.Model(&Midjourney{}).Where("id IN ? AND status NOT IN ?", taskIDs, []string{"SUCCESS", "FAILURE"}).Updates(updates).Error
}

func MjBulkUpdateByTaskIds(taskIDs []int, params map[string]any) error {
	return DB.Model(&Midjourney{}).
		Where("id in (?)", taskIDs).
		Updates(params).Error
}

// CountAllTasks returns total midjourney tasks for admin query
func CountAllTasks(queryParams TaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Midjourney{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}

// CountAllUserTask returns total midjourney tasks for user
func CountAllUserTask(userId int, queryParams TaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Midjourney{}).Where("user_id = ?", userId)
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}
