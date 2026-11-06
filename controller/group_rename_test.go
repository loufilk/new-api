package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
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

// Successful renames advance process-wide monotonic state. A fresh database
// in another test is a different deployment, so exercise each commit in its
// own process rather than teaching production to accept version rollback.
func groupRenameControllerIsolatedProcess(t *testing.T) bool {
	t.Helper()
	if os.Getenv("GROUP_RENAME_CONTROLLER_CHILD") == t.Name() {
		return false
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.count=1", "-test.timeout=1m")
	cmd.Env = append(os.Environ(), "GROUP_RENAME_CONTROLLER_CHILD="+t.Name())
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), "--- PASS: "+t.Name())
	t.Logf("GROUP_RENAME_CONTROLLER_CHILD_RESULT %q", output)
	return true
}

func setupGroupRenameControllerTest(t *testing.T) *gorm.DB {
	t.Helper()
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.Option{}, &model.GroupRenameOperation{}, &model.User{}, &model.UserSession{}, &model.Token{}, &model.Channel{},
		&model.ModelTestGate{}, &model.ModelFingerprintSiteConfig{}, &model.ModelFingerprintSiteAudit{},
		&model.Ability{}, &model.SubscriptionPlan{}, &model.UserSubscription{},
		&model.Log{}, &model.AuditLog{},
		&model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{},
	))
	root := model.User{Id: 990001, Username: "rename-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: "rename-root"}
	require.NoError(t, db.Create(&root).Error)
	require.NoError(t, db.Create(&model.UserSession{SID: "rename-root-session", UserID: root.Id, UserAuthVersion: 1, Version: 1, Status: model.UserSessionStatusActive, ExpiresAt: common.GetTimestamp() + 3600, RefreshHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}).Error)
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
	ctx, recorder := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{"renames": "invalid"}, 1)

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

	ctx, recorder := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
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
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)
	user := model.User{Username: "controller-rename-success", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	ctx, recorder := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
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
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "AutoGroups", Value: `["old"]`}).Error)
	user := model.User{Username: "controller-rename-options", Password: "hash", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)

	ctx, recorder := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
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

	ctx, recorder := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
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

	ctx, recorder := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{
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

func newGroupRenameContext(t *testing.T, method, target string, body any, _ int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	ctx, recorder := newAuthenticatedContext(t, method, target, body, 990001)
	ctx.Set("role", common.RoleRootUser)
	ctx.Set("session_id", "rename-root-session")
	ctx.Set("session_version", int64(1))
	return ctx, recorder
}

func TestRenameGroupsEndpointReceiptAuditReplayAndRevocation(t *testing.T) {
	if groupRenameControllerIsolatedProcess(t) {
		return
	}
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)
	user := model.User{Username: "receipt-customer", AffCode: "receipt-customer", Group: "old", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "synthetic-secret-not-in-audit", Group: "old", Status: common.TokenStatusDisabled}
	require.NoError(t, db.Create(&token).Error)
	body := map[string]any{"request_id": "controller-request-001", "renames": []model.GroupRenameMapping{{From: "old", To: "new"}}}
	for _, replayed := range []bool{false, true} {
		ctx, rec := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", body, 1)
		RenameGroups(ctx)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var response groupRenameAPIResponse
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
		require.True(t, response.Success)
		require.NotNil(t, response.Data)
		assert.Equal(t, replayed, response.Data.Replayed)
		assert.Equal(t, 1, response.Data.Tokens)
	}
	var operation model.GroupRenameOperation
	require.NoError(t, db.First(&operation).Error)
	assert.Equal(t, 990001, operation.ActorID)
	assert.Equal(t, common.RoleRootUser, operation.ActorRole)
	assert.NotContains(t, operation.Result, token.Key)
	var audits []model.AuditLog
	require.NoError(t, db.Where("action = ?", "option.group_rename").Find(&audits).Error)
	require.Len(t, audits, 2)
	raw, err := common.Marshal(audits)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), token.Key)
	assert.Contains(t, string(raw), `"tokens":1`)
	// A retry is not an authorization bypass: revoked sessions cannot even read
	// their former success receipt through the mutation endpoint.
	require.NoError(t, db.Model(&model.UserSession{}).Where("sid = ?", "rename-root-session").Update("status", model.UserSessionStatusRevoked).Error)
	ctx, rec := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", body, 1)
	RenameGroups(ctx)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRenameGroupsEndpointRechecksCurrentRootRole(t *testing.T) {
	db := setupGroupRenameControllerTest(t)
	require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"old":1}`}).Error)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 990001).Update("role", common.RoleAdminUser).Error)
	ctx, rec := newGroupRenameContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{"renames": []model.GroupRenameMapping{{From: "old", To: "new"}}}, 1)
	RenameGroups(ctx)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	var operationCount int64
	require.NoError(t, db.Model(&model.GroupRenameOperation{}).Count(&operationCount).Error)
	assert.Zero(t, operationCount)
	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
	assert.JSONEq(t, `{"old":1}`, option.Value)
}
