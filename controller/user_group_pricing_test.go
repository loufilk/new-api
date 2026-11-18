package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func performUserGroupPricingRequest(actorID, targetID int, method, body string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, fmt.Sprintf("/api/user/%d/group-pricing", targetID), strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", actorID)
	c.Set("role", common.RoleRootUser) // Authorization must use persisted roles.
	c.Set(common.RequestIdKey, "synthetic-personal-pricing")
	c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(targetID)}}
	handler(c)
	return response
}

func TestUserGroupPricingPostgresAPIContractAndPermissions(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 TEST_POSTGRES_DSN")
	}
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	f := setupInterceptionControllerTest(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, f.db.AutoMigrate(&model.Option{}, &model.UserGroupPricing{}, &model.UserGroupPricingAudit{}, &model.Channel{}, &model.Token{}, &model.Ability{}, &model.SubscriptionPlan{}))
	savedRatios, savedClass, savedUsable := ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupGroupRatio2JSONString(), setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedRatios))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(savedClass))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(savedUsable))
	})
	require.NoError(t, f.db.Create(&[]model.Option{{Key: "GroupRatio", Value: `{"public":0.8,"private":2}`}, {Key: "GroupGroupRatio", Value: `{"default":{"public":0.7}}`}, {Key: "UserUsableGroups", Value: `{"public":"Public"}`}}).Error)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"public":0.8,"private":2}`))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"default":{"public":0.7}}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"public":"Public"}`))
	body := `{"expected_revision":0,"ratios":{"public":0.5}}`
	assert.Equal(t, http.StatusForbidden, performUserGroupPricingRequest(f.user.Id, f.user.Id, http.MethodPut, body, UpdateUserGroupPricing).Code)
	assert.Equal(t, http.StatusForbidden, performUserGroupPricingRequest(f.admin.Id, f.root.Id, http.MethodPut, body, UpdateUserGroupPricing).Code)
	assert.Equal(t, http.StatusForbidden, performUserGroupPricingRequest(f.admin.Id, f.admin.Id, http.MethodGet, "", GetUserGroupPricing).Code)
	require.NoError(t, authz.SetUserPermissions(f.admin.Id, authz.PermissionsMap{authz.ResourceUserGroupPricing: {"read": true, "update": false}}))
	assert.Equal(t, http.StatusForbidden, performUserGroupPricingRequest(f.admin.Id, f.user.Id, http.MethodPut, body, UpdateUserGroupPricing).Code)
	require.NoError(t, authz.SetUserPermissions(f.admin.Id, authz.PermissionsMap{authz.ResourceUserGroupPricing: {"read": true, "update": true}}))
	for _, invalid := range []string{
		`{"ratios":{"public":0.5}}`, `{"expected_revision":0}`, `{"expected_revision":0,"ratios":null}`, `{"expected_revision":0,"ratios":{"public":null}}`,
		`{"expected_revision":0,"ratios":{"public":-1}}`, `{"expected_revision":0,"ratios":{"auto":0.1}}`, `{"expected_revision":0,"ratios":{"missing":0.1}}`,
	} {
		response := performUserGroupPricingRequest(f.admin.Id, f.user.Id, http.MethodPut, invalid, UpdateUserGroupPricing)
		assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
	response := performUserGroupPricingRequest(f.admin.Id, f.user.Id, http.MethodPut, body, UpdateUserGroupPricing)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			model.UserGroupPricing
			Groups []userGroupPricingGroup `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
	require.True(t, result.Success)
	assert.EqualValues(t, 1, result.Data.Revision)
	assert.Equal(t, map[string]float64{"public": 0.5}, result.Data.Ratios)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Len(t, result.Data.Groups, 2)
	assert.Equal(t, userGroupPricingGroup{Name: "private", BaseRatio: 2, EffectiveRatio: 2}, result.Data.Groups[0])
	assert.Equal(t, userGroupPricingGroup{Name: "public", Description: "Public", BaseRatio: 0.7, EffectiveRatio: 0.5, Available: true}, result.Data.Groups[1])
	conflict := performUserGroupPricingRequest(f.admin.Id, f.user.Id, http.MethodPut, body, UpdateUserGroupPricing)
	assert.Equal(t, http.StatusConflict, conflict.Code)
	assert.Contains(t, conflict.Body.String(), "user_group_pricing_conflict")
	response = performUserGroupPricingRequest(f.admin.Id, f.user.Id, http.MethodPut, `{"expected_revision":1,"ratios":{"public":0}}`, UpdateUserGroupPricing)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	response = performUserGroupPricingRequest(f.admin.Id, f.user.Id, http.MethodPut, `{"expected_revision":2,"ratios":{}}`, UpdateUserGroupPricing)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var after model.User
	require.NoError(t, f.db.First(&after, f.user.Id).Error)
	assert.Equal(t, f.user.Group, after.Group)
	assert.Equal(t, f.user.Role, after.Role)
	assert.Equal(t, f.user.Quota, after.Quota)
	var auditCount int64
	require.NoError(t, f.db.Model(&model.UserGroupPricingAudit{}).Where("user_id = ?", f.user.Id).Count(&auditCount).Error)
	assert.EqualValues(t, 3, auditCount)
}
