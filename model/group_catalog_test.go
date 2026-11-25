package model

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeGroupCatalog(t *testing.T) {
	settings, err := normalizeGroupCatalog(map[string]GroupCatalogEntry{
		"clinic": {Label: "  private display  ", Platform: "other", Exclusive: true, SortOrder: -2, Badges: []string{"authentic", "benefit"}},
	})
	require.NoError(t, err)
	assert.Equal(t, "private display", settings["clinic"].Label)
	assert.Equal(t, []string{"authentic", "benefit"}, settings["clinic"].Badges)
	for _, item := range []GroupCatalogEntry{
		{Description: "two\nlines"},
		{Platform: "raw supplier identity"},
		{Icon: "https://operator.invalid/logo"},
		{Icon: "<svg>"},
		{Badges: []string{"authentic", "authentic"}},
		{Badges: []string{"custom-js"}},
	} {
		_, err := normalizeGroupCatalog(map[string]GroupCatalogEntry{"test": item})
		require.ErrorIs(t, err, ErrInvalidGroupCatalog)
	}
	_, err = normalizeGroupCatalog(map[string]GroupCatalogEntry{"default": {Exclusive: true}})
	require.ErrorIs(t, err, ErrInvalidGroupCatalog)
}

func testExclusiveGroupCatalogLifecycle(t *testing.T) {
	setupGroupRenameTest(t)
	previous := groupCatalogRuntime.Load()
	t.Cleanup(func() {
		groupCatalogRuntime.Store(previous)
		_ = DB.Where("1 = 1").Delete(&UserGroupEntitlement{}).Error
	})
	seedGroupRenameOption(t, "GroupRatio", `{"default":1,"client-route":0.6}`)

	root := User{Username: "group-catalog-root", AffCode: "group-catalog-root", Role: common.RoleRootUser, Group: "default", Status: common.UserStatusEnabled, AuthVersion: 1}
	alice := User{Username: "group-catalog-alice", AffCode: "group-catalog-alice", Role: common.RoleCommonUser, Group: "default", Status: common.UserStatusEnabled, AuthVersion: 1}
	bob := User{Username: "group-catalog-bob", AffCode: "group-catalog-bob", Role: common.RoleCommonUser, Group: "default", Status: common.UserStatusEnabled, AuthVersion: 1}
	for _, user := range []*User{&root, &alice, &bob} {
		require.NoError(t, DB.Create(user).Error)
	}
	session := UserSession{SID: "group-catalog-session", UserID: root.Id, Version: 1, UserAuthVersion: 1, Status: UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, DB.Create(&session).Error)
	identity := AuthSessionIdentity{UserID: root.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}

	ctx := context.Background()
	current, err := ReadGroupCatalog(ctx)
	require.NoError(t, err)
	current.Groups["client-route"] = GroupCatalogEntry{Label: "Special routing", Icon: "azure", Platform: "openai", Exclusive: true, Badges: []string{"stable"}}
	snapshot, err := UpdateGroupCatalog(ctx, *current, identity)
	require.NoError(t, err)
	require.True(t, IsExclusiveGroup("client-route"))
	// Old builds strictly decode the original option; the sidecar must be
	// transactionally separate so a normal icon edit cannot disrupt grants.
	var policy, icons Option
	require.NoError(t, DB.Where(&Option{Key: groupCatalogOptionKey}).First(&policy).Error)
	assert.JSONEq(t, `{"client-route":{"label":"Special routing","platform":"openai","exclusive":true,"badges":["stable"]}}`, policy.Value)
	require.NoError(t, DB.Where(&Option{Key: groupCatalogIconsOptionKey}).First(&icons).Error)
	assert.JSONEq(t, `{"client-route":"azure"}`, icons.Value)
	_, err = UpdateGroupCatalog(ctx, *current, identity)
	require.ErrorIs(t, err, ErrGroupConfigChanged)

	projection, err := GetGroupCatalogProjection(ctx, map[string]string{"default": "Default"})
	require.NoError(t, err)
	require.Empty(t, projection, "unrelated viewers cannot receive private group metadata")
	grants, err := UpdateUserGroupEntitlements(ctx, alice.Id, &snapshot.Revision, []string{"client-route"}, identity)
	require.NoError(t, err)
	allowed, err := HasUserExclusiveGroup(ctx, alice.Id, "client-route")
	require.NoError(t, err)
	require.True(t, allowed)
	allowed, err = HasUserExclusiveGroup(ctx, bob.Id, "client-route")
	require.NoError(t, err)
	require.False(t, allowed, "shared legacy account group does not grant access")

	_, err = UpdateGroupCatalog(ctx, GroupCatalogSnapshot{Revision: grants.Revision, Groups: map[string]GroupCatalogEntry{}}, identity)
	require.ErrorIs(t, err, ErrGroupCatalogGrants)
	_, err = RenameGroups([]GroupRenameMapping{{From: "client-route", To: "private-renamed"}})
	require.NoError(t, err)
	require.True(t, IsExclusiveGroup("private-renamed"))
	renamed, err := GetGroupCatalogProjection(ctx, map[string]string{"private-renamed": "Granted"})
	require.NoError(t, err)
	assert.Equal(t, "azure", renamed["private-renamed"].Icon)
	assert.Equal(t, "openai", renamed["private-renamed"].Platform)
	require.NoError(t, DB.Where(&Option{Key: groupCatalogIconsOptionKey}).First(&icons).Error)
	assert.JSONEq(t, `{"private-renamed":"azure"}`, icons.Value)
	require.False(t, IsExclusiveGroup("client-route"))
	allowed, err = HasUserExclusiveGroup(ctx, alice.Id, "private-renamed")
	require.NoError(t, err)
	require.True(t, allowed)

	grants, err = ReadUserGroupEntitlements(ctx, alice.Id)
	require.NoError(t, err)
	_, err = UpdateUserGroupEntitlements(ctx, alice.Id, &grants.Revision, []string{}, identity)
	require.NoError(t, err)
	allowed, err = HasUserExclusiveGroup(ctx, alice.Id, "private-renamed")
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestDisplayIconSidecarCannotGrantOrDisableAccess(t *testing.T) {
	for _, raw := range []string{`{"hidden":"javascript:bad", "phantom":"azure"}`, `{"hidden":{"exclusive":false}}`, `malformed`} {
		result, err := parseGroupCatalog(`{"hidden":{"exclusive":true,"label":"Hidden"}}`, raw)
		require.NoError(t, err)
		assert.Equal(t, map[string]GroupCatalogEntry{"hidden": {Exclusive: true, Label: "Hidden", Badges: []string{}}}, result)
	}
	require.ErrorIs(t, validateOptionValue(groupCatalogIconsOptionKey, `{"fake":"openai"}`), ErrInvalidGroupCatalog)
}
