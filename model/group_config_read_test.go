package model

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func setGroupGuardOption(t *testing.T, key, value string) {
	t.Helper()
	require.NoError(t, DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&Option{Key: key, Value: value}).Error)
}

func TestGroupConfigReadRefreshesMissedRestrictivePolicy(t *testing.T) {
	setupGroupRenameTest(t)
	// A missed Pub/Sub event previously combined the renamed DB identity with
	// the old special policy, losing the premium deny rule entirely.
	require.NoError(t, updateOptionMap("GroupRatio", `{"old":0.25,"premium":2}`))
	require.NoError(t, updateOptionMap("group_ratio_setting.group_special_usable_group", `{"old":{"-:premium":"denied"}}`))
	setGroupGuardOption(t, "GroupRatio", `{"new":0.25,"premium":2}`)
	setGroupGuardOption(t, "group_ratio_setting.group_special_usable_group", `{"new":{"-:premium":"denied"}}`)
	setGroupGuardOption(t, groupConfigVersionOptionKey, "1")
	guard, err := BeginGroupConfigRead(context.Background())
	require.NoError(t, err)
	defer guard.Close()
	assert.Equal(t, int64(1), guard.Version)
	special, ok := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get("new")
	require.True(t, ok)
	assert.Equal(t, "denied", special["-:premium"])
	assert.False(t, ratio_setting.ContainsGroupRatio("old"))
	assert.Equal(t, 0.25, ratio_setting.GetGroupRatio("new"))
	require.NoError(t, guard.Finish())
}

func TestGroupConfigReadRejectsRemoteCommitAndStaleRouting(t *testing.T) {
	setupGroupRenameTest(t)
	setGroupGuardOption(t, "GroupRatio", `{"old":0.25}`)
	guard, err := BeginGroupConfigRead(context.Background())
	require.NoError(t, err)
	defer guard.Close()
	setGroupGuardOption(t, "GroupRatio", `{"new":0.25}`)
	setGroupGuardOption(t, groupConfigVersionOptionKey, "1")
	require.ErrorIs(t, guard.Finish(), ErrGroupConfigChanged)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/models", nil)
	c.Set(GroupConfigVersionContextKey, int64(0))
	_, err = BeginRequestGroupConfigRead(c)
	require.ErrorIs(t, err, ErrGroupConfigChanged)
	current, err := BeginGroupConfigRead(context.Background())
	require.NoError(t, err)
	require.NoError(t, current.Finish())
	assert.Equal(t, int64(1), groupConfigAppliedVersion.Load())
}

func TestGroupConfigReadFailedRefreshIsRetryableAndNeverAcknowledged(t *testing.T) {
	setupGroupRenameTest(t)
	setGroupGuardOption(t, "GroupRatio", `{"new":0.25}`)
	setGroupGuardOption(t, groupConfigVersionOptionKey, "1")
	const callback = "group_guard_injected_channel_failure"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("synthetic identity cache failure"))
		}
	}))
	t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
	failedRead, err := BeginGroupConfigRead(context.Background())
	if failedRead != nil {
		defer failedRead.Close()
	}
	require.ErrorContains(t, err, "synthetic identity cache failure")
	assert.Equal(t, int64(0), groupConfigAppliedVersion.Load())
	assert.True(t, groupConfigRuntimeInvalid.Load())
	require.NoError(t, DB.Callback().Query().Remove(callback))
	guard, err := BeginGroupConfigRead(context.Background())
	require.NoError(t, err)
	require.NoError(t, guard.Finish())
	assert.Equal(t, int64(1), groupConfigAppliedVersion.Load())
	assert.False(t, groupConfigRuntimeInvalid.Load())
}

func TestGroupConfigReadVersionZeroRejectsAliasConflictAndRecovers(t *testing.T) {
	setupGroupRenameTest(t)
	setGroupGuardOption(t, "GroupRatio", `{"old":1}`)
	setGroupGuardOption(t, "group_ratio_setting.group_ratio", `{"old":9}`)
	loadOptionsFromDatabase()
	assert.True(t, groupConfigRuntimeInvalid.Load())
	_, err := BeginGroupConfigRead(context.Background())
	require.Error(t, err)
	assert.Equal(t, int64(0), groupConfigAppliedVersion.Load())
	setGroupGuardOption(t, "group_ratio_setting.group_ratio", `{"old":1}`)
	guard, err := BeginGroupConfigRead(context.Background())
	require.NoError(t, err)
	require.NoError(t, guard.Finish())
	assert.False(t, groupConfigRuntimeInvalid.Load())
	assert.True(t, groupConfigVersionInitialized.Load())
}

func TestGroupConfigReadWriterCannotExposePartialRuntime(t *testing.T) {
	setupGroupRenameTest(t)
	setGroupGuardOption(t, "GroupRatio", `{"old":0.25}`)
	guard, err := BeginGroupConfigRead(context.Background())
	require.NoError(t, err)
	defer guard.Close()
	setGroupGuardOption(t, "GroupRatio", `{"new":0.25}`)
	setGroupGuardOption(t, groupConfigVersionOptionKey, "1")
	done := make(chan error, 1)
	go func() { done <- applyGroupConfigInvalidation(1, nil, true) }()
	select {
	case err := <-done:
		t.Fatalf("runtime writer crossed active read guard: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	assert.True(t, ratio_setting.ContainsGroupRatio("old"))
	require.ErrorIs(t, guard.Finish(), ErrGroupConfigChanged)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("runtime writer did not resume after short guard released")
	}
	assert.False(t, ratio_setting.ContainsGroupRatio("old"))
	assert.True(t, ratio_setting.ContainsGroupRatio("new"))
}

func TestGroupConfigVersionFencesTokenWritesInsideNamespaceTransaction(t *testing.T) {
	setupGroupRenameTest(t)
	setGroupGuardOption(t, "GroupRatio", `{"new":0.25,"premium":2}`)
	setGroupGuardOption(t, groupConfigVersionOptionKey, "1")
	user := User{Username: "group-version-owner", Password: "synthetic-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "new"}
	require.NoError(t, DB.Create(&user).Error)
	ip := "127.0.0.1"
	token := Token{UserId: user.Id, Key: "synthetic-version-test-key", Name: "original", Group: "premium", Status: common.TokenStatusDisabled, RemainQuota: 456, AllowIps: &ip, ExpiredTime: -1}
	require.ErrorIs(t, token.InsertAtGroupConfigVersion(1, 0), ErrGroupConfigChanged)
	var count int64
	require.NoError(t, DB.Model(&Token{}).Where("user_id = ?", user.Id).Count(&count).Error)
	assert.Zero(t, count)
	require.NoError(t, token.InsertAtGroupConfigVersion(1, 1))
	token.Name = "must-not-persist"
	token.RemainQuota = 789
	require.ErrorIs(t, token.UpdateAtGroupConfigVersion(1, 0), ErrGroupConfigChanged)
	var stored Token
	require.NoError(t, DB.First(&stored, token.Id).Error)
	assert.Equal(t, "original", stored.Name)
	assert.Equal(t, 456, stored.RemainQuota)
	assert.Equal(t, common.TokenStatusDisabled, stored.Status)
	assert.Equal(t, "synthetic-version-test-key", stored.Key)
	assert.Equal(t, ip, *stored.AllowIps)
}
