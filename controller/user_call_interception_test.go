package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type interceptionControllerFixture struct {
	admin, root, user model.User
	db                *gorm.DB
}

func setupInterceptionControllerTest(t *testing.T) interceptionControllerFixture {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL TEST_POSTGRES_DSN")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	previousDB, previousLog := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousRedis := common.IsMasterNode, common.RedisEnabled
	model.DB, model.LOG_DB = db, db
	common.IsMasterNode, common.RedisEnabled = true, false
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{}, &model.CasbinRule{}, &model.AuthzRole{}, &model.AuditLog{}))
	require.NoError(t, authz.Init(db))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.IsMasterNode, common.RedisEnabled = previousMaster, previousRedis
		if previousDB != nil {
			// Other legacy fixtures may leave a closed DB pointer behind. Restore
			// authorization only if the previous database is still live.
			if connection, err := previousDB.DB(); err == nil && connection.Ping() == nil {
				_ = authz.Init(previousDB)
			}
		}
	})
	fixture := interceptionControllerFixture{db: db,
		admin: model.User{Username: "interception-admin", AffCode: "interception-admin", Role: common.RoleAdminUser, Status: common.UserStatusEnabled},
		root:  model.User{Username: "interception-root", AffCode: "interception-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled},
		user:  model.User{Username: "interception-user", AffCode: "interception-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 30, QuotaPaid: 30},
	}
	for _, user := range []*model.User{&fixture.admin, &fixture.root, &fixture.user} {
		require.NoError(t, db.Create(user).Error)
	}
	return fixture
}

func performInterceptionControllerRequest(actorID, targetID int, method, query, body string, handler gin.HandlerFunc) (*httptest.ResponseRecorder, *gin.Context) {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, fmt.Sprintf("/api/user/%d/call-interception%s", targetID, query), strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", actorID)
	c.Set("role", common.RoleRootUser) // Persisted identity must override a forged/stale role context.
	c.Set(common.RequestIdKey, "synthetic-admin-request")
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(targetID)}}
	handler(c)
	return response, c
}

func TestUserCallInterceptionAPIEnforcesAdminPermissionsAndRoleHierarchy(t *testing.T) {
	f := setupInterceptionControllerTest(t)
	body := `{"enabled":true,"reply":"Synthetic reply","expected_revision":0}`
	response, _ := performInterceptionControllerRequest(f.user.Id, f.user.Id, http.MethodPut, "", body, UpdateUserCallInterception)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response, _ = performInterceptionControllerRequest(f.admin.Id, f.root.Id, http.MethodPut, "", body, UpdateUserCallInterception)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response, _ = performInterceptionControllerRequest(f.admin.Id, f.admin.Id, http.MethodGet, "", "", GetUserCallInterception)
	assert.Equal(t, http.StatusForbidden, response.Code)
	require.NoError(t, authz.SetUserPermissions(f.admin.Id, authz.PermissionsMap{authz.ResourceUserCallInterception: {"read": true, "update": false}}))
	response, _ = performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodPut, "", body, UpdateUserCallInterception)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response, _ = performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodGet, "", "", GetUserCallInterception)
	assert.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, authz.SetUserPermissions(f.admin.Id, authz.PermissionsMap{authz.ResourceUserCallInterception: {"read": false, "update": false}}))
	response, _ = performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodGet, "", "", GetUserCallInterceptionHits)
	assert.Equal(t, http.StatusForbidden, response.Code)
	response, _ = performInterceptionControllerRequest(f.root.Id, f.user.Id, http.MethodPut, "", body, UpdateUserCallInterception)
	assert.Equal(t, http.StatusOK, response.Code)
}

func TestUserCallInterceptionAPIValidatesPayloadAndReturnsConflictWithoutOverwrite(t *testing.T) {
	f := setupInterceptionControllerTest(t)
	for _, body := range []string{
		`{}`, `{"enabled":true,"reply":""}`, `{"enabled":true,"reply":" \n\t","expected_revision":0}`,
		`{"enabled":true,"reply":123,"expected_revision":0}`, `{"enabled":true,"reply":"x","expected_revision":-1}`,
		`{"enabled":true,"reply":"x","expected_revision":0} trailing`,
		`{"enabled":true,"reply":"visible\u0000","expected_revision":0}`,
		`{"enabled":false,"reply":"\u0000","expected_revision":0}`,
	} {
		response, _ := performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodPut, "", body, UpdateUserCallInterception)
		assert.Equal(t, http.StatusBadRequest, response.Code, body)
	}
	body := fmt.Sprintf(`{"enabled":true,"reply":"请联系管理员\nPlease contact the administrator.","expected_revision":0,"user_id":%d,"quota":999,"status":2}`, f.root.Id)
	response, context := performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodPut, "", body, UpdateUserCallInterception)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.True(t, common.GetContextKeyBool(context, constant.ContextKeyAuditLogged))
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	config, err := model.GetUserCallInterception(f.user.Id)
	require.NoError(t, err)
	assert.Equal(t, "请联系管理员\nPlease contact the administrator.", config.Reply)
	assert.Equal(t, f.user.Id, config.UserID)
	var stored model.User
	require.NoError(t, f.db.First(&stored, f.user.Id).Error)
	assert.Equal(t, f.user.Quota, stored.Quota)
	assert.Equal(t, f.user.Status, stored.Status)
	rootConfig, err := model.GetUserCallInterception(f.root.Id)
	require.NoError(t, err)
	assert.False(t, rootConfig.Enabled)
	response, _ = performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodPut, "", `{"enabled":false,"reply":"stale overwrite","expected_revision":0}`, UpdateUserCallInterception)
	assert.Equal(t, http.StatusConflict, response.Code)
	unchanged, err := model.GetUserCallInterception(f.user.Id)
	require.NoError(t, err)
	assert.Equal(t, config, unchanged)
}

func TestUserCallInterceptionAPIHidesDatabaseErrorsAndDoesNotClaimFailedSave(t *testing.T) {
	f := setupInterceptionControllerTest(t)
	require.NoError(t, f.db.Migrator().DropTable(&model.UserCallInterceptionAudit{}))
	response, context := performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodPut, "", `{"enabled":true,"reply":"sensitive synthetic reply must not appear in error","expected_revision":0}`, UpdateUserCallInterception)
	assert.Equal(t, http.StatusServiceUnavailable, response.Code)
	assert.NotContains(t, response.Body.String(), "sensitive synthetic reply")
	assert.NotContains(t, response.Body.String(), "INSERT")
	assert.NotContains(t, response.Body.String(), "SQLSTATE")
	assert.False(t, common.GetContextKeyBool(context, constant.ContextKeyAuditLogged))
	config, err := model.GetUserCallInterception(f.user.Id)
	require.NoError(t, err)
	assert.Zero(t, config.Revision)
}

func TestUserCallInterceptionAPIFiltersHitsAndBoundsPagination(t *testing.T) {
	f := setupInterceptionControllerTest(t)
	for _, hit := range []model.UserCallInterceptionHit{
		{Time: 100, UserID: f.user.Id, ActorUserID: f.user.Id, IdentityKind: model.UserCallInterceptionIdentityAPIKey, TokenID: 7, RequestID: "synthetic-request-a", Interface: "POST /v1/messages", Protocol: "claude", Status: 200, Revision: 1},
		{Time: 110, UserID: f.user.Id, ActorUserID: f.user.Id, IdentityKind: model.UserCallInterceptionIdentityAPIKey, TokenID: 8, RequestID: "synthetic-request-b", Interface: "GET /v1/responses", Protocol: "websocket", Status: 403, Revision: 2},
		{Time: 120, UserID: f.user.Id, ActorUserID: f.admin.Id, IdentityKind: model.UserCallInterceptionIdentityDashboardSession, RequestID: "admin-artifact-view", Interface: "GET /api/task/:id/artifacts/:artifact_key/content", Protocol: "artifact", Status: 403, Revision: 2},
	} {
		require.NoError(t, model.RecordUserCallInterceptionHit(&hit))
	}
	response, _ := performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodGet, "?identity_kind=api_key&request_id=synthetic-request-b&token_id=8&status=403&protocol=websocket&start_time=105&end_time=115&page_size=10000&p=-2", "", GetUserCallInterceptionHits)
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Items       []model.UserCallInterceptionHit `json:"items"`
			Total, Page int
			PageSize    int `json:"page_size"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	assert.Equal(t, 1, result.Data.Total)
	assert.Equal(t, 1, result.Data.Page)
	assert.Equal(t, 100, result.Data.PageSize)
	require.Len(t, result.Data.Items, 1)
	assert.Equal(t, "synthetic-request-b", result.Data.Items[0].RequestID)
	response, _ = performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodGet, fmt.Sprintf("?identity_kind=dashboard_session&actor_user_id=%d", f.admin.Id), "", GetUserCallInterceptionHits)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.Len(t, result.Data.Items, 1)
	assert.Equal(t, f.admin.Id, result.Data.Items[0].ActorUserID)
	assert.Equal(t, f.user.Id, result.Data.Items[0].UserID)
	for _, query := range []string{"?actor_user_id=-1", "?identity_kind=unknown", "?identity_kind=" + strings.Repeat("x", 33), "?status=abc", "?token_id=-1", "?start_time=10&end_time=2", "?request_id=" + strings.Repeat("x", 129)} {
		response, _ := performInterceptionControllerRequest(f.admin.Id, f.user.Id, http.MethodGet, query, "", GetUserCallInterceptionHits)
		assert.Equal(t, http.StatusBadRequest, response.Code)
	}
}

func TestUserCallInterceptionCannotBeChangedThroughProfileOrPreferenceMassAssignment(t *testing.T) {
	f := setupInterceptionControllerTest(t)
	before, err := model.SaveUserCallInterception(f.admin.Id, f.user.Id, model.UserCallInterceptionUpdate{Enabled: true, Reply: "Admin-controlled synthetic reply", RequestID: "profile-seed"})
	require.NoError(t, err)
	for _, body := range []string{
		`{"display_name":"Allowed","enabled":false,"reply":"injected","call_interception":{"enabled":false},"call_interception_enabled":false,"role":100,"status":2,"quota":999,"group":"injected"}`,
		`{"sidebar_modules":"{}","enabled":false,"reply":"injected","call_interception_enabled":false}`,
		`{"language":"en","enabled":false,"reply":"injected","call_interception_enabled":false}`,
	} {
		response, _ := performInterceptionControllerRequest(f.user.Id, f.user.Id, http.MethodPut, "", body, UpdateSelf)
		require.Equal(t, http.StatusOK, response.Code)
		assert.Contains(t, response.Body.String(), `"success":true`)
		after, err := model.GetUserCallInterception(f.user.Id)
		require.NoError(t, err)
		assert.Equal(t, before, after)
	}
	var stored model.User
	require.NoError(t, f.db.First(&stored, f.user.Id).Error)
	assert.Equal(t, "Allowed", stored.DisplayName)
	assert.Equal(t, f.user.Role, stored.Role)
	assert.Equal(t, f.user.Status, stored.Status)
	assert.Equal(t, f.user.Quota, stored.Quota)
	assert.Equal(t, f.user.Group, stored.Group)
}
