package router

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the production router and real PAT lookup, not an injected user ID.
func TestTokenRoutableModelsRouteRequiresOwnedTokenAndReadScope(t *testing.T) {
	engine, db := setupReferralRouteTest(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Channel{}, &model.Ability{}))
	previousMemory := common.MemoryCacheEnabled
	previousGroups := setting.UserUsableGroups2JSONString()
	previousRatios := ratio_setting.ModelRatio2JSONString()
	common.MemoryCacheEnabled = false
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"fixture-image":1}`))
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousMemory
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatios))
	})
	owner := seedReferralRouteUser(t, db, 810, "", 0, common.UserStatusEnabled)
	other := seedReferralRouteUser(t, db, 811, "", 0, common.UserStatusEnabled)
	reader := model.AccessTokenPrefix + "token-models-reader"
	wrongScope := model.AccessTokenPrefix + "token-models-profile-only"
	foreign := model.AccessTokenPrefix + "token-models-foreign-reader"
	seedRouteScopedToken(t, db, owner.Id, reader, "api_key:read")
	seedRouteScopedToken(t, db, owner.Id, wrongScope, "profile:read")
	seedRouteScopedToken(t, db, other.Id, foreign, "api_key:read")
	token := model.Token{UserId: owner.Id, Key: "synthetic-provider-key-not-returned", Name: "image-token", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true, Group: "default", ModelLimitsEnabled: true, ModelLimits: "fixture-image"}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Key: "synthetic-channel-secret", Status: common.ChannelStatusEnabled, Name: "fixture-channel", Models: "fixture-image", Group: "default"}
	require.NoError(t, db.Create(&channel).Error)
	priority := int64(0)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: "fixture-image", ChannelId: channel.Id, Enabled: true, Priority: &priority, Weight: 100}).Error)
	path := fmt.Sprintf("/api/token/%d/models", token.Id)
	for _, tc := range []struct {
		name, bearer, marker string
		status               int
		success              bool
	}{
		{"read scope permits owned token", reader, "fixture-image", http.StatusOK, true},
		{"wrong scope is denied before handler", wrongScope, "ACCESS_TOKEN_SCOPE_DENIED", http.StatusForbidden, false},
		{"read scope does not permit another users token", foreign, `"success":false`, http.StatusOK, false},
		{"anonymous denied", "", "AUTH_UNAUTHORIZED", http.StatusUnauthorized, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := performReferralRouteRequest(engine, http.MethodGet, path, "", tc.bearer)
			require.Equal(t, tc.status, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), tc.marker)
			assert.NotContains(t, response.Body.String(), "ACCESS_TOKEN_ROUTE_UNDECLARED")
			assert.NotContains(t, response.Body.String(), token.Key)
			assert.NotContains(t, response.Body.String(), channel.Key)
			if tc.success {
				assert.Contains(t, response.Body.String(), `"success":true`)
				assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			} else {
				assert.NotContains(t, response.Body.String(), `"data":["fixture-image"]`)
			}
		})
	}
}
