package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGroupRenameRateLimitBucketsPreserveHistoryAndIsolateReusedName(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	assert.Equal(t, "old", GroupRateLimitBucket("old"))
	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	assert.Equal(t, "old", GroupRateLimitBucket("new"), "retain pre-rename memory/Redis counter key")
	oldReusedBucket := GroupRateLimitBucket("old")
	assert.NotEqual(t, "old", oldReusedBucket)
	assert.Contains(t, oldReusedBucket, "\x00")
	loadOptionsFromDatabase()
	assert.Equal(t, "old", GroupRateLimitBucket("new"))
	require.NoError(t, UpdateOption("GroupRatio", `{"new":1,"old":1}`))
	assert.Equal(t, oldReusedBucket, GroupRateLimitBucket("old"))
	_, err = RenameGroups([]GroupRenameMapping{{From: "new", To: "final"}})
	require.NoError(t, err)
	assert.Equal(t, "old", GroupRateLimitBucket("final"))
	assert.Equal(t, oldReusedBucket, GroupRateLimitBucket("old"))
	assert.NotEqual(t, "old", GroupRateLimitBucket("new"))
	require.Error(t, UpdateOption(groupRateLimitBucketsOptionKey, `{}`))
	require.Error(t, UpdateOptionsBulk(map[string]string{groupRateLimitBucketsOptionKey: `{}`}))
}

func TestGroupRenameRateLimitBucketsRollbackAndCorruptionFailClosed(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	oldHook := groupRenameTxFailureHook
	t.Cleanup(func() { groupRenameTxFailureHook = oldHook })
	groupRenameTxFailureHook = func() error { return errors.New("forced bucket rollback") }
	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.Error(t, err)
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", groupRateLimitBucketsOptionKey).Count(&count).Error)
	assert.Zero(t, count)
	assert.Equal(t, "old", GroupRateLimitBucket("old"))
	groupRenameTxFailureHook = nil
	seedGroupRenameOption(t, groupRateLimitBucketsOptionKey, `{"new":""}`)
	_, err = RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.Error(t, err)
	_, err = BeginGroupConfigRead(t.Context())
	require.Error(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", groupRateLimitBucketsOptionKey).Update("value", `{}`).Error)
	guard, err := BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	require.NoError(t, guard.Finish())
}

func TestGroupRenameRateLimitBucketsRejectAmbiguousMetadata(t *testing.T) {
	for _, raw := range []string{`null`, `{"old":""}`, `{"old":"same","new":"same"}`, `{"\u0000bad":"old"}`, `{"new":"\u0000renamed:bad"}`, `{"new":"bad,name"}`} {
		_, err := parseGroupRateLimitBuckets(raw)
		require.Error(t, err, raw)
	}
}
