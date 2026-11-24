package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestIM21ModelWritersFailClosed(t *testing.T) {
	ctx := t.Context()
	var revision int64
	for _, mode := range []string{"", "locked", "ACTIVE", "active ", "unexpected"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(common.IM21RolloutModeEnv, mode)
			_, err := UpdateGroupCatalog(ctx, GroupCatalogSnapshot{Groups: map[string]GroupCatalogEntry{"vip": {Exclusive: true}}}, AuthSessionIdentity{})
			require.ErrorIs(t, err, common.ErrIM21RolloutLocked)
			_, err = UpdateUserGroupEntitlements(ctx, 7, &revision, []string{"vip"}, AuthSessionIdentity{})
			require.ErrorIs(t, err, common.ErrIM21RolloutLocked)
			_, err = SaveUserGroupPricing(ctx, AuthSessionIdentity{}, 7, UserGroupPricingUpdate{Group: "vip"})
			require.ErrorIs(t, err, common.ErrIM21RolloutLocked)
			_, err = RenameGroups([]GroupRenameMapping{{From: "before", To: "after"}})
			require.ErrorIs(t, err, common.ErrIM21RolloutLocked)
		})
	}
}

func TestIM21PostgresSupplierCompatibilityWrites(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 or 18.7")
	}
	ctx := t.Context()
	truncateTables(t)
	require.NoError(t, DB.AutoMigrate(&PartnerApplication{}, &PartnerApplicationEvent{}, &PartnerApplicationSettings{}))
	users := []User{{Username: "rollout-legacy", AffCode: "rollout-legacy"}, {Username: "rollout-active", AffCode: "rollout-active"}}
	for i := range users {
		users[i].Status, users[i].Role = common.UserStatusEnabled, common.RoleCommonUser
		require.NoError(t, DB.Create(&users[i]).Error)
	}
	t.Cleanup(func() {
		for _, user := range users {
			require.NoError(t, DB.Where("user_id = ?", user.Id).Delete(&PartnerApplicationEvent{}).Error)
			require.NoError(t, DB.Where("user_id = ?", user.Id).Delete(&PartnerApplication{}).Error)
		}
	})
	t.Setenv(common.IM21RolloutModeEnv, "locked")
	legacy := validPartnerApplicationInput()
	legacy.ContactType = "phone"
	legacy.Quotes = nil
	for _, platform := range []string{"openai", "anthropic", "google", "deepseek", "alibaba", "bytedance", "zhipu", "moonshot", "other"} {
		legacy.Quotes = append(legacy.Quotes, PartnerQuote{Platform: platform, Group: platform, Rate: "0.25"})
	}
	saved, err := CreatePartnerApplication(ctx, users[0].Id, "rollout-legacy-submit", legacy)
	require.NoError(t, err)
	require.Equal(t, legacy.Quotes, saved.Quotes)
	replay, err := CreatePartnerApplication(ctx, users[0].Id, "rollout-legacy-submit", legacy)
	require.NoError(t, err)
	require.Equal(t, saved.ID, replay.ID)

	input := validPartnerApplicationInput()
	input.ContactType = "telegram"
	_, err = CreatePartnerApplication(ctx, users[1].Id, "rollout-new-submit", input)
	require.ErrorIs(t, err, common.ErrIM21RolloutLocked)
	input.ContactType = "qq"
	input.Quotes = append(input.Quotes, PartnerQuote{Platform: "zhipu-glm", Group: "another", Rate: "0.25"})
	_, err = CreatePartnerApplication(ctx, users[1].Id, "rollout-new-submit", input)
	require.ErrorIs(t, err, common.ErrIM21RolloutLocked)
	var count int64
	for _, table := range []any{&PartnerApplication{}, &PartnerApplicationEvent{}} {
		require.NoError(t, DB.Model(table).Where("user_id = ?", users[1].Id).Count(&count).Error)
		require.Zero(t, count)
	}

	t.Setenv(common.IM21RolloutModeEnv, "active")
	input.ContactType = "telegram"
	saved, err = CreatePartnerApplication(ctx, users[1].Id, "rollout-new-submit", input)
	require.NoError(t, err)
	require.Equal(t, input.ContactType, saved.ContactType)
	require.Equal(t, input.Quotes, saved.Quotes)
}

func TestIM21GenericOptionsCannotReplaceGroupNamespaceMetadata(t *testing.T) {
	// Capture persisted state instead of assuming another fixture's namespace
	// revision is zero. Neither a direct nor a bundled option write may change it.
	for _, tc := range []struct{ key, value string }{
		{groupRenameAliasesOptionKey, `{"retired":"other"}`},
		{groupConfigVersionOptionKey, "0"},
		{groupCatalogOptionKey, `{"vip":{"exclusive":true}}`},
	} {
		t.Run(tc.key, func(t *testing.T) {
			var before, after []Option
			require.NoError(t, DB.Where(&Option{Key: tc.key}).Find(&before).Error)
			require.Error(t, UpdateOption(tc.key, tc.value))
			require.Error(t, UpdateOptionsBulk(map[string]string{tc.key: tc.value, "RolloutFixtureOption": "must-not-commit"}))
			require.NoError(t, DB.Where(&Option{Key: tc.key}).Find(&after).Error)
			require.Equal(t, before, after)
			var count int64
			require.NoError(t, DB.Model(&Option{}).Where(&Option{Key: "RolloutFixtureOption"}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
