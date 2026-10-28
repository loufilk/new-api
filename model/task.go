package model

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	commonRelay "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"gorm.io/gorm"
)

type TaskStatus string

func (t TaskStatus) ToVideoStatus() string {
	var status string
	switch t {
	case TaskStatusNotStart, TaskStatusQueued, TaskStatusSubmitted:
		status = dto.VideoStatusQueued
	case TaskStatusInProgress:
		status = dto.VideoStatusInProgress
	case TaskStatusSuccess:
		status = dto.VideoStatusCompleted
	case TaskStatusFailure:
		status = dto.VideoStatusFailed
	default:
		status = dto.VideoStatusUnknown // Default fallback
	}
	return status
}

const (
	TaskStatusNotStart   TaskStatus = "NOT_START"
	TaskStatusSubmitted             = "SUBMITTED"
	TaskStatusQueued                = "QUEUED"
	TaskStatusInProgress            = "IN_PROGRESS"
	TaskStatusFailure               = "FAILURE"
	TaskStatusSuccess               = "SUCCESS"
	TaskStatusUnknown               = "UNKNOWN"
)

// TaskRefundLegacyCutoff separates tasks created before timeout refunds were
// introduced. Those legacy tasks are failed without an automatic refund.
const TaskRefundLegacyCutoff int64 = 1771718400 // 2026-02-22 00:00:00 UTC

type Task struct {
	ID        int64                 `json:"id" gorm:"primary_key;AUTO_INCREMENT"`
	CreatedAt int64                 `json:"created_at" gorm:"index"`
	UpdatedAt int64                 `json:"updated_at"`
	TaskID    string                `json:"task_id" gorm:"type:varchar(191);index"` // 第三方id，不一定有/ song id\ Task id
	Platform  constant.TaskPlatform `json:"platform" gorm:"type:varchar(30);index"` // 平台
	UserId    int                   `json:"user_id" gorm:"index"`
	Group     string                `json:"group" gorm:"type:varchar(50)"` // 修正计费用
	ChannelId int                   `json:"channel_id" gorm:"index"`
	Quota     int                   `json:"quota"`
	// SettlementPending keeps completed tasks discoverable until their main-DB
	// funding, token and usage transaction commits. Old rows may contain NULL.
	SettlementPending     bool       `json:"-" gorm:"index"`
	SettlementAttemptedAt int64      `json:"-" gorm:"index"`
	Action                string     `json:"action" gorm:"type:varchar(40);index"` // 任务类型, song, lyrics, description-mode
	Status                TaskStatus `json:"status" gorm:"type:varchar(20);index"` // 任务状态
	FailReason            string     `json:"fail_reason"`
	SubmitTime            int64      `json:"submit_time" gorm:"index"`
	StartTime             int64      `json:"start_time" gorm:"index"`
	FinishTime            int64      `json:"finish_time" gorm:"index"`
	Progress              string     `json:"progress" gorm:"type:varchar(20);index"`
	Properties            Properties `json:"properties" gorm:"type:json"`
	Username              string     `json:"username,omitempty" gorm:"-"`
	// 禁止返回给用户，内部可能包含key等隐私信息
	PrivateData TaskPrivateData `json:"-" gorm:"column:private_data;type:json"`
	Data        json.RawMessage `json:"data" gorm:"type:json"`
}

func (t *Task) SetData(data any) {
	b, _ := common.Marshal(data)
	t.Data = json.RawMessage(b)
}

func (t *Task) GetData(v any) error {
	return common.Unmarshal(t.Data, &v)
}

type Properties struct {
	Input             string `json:"input"`
	UpstreamModelName string `json:"upstream_model_name,omitempty"`
	OriginModelName   string `json:"origin_model_name,omitempty"`
}

func (m *Properties) Scan(val any) error {
	bytesValue := jsonScanBytes(val)
	if len(bytesValue) == 0 {
		*m = Properties{}
		return nil
	}
	return common.Unmarshal(bytesValue, m)
}

func (m Properties) Value() (driver.Value, error) {
	if m == (Properties{}) {
		return nil, nil
	}
	// 必须返回 string 而非 []byte:PG simple protocol 下 []byte 按 bytea 编码,
	// 写 json 列会触发 SQLSTATE 22P02。
	b, err := common.Marshal(m)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

type TaskPrivateData struct {
	Key            string `json:"key,omitempty"`
	UpstreamTaskID string `json:"upstream_task_id,omitempty"` // 上游真实 task ID
	ResultURL      string `json:"result_url,omitempty"`       // 任务成功后的结果 URL（视频地址等）
	// Execution records safe, immutable request provenance. It lives next to
	// other private task state so public task DTOs cannot expose it by accident.
	Execution *TaskExecutionSnapshot `json:"execution,omitempty"`
	// RequestId 提交请求的账本引用（预扣费 reference），用于返利对账。
	RequestId string `json:"request_id,omitempty"`
	// 计费上下文：用于异步退款/差额结算（轮询阶段读取）
	BillingSource              string              `json:"billing_source,omitempty"`  // "wallet" 或 "subscription"
	SubscriptionId             int                 `json:"subscription_id,omitempty"` // 订阅 ID，用于订阅退款
	SubscriptionQuotaPeriod    int64               `json:"subscription_quota_period,omitempty"`
	SubscriptionReservedAmount int64               `json:"subscription_reserved_amount,omitempty"`
	TokenFingerprint           string              `json:"token_fingerprint,omitempty"`
	TokenId                    int                 `json:"token_id,omitempty"`        // 令牌 ID，用于令牌额度退款
	NodeName                   string              `json:"node_name,omitempty"`       // 发起任务的节点名，轮询结算阶段据此归属日志而非最后查询节点
	BillingContext             *TaskBillingContext `json:"billing_context,omitempty"` // 计费参数快照（用于轮询阶段重新计算）
	// WalletDebit 钱包计费各账户实际扣减份额（按扣减顺序），异步结算补扣时
	// 累计更新；退款按此精确退回原账户。缺失来源凭证时必须人工对账，不能猜测 paid。
	WalletDebit []LedgerShare `json:"wallet_debit,omitempty"`
	// ResponsesBackground records that the openai_responses create request
	// asked for background:true. Every task is durable and survives client
	// disconnect regardless; this only echoes the protocol-level request
	// attribute back on retrieval snapshots.
	ResponsesBackground bool `json:"responses_background,omitempty"`
	// PluginState is plugin-owned cross-round data. Unlike Task.Data it is
	// only replaced when a hook explicitly returns state.
	PluginState json.RawMessage `json:"plugin_state,omitempty"`
	// PollFailures counts consecutive unrecognized or transient poll outcomes.
	PollFailures int `json:"poll_failures,omitempty"`
	// ResultDiscarded marks an immediate terminal result whose submit route
	// declared retainResult: false. The upstream snapshot was never written
	// and every retrieval surface treats the task as not found. The zero
	// value keeps historical rows retained and retrievable.
	ResultDiscarded bool `json:"result_discarded,omitempty"`
	// PendingSettlement is historical private-policy data. New official tasks
	// never write it; compatibility guards keep old rows read-only.
	PendingSettlement   *TaskSettlement `json:"pending_settlement,omitempty"`
	SubmissionAccounted bool            `json:"submission_accounted,omitempty"`
	// Version 2 identifies only newly written financial work. Historical
	// PendingSettlement records are never upgraded or replayed implicitly.
	FinancialVersion      int                  `json:"financial_version,omitempty"`
	FinancialIntent       *TaskFinancialIntent `json:"financial_intent,omitempty"`
	FinancialOperationID  string               `json:"financial_operation_id,omitempty"`
	FinancialActualQuota  int                  `json:"financial_actual_quota,omitempty"`
	FinancialReviewReason string               `json:"financial_review_reason,omitempty"`
}

// TaskFinancialIntent is the current terminal adjustment saved with the task
// status. Its operation ID is immutable across retries and becomes the receipt
// proving that funding, token, usage and task state committed together.
type TaskFinancialIntent struct {
	Kind          string             `json:"kind"`
	PreviousQuota int                `json:"previous_quota"`
	ActualQuota   int                `json:"actual_quota"`
	OperationID   string             `json:"operation_id"`
	Reason        *common.Message    `json:"reason,omitempty"`
	Clamp         *common.QuotaClamp `json:"clamp,omitempty"`
}

// TaskSettlement decodes historical private-policy intent only. No production
// writer/recovery is connected; retain the schema without executing old intent.
type TaskSettlement struct {
	Phase     string             `json:"phase"`
	Quota     int                `json:"quota"`
	Reason    *common.Message    `json:"reason,omitempty"`
	Clamp     *common.QuotaClamp `json:"clamp,omitempty"`
	SkipFunds bool               `json:"skip_funds,omitempty"`
}

type TaskExecutionSnapshot struct {
	RequestID   string              `json:"request_id,omitempty"`
	RequestPath string              `json:"request_path,omitempty"`
	TaskPlugin  *TaskPluginSnapshot `json:"task_plugin,omitempty"`
}

// TaskPluginSnapshot contains credential-free identity only. Plugin source,
// request/response payloads, and channel secrets must never be added here.
type TaskPluginSnapshot struct {
	Key        string                    `json:"key"`
	Name       string                    `json:"name"`
	Version    string                    `json:"version"`
	Author     *TaskPluginAuthorSnapshot `json:"author,omitempty"`
	APIVersion int                       `json:"api_version"`
	Generation uint64                    `json:"generation"`
}

type TaskPluginAuthorSnapshot struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

// TaskBillingContext 记录任务提交时的计费参数，以便轮询阶段可以重新计算额度。
type TaskBillingContext struct {
	ModelPrice       float64                      `json:"model_price,omitempty"`        // 模型单价
	GroupRatio       float64                      `json:"group_ratio,omitempty"`        // 分组倍率
	ModelRatio       float64                      `json:"model_ratio,omitempty"`        // 模型倍率
	OtherRatios      map[string]float64           `json:"other_ratios,omitempty"`       // 附加倍率（时长、分辨率等）
	OriginModelName  string                       `json:"origin_model_name,omitempty"`  // 用户请求/逻辑模型身份（旧任务唯一身份来源，必须保留）
	BillingModelName string                       `json:"billing_model_name,omitempty"` // 价格/倍率/赠金资格使用的计费模型身份
	PerCallBilling   bool                         `json:"per_call_billing,omitempty"`   // 按次计费：跳过轮询阶段的差额结算
	TieredSnapshot   *billingexpr.BillingSnapshot `json:"tiered_snapshot,omitempty"`
}

// EffectiveBillingModelName 返回任务的计费模型身份。
// 新任务直接使用提交时持久化的 BillingModelName；旧任务（仅写入
// OriginModelName 的历史数据）回退到 OriginModelName，保证老数据结算行为不变。
func (c *TaskBillingContext) EffectiveBillingModelName() string {
	if c == nil {
		return ""
	}
	if c.BillingModelName != "" {
		return c.BillingModelName
	}
	return c.OriginModelName
}

// EffectiveRequestID 解析提交请求的账本引用（预扣费 reference），供账本与返利对账使用。
//
// 优先级：
//  1. Execution.RequestID —— RC30 及之后创建的任务的唯一真源。它在提交时一次性
//     写入，之后不再改写。
//  2. RequestId —— 历史字段，仅为让 RC30 之前持久化的任务仍能对账而保留；
//     任何情况下都不会优先于 Execution.RequestID。
//
// 两者同时非空且不一致时选 Execution.RequestID：此时残留的 legacy 值属于旧写入
// 路径，而 Execution 快照才与实际写入的账本条目匹配。两者均为空时返回 ""。
func (p *TaskPrivateData) EffectiveRequestID() string {
	if p == nil {
		return ""
	}
	if p.Execution != nil && p.Execution.RequestID != "" {
		return p.Execution.RequestID
	}
	return p.RequestId
}

// ResultRetrievable reports whether retrieval surfaces (native query routes,
// protocol retrieve endpoints, artifact projection) may serve this task.
func (t *Task) ResultRetrievable() bool {
	return !t.PrivateData.ResultDiscarded
}

// GetUpstreamTaskID 获取上游真实 task ID（用于与 provider 通信）
// 旧数据没有 UpstreamTaskID 时，TaskID 本身就是上游 ID
func (t *Task) GetUpstreamTaskID() string {
	if t.PrivateData.UpstreamTaskID != "" {
		return t.PrivateData.UpstreamTaskID
	}
	return t.TaskID
}

// GetResultURL 获取任务结果 URL（视频地址等）
// 新数据存在 PrivateData.ResultURL 中；旧数据回退到 FailReason（历史兼容）
func (t *Task) GetResultURL() string {
	if t.PrivateData.ResultURL != "" {
		return t.PrivateData.ResultURL
	}
	return t.FailReason
}

// GenerateTaskID 生成对外暴露的 task_xxxx 格式 ID
func GenerateTaskID() string {
	key, _ := common.GenerateRandomCharsKey(32)
	return "task_" + key
}

func (p *TaskPrivateData) Scan(val any) error {
	*p = TaskPrivateData{}
	bytesValue := jsonScanBytes(val)
	if len(bytesValue) == 0 {
		return nil
	}
	return common.Unmarshal(bytesValue, p)
}

func (p TaskPrivateData) Value() (driver.Value, error) {
	if p.Key == "" && p.UpstreamTaskID == "" && p.ResultURL == "" && p.Execution == nil &&
		p.BillingSource == "" && p.SubscriptionId == 0 && p.SubscriptionQuotaPeriod == 0 && p.SubscriptionReservedAmount == 0 && p.TokenId == 0 && p.TokenFingerprint == "" &&
		p.NodeName == "" && p.BillingContext == nil && len(p.WalletDebit) == 0 &&
		p.RequestId == "" && !p.ResponsesBackground &&
		len(p.PluginState) == 0 && p.PollFailures == 0 && !p.ResultDiscarded && p.PendingSettlement == nil && !p.SubmissionAccounted && p.FinancialVersion == 0 && p.FinancialIntent == nil && p.FinancialOperationID == "" && p.FinancialActualQuota == 0 && p.FinancialReviewReason == "" {
		return nil, nil
	}
	// 同 Properties.Value:string 避免 PG simple protocol 的 bytea 编码。
	b, err := common.Marshal(p)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

// SyncTaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type SyncTaskQueryParams struct {
	Platform       constant.TaskPlatform
	ChannelID      string
	TaskID         string
	UserID         string
	Action         string
	Status         string
	StartTimestamp int64
	EndTimestamp   int64
	UserIDs        []int
}

func InitTask(platform constant.TaskPlatform, relayInfo *commonRelay.RelayInfo) *Task {
	properties := Properties{}
	privateData := TaskPrivateData{}
	if relayInfo != nil && relayInfo.ChannelMeta != nil {
		// A New API channel may rotate between several gateway tokens, so the
		// task keeps the key that submitted it and polls with the same identity.
		if relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeGemini ||
			relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeVertexAi ||
			relayInfo.ChannelMeta.ChannelType == constant.ChannelTypeNewAPI {
			privateData.Key = relayInfo.ChannelMeta.ApiKey
		}
		if relayInfo.UpstreamModelName != "" {
			properties.UpstreamModelName = relayInfo.UpstreamModelName
		}
		if relayInfo.OriginModelName != "" {
			properties.OriginModelName = relayInfo.OriginModelName
		}
	}

	// 使用预生成的公开 ID（如果有），否则新生成
	taskID := ""
	if relayInfo.TaskRelayInfo != nil && relayInfo.TaskRelayInfo.PublicTaskID != "" {
		taskID = relayInfo.TaskRelayInfo.PublicTaskID
	} else {
		taskID = GenerateTaskID()
	}

	t := &Task{
		TaskID:      taskID,
		UserId:      relayInfo.UserId,
		Group:       relayInfo.UsingGroup,
		SubmitTime:  time.Now().Unix(),
		Status:      TaskStatusNotStart,
		Progress:    "0%",
		ChannelId:   relayInfo.ChannelId,
		Platform:    platform,
		Properties:  properties,
		PrivateData: privateData,
	}
	return t
}

func TaskGetAllUserTask(userId int, startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	// Task lists never render the persisted upstream snapshot; the dashboard
	// loads media through the artifacts endpoint instead.
	err = query.Omit("channel_id", "data").Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

func TaskGetAllTasks(startIdx int, num int, queryParams SyncTaskQueryParams) []*Task {
	var tasks []*Task
	var err error

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	err = query.Omit("data").Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error
	if err != nil {
		return nil
	}

	return tasks
}

// HasLegacyBillingIntent identifies historical custom settlement records.
// It is a compatibility read guard, not a new official task state machine.
func (t *Task) HasLegacyBillingIntent() bool {
	return t != nil && (t.PrivateData.PendingSettlement != nil || t.SettlementPending && t.PrivateData.FinancialVersion != 2)
}

func excludeLegacyBillingTasks(tasks []*Task) []*Task {
	out := tasks[:0]
	for _, task := range tasks {
		if !task.HasLegacyBillingIntent() {
			out = append(out, task)
		}
	}
	return out
}

func GetTimedOutUnfinishedTasks(cutoffUnix int64, limit int) []*Task {
	var tasks []*Task
	err := DB.Where("settlement_pending = ? OR settlement_pending IS NULL", false).Where("progress != ?", "100%").
		Where("status NOT IN ?", []string{TaskStatusFailure, TaskStatusSuccess, TaskStatusUnknown}).
		Where("submit_time < ?", cutoffUnix).
		Order("submit_time").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil
	}
	return excludeLegacyBillingTasks(tasks)
}

func GetAllUnFinishSyncTasks(limit int) []*Task {
	var tasks []*Task
	var err error
	// get all tasks progress is not 100%
	err = DB.Where("settlement_pending = ? OR settlement_pending IS NULL", false).Where("progress != ?", "100%").Where("status != ?", TaskStatusFailure).Where("status != ?", TaskStatusSuccess).Where("status != ?", TaskStatusUnknown).Limit(limit).Order("id").Find(&tasks).Error
	if err != nil {
		return nil
	}
	return excludeLegacyBillingTasks(tasks)
}

// HasUnfinishedSyncTasks reports whether at least one async (Suno/video) task is
// still in progress. It is a cheap existence check (LIMIT 1) used to decide
// whether the async_task_poll system task needs to run; when no task is pending
// the scheduler skips creating a row entirely.
func HasUnfinishedSyncTasks() bool {
	var id int64
	err := DB.Model(&Task{}).
		Where("settlement_pending = ? OR settlement_pending IS NULL", false).
		Where("progress != ?", "100%").
		Where("status != ?", TaskStatusFailure).
		Where("status != ?", TaskStatusSuccess).
		Where("status != ?", TaskStatusUnknown).
		Limit(1).
		Pluck("id", &id).Error
	return err == nil && id != 0
}

func GetByOnlyTaskId(taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("task_id = ?", taskId).First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

// GetUniqueByOnlyTaskId resolves a public task identifier only when exactly one
// row owns it. Historical task identifiers were not globally unique, so
// capability-based reads must fail closed instead of selecting an arbitrary
// tenant's row.
func GetUniqueByOnlyTaskId(taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var tasks []*Task
	if err := DB.Where("task_id = ?", taskId).Order("id").Limit(2).Find(&tasks).Error; err != nil {
		return nil, false, err
	}
	if len(tasks) != 1 {
		return nil, false, nil
	}
	return tasks[0], true, nil
}

func GetByTaskId(userId int, taskId string) (*Task, bool, error) {
	if taskId == "" {
		return nil, false, nil
	}
	var task *Task
	var err error
	err = DB.Where("user_id = ? and task_id = ?", userId, taskId).
		First(&task).Error
	exist, err := RecordExist(err)
	if err != nil {
		return nil, false, err
	}
	return task, exist, err
}

func GetByTaskIdsForPlatforms(userID int, platforms []constant.TaskPlatform, taskIDs []string) ([]*Task, error) {
	if len(platforms) == 0 || len(taskIDs) == 0 {
		return nil, nil
	}
	var tasks []*Task
	err := DB.
		Where("user_id = ? AND platform IN ? AND task_id IN ?", userID, platforms, taskIDs).
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// GetTaskForProtocolObservation reloads one public task through the ownership
// boundary used by long-lived plugin protocol observers. A missing task,
// foreign user, and wrong plugin platform are deliberately indistinguishable.
func GetTaskForProtocolObservation(ctx context.Context, userID int, platform constant.TaskPlatform, taskID string) (*Task, bool, error) {
	if taskID == "" {
		return nil, false, nil
	}
	var task Task
	err := DB.WithContext(ctx).
		Where("user_id = ? AND platform = ? AND task_id = ?", userID, platform, taskID).
		First(&task).Error
	exists, err := RecordExist(err)
	if err != nil || !exists {
		return nil, exists, err
	}
	return &task, true, nil
}

func (Task *Task) Insert() error {
	return Task.InsertWithContext(context.Background())
}

// InsertWithContext creates the row. omitColumns are left out of the INSERT
// (for example "data" when the submit route discards the upstream snapshot)
// while the in-memory task keeps its values for presentation.
func (Task *Task) InsertWithContext(ctx context.Context, omitColumns ...string) error {
	tx := DB.WithContext(ctx)
	if len(omitColumns) > 0 {
		tx = tx.Omit(omitColumns...)
	}
	return tx.Create(Task).Error
}

type taskSnapshot struct {
	Status       TaskStatus
	Progress     string
	StartTime    int64
	FinishTime   int64
	FailReason   string
	ResultURL    string
	Data         json.RawMessage
	PluginState  json.RawMessage
	PollFailures int
}

func (s taskSnapshot) Equal(other taskSnapshot) bool {
	return s.Status == other.Status &&
		s.Progress == other.Progress &&
		s.StartTime == other.StartTime &&
		s.FinishTime == other.FinishTime &&
		s.FailReason == other.FailReason &&
		s.ResultURL == other.ResultURL &&
		bytes.Equal(s.Data, other.Data) &&
		bytes.Equal(s.PluginState, other.PluginState) &&
		s.PollFailures == other.PollFailures
}

func (t *Task) Snapshot() taskSnapshot {
	return taskSnapshot{
		Status:       t.Status,
		Progress:     t.Progress,
		StartTime:    t.StartTime,
		FinishTime:   t.FinishTime,
		FailReason:   t.FailReason,
		ResultURL:    t.PrivateData.ResultURL,
		Data:         t.Data,
		PluginState:  t.PrivateData.PluginState,
		PollFailures: t.PrivateData.PollFailures,
	}
}

// Update is a compatibility observation write, never a financial write. New
// polling callers should use UpdateWithStatus with their observed status.
func (t *Task) Update() error {
	_, err := t.updateObservation(nil)
	return err
}

func (t *Task) UpdateQuota() error {
	return DB.Model(t).Update("quota", t.Quota).Error
}

// UpdateBillingStateTx commits the final quota and its wallet receipt together.
// The previous quota guards against a stale observer applying the same token or
// subscription adjustment twice. A failed update rolls back the caller's funds.
func (t *Task) UpdateBillingStateTx(tx *gorm.DB, previousQuota int) error {
	result := tx.Model(&Task{}).Where("id = ? AND quota = ?", t.ID, previousQuota).
		Select("quota", "private_data", "settlement_pending").Updates(t)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("task billing state changed for task %s", t.TaskID)
	}
	return nil
}

// UpdateWithStatus locks and merges only observation fields. In particular a
// stale poll cannot replace a newer source receipt, review reason or financial
// intent embedded in private_data, even when its status still matches.
func (t *Task) UpdateWithStatus(fromStatus TaskStatus) (bool, error) {
	return t.updateObservation(&fromStatus)
}

func (t *Task) updateObservation(fromStatus *TaskStatus) (bool, error) {
	if t == nil || t.ID <= 0 || t.UserId <= 0 {
		return false, fmt.Errorf("invalid task observation identity")
	}
	won := false
	var current *Task
	err := DB.Transaction(func(tx *gorm.DB) error {
		stored, err := LoadTaskFinancialTx(tx, t.ID, t.UserId)
		if err != nil {
			return err
		}
		current = stored
		if stored.TaskID != t.TaskID {
			return ErrTaskFinancialReview
		}
		if stored.HasLegacyBillingIntent() || stored.SettlementPending || stored.Status == TaskStatusSuccess || stored.Status == TaskStatusFailure || fromStatus != nil && stored.Status != *fromStatus {
			return nil
		}
		stored.Status, stored.Progress, stored.FailReason = t.Status, t.Progress, t.FailReason
		stored.SubmitTime, stored.StartTime, stored.FinishTime = t.SubmitTime, t.StartTime, t.FinishTime
		stored.Data = t.Data
		stored.PrivateData.ResultURL = t.PrivateData.ResultURL
		stored.PrivateData.PluginState = t.PrivateData.PluginState
		stored.PrivateData.PollFailures = t.PrivateData.PollFailures
		stored.PrivateData.BillingContext = t.PrivateData.BillingContext
		result := tx.Model(stored).Select("status", "progress", "fail_reason", "submit_time", "start_time", "finish_time", "data", "private_data").Updates(stored)
		if result.Error != nil {
			return result.Error
		}
		won = result.RowsAffected > 0
		return nil
	})
	if err == nil && current != nil {
		*t = *current
	}
	return won && err == nil, err
}

// TaskBulkUpdateByID preserves concurrent terminal observations for any status
// update. Financial transitions should still use the per-task terminal intent
// transaction; this guard prevents a stale compatibility caller from reverting
// an already successful or failed task.
func TaskBulkUpdateByID(ids []int64, params map[string]any) error {
	if len(ids) == 0 {
		return nil
	}
	query := DB.Model(&Task{}).Where("id IN ?", ids)
	if _, changesStatus := params["status"]; changesStatus {
		query = query.Where("status NOT IN ?", []TaskStatus{TaskStatusSuccess, TaskStatusFailure}).
			Where("settlement_pending = ? OR settlement_pending IS NULL", false)
	}
	return query.Updates(params).Error
}

type TaskQuotaUsage struct {
	Mode  string  `json:"mode"`
	Count float64 `json:"count"`
}

// TaskCountAllTasks returns total tasks that match the given query params (admin usage)
func TaskCountAllTasks(queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.UserID != "" {
		query = query.Where("user_id = ?", queryParams.UserID)
	}
	if len(queryParams.UserIDs) != 0 {
		query = query.Where("user_id in (?)", queryParams.UserIDs)
	}
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}

// TaskCountAllUserTask returns total tasks for given user
func TaskCountAllUserTask(userId int, queryParams SyncTaskQueryParams) int64 {
	var total int64
	query := DB.Model(&Task{}).Where("user_id = ?", userId)
	if queryParams.TaskID != "" {
		query = query.Where("task_id = ?", queryParams.TaskID)
	}
	if queryParams.Action != "" {
		query = query.Where("action = ?", queryParams.Action)
	}
	if queryParams.Status != "" {
		query = query.Where("status = ?", queryParams.Status)
	}
	if queryParams.Platform != "" {
		query = query.Where("platform = ?", queryParams.Platform)
	}
	if queryParams.StartTimestamp != 0 {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != 0 {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	_ = query.Count(&total).Error
	return total
}
func (t *Task) ToOpenAIVideo() *dto.OpenAIVideo {
	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = t.TaskID
	openAIVideo.Status = t.Status.ToVideoStatus()
	openAIVideo.Model = t.Properties.OriginModelName
	openAIVideo.SetProgressStr(t.Progress)
	openAIVideo.CreatedAt = t.CreatedAt
	if t.Status == TaskStatusSuccess {
		if t.FinishTime != 0 {
			openAIVideo.CompletedAt = t.FinishTime
		} else {
			openAIVideo.CompletedAt = t.UpdatedAt
		}
	}
	return openAIVideo
}
