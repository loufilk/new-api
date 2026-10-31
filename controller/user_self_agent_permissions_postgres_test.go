package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the refresh endpoint itself, not only its shared DTO helper: the
// regression was GetSelf replacing the helper's live profile permissions.
func TestAgentGetSelfPreservesLiveCapabilitiesPostgres(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL acceptance; acceptance runner forbids skips")
	}
	t.Setenv("TEST_SECURITY_DIALECT", "postgres")
	for _, test := range []struct {
		name                  string
		profile               *model.AgentProfile
		rowRole, verifiedRole int
		disabled              bool
		history, sales        bool
		settlement, admin     bool
	}{
		{name: "active common user retains agent capabilities on refresh", profile: &model.AgentProfile{Status: model.AgentProfileActive, Version: 1}, history: true, sales: true, settlement: true},
		{name: "both business freezes preserve history only", profile: &model.AgentProfile{Status: model.AgentProfileActive, Version: 2, SalesFrozen: true, SettlementFrozen: true}, history: true},
		{name: "exited agent retains historical settlement rights", profile: &model.AgentProfile{Status: model.AgentProfileExited, Version: 3}, history: true, settlement: true},
		{name: "ordinary user without profile gets no business rights"},
		{name: "version zero profile cannot grant rights", profile: &model.AgentProfile{Status: model.AgentProfileActive}},
		{name: "disabled user cannot regain rights from profile", profile: &model.AgentProfile{Status: model.AgentProfileActive, Version: 1}, disabled: true},
		{name: "higher database role cannot elevate authenticated common role", rowRole: common.RoleRootUser, verifiedRole: common.RoleCommonUser},
		{name: "stale authenticated root role cannot elevate current common row", rowRole: common.RoleCommonUser, verifiedRole: common.RoleRootUser},
		{name: "matching admin roles retain administrative DTO", rowRole: common.RoleAdminUser, verifiedRole: common.RoleAdminUser, history: true, admin: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			user, identity := setupSecurityEnrollmentTest(t)
			require.NoError(t, model.DB.AutoMigrate(&model.AgentProfile{}, &model.Token{}))
			rowRole, verifiedRole := test.rowRole, test.verifiedRole
			if rowRole == 0 {
				rowRole = common.RoleCommonUser
			}
			if verifiedRole == 0 {
				verifiedRole = common.RoleCommonUser
			}
			status := common.UserStatusEnabled
			if test.disabled {
				status = common.UserStatusDisabled
			}
			require.NoError(t, model.DB.Model(user).Updates(map[string]any{"role": rowRole, "status": status}).Error)
			if test.profile != nil {
				profile := *test.profile
				profile.UserID = user.Id
				require.NoError(t, model.DB.Create(&profile).Error)
			}
			key := model.Token{UserId: user.Id, Key: "synthetic-disabled-key", Name: "historically disabled", Status: common.TokenStatusDisabled, ExpiredTime: -1, RemainQuota: 700}
			require.NoError(t, model.DB.Create(&key).Error)
			pat := model.UserAccessToken{UserId: user.Id, Name: "expired personal access", TokenHash: model.AccessTokenFingerprint("synthetic-expired-pat"), ExpiresAt: time.Now().Unix() - 60}
			require.NoError(t, pat.SetScopes([]string{"profile:read"}))
			require.NoError(t, model.DB.Create(&pat).Error)
			var beforeUser model.User
			require.NoError(t, model.DB.First(&beforeUser, user.Id).Error)

			engine := gin.New()
			engine.GET("/api/user/self", func(c *gin.Context) {
				// Neither UserAuth's role nor the row may elevate permissions
				// when their snapshots differ. Middleware denial
				// itself is covered in the separate browser/session regressions.
				c.Set("id", identity.UserID)
				c.Set("role", verifiedRole)
				GetSelf(c)
			})
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/user/self", nil))
			require.Equal(t, http.StatusOK, response.Code)
			var result struct {
				Success bool `json:"success"`
				Data    struct {
					Permissions map[string]any `json:"permissions"`
				} `json:"data"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			require.True(t, result.Success)
			for name, expected := range map[string]bool{"agent_history": test.history, "agent_sales": test.sales, "agent_settlement": test.settlement, "agent_admin": test.admin, "agent_redemption": test.history} {
				actual, present := result.Data.Permissions[name]
				assert.True(t, present, "refresh DTO must retain explicit %s", name)
				assert.Equal(t, expected, actual, name)
			}
			assert.Contains(t, result.Data.Permissions, "admin_permissions")
			if min(verifiedRole, rowRole) == common.RoleCommonUser {
				adminPermissions, ok := result.Data.Permissions["admin_permissions"].(map[string]any)
				require.True(t, ok)
				for resource, rawActions := range adminPermissions {
					actions, ok := rawActions.(map[string]any)
					require.True(t, ok)
					for action, granted := range actions {
						assert.Equal(t, false, granted, "neither stale role may grant %s:%s", resource, action)
					}
				}
			}
			var afterUser model.User
			var afterKey model.Token
			var afterPAT model.UserAccessToken
			require.NoError(t, model.DB.First(&afterUser, user.Id).Error)
			require.NoError(t, model.DB.First(&afterKey, key.Id).Error)
			require.NoError(t, model.DB.First(&afterPAT, pat.Id).Error)
			assert.Equal(t, beforeUser, afterUser, "refresh is read-only, including all wallets and auth version")
			assert.Equal(t, key, afterKey, "refresh must not revive or mutate a historically disabled model key")
			assert.Equal(t, pat, afterPAT, "refresh must not revive or mutate an expired PAT")
			assert.NotContains(t, response.Body.String(), key.Key)
			assert.NotContains(t, response.Body.String(), pat.TokenHash)
		})
	}
}
