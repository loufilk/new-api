package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestGroupRenameRouteRequiresRootAuth proves the root-only authorization on
// POST /api/option/group-rename through the real router and middleware stack:
// unauthenticated callers are rejected, normal users and admins are denied, and
// only root may commit the rename.
func TestGroupRenameRouteRequiresRootAuth(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	previousLimiter := common.GlobalApiRateLimitEnable
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousGroupRatio := ratio_setting.GroupRatio2JSONString()
	previousOptionMap := make(map[string]string)
	common.OptionMapRWMutex.RLock()
	hadOptionMap := common.OptionMap != nil
	for key, value := range common.OptionMap {
		previousOptionMap[key] = value
	}
	common.OptionMapRWMutex.RUnlock()

	db, err := gorm.Open(sqlite.Open("file:group-rename-router?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.GlobalApiRateLimitEnable = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
		common.GlobalApiRateLimitEnable = previousLimiter
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.OptionMapRWMutex.Lock()
		if hadOptionMap {
			common.OptionMap = previousOptionMap
		} else {
			common.OptionMap = nil
		}
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatio))
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(
		&model.Option{}, &model.User{}, &model.Token{}, &model.Channel{},
		&model.Ability{}, &model.SubscriptionPlan{}, &model.UserSubscription{},
		&model.Log{}, &model.AuditLog{}, &model.UserAccessToken{},
	))

	commonToken := model.AccessTokenPrefix + "route-common-token"
	adminToken := model.AccessTokenPrefix + "route-admin-token"
	rootToken := model.AccessTokenPrefix + "route-root-token"
	users := []model.User{
		{Username: "route-common", Password: "hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "old", AffCode: "route-common-aff"},
		{Username: "route-admin", Password: "hash", Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "old", AffCode: "route-admin-aff"},
		{Username: "route-root", Password: "hash", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "old", AffCode: "route-root-aff"},
	}
	for i := range users {
		require.NoError(t, db.Create(&users[i]).Error)
	}
	seedRouteScopedToken(t, db, users[0].Id, commonToken, "profile:read")
	seedRouteScopedToken(t, db, users[1].Id, adminToken, "user:read")
	seedRouteScopedToken(t, db, users[2].Id, rootToken, "option:write")
	rootReadToken := model.AccessTokenPrefix + "route-root-read-token"
	seedRouteScopedToken(t, db, users[2].Id, rootReadToken, "option:read")
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	request := func(token string) *httptest.ResponseRecorder {
		body := strings.NewReader(`{"renames":[{"from":"old","to":"new"}]}`)
		req := httptest.NewRequest(http.MethodPost, "/api/option/group-rename", body)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	require.Equal(t, http.StatusUnauthorized, request("").Code, "unauthenticated caller must be rejected")
	require.Equal(t, http.StatusForbidden, request(commonToken).Code, "normal user must not rename groups")
	require.Equal(t, http.StatusForbidden, request(adminToken).Code, "admin must not rename groups")

	readOnly := request(rootReadToken)
	require.Equal(t, http.StatusForbidden, readOnly.Code)
	assert.Contains(t, readOnly.Body.String(), "ACCESS_TOKEN_SCOPE_DENIED", "root role must not bypass the token grant")

	rootRecorder := request(rootToken)
	require.Equal(t, http.StatusOK, rootRecorder.Code, "root must be allowed to rename groups: %s", rootRecorder.Body.String())

	var storedUser model.User
	require.NoError(t, db.Where("username = ?", "route-common").First(&storedUser).Error)
	assert.Equal(t, "new", storedUser.Group)
	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
	assert.JSONEq(t, `{"new":1}`, option.Value)
}
