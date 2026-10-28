package model

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func captureGroupRenameRuntimeState(t *testing.T) {
	t.Helper()
	originalOptionMap := make(map[string]string)
	common.OptionMapRWMutex.RLock()
	hadOptionMap := common.OptionMap != nil
	for key, value := range common.OptionMap {
		originalOptionMap[key] = value
	}
	common.OptionMapRWMutex.RUnlock()
	originalGroupRatio := ratio_setting.GroupRatio2JSONString()
	originalGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	originalSpecialUsable := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.MarshalJSONString()
	originalTopupRatio := common.TopupGroupRatio2JSONString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalRateLimit := setting.ModelRequestRateLimitGroup2JSONString()
	originalMaxTokenAutoGroups := setting.GetMaxTokenAutoGroups()
	originalDefaultUseAutoGroup := setting.DefaultUseAutoGroup
	originalExemptGroups := setting.SensitiveCheckExemptGroupsToString()
	originalPolicySnapshot := requestPolicySnapshot.Load()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadOptionMap {
			common.OptionMap = originalOptionMap
		} else {
			common.OptionMap = nil
		}
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatio))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalGroupGroupRatio))
		require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(originalSpecialUsable)))
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(originalTopupRatio))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateModelRequestRateLimitGroupByJSONString(originalRateLimit))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(strconv.Itoa(originalMaxTokenAutoGroups)))
		setting.DefaultUseAutoGroup = originalDefaultUseAutoGroup
		setting.LoadSensitiveCheckExemptGroups(originalExemptGroups)
		requestPolicySnapshot.Store(originalPolicySnapshot)
	})
}

func setupGroupRenameTest(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	captureGroupRenameRuntimeState(t)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM options")
	})
}

func seedGroupRenameOption(t *testing.T, key, value string) {
	t.Helper()
	require.NoError(t, DB.Create(&Option{Key: key, Value: value}).Error)
	if key == "GroupRatio" {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(value))
	}
}

func loadGroupRenameOptionJSON(t *testing.T, key string, dest any) {
	t.Helper()
	var option Option
	require.NoError(t, DB.Where(&Option{Key: key}).First(&option).Error)
	require.NoError(t, common.UnmarshalJsonStr(option.Value, dest))
}

func TestGroupRenameMigratesEveryGroupSurface(t *testing.T) {
	setupGroupRenameTest(t)

	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8,"keep":1}`)
	seedGroupRenameOption(t, "TopupGroupRatio", `{"old":1.2,"keep":1}`)
	seedGroupRenameOption(t, "UserUsableGroups", `{"old":"Old group","keep":"Keep"}`)
	seedGroupRenameOption(t, "GroupGroupRatio", `{"old":{"keep":0.5,"old":0.4},"keep":{"old":0.9}}`)
	seedGroupRenameOption(t, "AutoGroups", `["old","keep"]`)
	seedGroupRenameOption(t, "ModelRequestRateLimitGroup", `{"old":[100,50]}`)
	seedGroupRenameOption(t, "group_ratio_setting.group_special_usable_group", `{"old":{"+:keep":"k","-:old":"h","plain":"p"},"keep":{"+:old":"o"}}`)
	seedGroupRenameOption(t, setting.SensitiveCheckExemptGroupsOptionKey, " old \nkeep\nold ")

	channel := Channel{Id: 1, Key: "sk-test", Name: "channel", Status: common.ChannelStatusEnabled, Group: "old,keep", Models: "m1"}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Ability{Group: "old", Model: "m1", ChannelId: 1, Enabled: true}).Error)
	require.NoError(t, DB.Create(&Ability{Group: "keep", Model: "m1", ChannelId: 1, Enabled: true}).Error)

	user := User{Username: "rename-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)

	token := Token{UserId: user.Id, Key: "token-old", Name: "t", Status: common.TokenStatusEnabled, Group: "old", AutoGroups: `["old","keep"]`}
	require.NoError(t, DB.Create(&token).Error)
	unrelatedToken := Token{UserId: user.Id, Key: "token-keep", Name: "t2", Status: common.TokenStatusEnabled, Group: "keep", AutoGroups: `["keep"]`}
	require.NoError(t, DB.Create(&unrelatedToken).Error)

	plan := SubscriptionPlan{Title: "plan", UpgradeGroup: "old", DowngradeGroup: "keep", Enabled: true}
	require.NoError(t, DB.Create(&plan).Error)
	subscription := UserSubscription{UserId: user.Id, PlanId: plan.Id, UpgradeGroup: "old", PrevUserGroup: "old", DowngradeGroup: "keep", Status: "active"}
	require.NoError(t, DB.Create(&subscription).Error)

	logEntry := Log{UserId: user.Id, Type: LogTypeConsume, Content: "consume", Group: "old"}
	require.NoError(t, DB.Create(&logEntry).Error)

	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, []GroupRenameMapping{{From: "old", To: "new"}}, result.Renames)
	assert.Equal(t, 8, result.Options)
	assert.Equal(t, 1, result.Channels)
	assert.Equal(t, int64(1), result.Abilities)
	assert.Equal(t, 1, result.Users)
	assert.Equal(t, 1, result.Tokens)
	assert.Equal(t, int64(1), result.SubscriptionPlans)
	assert.Equal(t, int64(2), result.UserSubscriptions)

	var storedChannel Channel
	require.NoError(t, DB.First(&storedChannel, channel.Id).Error)
	assert.Equal(t, "new,keep", storedChannel.Group)

	abilities := make([]Ability, 0)
	require.NoError(t, DB.Find(&abilities).Error)
	require.Len(t, abilities, 2)
	abilityGroups := make(map[string]bool, len(abilities))
	for _, ability := range abilities {
		abilityGroups[ability.Group] = true
	}
	assert.True(t, abilityGroups["keep"])
	assert.True(t, abilityGroups["new"])

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, "new", storedUser.Group)

	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, "new", storedToken.Group)
	assert.Equal(t, `["new","keep"]`, storedToken.AutoGroups)
	storedToken = Token{}
	require.NoError(t, DB.First(&storedToken, unrelatedToken.Id).Error)
	assert.Equal(t, "keep", storedToken.Group)
	assert.Equal(t, `["keep"]`, storedToken.AutoGroups)

	require.NoError(t, DB.First(&plan, plan.Id).Error)
	assert.Equal(t, "new", plan.UpgradeGroup)
	assert.Equal(t, "keep", plan.DowngradeGroup)
	require.NoError(t, DB.First(&subscription, subscription.Id).Error)
	assert.Equal(t, "new", subscription.UpgradeGroup)
	assert.Equal(t, "new", subscription.PrevUserGroup)
	assert.Equal(t, "keep", subscription.DowngradeGroup)

	var storedLog Log
	require.NoError(t, DB.First(&storedLog, logEntry.Id).Error)
	assert.Equal(t, "old", storedLog.Group)

	groupRatio := make(map[string]float64)
	loadGroupRenameOptionJSON(t, "GroupRatio", &groupRatio)
	assert.Equal(t, map[string]float64{"keep": 1, "new": 0.8}, groupRatio)
	topupRatio := make(map[string]float64)
	loadGroupRenameOptionJSON(t, "TopupGroupRatio", &topupRatio)
	assert.Equal(t, map[string]float64{"keep": 1, "new": 1.2}, topupRatio)
	usableGroups := make(map[string]string)
	loadGroupRenameOptionJSON(t, "UserUsableGroups", &usableGroups)
	assert.Equal(t, map[string]string{"keep": "Keep", "new": "Old group"}, usableGroups)
	groupGroupRatio := make(map[string]map[string]float64)
	loadGroupRenameOptionJSON(t, "GroupGroupRatio", &groupGroupRatio)
	assert.Equal(t, map[string]map[string]float64{"keep": {"new": 0.9}, "new": {"keep": 0.5, "new": 0.4}}, groupGroupRatio)
	autoGroups := make([]string, 0)
	loadGroupRenameOptionJSON(t, "AutoGroups", &autoGroups)
	assert.Equal(t, []string{"new", "keep"}, autoGroups)
	rateLimits := make(map[string][2]int)
	loadGroupRenameOptionJSON(t, "ModelRequestRateLimitGroup", &rateLimits)
	assert.Equal(t, map[string][2]int{"new": {100, 50}}, rateLimits)
	specialUsable := make(map[string]map[string]string)
	loadGroupRenameOptionJSON(t, "group_ratio_setting.group_special_usable_group", &specialUsable)
	assert.Equal(t, map[string]map[string]string{
		"keep": {"+:new": "o"},
		"new":  {"+:keep": "k", "-:new": "h", "plain": "p"},
	}, specialUsable)

	assert.True(t, ratio_setting.ContainsGroupRatio("new"))
	assert.False(t, ratio_setting.ContainsGroupRatio("old"))
	assert.Equal(t, 1.2, common.GetTopupGroupRatio("new"))
	assert.Equal(t, "Old group", setting.GetUserUsableGroupsCopy()["new"])
	assert.Contains(t, setting.GetAutoGroups(), "new")
	assertSensitiveGroupPolicy(t, "new\nkeep")
	_, _, found := setting.GetGroupRateLimit("new")
	assert.True(t, found)
	nestedRatio, ok := ratio_setting.GetGroupGroupRatio("new", "keep")
	assert.True(t, ok)
	assert.Equal(t, 0.5, nestedRatio)
	special, ok := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get("new")
	require.True(t, ok)
	assert.Equal(t, "k", special["+:keep"])

	common.OptionMapRWMutex.RLock()
	storedGroupRatio, ok := common.OptionMap["GroupRatio"]
	common.OptionMapRWMutex.RUnlock()
	require.True(t, ok)
	assert.Contains(t, storedGroupRatio, `"new"`)
}

func TestGroupRenameRejectsInvalidMappings(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1,"keep":1}`)
	user := User{Username: "rename-invalid-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)

	longName := strings.Repeat("g", maxGroupNameRuneCount+1)
	tests := []struct {
		name     string
		mappings []GroupRenameMapping
		wantErr  error
	}{
		{"empty request", nil, ErrGroupRenameEmpty},
		{"self mapping", []GroupRenameMapping{{From: "old", To: "old"}}, ErrGroupRenameSelfMapping},
		{"duplicate source", []GroupRenameMapping{{From: "old", To: "new"}, {From: "old", To: "other"}}, ErrGroupRenameDuplicateFrom},
		{"duplicate target", []GroupRenameMapping{{From: "old", To: "new"}, {From: "keep", To: "new"}}, ErrGroupRenameDuplicateTo},
		{"chained rename", []GroupRenameMapping{{From: "old", To: "mid"}, {From: "mid", To: "new"}}, ErrGroupRenameChain},
		{"reserved source", []GroupRenameMapping{{From: "auto", To: "new"}}, ErrGroupRenameReservedName},
		{"reserved target", []GroupRenameMapping{{From: "old", To: "all"}}, ErrGroupRenameReservedName},
		{"invalid target", []GroupRenameMapping{{From: "old", To: "bad,name"}}, ErrGroupRenameInvalidName},
		{"empty target", []GroupRenameMapping{{From: "old", To: ""}}, ErrGroupRenameInvalidName},
		{"overlong target", []GroupRenameMapping{{From: "old", To: longName}}, ErrGroupRenameInvalidName},
		{"unknown source", []GroupRenameMapping{{From: "missing", To: "new"}}, ErrGroupRenameUnknownSource},
		{"target collision", []GroupRenameMapping{{From: "old", To: "keep"}}, ErrGroupRenameCollision},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := RenameGroups(test.mappings)
			require.ErrorIs(t, err, test.wantErr)
			assert.Nil(t, result)

			var storedUser User
			require.NoError(t, DB.First(&storedUser, user.Id).Error)
			assert.Equal(t, "old", storedUser.Group)
			var option Option
			require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
			assert.Equal(t, `{"old":1,"keep":1}`, option.Value)
			assert.True(t, ratio_setting.ContainsGroupRatio("old"))
			assert.False(t, ratio_setting.ContainsGroupRatio("new"))
		})
	}
}

func TestGroupRenameRollsBackAllWritesOnFailure(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	seedGroupRenameOption(t, setting.SensitiveCheckExemptGroupsOptionKey, "old")
	require.NoError(t, updateOptionMap(setting.SensitiveCheckExemptGroupsOptionKey, "old"))
	require.NoError(t, refreshRequestPolicySnapshot())
	user := User{Username: "rename-rollback-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	channel := Channel{Id: 2, Key: "sk-rollback", Name: "channel", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
	require.NoError(t, DB.Create(&channel).Error)
	token := Token{UserId: user.Id, Key: "token-rollback", Name: "t", Status: common.TokenStatusEnabled, Group: "old", AutoGroups: `["old"]`}
	require.NoError(t, DB.Create(&token).Error)

	forcedErr := errors.New("forced rename failure")
	groupRenameTxFailureHook = func() error { return forcedErr }
	t.Cleanup(func() {
		groupRenameTxFailureHook = nil
	})

	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.ErrorIs(t, err, forcedErr)
	assert.Nil(t, result)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, "old", storedUser.Group)
	var storedChannel Channel
	require.NoError(t, DB.First(&storedChannel, channel.Id).Error)
	assert.Equal(t, "old", storedChannel.Group)
	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, "old", storedToken.Group)
	assert.Equal(t, `["old"]`, storedToken.AutoGroups)
	var option Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"old":1}`, option.Value)
	assertSensitiveGroupPolicy(t, "old")
	assert.True(t, ratio_setting.ContainsGroupRatio("old"))
	assert.False(t, ratio_setting.ContainsGroupRatio("new"))
}

func TestGroupRenameDuplicateSubmissionIsRejectedAfterCommit(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	user := User{Username: "rename-duplicate-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "token-duplicate", Name: "t", Status: common.TokenStatusEnabled, AutoGroups: `["old"]`}
	require.NoError(t, DB.Create(&token).Error)

	first, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	require.NotNil(t, first)

	second, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.ErrorIs(t, err, ErrGroupRenameUnknownSource)
	assert.Nil(t, second)

	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, `["new"]`, storedToken.AutoGroups)
	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, "new", storedUser.Group)
	assert.False(t, ratio_setting.ContainsGroupRatio("old"))
}

func TestGroupRenamePreservesExactRatioDecimals(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.12345678901234567,"keep":1.1000000000000001}`)
	seedGroupRenameOption(t, "TopupGroupRatio", `{"old":2.0000000000000004}`)

	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)

	var groupRatio Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&groupRatio).Error)
	assert.Contains(t, groupRatio.Value, `"new":0.12345678901234567`)
	assert.Contains(t, groupRatio.Value, `"keep":1.1000000000000001`)
	var topupRatio Option
	require.NoError(t, DB.Where(&Option{Key: "TopupGroupRatio"}).First(&topupRatio).Error)
	assert.Equal(t, `{"new":2.0000000000000004}`, topupRatio.Value)
}

func TestGroupRenamePreservesAbilityAttributes(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	priority := int64(7)
	tag := "primary"
	require.NoError(t, DB.Create(&Ability{
		Group: "old", Model: "m1", ChannelId: 9, Enabled: true,
		Priority: &priority, Weight: 3, Tag: &tag,
	}).Error)

	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	assert.Equal(t, int64(1), result.Abilities)

	abilities := make([]Ability, 0)
	require.NoError(t, DB.Find(&abilities).Error)
	require.Len(t, abilities, 1)
	assert.Equal(t, "new", abilities[0].Group)
	assert.Equal(t, "m1", abilities[0].Model)
	assert.Equal(t, 9, abilities[0].ChannelId)
	assert.True(t, abilities[0].Enabled)
	require.NotNil(t, abilities[0].Priority)
	assert.Equal(t, int64(7), *abilities[0].Priority)
	assert.Equal(t, uint(3), abilities[0].Weight)
	require.NotNil(t, abilities[0].Tag)
	assert.Equal(t, "primary", *abilities[0].Tag)
}

func TestGroupRenameMigratesOnlyActiveSubscriptions(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	plan := SubscriptionPlan{Title: "plan", UpgradeGroup: "old", DowngradeGroup: "old", Enabled: true}
	require.NoError(t, DB.Create(&plan).Error)
	now := common.GetTimestamp()
	active := UserSubscription{UserId: 1, PlanId: plan.Id, UpgradeGroup: "old", PrevUserGroup: "old", DowngradeGroup: "old", Status: "active", EndTime: now + 3600}
	activeUnreconciled := UserSubscription{UserId: 2, PlanId: plan.Id, UpgradeGroup: "old", PrevUserGroup: "old", DowngradeGroup: "old", Status: "active", EndTime: now - 3600}
	expired := UserSubscription{UserId: 3, PlanId: plan.Id, UpgradeGroup: "old", PrevUserGroup: "old", DowngradeGroup: "old", Status: "expired", EndTime: now - 7200}
	cancelled := UserSubscription{UserId: 4, PlanId: plan.Id, UpgradeGroup: "old", PrevUserGroup: "old", DowngradeGroup: "old", Status: "cancelled", EndTime: now}
	for _, subscription := range []*UserSubscription{&active, &activeUnreconciled, &expired, &cancelled} {
		require.NoError(t, DB.Create(subscription).Error)
	}

	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	assert.Equal(t, int64(2), result.SubscriptionPlans)
	assert.Equal(t, int64(6), result.UserSubscriptions)

	require.NoError(t, DB.First(&plan, plan.Id).Error)
	assert.Equal(t, "new", plan.UpgradeGroup)
	assert.Equal(t, "new", plan.DowngradeGroup)

	checkSubscription := func(id int, wantStatus, wantGroup string) {
		t.Helper()
		var stored UserSubscription
		require.NoError(t, DB.First(&stored, id).Error)
		assert.Equal(t, wantStatus, stored.Status)
		assert.Equal(t, wantGroup, stored.UpgradeGroup)
		assert.Equal(t, wantGroup, stored.PrevUserGroup)
		assert.Equal(t, wantGroup, stored.DowngradeGroup)
	}
	checkSubscription(active.Id, "active", "new")
	checkSubscription(activeUnreconciled.Id, "active", "new")
	checkSubscription(expired.Id, "expired", "old")
	checkSubscription(cancelled.Id, "cancelled", "old")
}

func TestGroupRenameRejectsMalformedAutoGroupsWithoutWrites(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	user := User{Username: "rename-malformed-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "token-malformed", Name: "t", Status: common.TokenStatusEnabled, AutoGroups: `["old"`}
	require.NoError(t, DB.Create(&token).Error)

	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.ErrorIs(t, err, ErrGroupRenameInvalidAutoGroups)
	assert.Nil(t, result)

	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, "old", storedUser.Group)
	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, `["old"`, storedToken.AutoGroups)
	var option Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"old":1}`, option.Value)
}

func TestGroupRenameMigratesEscapedAutoGroupKeys(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	user := User{Username: "rename-escaped-user", Password: "hash", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "token-escaped", Name: "t", Status: common.TokenStatusEnabled, AutoGroups: `["o\u006cd","keep"]`}
	require.NoError(t, DB.Create(&token).Error)

	result, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Tokens)

	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, `["new","keep"]`, storedToken.AutoGroups)
}

func TestGroupRenameCombinedOptionsCommitAtomically(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	seedGroupRenameOption(t, "AutoGroups", `["old"]`)
	user := User{Username: "rename-combined-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Key: "token-combined", Name: "t", Status: common.TokenStatusEnabled, Group: "old", AutoGroups: `["old"]`}
	require.NoError(t, DB.Create(&token).Error)

	result, err := ApplyGroupRename(GroupRenameApplyRequest{
		Mappings: []GroupRenameMapping{{From: "old", To: "new"}},
		Options: map[string]string{
			"GroupRatio":         `{"new":0.5}`,
			"AutoGroups":         `["new"]`,
			"MaxTokenAutoGroups": "7",
		},
		Expected: map[string]string{
			"GroupRatio": `{"old":1}`,
			"AutoGroups": `["old"]`,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 3, result.Options)
	require.Contains(t, result.AppliedOptions, "GroupRatio")
	assert.Equal(t, `{"new":0.5}`, result.AppliedOptions["GroupRatio"])
	assert.Equal(t, `["new"]`, result.AppliedOptions["AutoGroups"])
	assert.Equal(t, "7", result.AppliedOptions["MaxTokenAutoGroups"])

	var groupRatio Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&groupRatio).Error)
	assert.Equal(t, `{"new":0.5}`, groupRatio.Value)
	var maxTokenAutoGroups Option
	require.NoError(t, DB.Where(&Option{Key: "MaxTokenAutoGroups"}).First(&maxTokenAutoGroups).Error)
	assert.Equal(t, "7", maxTokenAutoGroups.Value)
	assert.Equal(t, 7, setting.GetMaxTokenAutoGroups())
	for _, key := range groupRenameOptionKeys {
		var count int64
		require.NoError(t, DB.Model(&Option{}).Where(&Option{Key: key}).Count(&count).Error)
		assert.Equal(t, int64(1), count, "option row %s must be materialized", key)
	}

	var storedToken Token
	require.NoError(t, DB.First(&storedToken, token.Id).Error)
	assert.Equal(t, "new", storedToken.Group)
	assert.Equal(t, `["new"]`, storedToken.AutoGroups)
	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, "new", storedUser.Group)
}

func TestGroupRenameRejectsStaleExpectedOptionValues(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1}`)
	user := User{Username: "rename-stale-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)

	result, err := ApplyGroupRename(GroupRenameApplyRequest{
		Mappings: []GroupRenameMapping{{From: "old", To: "new"}},
		Options:  map[string]string{"GroupRatio": `{"new":1}`},
		Expected: map[string]string{"GroupRatio": `{"old":2}`},
	})
	require.ErrorIs(t, err, ErrGroupRenameStale)
	assert.Nil(t, result)

	var option Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"old":1}`, option.Value)
	var storedUser User
	require.NoError(t, DB.First(&storedUser, user.Id).Error)
	assert.Equal(t, "old", storedUser.Group)
}

func TestGroupRenameRejectsReferencedGroupRemoval(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"cust0m":1,"keep":1}`)
	user := User{Username: "rename-removal-user", Password: "hash", Group: "cust0m", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)

	result, err := ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{
		"GroupRatio": `{"keep":1}`,
	}})
	require.ErrorIs(t, err, ErrGroupRenameReferencedGroup)
	assert.Nil(t, result)

	var option Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"cust0m":1,"keep":1}`, option.Value)
}

func TestGroupRenameRejectsAmbiguousReplacementWithoutMapping(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1,"keep":1}`)

	result, err := ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{
		"GroupRatio": `{"replacement":1,"keep":1}`,
	}})
	require.ErrorIs(t, err, ErrGroupRenameUnmappedChange)
	assert.Nil(t, result)
}

func TestGroupRenameRejectsMappedTargetDroppedByOptionValue(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":1,"keep":1}`)

	result, err := ApplyGroupRename(GroupRenameApplyRequest{
		Mappings: []GroupRenameMapping{{From: "old", To: "new"}},
		Options:  map[string]string{"GroupRatio": `{"keep":1}`},
	})
	require.ErrorIs(t, err, ErrGroupRenameUnmappedChange)
	assert.Nil(t, result)
}

func TestGroupRenameAllowsCoordinatedUnreferencedRemoval(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"default":1,"vip":1,"keep":1}`)
	seedGroupRenameOption(t, "TopupGroupRatio", `{"default":1,"vip":1,"keep":1}`)
	seedGroupRenameOption(t, "UserUsableGroups", `{"default":"Default","vip":"VIP","keep":"Keep"}`)

	// The runtime GroupGroupRatio default also references vip; a safe removal
	// clears every option that names the group in one request.
	result, err := ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{
		"GroupRatio":       `{"default":1,"keep":1}`,
		"TopupGroupRatio":  `{"default":1,"keep":1}`,
		"UserUsableGroups": `{"default":"Default","keep":"Keep"}`,
		"GroupGroupRatio":  `{}`,
	}})
	require.NoError(t, err)
	assert.Equal(t, 4, result.Options)

	var option Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"default":1,"keep":1}`, option.Value)
	var nested Option
	require.NoError(t, DB.Where(&Option{Key: "GroupGroupRatio"}).First(&nested).Error)
	assert.Equal(t, `{}`, nested.Value)
}

func TestUpdateOptionRejectsReferencedGroupRemoval(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"cust0m":1,"keep":1}`)
	user := User{Username: "rename-update-option-user", Password: "hash", Group: "cust0m", Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)

	err := UpdateOption("GroupRatio", `{"keep":1}`)
	require.ErrorIs(t, err, ErrGroupRenameReferencedGroup)

	require.NoError(t, DB.Unscoped().Delete(&user).Error)
	require.NoError(t, UpdateOption("GroupRatio", `{"keep":1}`))
	var option Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"keep":1}`, option.Value)
}

func TestGroupRenameConcurrentDuplicateSubmissionsPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	previousDB, previousLogDB := DB, LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	DB, LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
	})
	require.NoError(t, db.AutoMigrate(&Option{}, &User{}, &Token{}, &Channel{}, &Ability{}, &SubscriptionPlan{}, &UserSubscription{}))

	oldGroup := "group_rename_concurrent_old"
	newGroup := "group_rename_concurrent_new"
	username := "group_rename_concurrent_user"
	db.Where(map[string]any{"key": groupRenameOptionKeys}).Delete(&Option{})
	db.Unscoped().Where(map[string]any{"group": []string{oldGroup, newGroup}}).Delete(&User{})
	db.Unscoped().Where(map[string]any{"group": []string{oldGroup, newGroup}}).Delete(&Token{})
	db.Where(map[string]any{"group": []string{oldGroup, newGroup}}).Delete(&Channel{})
	db.Where(map[string]any{"group": []string{oldGroup, newGroup}}).Delete(&Ability{})
	db.Where("upgrade_group IN ? OR downgrade_group IN ?", []string{oldGroup, newGroup}, []string{oldGroup, newGroup}).Delete(&SubscriptionPlan{})
	db.Where("upgrade_group IN ? OR prev_user_group IN ? OR downgrade_group IN ?", []string{oldGroup, newGroup}, []string{oldGroup, newGroup}, []string{oldGroup, newGroup}).Delete(&UserSubscription{})
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})
	captureGroupRenameRuntimeState(t)

	seedGroupRenameOption(t, "GroupRatio", fmt.Sprintf(`{"%s":1}`, oldGroup))
	user := User{Username: username, Password: "hash", Group: oldGroup, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	channel := Channel{Key: "sk-concurrent", Name: "channel", Status: common.ChannelStatusEnabled, Group: oldGroup, Models: "m1"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&Ability{Group: oldGroup, Model: "m1", ChannelId: channel.Id, Enabled: true}).Error)
	require.NoError(t, db.Create(&Token{UserId: user.Id, Key: "token-concurrent", Name: "t", Status: common.TokenStatusEnabled, Group: oldGroup, AutoGroups: fmt.Sprintf(`["%s"]`, oldGroup)}).Error)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range 2 {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, errs[index] = RenameGroups([]GroupRenameMapping{{From: oldGroup, To: newGroup}})
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		require.ErrorIs(t, err, ErrGroupRenameUnknownSource)
	}
	require.Equal(t, 1, successes, "exactly one conflicting rename must commit")

	var storedUser User
	require.NoError(t, db.Where("username = ?", username).First(&storedUser).Error)
	var storedChannel Channel
	require.NoError(t, db.Where("id = ?", channel.Id).First(&storedChannel).Error)
	var storedToken Token
	require.NoError(t, db.Where("key = ?", "token-concurrent").First(&storedToken).Error)
	var storedAbility Ability
	require.NoError(t, db.Where(map[string]any{"group": newGroup}).First(&storedAbility).Error)
	assert.Equal(t, newGroup, storedUser.Group)
	assert.Equal(t, newGroup, storedChannel.Group)
	assert.Equal(t, newGroup, storedToken.Group)
	assert.Equal(t, fmt.Sprintf(`["%s"]`, newGroup), storedToken.AutoGroups)
	assert.Equal(t, newGroup, storedAbility.Group)
	var oldAbilities int64
	require.NoError(t, db.Model(&Ability{}).Where(map[string]any{"group": oldGroup}).Count(&oldAbilities).Error)
	assert.Zero(t, oldAbilities, "no partial references may remain under the old group")
	var storedOption Option
	require.NoError(t, db.Where(&Option{Key: "GroupRatio"}).First(&storedOption).Error)
	assert.Equal(t, fmt.Sprintf(`{"%s":1}`, newGroup), storedOption.Value)
}
