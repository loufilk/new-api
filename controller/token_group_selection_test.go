package controller

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddTokenRequiresExplicitAuthorizedGroup(t *testing.T) {
	for _, smart := range []bool{false, true} {
		t.Run(fmt.Sprintf("smart=%v", smart), func(t *testing.T) {
			configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
			user := setupTokenAutoGroupsControllerTest(t)
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","auto":"Auto","retired":"Retired"}`))
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"private":1}`))
			for _, tc := range []struct {
				name       string
				group      any
				omit, want bool
			}{
				{name: "omitted", omit: true},
				{name: "null"},
				{name: "empty", group: ""},
				{name: "whitespace", group: " \t\n"},
				{name: "default", group: "default", want: true},
				{name: "auto", group: "auto", want: true},
				{name: "named", group: "vip", want: true},
				{name: "unknown", group: "missing"},
				{name: "unauthorized", group: "private"},
				{name: "removed", group: "retired"},
				{name: "not an exact name", group: " default "},
			} {
				t.Run(tc.name, func(t *testing.T) {
					request := baseAutoTokenRequest("selection-" + tc.name)
					request["smart_routing"] = smart
					request["user_id"] = user.Id + 1
					if tc.omit {
						delete(request, "group")
					} else {
						request["group"] = tc.group
					}
					ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPost, "/api/token/", request, user.Id)
					AddToken(ctx)
					response := decodeAPIResponse(t, recorder)
					assert.Equal(t, tc.want, response.Success, response.Message)
					var saved []model.Token
					require.NoError(t, model.DB.Where("name = ?", request["name"]).Find(&saved).Error)
					if !tc.want {
						assert.Empty(t, saved, "invalid selections must not create an API key")
						return
					}
					require.Len(t, saved, 1)
					assert.Equal(t, tc.group, saved[0].Group)
					assert.Equal(t, user.Id, saved[0].UserId, "the authenticated owner controls the credential identity")
					assert.Equal(t, smart, saved[0].SmartRouting)
				})
			}
		})
	}
}

func TestUpdateTokenPreservesGroupUnlessExplicitlySelected(t *testing.T) {
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	for index, tc := range []struct {
		name, previous, expected string
		group                    any
		omit, smart, want        bool
	}{
		{name: "named omitted", previous: "vip", expected: "vip", omit: true, want: true},
		{name: "named null", previous: "vip", expected: "vip", want: true},
		{name: "legacy omitted", omit: true, want: true},
		{name: "legacy null", want: true},
		{name: "legacy unchanged", group: "", want: true},
		{name: "named cannot clear", previous: "vip", expected: "vip", group: ""},
		{name: "named cannot use whitespace", previous: "vip", expected: "vip", group: " \t"},
		{name: "legacy cannot use whitespace", group: " \t"},
		{name: "explicit default", previous: "vip", group: "default", expected: "default", want: true},
		{name: "legacy explicit selection", group: "vip", expected: "vip", want: true},
		{name: "smart named cannot clear", previous: "vip", expected: "vip", group: "", smart: true},
		{name: "smart legacy unchanged", group: "", smart: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := seedToken(t, model.DB, user.Id, "before", fmt.Sprintf("synthetic-selection-%d", index))
			token.Group, token.SmartRouting = tc.previous, tc.smart
			require.NoError(t, model.DB.Save(token).Error)
			request := baseAutoTokenRequest("after")
			request["id"], request["user_id"] = token.Id, user.Id+1
			request["key"] = "must-not-replace-the-key"
			if tc.omit {
				delete(request, "group")
			} else {
				request["group"] = tc.group
			}
			ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
			UpdateToken(ctx)
			response := decodeAPIResponse(t, recorder)
			assert.Equal(t, tc.want, response.Success, response.Message)
			var saved model.Token
			require.NoError(t, model.DB.First(&saved, token.Id).Error)
			assert.Equal(t, tc.expected, saved.Group)
			assert.Equal(t, token.UserId, saved.UserId)
			assert.Equal(t, token.Key, saved.Key)
			assert.Equal(t, tc.smart, saved.SmartRouting)
			if tc.want {
				assert.Equal(t, "after", saved.Name)
			} else {
				assert.Equal(t, "before", saved.Name, "rejected changes must be atomic")
			}
		})
	}
}

func TestUpdateTokenStatusOnlyAllowsUserStates(t *testing.T) {
	configureTokenAutoGroupsTest(t, "5", `["default","vip"]`)
	user := setupTokenAutoGroupsControllerTest(t)
	for index, status := range []any{nil, -1, 0, common.TokenStatusExpired, common.TokenStatusExhausted, 99} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			token := seedToken(t, model.DB, user.Id, "status", fmt.Sprintf("synthetic-status-%d", index))
			request := map[string]any{"id": token.Id}
			if status != nil {
				request["status"] = status
			}
			ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", request, user.Id)
			UpdateToken(ctx)
			assert.False(t, decodeAPIResponse(t, recorder).Success)
			var saved model.Token
			require.NoError(t, model.DB.First(&saved, token.Id).Error)
			assert.Equal(t, common.TokenStatusEnabled, saved.Status)
		})
	}
	owned := seedToken(t, model.DB, user.Id, "owned-status", "synthetic-owned-status")
	owned.Group, owned.ModelLimitsEnabled, owned.ModelLimits = "vip", true, "allowed-model"
	require.NoError(t, model.DB.Save(owned).Error)
	for _, status := range []int{common.TokenStatusDisabled, common.TokenStatusEnabled} {
		ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", map[string]any{
			"id": owned.Id, "status": status, "group": "", "smart_routing": true,
			"model_limits_enabled": false, "model_limits": "", "user_id": user.Id + 1,
		}, user.Id)
		UpdateToken(ctx)
		response := decodeAPIResponse(t, recorder)
		require.True(t, response.Success, response.Message)
		var saved model.Token
		require.NoError(t, model.DB.First(&saved, owned.Id).Error)
		assert.Equal(t, status, saved.Status)
		assert.Equal(t, "vip", saved.Group)
		assert.False(t, saved.SmartRouting)
		assert.True(t, saved.ModelLimitsEnabled)
		assert.Equal(t, "allowed-model", saved.ModelLimits)
		assert.Equal(t, user.Id, saved.UserId)
	}
	ctx, recorder := newTokenAutoGroupsAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", map[string]any{
		"id": owned.Id, "status": common.TokenStatusDisabled, "user_id": user.Id,
	}, user.Id+1)
	UpdateToken(ctx)
	assert.False(t, decodeAPIResponse(t, recorder).Success)
	var unchanged model.Token
	require.NoError(t, model.DB.First(&unchanged, owned.Id).Error)
	assert.Equal(t, common.TokenStatusEnabled, unchanged.Status)
	assert.NotContains(t, recorder.Body.String(), owned.Key)
}
