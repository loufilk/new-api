//go:build enterprise_lane_lab

package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// P3's persisted columns without the P4 measured usage/outbox columns.
type enterpriseLaneP3RequestFixture struct {
	RequestID        string `gorm:"type:varchar(64);primaryKey"`
	LaneID           string `gorm:"type:varchar(64);not null"`
	Epoch            int64  `gorm:"not null"`
	UserID           int    `gorm:"not null"`
	TokenID          int    `gorm:"not null"`
	TokenFingerprint string `gorm:"type:varchar(64);not null"`
	Held             int    `gorm:"type:bigint;not null"`
	Actual           int    `gorm:"type:bigint;not null"`
	State            string `gorm:"type:varchar(24);not null"`
	RefundProof      string `gorm:"type:varchar(32);not null"`
	CreatedAt        int64  `gorm:"autoCreateTime"`
	UpdatedAt        int64  `gorm:"autoUpdateTime"`
}

func (enterpriseLaneP3RequestFixture) TableName() string { return "enterprise_lane_requests" }

func TestEnterpriseLaneMeasuredSchemaUpgradePostgres(t *testing.T) {
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable loopback PostgreSQL TEST_MODEL_POSTGRES_DSN")
	}
	tx := DB.WithContext(t.Context()).Begin()
	require.NoError(t, tx.Error)
	defer func() { require.NoError(t, tx.Rollback().Error) }()
	schema := "enterprise_usage_upgrade_" + common.GetUUID()[:8]
	require.NoError(t, tx.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	require.NoError(t, tx.Exec(fmt.Sprintf("SET LOCAL search_path TO %q", schema)).Error)
	require.NoError(t, tx.AutoMigrate(&enterpriseLaneP3RequestFixture{}))
	identity := EnterpriseLaneIdentity{LaneID: "p3-lane", Epoch: 1, UserID: 50, TokenID: 51, TokenFingerprint: BillingTokenFingerprint("synthetic")}
	before := enterpriseLaneP3RequestFixture{RequestID: "p3-retained-owner", Held: 100, Actual: 25, State: EnterpriseLaneSettled,
		LaneID: identity.LaneID, Epoch: identity.Epoch, UserID: identity.UserID, TokenID: identity.TokenID, TokenFingerprint: identity.TokenFingerprint}
	require.NoError(t, tx.Create(&before).Error)
	require.False(t, tx.Migrator().HasIndex(&EnterpriseLaneRequest{}, "idx_enterprise_lane_requests_lane_epoch"))
	for range 2 {
		require.NoError(t, tx.AutoMigrate(EnterpriseLaneModels()...))
		var after EnterpriseLaneRequest
		require.NoError(t, tx.First(&after, "request_id = ?", before.RequestID).Error)
		assert.Equal(t, identity, after.EnterpriseLaneIdentity)
		assert.Equal(t, before.State, after.State)
		assert.Equal(t, before.Held, after.Held)
		assert.Equal(t, before.Actual, after.Actual)
		assert.Zero(t, after.Usage)
		assert.False(t, after.UsageProjectionPending, "historical amount-only owners never become reportable usage")
		var indexDef string
		require.NoError(t, tx.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = ?", "idx_enterprise_lane_usage_pending").Scan(&indexDef).Error)
		assert.Contains(t, indexDef, "WHERE usage_projection_pending")
	}
	var indexDef string
	require.NoError(t, tx.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = current_schema() AND indexname = ?", "idx_enterprise_lane_requests_lane_epoch").Scan(&indexDef).Error)
	require.Contains(t, indexDef, "USING btree (lane_id, epoch)")
	// One retained fixture row naturally favors a sequential scan. Confirm
	// the exact two-column lookup can use the index by disabling that choice
	// only inside this disposable migration transaction.
	require.NoError(t, tx.Exec("SET LOCAL enable_seqscan = off").Error)
	var plan []string
	require.NoError(t, tx.Raw(`EXPLAIN (COSTS OFF) SELECT COALESCE(SUM(actual), 0) FROM enterprise_lane_requests WHERE lane_id = ? AND epoch = ?`, identity.LaneID, identity.Epoch).Scan(&plan).Error)
	require.Contains(t, fmt.Sprint(plan), "idx_enterprise_lane_requests_lane_epoch")
	t.Logf("cold drain receipt lookup: %v", plan)
}

func TestEnterpriseLaneMeasuredUsagePostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	ctx := t.Context()
	_, err := CreateEnterpriseLane(ctx, DB, id, 200, 180)
	require.NoError(t, err)
	rid := id.LaneID + "-usage"
	_, err = ReserveEnterpriseLane(ctx, DB, id, rid, 50)
	require.NoError(t, err)
	usage := EnterpriseLaneUsage{ChannelID: 17, PromptTokens: 80, CompletionTokens: 20, Model: "lab-native-sse", Group: "default", ObservedAt: common.GetTimestamp()}
	_, err = SettleEnterpriseLaneWithUsage(ctx, DB, id, rid, 25, usage)
	require.ErrorIs(t, err, ErrEnterpriseLaneState, "a quote/hold does not prove provider usage")
	_, err = ResumeEnterpriseLaneObservedUsage(ctx, DB, id, rid)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
	require.NoError(t, err)
	for _, test := range []struct {
		name   string
		mutate func(*EnterpriseLaneUsage)
	}{
		{"unknown_channel", func(u *EnterpriseLaneUsage) { u.ChannelID = 0 }},
		{"negative_tokens", func(u *EnterpriseLaneUsage) { u.CompletionTokens = -1 }},
		{"integer_sum_out_of_range", func(u *EnterpriseLaneUsage) { u.PromptTokens = common.MaxQuota }},
		{"empty_usage", func(u *EnterpriseLaneUsage) { u.PromptTokens, u.CompletionTokens = 0, 0 }},
		{"model_control_character", func(u *EnterpriseLaneUsage) { u.Model = "lab\nrow" }},
		{"missing_observed_time", func(u *EnterpriseLaneUsage) { u.ObservedAt = 0 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := usage
			test.mutate(&invalid)
			_, err := SettleEnterpriseLaneWithUsage(t.Context(), DB, id, rid, 25, invalid)
			require.ErrorIs(t, err, ErrEnterpriseLaneUsage)
		})
	}
	var committed EnterpriseLaneRequest
	require.NoError(t, DB.First(&committed, "request_id = ?", rid).Error)
	assert.Equal(t, EnterpriseLaneDispatch, committed.State)
	assert.Zero(t, committed.Usage)
	assert.False(t, committed.UsageProjectionPending)

	const hook = "lab_terminal_balance_rollback_after_measured_freeze"
	commitFailed := errors.New("synthetic terminal lane receipt write failure")
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		updates, ok := tx.Statement.Dest.(map[string]any)
		if tx.Statement.Table == "enterprise_lane_requests" && ok && updates["state"] == EnterpriseLaneSettled {
			tx.AddError(commitFailed)
		}
	}))
	_, err = SettleEnterpriseLaneWithUsage(ctx, DB, id, rid, 25, usage)
	require.NoError(t, DB.Callback().Update().Remove(hook))
	require.ErrorIs(t, err, commitFailed)
	require.NoError(t, DB.First(&committed, "request_id = ?", rid).Error)
	assert.Equal(t, EnterpriseLaneObserved, committed.State)
	assert.Equal(t, usage, committed.Usage)
	assert.Equal(t, 25, committed.Actual)
	assert.False(t, committed.UsageProjectionPending, "observed or rolled back money operations cannot project")
	lane := readLabLane(t, id)
	assert.Equal(t, 50, lane.Held)
	assert.Zero(t, lane.Spent)

	_, err = SettleEnterpriseLane(ctx, DB, id, rid, 25)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity, "amount-only retry cannot reinterpret a measured owner")
	other := usage
	other.ChannelID++
	_, err = SettleEnterpriseLaneWithUsage(ctx, DB, id, rid, 25, other)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
	_, err = SettleEnterpriseLaneWithUsage(ctx, DB, id, rid, 24, usage)
	require.ErrorIs(t, err, ErrEnterpriseLaneIdentity)
	_, err = RefundEnterpriseLane(ctx, DB, id, rid)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)

	sqlDB, err := DB.DB()
	require.NoError(t, err)
	lost := DB.Session(&gorm.Session{NewDB: true, Context: ctx})
	lost.Statement.ConnPool = loseQuotaCommitAck{sqlDB}
	_, err = ResumeEnterpriseLaneObservedUsage(ctx, lost, id, rid)
	require.ErrorContains(t, err, "synthetic lost acknowledgment")
	settled, err := ResumeEnterpriseLaneObservedUsage(ctx, DB, id, rid)
	require.NoError(t, err)
	assert.Equal(t, EnterpriseLaneSettled, settled.State)
	assert.Equal(t, usage, settled.Usage)
	assert.Equal(t, 25, settled.Actual)
	assert.True(t, settled.UsageProjectionPending)
	replayed, err := SettleEnterpriseLaneWithUsage(ctx, DB, id, rid, 25, usage)
	require.NoError(t, err)
	assert.Equal(t, settled, replayed)
	lane = readLabLane(t, id)
	assert.Zero(t, lane.Held)
	assert.Equal(t, 25, lane.Spent)
	assert.Equal(t, 175, lane.PaidAvailable)
	assert.Equal(t, 155, lane.TokenAvailable)
	checkLaneBaseAccounts(t, id, 800, 820, 100)
}

func TestEnterpriseLaneMeasuredOverCapQuarantinePostgres(t *testing.T) {
	id := enterpriseLaneFixture(t)
	ctx := t.Context()
	_, err := CreateEnterpriseLane(ctx, DB, id, 200, 200)
	require.NoError(t, err)
	rid := id.LaneID + "-overcap"
	_, err = ReserveEnterpriseLane(ctx, DB, id, rid, 50)
	require.NoError(t, err)
	_, err = MarkEnterpriseLaneDispatched(ctx, DB, id, rid)
	require.NoError(t, err)
	usage := EnterpriseLaneUsage{ChannelID: 17, PromptTokens: 80, CompletionTokens: 20, Model: "lab-native-sse", Group: "default", ObservedAt: common.GetTimestamp()}
	owner, err := SettleEnterpriseLaneWithUsage(ctx, DB, id, rid, 51, usage)
	require.ErrorIs(t, err, ErrEnterpriseLaneExposure)
	assert.Equal(t, EnterpriseLaneReview, owner.State)
	assert.Equal(t, usage, owner.Usage)
	assert.False(t, owner.UsageProjectionPending)
	_, err = ResumeEnterpriseLaneObservedUsage(ctx, DB, id, rid)
	require.ErrorIs(t, err, ErrEnterpriseLaneExposure)
	_, err = MarkEnterpriseLaneDeliveryUnknown(ctx, DB, id, rid)
	require.ErrorIs(t, err, ErrEnterpriseLaneState)
	lane := readLabLane(t, id)
	assert.Equal(t, 50, lane.Held)
	assert.Zero(t, lane.Spent)
}
