package model

import (
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestGroupRenameOperationReplayConflictRollback(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	request := GroupRenameApplyRequest{RequestID: "synthetic-request-0001", ActorID: 41, ActorRole: common.RoleRootUser, Mappings: []GroupRenameMapping{{From: "old", To: "new"}}}
	oldHook := groupRenameTxFailureHook
	t.Cleanup(func() { groupRenameTxFailureHook = oldHook })
	groupRenameTxFailureHook = func() error { return errors.New("synthetic forced rollback") }
	_, err := ApplyGroupRename(request)
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&GroupRenameOperation{}).Count(&count).Error)
	assert.Zero(t, count)
	groupRenameTxFailureHook = nil
	first, err := ApplyGroupRename(request)
	require.NoError(t, err)
	assert.False(t, first.Replayed)
	second, err := ApplyGroupRename(request)
	require.NoError(t, err)
	assert.True(t, second.Replayed)
	second.Replayed = false
	assert.Equal(t, *first, *second)
	request.ActorID = 42
	_, err = ApplyGroupRename(request)
	require.ErrorIs(t, err, ErrGroupRenameRequestConflict)
	request.ActorID = 41
	request.Mappings[0].To = "third"
	_, err = ApplyGroupRename(request)
	require.ErrorIs(t, err, ErrGroupRenameRequestConflict)
	var receipt GroupRenameOperation
	require.NoError(t, DB.First(&receipt).Error)
	assert.Equal(t, 41, receipt.ActorID)
	assert.Equal(t, common.RoleRootUser, receipt.ActorRole)
	assert.Contains(t, receipt.Result, `"from":"old","to":"new"`)
	require.NoError(t, DB.Model(&GroupRenameOperation{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestGroupRenameOperationConcurrentReplay(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	request := GroupRenameApplyRequest{RequestID: "concurrent-request-0001", Mappings: []GroupRenameMapping{{From: "old", To: "new"}}}
	start := make(chan struct{})
	results := make(chan *GroupRenameResult, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { <-start; r, e := ApplyGroupRename(request); results <- r; errs <- e })
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	replays := 0
	for r := range results {
		if r.Replayed {
			replays++
		}
	}
	assert.Equal(t, 1, replays)
}

func TestGroupRenameBatchPreservesCredentialsMoneyAndHistory(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8,"keep":1}`)
	user := User{Username: "batch-rename", AffCode: "batch-rename", Group: "old", Status: common.UserStatusDisabled, Quota: 600, QuotaPaid: 300, QuotaBonus: 200, QuotaReward: 100, UsedQuota: 77}
	require.NoError(t, DB.Create(&user).Error)
	ips := "127.0.0.1/32"
	tokens := make([]Token, 1003) // More than two 500-row auto-group batches.
	for i := range tokens {
		tokens[i] = Token{UserId: user.Id, Key: fmt.Sprintf("synthetic-key-%04d", i), Group: "old", AutoGroups: `["old","keep"]`, Status: common.TokenStatusDisabled, RemainQuota: 213, UsedQuota: 19, ExpiredTime: 1, ModelLimitsEnabled: true, ModelLimits: "m1", AllowIps: &ips, CrossGroupRetry: true}
	}
	require.NoError(t, DB.CreateInBatches(tokens, 200).Error)
	log := Log{UserId: user.Id, Group: "old", Quota: 17, Type: LogTypeConsume}
	require.NoError(t, DB.Create(&log).Error)
	task := Task{UserId: user.Id, Group: "old", Quota: 17, Status: TaskStatusSuccess, TaskID: "synthetic-history"}
	require.NoError(t, DB.Create(&task).Error)
	quota := QuotaData{UserID: user.Id, UseGroup: "old", Quota: 17, Count: 1}
	require.NoError(t, DB.Create(&quota).Error)
	metric := PerfMetric{ModelName: "m1", Group: "old", BucketTs: 1, RequestCount: 1}
	require.NoError(t, DB.Create(&metric).Error)
	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	assert.Equal(t, len(tokens), result.Tokens)
	var stored []Token
	require.NoError(t, DB.Order("id").Find(&stored).Error)
	require.Len(t, stored, len(tokens))
	for i := range stored {
		expected := tokens[i]
		expected.Group = "new"
		expected.AutoGroups = `["new","keep"]`
		expected.CacheRevision++
		assert.Equal(t, expected, stored[i])
	}
	var current User
	require.NoError(t, DB.First(&current, user.Id).Error)
	assert.Equal(t, "new", current.Group)
	assert.Equal(t, user.Status, current.Status)
	assert.Equal(t, user.Quota, current.Quota)
	assert.Equal(t, user.QuotaPaid, current.QuotaPaid)
	assert.Equal(t, user.QuotaBonus, current.QuotaBonus)
	assert.Equal(t, user.QuotaReward, current.QuotaReward)
	assert.Equal(t, user.UsedQuota, current.UsedQuota)
	var currentLog Log
	require.NoError(t, DB.First(&currentLog, log.Id).Error)
	assert.Equal(t, log, currentLog)
	var currentTask Task
	require.NoError(t, DB.First(&currentTask, task.ID).Error)
	assert.Equal(t, task, currentTask)
	var currentQuota QuotaData
	require.NoError(t, DB.First(&currentQuota, quota.Id).Error)
	assert.Equal(t, quota, currentQuota)
	var currentMetric PerfMetric
	require.NoError(t, DB.First(&currentMetric, metric.Id).Error)
	assert.Equal(t, metric, currentMetric)
	var receipt GroupRenameOperation
	require.NoError(t, DB.First(&receipt).Error)
	assert.NotContains(t, receipt.Result, "synthetic-key")
	assert.NotContains(t, receipt.Result, ips)
}

func TestGroupRenameAliasOptionsStaySynchronized(t *testing.T) {
	for _, aliasOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(aliasOnly), func(t *testing.T) {
			setupGroupRenameTest(t)
			if !aliasOnly {
				seedGroupRenameOption(t, "GroupRatio", `{"old":0.12500000000000001}`)
			}
			seedGroupRenameOption(t, "group_ratio_setting.group_ratio", `{"old":0.12500000000000001}`)
			seedGroupRenameOption(t, "GroupGroupRatio", `{"old":{"old":0.9}}`)
			seedGroupRenameOption(t, "group_ratio_setting.group_group_ratio", `{"old":{"old":0.9}}`)
			_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
			require.NoError(t, err)
			for alias, canonical := range groupOptionAliases {
				var rows []Option
				require.NoError(t, DB.Where(map[string]any{"key": []string{alias, canonical}}).Order("key").Find(&rows).Error)
				require.Len(t, rows, 2)
				assert.JSONEq(t, rows[0].Value, rows[1].Value)
				assert.NotContains(t, rows[0].Value, "old")
			}
			loadOptionsFromDatabase()
			assert.True(t, ratio_setting.ContainsGroupRatio("new"))
			assert.False(t, ratio_setting.ContainsGroupRatio("old"))
			require.NoError(t, UpdateOption("group_ratio_setting.group_ratio", `{"new":0.5}`))
			assert.Equal(t, 0.5, ratio_setting.GetGroupRatio("new"))
			var canonical Option
			require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&canonical).Error)
			assert.JSONEq(t, `{"new":0.5}`, canonical.Value)
		})
	}
}

func TestGroupRenameAliasConflictRejectsWithoutGuessing(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	seedGroupRenameOption(t, "group_ratio_setting.group_ratio", `{"old":0.9}`)
	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.ErrorIs(t, err, ErrGroupRenameInvalidOption)
	require.NoError(t, UpdateOption("GroupRatio", `{"old":0.8}`))
	_, err = RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
}

func TestGroupRenameAbsentAliasCannotResurrectStaleRuntimeSource(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	user := User{Username: "stale-alias-user", AffCode: "stale-alias-user", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	stale := user
	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	// Simulate a peer whose option cache missed the rename, while its namespace
	// writer reads the authoritative new rows. There are no persisted aliases.
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"old":0.8}`))
	require.ErrorIs(t, stale.Update(false), ErrGroupReferenceRenamed)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "new", stored.Group)
}
