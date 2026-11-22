package model

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestParsePersonalGroupRatio(t *testing.T) {
	for raw, expected := range map[string]float64{"0": 0, "0.00000001": 0.00000001, "0.85": 0.85, "10000": 10000} {
		value, err := ParsePersonalGroupRatio(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, expected, value, raw)
	}
	for _, raw := range []string{"", "-0", "-1", "NaN", "1e4", " 1", ".", "1.", ".1", "10000.01", "0.123456789", "1.2.3"} {
		_, err := ParsePersonalGroupRatio(raw)
		require.ErrorIs(t, err, ErrUserGroupPricingInvalid, raw)
	}
}

func TestUserGroupPricingPostgresQuoteIsolationAndRevision(t *testing.T) {
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 or 18.7")
	}
	ctx := context.Background()
	setupGroupRenameTest(t)
	t.Setenv(UserGroupPricingEnabledEnv, "true")
	// The existing test schema is an upgrade fixture. Repeated migration must
	// leave the existing users and the personal-pricing key intact.
	root := User{Username: "pricing-root-test", AffCode: "pricing-root-test", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	first := User{Username: "pricing-a-test", AffCode: "pricing-a-test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "tier"}
	second := User{Username: "pricing-b-test", AffCode: "pricing-b-test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "tier"}
	for _, user := range []*User{&root, &first, &second} {
		require.NoError(t, DB.Create(user).Error)
	}
	session := UserSession{SID: "pricing-active-root", UserID: root.Id, Version: 1, UserAuthVersion: 1,
		Status: UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64), LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, DB.Create(&session).Error)
	identity := AuthSessionIdentity{UserID: root.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}
	for range 2 {
		require.NoError(t, DB.AutoMigrate(&UserGroupPricing{}, &UserGroupPricingAudit{}))
	}
	t.Cleanup(func() {
		require.NoError(t, DB.Where("user_id IN ?", []int{first.Id, second.Id}).Delete(&UserGroupPricingAudit{}).Error)
		require.NoError(t, DB.Where("user_id IN ?", []int{first.Id, second.Id}).Delete(&UserGroupPricing{}).Error)
	})
	seedGroupRenameOption(t, "GroupRatio", `{"g":1.2,"h":1.4}`)
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"tier":{"g":0.8}}`))
	zero := 0.0
	half := 0.5
	update := UserGroupPricingUpdate{Group: "g", Ratio: &zero, RequestID: "pricing-zero"}
	created, err := SaveUserGroupPricing(ctx, identity, first.Id, update)
	require.NoError(t, err)
	require.EqualValues(t, 1, created.Revision)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/synthetic-pricing", nil)
	admitted, err := GetRequestUserGroupPricing(c, first.Id, first.Group)
	require.NoError(t, err)
	quote := admitted.ResolveGroupRatio("tier", "g")
	require.NotNil(t, quote.UserGroupPriceOverride)
	assert.Zero(t, quote.GroupRatio)
	assert.Equal(t, 0.8, quote.InheritedGroupRatio)
	assert.Equal(t, "personal", quote.RateSource)
	other, err := LoadEffectiveUserGroupPricing(ctx, second.Id)
	require.NoError(t, err)
	assert.Equal(t, 0.8, other.ResolveGroupRatio("tier", "g").GroupRatio)
	assert.Equal(t, 1.4, admitted.ResolveGroupRatio("tier", "h").GroupRatio)

	update.Ratio = &half
	_, err = SaveUserGroupPricing(ctx, identity, first.Id, update)
	require.ErrorIs(t, err, ErrUserGroupPricingConflict)
	update.ExpectedRevision = created.Revision
	changed, err := SaveUserGroupPricing(ctx, identity, first.Id, update)
	require.NoError(t, err)
	require.EqualValues(t, 2, changed.Revision)
	stillAdmitted, err := GetRequestUserGroupPricing(c, first.Id, first.Group)
	require.NoError(t, err)
	assert.Zero(t, stillAdmitted.ResolveGroupRatio("tier", "g").GroupRatio)
	_, err = GetRequestUserGroupPricing(c, second.Id, second.Group)
	require.ErrorIs(t, err, ErrUserGroupPricingUnavailable)
	ResetRequestUserGroupPricing(c)
	current, err := GetRequestUserGroupPricing(c, first.Id, first.Group)
	require.NoError(t, err)
	assert.Equal(t, 0.5, current.ResolveGroupRatio("tier", "g").GroupRatio)
	// A shutdown of new pricing admission must not reprice admitted work.
	t.Setenv(UserGroupPricingEnabledEnv, "false")
	disabled, err := LoadEffectiveUserGroupPricing(ctx, first.Id)
	require.NoError(t, err)
	assert.Equal(t, 0.8, disabled.ResolveGroupRatio("tier", "g").GroupRatio)
	assert.Equal(t, 0.5, current.ResolveGroupRatio("tier", "g").GroupRatio)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", root.Id).Update("role", common.RoleAdminUser).Error)
	update.ExpectedRevision = changed.Revision
	update.Ratio = nil
	_, err = SaveUserGroupPricing(ctx, identity, first.Id, update)
	require.ErrorIs(t, err, ErrUserGroupPricingPermission)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", root.Id).Update("role", common.RoleRootUser).Error)

	// The actor still has Root and the request carries the same identity that
	// previously succeeded. Revocation during request preparation must stop
	// both the rule update and the audit inside the write transaction.
	require.NoError(t, DB.Model(&UserSession{}).Where("sid = ?", session.SID).
		Updates(map[string]any{"status": UserSessionStatusRevoked, "revoked_at": time.Now().Unix()}).Error)
	_, err = SaveUserGroupPricing(ctx, identity, first.Id, update)
	require.ErrorIs(t, err, ErrUserGroupPricingPermission)
	// Retaining a valid token/role is also not a bypass after its write scope
	// was removed between request admission and the database transaction.
	pat := createTestUserAccessToken(t, root.Id, "personal-pricing-synthetic-access", time.Now().Add(time.Hour).Unix())
	_, err = UpdateUserAccessToken(root.Id, pat.Id, "synthetic pricing write", []string{"option:write"})
	require.NoError(t, err)
	patIdentity := AuthSessionIdentity{
		UserID: root.Id, SessionID: AccessTokenSessionID(pat.Id), UserAuthVersion: 1, SessionVersion: AccessTokenSessionVersion,
	}
	_, err = UpdateUserAccessToken(root.Id, pat.Id, "synthetic pricing read only", []string{"option:read"})
	require.NoError(t, err)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ValidateAuthSessionWithTx(tx, patIdentity) }))
	_, err = SaveUserGroupPricing(ctx, patIdentity, first.Id, update)
	require.ErrorIs(t, err, ErrUserGroupPricingPermission)
	untouched, err := GetUserGroupPricing(ctx, first.Id)
	require.NoError(t, err)
	assert.Equal(t, changed.Revision, untouched.Revision)
	assert.Equal(t, map[string]float64{"g": .5}, untouched.Ratios)
	var auditCount int64
	require.NoError(t, DB.Model(&UserGroupPricingAudit{}).Where("user_id = ?", first.Id).Count(&auditCount).Error)
	assert.EqualValues(t, 2, auditCount)
	// Use a separate current session for the remaining legitimate action.
	session.SID, session.Status, session.RevokedAt = "pricing-next-root", UserSessionStatusActive, 0
	require.NoError(t, DB.Create(&session).Error)
	identity.SessionID = session.SID
	referenced, err := userGroupPricingReferencesGroup(DB, "g")
	require.NoError(t, err)
	require.True(t, referenced)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return renameUserGroupPricingTx(tx, []GroupRenameMapping{{From: "g", To: "new-g"}}, root.Id)
	}))
	renamed, err := GetUserGroupPricing(ctx, first.Id)
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"new-g": .5}, renamed.Ratios)
	update.Group = "new-g"
	update.ExpectedRevision = renamed.Revision
	removed, err := SaveUserGroupPricing(ctx, identity, first.Id, update)
	require.NoError(t, err)
	assert.Empty(t, removed.Ratios)
	require.NoError(t, DB.Model(&UserGroupPricingAudit{}).Where("user_id = ?", first.Id).Count(&auditCount).Error)
	assert.EqualValues(t, 4, auditCount)
}
