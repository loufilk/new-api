package model

import (
	"context"
	"errors"
	"math"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const pricingAdminID, pricingAliceID, pricingBobID = 99301, 99302, 99303

func setupUserGroupPricingTest(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN")
	}
	require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL))
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"price-a":0.8,"price-b":2}`)
	seedGroupRenameOption(t, "GroupGroupRatio", `{"member":{"price-a":0.7}}`)
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"member":{"price-a":0.7}}`))
	require.NoError(t, DB.Create(&[]User{
		{Id: pricingAdminID, Username: "pricing-admin", AffCode: "pricing-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled},
		{Id: pricingAliceID, Username: "pricing-alice", AffCode: "pricing-alice", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "member", Quota: 500, QuotaPaid: 500},
		{Id: pricingBobID, Username: "pricing-bob", AffCode: "pricing-bob", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "member", Quota: 500, QuotaPaid: 500},
	}).Error)
}

func TestUserGroupPricingPostgresPrecedenceSnapshotClearAndMigration(t *testing.T) {
	setupUserGroupPricingTest(t)
	ctx := context.Background()
	var before User
	require.NoError(t, DB.First(&before, pricingAliceID).Error)
	// Upgrade an existing user database whose release had no personal-price
	// tables. The new feature is additive, and repeated migration keeps policy.
	require.NoError(t, DB.Migrator().DropTable(&UserGroupPricingAudit{}, &UserGroupPricing{}))
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&UserGroupPricing{}, &UserGroupPricingAudit{}))
	}
	initial, err := GetUserGroupPricing(ctx, pricingAliceID)
	require.NoError(t, err)
	assert.Zero(t, initial.Revision)
	assert.Empty(t, initial.Ratios)
	policy, err := SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-a": 0.6}, RequestID: "personal-create"})
	require.NoError(t, err)
	assert.Equal(t, 0.6, policy.ResolveGroupRatio("member", "price-a").GroupRatio, "personal is absolute, not 0.6 * 0.7 * 0.8")
	assert.Equal(t, 2.0, policy.ResolveGroupRatio("member", "price-b").GroupRatio)
	bob, err := GetUserGroupPricing(ctx, pricingBobID)
	require.NoError(t, err)
	assert.Equal(t, 0.7, bob.ResolveGroupRatio("member", "price-a").GroupRatio)
	request, _ := gin.CreateTestContext(httptest.NewRecorder())
	request.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	admitted, err := GetRequestUserGroupPricing(request, pricingAliceID)
	require.NoError(t, err)
	zero, err := SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-a": 0}, ExpectedRevision: 1, RequestID: "personal-free"})
	require.NoError(t, err)
	assert.Zero(t, zero.ResolveGroupRatio("member", "price-a").GroupRatio)
	retained, err := GetRequestUserGroupPricing(request, pricingAliceID)
	require.NoError(t, err)
	assert.Same(t, admitted, retained)
	assert.Equal(t, 0.6, retained.ResolveGroupRatio("member", "price-a").GroupRatio)
	newRequest, _ := gin.CreateTestContext(httptest.NewRecorder())
	latest, err := GetRequestUserGroupPricing(newRequest, pricingAliceID)
	require.NoError(t, err)
	assert.EqualValues(t, 2, latest.Revision)
	assert.Zero(t, latest.ResolveGroupRatio("member", "price-a").GroupRatio)
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&UserGroupPricing{}, &UserGroupPricingAudit{}))
	}
	cleared, err := SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{}, ExpectedRevision: 2, RequestID: "personal-clear"})
	require.NoError(t, err)
	assert.EqualValues(t, 3, cleared.Revision)
	assert.Equal(t, 0.7, cleared.ResolveGroupRatio("member", "price-a").GroupRatio)
	var after User
	require.NoError(t, DB.First(&after, pricingAliceID).Error)
	assert.Equal(t, before, after, "pricing changes do not alter identity, permissions, session or balance")
	var audits []UserGroupPricingAudit
	require.NoError(t, DB.Where("user_id = ?", pricingAliceID).Order("revision").Find(&audits).Error)
	require.Len(t, audits, 3)
	assert.Equal(t, `{"price-a":0.6}`, audits[0].RatiosJSON)
	assert.Equal(t, `{"price-a":0}`, audits[2].PreviousRatiosJSON)
	assert.Equal(t, "{}", audits[2].RatiosJSON)
}

func TestUserGroupPricingPostgresConflictingWritesAndAuditRollback(t *testing.T) {
	setupUserGroupPricingTest(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, ratio := range []float64{0.4, 0.5} {
		workers.Go(func() {
			<-start
			_, err := SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-a": ratio}, RequestID: "parallel-save"})
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrUserGroupPricingConflict) {
			conflicts++
		} else {
			require.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, conflicts)
	before, err := GetUserGroupPricing(ctx, pricingAliceID)
	require.NoError(t, err)
	var count int64
	require.NoError(t, DB.Model(&UserGroupPricingAudit{}).Where("user_id = ?", pricingAliceID).Count(&count).Error)
	assert.EqualValues(t, 1, count)
	require.NoError(t, DB.Migrator().DropTable(&UserGroupPricingAudit{}))
	t.Cleanup(func() { require.NoError(t, DB.AutoMigrate(&UserGroupPricingAudit{})) })
	_, err = SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{}, ExpectedRevision: 1, RequestID: "audit-failure"})
	require.Error(t, err)
	after, err := GetUserGroupPricing(ctx, pricingAliceID)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestUserGroupPricingPostgresValidationAndRoleIsolation(t *testing.T) {
	setupUserGroupPricingTest(t)
	for name, ratios := range map[string]map[string]float64{
		"missing": nil, "auto": {"auto": 0.5}, "unknown": {"absent": 0.5}, "negative": {"price-a": -1}, "nan": {"price-a": math.NaN()}, "infinity": {"price-a": math.Inf(1)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := SaveUserGroupPricing(context.Background(), pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: ratios, RequestID: "invalid-pricing"})
			require.ErrorIs(t, err, ErrUserGroupPricingInvalid)
		})
	}
	_, err := SaveUserGroupPricing(context.Background(), pricingAliceID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-a": 0}, RequestID: "self-pricing"})
	require.ErrorIs(t, err, ErrUserGroupPricingPermission)
	_, err = SaveUserGroupPricing(context.Background(), pricingAdminID, pricingAdminID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-a": 0}, RequestID: "peer-pricing"})
	require.ErrorIs(t, err, ErrUserGroupPricingPermission)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", pricingAdminID).Update("status", common.UserStatusDisabled).Error)
	_, err = SaveUserGroupPricing(context.Background(), pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-a": 0}, RequestID: "disabled-pricing"})
	require.ErrorIs(t, err, ErrUserGroupPricingPermission)
}

func TestUserGroupPricingPostgresGroupRenameAndRemoval(t *testing.T) {
	setupUserGroupPricingTest(t)
	ctx := context.Background()
	_, err := SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-b": 1.5}, RequestID: "before-rename"})
	require.NoError(t, err)
	request, _ := gin.CreateTestContext(httptest.NewRecorder())
	frozen, err := GetRequestUserGroupPricing(request, pricingAliceID)
	require.NoError(t, err)
	_, err = ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{"GroupRatio": `{"price-a":0.8}`}})
	require.ErrorIs(t, err, ErrGroupRenameReferencedGroup)
	_, err = ApplyGroupRename(GroupRenameApplyRequest{Mappings: []GroupRenameMapping{{From: "price-b", To: "price-renamed"}}, ActorID: pricingAdminID})
	require.NoError(t, err)
	current, err := GetUserGroupPricing(ctx, pricingAliceID)
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"price-renamed": 1.5}, current.Ratios)
	assert.EqualValues(t, 2, current.Revision)
	assert.Equal(t, map[string]float64{"price-b": 1.5}, frozen.Ratios)
	_, err = SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{Ratios: map[string]float64{"price-b": 0.5}, ExpectedRevision: 2, RequestID: "retired-group"})
	require.ErrorIs(t, err, ErrUserGroupPricingInvalid)
}

func TestUserGroupPricingPostgresDeletedOwnersDoNotBlockGroupLifecycle(t *testing.T) {
	for _, ownerState := range []string{"enabled", "disabled", "soft_deleted", "hard_deleted"} {
		t.Run(ownerState, func(t *testing.T) {
			setupUserGroupPricingTest(t)
			ctx := context.Background()
			original, err := SaveUserGroupPricing(ctx, pricingAdminID, pricingAliceID, UserGroupPricingUpdate{
				Ratios: map[string]float64{"price-a": 0.6, "price-b": 1.5}, RequestID: "owner-lifecycle",
			})
			require.NoError(t, err)
			var originalAudits []UserGroupPricingAudit
			require.NoError(t, DB.Where("user_id = ?", pricingAliceID).Order("id").Find(&originalAudits).Error)
			require.Len(t, originalAudits, 1)

			switch ownerState {
			case "disabled":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", pricingAliceID).Update("status", common.UserStatusDisabled).Error)
			case "soft_deleted":
				require.NoError(t, DB.Delete(&User{Id: pricingAliceID}).Error)
			case "hard_deleted":
				require.NoError(t, DB.Unscoped().Delete(&User{Id: pricingAliceID}).Error)
			}
			_, err = ApplyGroupRename(GroupRenameApplyRequest{
				Mappings: []GroupRenameMapping{{From: "price-a", To: "price-renamed"}}, ActorID: pricingAdminID,
			})
			require.NoError(t, err)
			_, err = ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{"GroupRatio": `{"price-renamed":0.8}`}})
			ownerExists := ownerState == "enabled" || ownerState == "disabled"
			if ownerExists {
				require.ErrorIs(t, err, ErrGroupRenameReferencedGroup, "existing owners retain their group reference, including disabled users")
			} else {
				require.NoError(t, err, "deleted owners' historical policies must not prevent group removal")
			}

			current, err := GetUserGroupPricing(ctx, pricingAliceID)
			require.NoError(t, err)
			var currentAudits []UserGroupPricingAudit
			require.NoError(t, DB.Where("user_id = ?", pricingAliceID).Order("id").Find(&currentAudits).Error)
			if ownerExists {
				assert.Equal(t, map[string]float64{"price-renamed": 0.6, "price-b": 1.5}, current.Ratios)
				assert.EqualValues(t, 2, current.Revision)
				require.Len(t, currentAudits, 2)
				assert.Equal(t, originalAudits[0], currentAudits[0])
			} else {
				assert.Equal(t, original, current, "deleted owners' policy history is not renamed or cleared")
				assert.Equal(t, originalAudits, currentAudits, "deleted owners' audit history is unchanged")
			}
		})
	}
}
