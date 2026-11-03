package service

import (
	"fmt"
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

func configureRequestAutoGroupsTest(t *testing.T) {
	t.Helper()
	originalMax := setting.GetMaxTokenAutoGroups()
	originalAutoGroups := setting.AutoGroups2JsonString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("2"))
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["vip","default","svip"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","svip":"SVIP"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":1,"svip":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", originalMax)))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAutoGroups))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios))
	})
}

func newRequestAutoGroupsContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	return ctx
}

func TestGetRequestAutoGroupsInheritedListIsNotLimited(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default", "svip"}, groups)
}

func TestGetRequestAutoGroupsFiltersBeforeApplyingCurrentLimit(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"revoked", "vip", "default", "svip"})
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Equal(t, []string{"vip", "default"}, groups)
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("1"))
	assert.Equal(t, []string{"vip"}, GetRequestAutoGroups(ctx, "default"))
}

func TestGetRequestAutoGroupsDoesNotFallBackAfterPermissionChange(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"vip"})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))

	groups := GetRequestAutoGroups(ctx, "default")

	assert.Empty(t, groups)
}

func TestSmartTokenScopeUsesLiveAuthorizedGroupsNotVirtualAuto(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`[]`))
	require.NoError(t, setting.UpdateMaxTokenAutoGroups("1"))
	ctx := newRequestAutoGroupsContext()
	common.SetContextKey(ctx, constant.ContextKeyTokenSmartRouting, true)
	common.SetContextKey(ctx, constant.ContextKeyTokenAutoGroups, []string{"forbidden"})
	want := []string{"default", "svip", "vip"}
	assert.Equal(t, want, GetRequestAutoGroups(ctx, "default"))
	token := &model.Token{Group: "forbidden", AutoGroups: `["forbidden"]`, SmartRouting: true}
	groups, err := GetTokenEffectiveGroups("default", token)
	require.NoError(t, err)
	assert.Equal(t, want, groups)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	assert.Equal(t, []string{"default"}, GetRequestAutoGroups(ctx, "default"))
	token.SmartRouting = false
	groups, err = GetTokenEffectiveGroups("default", token)
	require.NoError(t, err)
	assert.Empty(t, groups, "turning smart routing off must restore named-group permission checks")
}

func TestManualTokenEffectiveGroupsWithoutVirtualAutoPreservesOrderAndFailsClosed(t *testing.T) {
	configureRequestAutoGroupsTest(t)
	token := &model.Token{Group: "auto", AutoGroups: `["svip","vip"]`}
	groups, err := GetTokenEffectiveGroups("default", token)
	require.NoError(t, err)
	assert.Equal(t, []string{"svip", "vip"}, groups)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	groups, err = GetTokenEffectiveGroups("default", token)
	require.NoError(t, err)
	assert.Equal(t, []string{"vip"}, groups)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
	groups, err = GetTokenEffectiveGroups("default", token)
	require.NoError(t, err)
	assert.Empty(t, groups)
	for _, snapshot := range []string{"", `[]`, `null`} {
		token.AutoGroups = snapshot
		groups, err = GetTokenEffectiveGroups("default", token)
		require.NoError(t, err)
		assert.Empty(t, groups, "empty inherited Auto still requires its separate permission")
	}
	token.AutoGroups = `invalid-json`
	groups, err = GetTokenEffectiveGroups("default", token)
	require.Error(t, err)
	assert.Empty(t, groups)
}
