package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTokenModelsControllerTest(t *testing.T) (*gorm.DB, *model.User) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}))

	previousAuto := setting.AutoGroups2JsonString()
	previousUsable := setting.UserUsableGroups2JSONString()
	previousRatios := ratio_setting.GroupRatio2JSONString()
	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, setting.UpdateAutoGroupsByJsonString(`["a","b","default"]`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A","b":"B","auto":"Auto"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"a":1,"b":1}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o":1,"gpt-4.1":1}`))
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(previousAuto))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRatios))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios))
	})

	user := &model.User{
		Id:       501,
		Username: "ccswitch-token-user",
		Password: "password",
		Group:    "default",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
	}
	require.NoError(t, db.Create(user).Error)
	return db, user
}

func seedTokenModelsToken(t *testing.T, db *gorm.DB, userID int, name, group string, autoGroups []string, limits ...string) *model.Token {
	t.Helper()
	token := &model.Token{
		UserId:             userID,
		Name:               name,
		Key:                "key-" + name,
		Status:             common.TokenStatusEnabled,
		CreatedTime:        1,
		AccessedTime:       1,
		ExpiredTime:        -1,
		UnlimitedQuota:     true,
		Group:              group,
		ModelLimitsEnabled: len(limits) > 0,
	}
	if len(limits) > 0 {
		for i, modelName := range limits {
			if i > 0 {
				token.ModelLimits += ","
			}
			token.ModelLimits += modelName
		}
	}
	require.NoError(t, token.SetAutoGroups(autoGroups))
	require.NoError(t, db.Create(token).Error)
	return token
}

func seedTokenModelsAbility(t *testing.T, db *gorm.DB, group, modelName string, channelType int, enabled bool) *model.Channel {
	t.Helper()
	channel := &model.Channel{
		Type:   channelType,
		Key:    "channel-key",
		Status: common.ChannelStatusEnabled,
		Name:   fmt.Sprintf("%s-%s-%d", group, modelName, channelType),
		Models: modelName,
		Group:  group,
	}
	require.NoError(t, db.Create(channel).Error)
	priority := int64(0)
	require.NoError(t, db.Create(&model.Ability{
		Group:     group,
		Model:     modelName,
		ChannelId: channel.Id,
		Enabled:   enabled,
		Priority:  &priority,
		Weight:    100,
	}).Error)
	return channel
}

func requestTokenModels(t *testing.T, tokenID, userID int, app string) ([]string, tokenAPIResponse) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/user/tokens/"+strconv.Itoa(tokenID)+"/models?app="+app, nil, userID)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tokenID)}}
	ctx.Request.URL.RawQuery = "app=" + app
	GetTokenModels(ctx)
	response := decodeAPIResponse(t, recorder)
	var models []string
	if response.Success {
		require.NoError(t, common.Unmarshal(response.Data, &models))
	}
	return models, response
}

func requestTokenRoutableModels(t *testing.T, tokenID, userID int) ([]string, tokenAPIResponse) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/"+strconv.Itoa(tokenID)+"/models", nil, userID)
	ctx.Params = gin.Params{{Key: "id", Value: strconv.Itoa(tokenID)}}
	GetTokenRoutableModels(ctx)
	assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
	response := decodeAPIResponse(t, recorder)
	var models []string
	if response.Success {
		require.NoError(t, common.Unmarshal(response.Data, &models))
	}
	assert.NotContains(t, recorder.Body.String(), `"key"`)
	return models, response
}

func TestGetTokenRoutableModelsUsesOwnedTokenScopeWithoutCLIApp(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"image-a":1,"image-b":1,"image-blocked":1,"image-disabled":1}`))
	seedTokenModelsAbility(t, db, "a", "image-a", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "image-b", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "a", "image-blocked", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "a", "image-unpriced", constant.ChannelTypeOpenAI, true)
	disabled := seedTokenModelsAbility(t, db, "a", "image-disabled", constant.ChannelTypeOpenAI, true)
	require.NoError(t, db.Model(disabled).Update("status", common.ChannelStatusManuallyDisabled).Error)
	tokenA := seedTokenModelsToken(t, db, user.Id, "image-auto-a", "auto", []string{"a"}, "image-a", "image-disabled", "image-unpriced")
	tokenB := seedTokenModelsToken(t, db, user.Id, "image-auto-b", "auto", []string{"b"}, "image-b")

	models, response := requestTokenRoutableModels(t, tokenA.Id, user.Id)
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"image-a"}, models)
	models, response = requestTokenRoutableModels(t, tokenB.Id, user.Id)
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"image-b"}, models)

	// Route and whitelist edits take effect on the next request for this token.
	require.NoError(t, tokenA.SetAutoGroups([]string{"b"}))
	tokenA.ModelLimits = "image-b"
	require.NoError(t, db.Model(tokenA).Select("auto_groups", "model_limits").Updates(tokenA).Error)
	models, response = requestTokenRoutableModels(t, tokenA.Id, user.Id)
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"image-b"}, models)

	// Losing the account's group entitlement cannot fall back to other groups.
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A","auto":"Auto"}`))
	models, response = requestTokenRoutableModels(t, tokenA.Id, user.Id)
	require.True(t, response.Success, response.Message)
	assert.Empty(t, models)

	for _, id := range []int{tokenA.Id, tokenB.Id} {
		models, response = requestTokenRoutableModels(t, id, user.Id+1)
		assert.False(t, response.Success)
		assert.Empty(t, models)
	}
	for _, id := range []int{0, -1, tokenB.Id + 1000} {
		models, response = requestTokenRoutableModels(t, id, user.Id)
		assert.False(t, response.Success)
		assert.Empty(t, models)
	}
}

func requestTokenModelScope(t *testing.T, userID int, group string, autoGroups []string) ([]string, tokenAPIResponse) {
	t.Helper()
	body := tokenModelScopeRequest{
		Group:      group,
		AutoGroups: autoGroups,
	}
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/models", body, userID)
	PreviewTokenModels(ctx)
	response := decodeAPIResponse(t, recorder)
	var models []string
	if response.Success {
		require.NoError(t, common.Unmarshal(response.Data, &models))
	}
	return models, response
}

func TestPreviewTokenModelsUsesSelectedGroup(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModelScope(t, user.Id, "b", nil)
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"gpt-4.1"}, models)
	assert.NotContains(t, models, "gpt-4o")
}

func TestPreviewTokenModelsUsesEditedAutoGroups(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModelScope(t, user.Id, "auto", []string{"b"})
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"gpt-4.1"}, models)
	assert.NotContains(t, models, "gpt-4o")
}

func TestPreviewTokenModelsCustomAutoEmptyDoesNotInheritGlobalGroups(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)

	body := tokenModelScopeRequest{
		Group:          "auto",
		AutoGroupsMode: "custom",
		AutoGroups:     []string{},
	}
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/token/models", body, user.Id)
	PreviewTokenModels(ctx)
	response := decodeAPIResponse(t, recorder)
	require.True(t, response.Success, response.Message)

	var models []string
	require.NoError(t, common.Unmarshal(response.Data, &models))
	assert.Empty(t, models)
}

func TestPreviewTokenModelsFiltersDisabledChannelsAndUnpricedModels(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"priced-model":1,"disabled-model":1}`))
	seedTokenModelsAbility(t, db, "b", "priced-model", constant.ChannelTypeOpenAI, true)
	disabledChannel := seedTokenModelsAbility(t, db, "b", "disabled-model", constant.ChannelTypeOpenAI, true)
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", disabledChannel.Id).Update("status", common.ChannelStatusManuallyDisabled).Error)
	seedTokenModelsAbility(t, db, "b", "unpriced-model", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModelScope(t, user.Id, "b", nil)
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"priced-model"}, models)
}

func TestPreviewTokenModelsRespectsUserGroupPermissions(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A","auto":"Auto"}`))
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModelScope(t, user.Id, "b", nil)
	require.True(t, response.Success, response.Message)
	assert.Empty(t, models)
}

func TestGetTokenModelsUsesConcreteTokenGroup(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	token := seedTokenModelsToken(t, db, user.Id, "fixed-b", "b", nil)
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModels(t, token.Id, user.Id, "codex")
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"gpt-4.1"}, models)
}

func TestGetTokenModelsUsesTokenAutoGroupsSnapshot(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	token := seedTokenModelsToken(t, db, user.Id, "auto-a", "auto", []string{"a"})
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModels(t, token.Id, user.Id, "claude")
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"gpt-4o"}, models)
}

func TestGetTokenModelsAppliesModelLimits(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	token := seedTokenModelsToken(t, db, user.Id, "limited", "a", nil, "gpt-4.1")
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "a", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModels(t, token.Id, user.Id, "gemini")
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"gpt-4.1"}, models)
}

func TestGetTokenModelsFiltersByApplicationProtocolCapability(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	token := seedTokenModelsToken(t, db, user.Id, "protocol", "a", nil)
	seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeDify, true)
	seedTokenModelsAbility(t, db, "a", "gpt-4.1", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModels(t, token.Id, user.Id, "codex")
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"gpt-4.1"}, models)
}

func TestGetTokenModelsFiltersModelsWithoutBillingConfig(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"priced-model":1}`))
	token := seedTokenModelsToken(t, db, user.Id, "pricing", "a", nil)
	seedTokenModelsAbility(t, db, "a", "priced-model", constant.ChannelTypeOpenAI, true)
	seedTokenModelsAbility(t, db, "a", "unpriced-model", constant.ChannelTypeOpenAI, true)

	models, response := requestTokenModels(t, token.Id, user.Id, "codex")
	require.True(t, response.Success, response.Message)
	assert.Equal(t, []string{"priced-model"}, models)
}

func TestGetTokenModelsRejectsOtherUsersToken(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	token := seedTokenModelsToken(t, db, user.Id, "owned", "a", nil)
	_, response := requestTokenModels(t, token.Id, user.Id+1, "codex")
	assert.False(t, response.Success)
}
