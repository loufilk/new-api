package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func configureTokenAutoGroupsTest(t *testing.T, maxCount string, autoGroups string) {
	t.Helper()
	originalMax := setting.GetMaxTokenAutoGroups()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups(maxCount))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(autoGroups))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","auto":"Auto"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(stringInt(originalMax)))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func stringInt(value int) string {
	return fmt.Sprintf("%d", value)
}

func setupTokenAutoGroupsControllerTest(t *testing.T) *model.User {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.PerfMetric{}))
	user := &model.User{
		Id:       101,
		Username: "token-auto-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func baseTokenRequest(name string) map[string]any {
	return map[string]any{
		"name":            name,
		"expired_time":    -1,
		"remain_quota":    0,
		"unlimited_quota": true,
		"group":           "default",
	}
}

func newTokenAutoGroupsAuthenticatedContext(t *testing.T, method string, target string, body any, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, method, target, body, userID)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "default")
	return ctx, recorder
}

func TestCreateAndEditRejectAutomaticRouting(t *testing.T) {
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	key := seedToken(t, model.DB, user.Id, "original", "synthetic-single-group")
	for _, tc := range []struct {
		name  string
		extra map[string]any
	}{
		{"virtual auto", map[string]any{"group": "auto"}},
		{"virtual auto with empty list", map[string]any{"group": "auto", "auto_groups": []string{}}},
		{"virtual auto with null list", map[string]any{"group": "auto", "auto_groups": nil}},
		{"manual multi-group", map[string]any{"auto_groups": []string{"vip", "default"}}},
		{"manual auto", map[string]any{"auto_groups_mode": "custom"}},
		{"inherit auto", map[string]any{"auto_groups_mode": "inherit"}},
		{"smart", map[string]any{"smart_routing": true}},
		{"strategy", map[string]any{"routing_strategy": "price"}},
		{"cross-group retry", map[string]any{"cross_group_retry": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := baseTokenRequest(tc.name)
			for name, value := range tc.extra {
				body[name] = value
			}
			ctx, rec := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", body, user.Id)
			AddToken(ctx)
			assert.False(t, decodeAPIResponse(t, rec).Success)
			var count int64
			require.NoError(t, model.DB.Model(&model.Token{}).Count(&count).Error)
			assert.EqualValues(t, 1, count)

			body["id"], body["expected_version"] = key.Id, key.CacheRevision
			ctx, rec = newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", body, user.Id)
			UpdateToken(ctx)
			assert.False(t, decodeAPIResponse(t, rec).Success)
			var stored model.Token
			require.NoError(t, model.DB.First(&stored, key.Id).Error)
			assert.Equal(t, key.Name, stored.Name)
			assert.Equal(t, key.CacheRevision, stored.CacheRevision)
			assert.Equal(t, "default", stored.Group)
			assert.Empty(t, stored.AutoGroups)
			assert.False(t, stored.SmartRouting)
			assert.False(t, stored.CrossGroupRetry)
		})
	}
}

func TestReadLegacyTokenRetainsMaskedKeyAndOrderedGroupSnapshot(t *testing.T) {
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	token := seedToken(t, model.DB, user.Id, "ordered-snapshot", "synthetic-auto-ordered-key")
	token.Group, token.CrossGroupRetry = "auto", true
	require.NoError(t, token.SetAutoGroups([]string{"vip", "default"}))
	require.NoError(t, model.DB.Save(token).Error)

	ctx, rec := newTokenAutoGroupsAuthenticatedContext(t, http.MethodGet, "/api/token/"+stringInt(token.Id), nil, user.Id)
	ctx.Params = gin.Params{{Key: "id", Value: stringInt(token.Id)}}
	GetToken(ctx)
	res := decodeAPIResponse(t, rec)
	require.True(t, res.Success, res.Message)
	var data struct {
		AutoGroups []string `json:"auto_groups"`
		Key        string   `json:"key"`
	}
	require.NoError(t, common.Unmarshal(res.Data, &data))
	assert.Equal(t, []string{"vip", "default"}, data.AutoGroups)
	assert.Equal(t, token.GetMaskedKey(), data.Key)
	assert.NotContains(t, rec.Body.String(), token.Key)
}

func TestGetTokenAutoGroupsReturnsFullFilteredGlobalOrderAndLimit(t *testing.T) {
	configureTokenAutoGroupsTest(t, "1", `["vip","missing","default"]`)
	user := setupTokenAutoGroupsControllerTest(t)

	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodGet, "/api/token/auto-groups", nil, user.Id)
	GetTokenAutoGroups(ctx)

	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)
	var data struct {
		Groups   []string `json:"groups"`
		MaxCount int      `json:"max_count"`
	}
	require.NoError(t, common.Unmarshal(response.Data, &data))
	assert.Equal(t, []string{"vip", "default"}, data.Groups)
	assert.Equal(t, 1, data.MaxCount)
}
