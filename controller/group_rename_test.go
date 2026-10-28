package controller

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type groupRenameAPIResponse struct {
	Success bool                     `json:"success"`
	Message string                   `json:"message"`
	Code    string                   `json:"code"`
	Data    *model.GroupRenameResult `json:"data"`
}

func setupGroupRenameControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Option{}, &model.User{}, &model.Token{}, &model.Channel{},
		&model.Ability{}, &model.SubscriptionPlan{}, &model.UserSubscription{},
		&model.Log{}, &model.AuditLog{},
	))
	originalGroupRatio := ratio_setting.GroupRatio2JSONString()
	originalGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	originalUsable := setting.UserUsableGroups2JSONString()
	originalAuto := setting.AutoGroups2JsonString()
	originalSpecial := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.MarshalJSONString()
	originalMax := setting.GetMaxTokenAutoGroups()

	originalOptionMap := make(map[string]string)
	common.OptionMapRWMutex.RLock()
	hadOptionMap := common.OptionMap != nil
	for key, value := range common.OptionMap {
		originalOptionMap[key] = value
	}
	common.OptionMapRWMutex.RUnlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadOptionMap {
			common.OptionMap = originalOptionMap
		} else {
			common.OptionMap = nil
		}
		common.OptionMapRWMutex.Unlock()
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatio))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(originalGroupGroupRatio))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsable))
		require.NoError(t, setting.UpdateAutoGroupsByJsonString(originalAuto))
		require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(originalSpecial)))
		require.NoError(t, setting.UpdateMaxTokenAutoGroups(stringInt(originalMax)))
	})
	return db
}

func TestRenameGroupsEndpointRejectsMalformedBody(t *testing.T) {
	setupGroupRenameControllerTest(t)
	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{"renames": "invalid"}, 1)

	RenameGroups(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	var response groupRenameAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
}

func TestRenameGroupsEndpointRejectsUnknownSourceWithoutWrites(t *testing.T) {
	db := setupGroupRenameControllerTest(t)
	user := model.User{Username: "controller-rename-user", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
		"renames": []map[string]string{{"from": "missing", "to": "new"}},
	}, 1)
	RenameGroups(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	var response groupRenameAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "unknown source group")

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, "old", stored.Group)
}

func TestRenameGroupsEndpointMigratesAndReturnsCounts(t *testing.T) {
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)
	user := model.User{Username: "controller-rename-success", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
		"renames": []map[string]string{{"from": "old", "to": "new"}},
	}, 1)
	RenameGroups(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response groupRenameAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotNil(t, response.Data)
	assert.Equal(t, []model.GroupRenameMapping{{From: "old", To: "new"}}, response.Data.Renames)
	assert.Equal(t, 1, response.Data.Users)

	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, "new", stored.Group)
	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
	assert.JSONEq(t, `{"new":1}`, option.Value)
}

func TestRenameGroupsEndpointAppliesCombinedOptionsAtomically(t *testing.T) {
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "AutoGroups", Value: `["old"]`}).Error)
	user := model.User{Username: "controller-rename-options", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
		"renames": []map[string]string{{"from": "old", "to": "new"}},
		"options": map[string]string{
			"GroupRatio":         `{"new":0.5}`,
			"AutoGroups":         `["new"]`,
			"MaxTokenAutoGroups": "6",
		},
		"expected": map[string]string{
			"GroupRatio": `{"old":1}`,
			"AutoGroups": `["old"]`,
		},
	}, 1)
	RenameGroups(ctx)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response groupRenameAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotNil(t, response.Data)
	assert.Equal(t, 3, response.Data.Options)
	assert.Equal(t, `{"new":0.5}`, response.Data.AppliedOptions["GroupRatio"])
	assert.Equal(t, `["new"]`, response.Data.AppliedOptions["AutoGroups"])

	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"new":0.5}`, option.Value)
	var maxTokenAutoGroups model.Option
	require.NoError(t, db.Where(&model.Option{Key: "MaxTokenAutoGroups"}).First(&maxTokenAutoGroups).Error)
	assert.Equal(t, "6", maxTokenAutoGroups.Value)
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	assert.Equal(t, "new", stored.Group)
}

func TestRenameGroupsEndpointReportsStaleOptionEdit(t *testing.T) {
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
		"renames":  []map[string]string{{"from": "old", "to": "new"}},
		"options":  map[string]string{"GroupRatio": `{"new":1}`},
		"expected": map[string]string{"GroupRatio": `{"old":2}`},
	}, 1)
	RenameGroups(ctx)

	assert.Equal(t, http.StatusConflict, recorder.Code)
	var response groupRenameAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "GROUP_RENAME_STALE", response.Code)

	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"old":1}`, option.Value)
}

func TestRenameGroupsEndpointRejectsReferencedGroupRemoval(t *testing.T) {
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"cust0m":1,"keep":1}`}).Error)
	user := model.User{Username: "controller-rename-removal", Password: "hash", Group: "cust0m", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	ctx, recorder := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
		"options": map[string]string{"GroupRatio": `{"keep":1}`},
	}, 1)
	RenameGroups(ctx)

	assert.Equal(t, http.StatusBadRequest, recorder.Code)
	var response groupRenameAPIResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "GROUP_RENAME_REFERENCED", response.Code)

	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
	assert.Equal(t, `{"cust0m":1,"keep":1}`, option.Value)
}
