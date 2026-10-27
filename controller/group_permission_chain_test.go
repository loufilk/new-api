package controller

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Use an owned, isolated schema for the focused native permission tests. The
// normal test suite keeps its in-memory default when no PostgreSQL is selected.
func openGroupPermissionPostgresTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	parsed, err := url.Parse(os.Getenv("TEST_POSTGRES_DSN"))
	require.NoError(t, err)
	require.NotNil(t, net.ParseIP(parsed.Hostname()))
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	admin, err := gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
	require.NoError(t, err)
	adminConn, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminConn.Close()) })
	schema := fmt.Sprintf("group_permission_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	var db *gorm.DB
	t.Cleanup(func() {
		if db != nil {
			conn, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, conn.Close())
		}
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err = gorm.Open(postgres.New(postgres.Config{DSN: parsed.String(), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	// Initialize the production dialect column names without running startup
	// migrations or creating any accounts.
	model.DB = db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Setenv("LOG_SQL_DSN", "")
	master := common.IsMasterNode
	common.IsMasterNode = false
	err = model.InitLogDB()
	common.IsMasterNode = master
	require.NoError(t, err)

	var version string
	require.NoError(t, db.Raw("SHOW server_version").Scan(&version).Error)
	t.Logf("isolated group permission database: PostgreSQL %s", version)
	return db
}

func TestGroupPermissionEditsDoNotDeleteDefinitions(t *testing.T) {
	for _, tc := range []struct{ name, key, before, after string }{
		{"user selectable", "UserUsableGroups", `{"default":"Default","a":"A"}`, `{"default":"Default"}`},
		{"auto membership", "AutoGroups", `["a","default"]`, `["default"]`},
		{"auto permission", "UserUsableGroups", `{"default":"Default","auto":"Auto"}`, `{"default":"Default"}`},
		{"enterprise authorization", "group_ratio_setting.group_special_usable_group", `{"enterprise":{"+:a":"A"},"default":{}}`, `{"enterprise":{},"default":{}}`},
		{"empty rule removal", "group_ratio_setting.group_special_usable_group", `{"a":{},"default":{}}`, `{"default":{},"enterprise":{}}`},
		{"nested ratio override", "GroupGroupRatio", `{"default":{"a":1}}`, `{"default":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupGroupRenameControllerTest(t)
			require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"default":1,"a":1,"enterprise":1}`}).Error)
			require.NoError(t, db.Create(&model.Option{Key: tc.key, Value: tc.before}).Error)
			user := model.User{Username: "referenced", Password: "synthetic", Group: "a", Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			// A permission-only edit must not scan unrelated legacy snapshots as
			// though the group were being renamed or deleted.
			require.NoError(t, db.Create(&model.Token{UserId: user.Id, Key: "synthetic-malformed", Group: "a", AutoGroups: "["}).Error)
			ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{"options": map[string]string{tc.key: tc.after}, "expected": map[string]string{tc.key: tc.before}}, 1)
			RenameGroups(ctx)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var response groupRenameAPIResponse
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response))
			require.True(t, response.Success, response.Message)
			var option model.Option
			require.NoError(t, db.Where(&model.Option{Key: tc.key}).First(&option).Error)
			assert.JSONEq(t, tc.after, option.Value)
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, "a", user.Group)
		})
	}
}

func TestGroupPermissionConflictRejectsAtomicWrite(t *testing.T) {
	for _, visible := range []string{"+:a", "a"} {
		t.Run(visible, func(t *testing.T) {
			db := setupGroupRenameControllerTest(t)
			const key = "group_ratio_setting.group_special_usable_group"
			require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"default":1,"a":1}`}).Error)
			require.NoError(t, db.Create(&model.Option{Key: key, Value: `{"default":{}}`}).Error)
			require.NoError(t, db.Create(&model.Option{Key: "MaxTokenAutoGroups", Value: "5"}).Error)
			conflicting := fmt.Sprintf(`{"default":{%q:"Visible","-:a":"Hidden"}}`, visible)
			ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/option/group-rename", map[string]any{"options": map[string]string{key: conflicting, "MaxTokenAutoGroups": "6"}}, 1)
			RenameGroups(ctx)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			var message struct {
				Key    string            `json:"message_key"`
				Params map[string]string `json:"message_params"`
			}
			require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &message))
			assert.Equal(t, "Group {{target}} cannot be both extra visible and hidden for {{group}}. Remove one rule before saving.", message.Key)
			assert.Equal(t, map[string]string{"target": "a", "group": "default"}, message.Params)
			var option model.Option
			require.NoError(t, db.Where(&model.Option{Key: key}).First(&option).Error)
			assert.JSONEq(t, `{"default":{}}`, option.Value)
			option = model.Option{}
			require.NoError(t, db.Where(&model.Option{Key: "MaxTokenAutoGroups"}).First(&option).Error)
			assert.Equal(t, "5", option.Value)
		})
	}
}

func configureGroupPermissionScope(t *testing.T, autoAllowed bool) {
	t.Helper()
	previous := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.MarshalJSONString()
	require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(`{"enterprise":{"+:b":"Private B"}}`)))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(previous)))
	})
	usable := `{"default":"Default","a":"A"}`
	if autoAllowed {
		usable = `{"default":"Default","a":"A","auto":"Auto"}`
	}
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usable))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"a":1,"b":1,"enterprise":1}`))
}

func TestTokenGroupPermissionSaveAndRuntime(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		for _, tc := range []struct {
			name, owner, group string
			autoAllowed        bool
			children           []string
			allowed            bool
		}{
			{name: "inherit", owner: "default", group: "", allowed: true},
			{name: "public", owner: "default", group: "a", allowed: true},
			{name: "private", owner: "default", group: "b"},
			{name: "unknown", owner: "default", group: "unknown"},
			{name: "enterprise grant", owner: "enterprise", group: "b", allowed: true},
			{name: "auto denied inherited", owner: "default", group: "auto"},
			{name: "auto denied custom", owner: "default", group: "auto", children: []string{"a"}},
			{name: "auto scoped allowed", owner: "enterprise", group: "auto", autoAllowed: true, children: []string{"a", "b"}, allowed: true},
			{name: "auto scoped unauthorized child", owner: "default", group: "auto", autoAllowed: true, children: []string{"a", "b"}},
		} {
			t.Run(fmt.Sprintf("role%d/%s", role, tc.name), func(t *testing.T) {
				db, user := setupTokenModelsControllerTest(t)
				configureGroupPermissionScope(t, tc.autoAllowed)
				user.Group, user.Role = tc.owner, role
				require.NoError(t, db.Save(user).Error)
				body := baseAutoTokenRequest("permission-create")
				body["group"] = tc.group
				body["auto_groups"] = tc.children
				ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/token/", body, user.Id)
				ctx.Set("role", role)
				AddToken(ctx)
				response := decodeAPIResponse(t, rec)
				assert.Equal(t, tc.allowed, response.Success, response.Message)
				var count int64
				require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
				if tc.allowed {
					assert.EqualValues(t, 1, count)
				} else {
					assert.Zero(t, count)
				}
				existing := seedTokenModelsToken(t, db, user.Id, "prior", "a", nil)
				body["id"] = existing.Id
				body["name"] = "changed"
				ctx, rec = newAuthenticatedContext(t, http.MethodPut, "/api/token/", body, user.Id)
				ctx.Set("role", role)
				UpdateToken(ctx)
				response = decodeAPIResponse(t, rec)
				assert.Equal(t, tc.allowed, response.Success, response.Message)
				require.NoError(t, db.First(existing, existing.Id).Error)
				if tc.allowed {
					assert.Equal(t, "changed", existing.Name)
					assert.Equal(t, tc.group, existing.Group)
				} else {
					assert.Equal(t, "prior", existing.Name)
					assert.Equal(t, "a", existing.Group)
				}
			})
		}
	}
}

func TestGroupPermissionPreviewMatchesRuntime(t *testing.T) {
	require.NoError(t, i18n.Init())
	for _, tc := range []struct {
		name, owner, group, mode string
		autoAllowed              bool
		children, models         []string
		runtimeAllowed           bool
	}{
		{name: "private", owner: "default", group: "b"},
		{name: "auto inherited denied", owner: "default", group: "auto"},
		{name: "auto custom denied", owner: "default", group: "auto", mode: "custom", children: []string{"a"}},
		{name: "public", owner: "default", group: "a", models: []string{"gpt-4o"}, runtimeAllowed: true},
		{name: "enterprise", owner: "enterprise", group: "b", models: []string{"gpt-4.1"}, runtimeAllowed: true},
		{name: "auto scoped", owner: "enterprise", group: "auto", autoAllowed: true, mode: "custom", children: []string{"a", "b"}, models: []string{"gpt-4.1", "gpt-4o"}, runtimeAllowed: true},
		{name: "auto child revoked", owner: "default", group: "auto", autoAllowed: true, mode: "custom", children: []string{"a", "b"}, models: []string{"gpt-4o"}, runtimeAllowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, user := setupTokenModelsControllerTest(t)
			configureGroupPermissionScope(t, tc.autoAllowed)
			user.Group = tc.owner
			require.NoError(t, db.Save(user).Error)
			seedTokenModelsAbility(t, db, "a", "gpt-4o", constant.ChannelTypeOpenAI, true)
			seedTokenModelsAbility(t, db, "b", "gpt-4.1", constant.ChannelTypeOpenAI, true)
			token := seedTokenModelsToken(t, db, user.Id, "scope", "auto", tc.children)
			token.Group = tc.group
			token.Key = strings.Repeat("s", 48)
			require.NoError(t, db.Save(token).Error)
			ctx, rec := newAuthenticatedContext(t, http.MethodPost, "/api/token/models", tokenModelScopeRequest{Group: tc.group, AutoGroupsMode: tc.mode, AutoGroups: tc.children}, user.Id)
			PreviewTokenModels(ctx)
			response := decodeAPIResponse(t, rec)
			require.True(t, response.Success, response.Message)
			var models []string
			require.NoError(t, common.Unmarshal(response.Data, &models))
			assert.ElementsMatch(t, tc.models, models)
			models, response = requestTokenRoutableModels(t, token.Id, user.Id)
			require.True(t, response.Success, response.Message)
			assert.ElementsMatch(t, tc.models, models)
			router := gin.New()
			router.GET("/runtime", middleware.TokenAuth(), func(c *gin.Context) {
				var groups []string
				if token.Group == "auto" {
					groups = service.GetRequestAutoGroups(c, user.Group)
				} else {
					groups = []string{common.GetContextKeyString(c, constant.ContextKeyUsingGroup)}
				}
				discovered, err := getRoutableModelsForGroups(&model.UserBase{Group: user.Group}, groups, nil)
				require.NoError(t, err)
				c.JSON(http.StatusOK, discovered)
			})
			req := httptest.NewRequest(http.MethodGet, "/runtime", nil)
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			runtime := httptest.NewRecorder()
			router.ServeHTTP(runtime, req)
			if tc.runtimeAllowed {
				require.Equal(t, http.StatusOK, runtime.Code, runtime.Body.String())
				var actual []string
				require.NoError(t, common.Unmarshal(runtime.Body.Bytes(), &actual))
				assert.ElementsMatch(t, tc.models, actual)
			} else {
				assert.Equal(t, http.StatusForbidden, runtime.Code, runtime.Body.String())
				assert.NotContains(t, runtime.Body.String(), "gpt-")
			}
		})
	}
}

func TestGroupPermissionConflictDirectAndBulk(t *testing.T) {
	const key = "group_ratio_setting.group_special_usable_group"
	for _, mode := range []string{"direct", "bulk", "rewrite"} {
		t.Run(mode, func(t *testing.T) {
			db := setupGroupRenameControllerTest(t)
			require.NoError(t, db.Create(&model.Option{Key: "GroupRatio", Value: `{"a":1,"default":1}`}).Error)
			conflicting := `{"default":{"+:a":"Visible","-:a":"Hidden"}}`
			baseline := `{"default":{}}`
			if mode == "rewrite" {
				baseline = conflicting
			}
			require.NoError(t, db.Create(&model.Option{Key: key, Value: baseline}).Error)
			switch mode {
			case "direct":
				ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/option/", map[string]any{"key": key, "value": conflicting}, 1)
				UpdateOption(ctx)
				assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			case "bulk":
				err := model.UpdateOptionsBulk(map[string]string{key: conflicting, "MaxTokenAutoGroups": "6"})
				require.Error(t, err)
			case "rewrite":
				_, err := model.RenameGroups([]model.GroupRenameMapping{{From: "a", To: "renamed"}})
				require.Error(t, err)
			}
			var option model.Option
			require.NoError(t, db.Where(&model.Option{Key: key}).First(&option).Error)
			assert.Equal(t, baseline, option.Value)
			option = model.Option{}
			require.NoError(t, db.Where(&model.Option{Key: "GroupRatio"}).First(&option).Error)
			assert.JSONEq(t, `{"a":1,"default":1}`, option.Value)
		})
	}
}

func TestTokenGroupPermissionStatusOnly(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	configureGroupPermissionScope(t, false)
	token := seedTokenModelsToken(t, db, user.Id, "legacy-unauthorized", "b", nil)
	for _, status := range []int{common.TokenStatusDisabled, common.TokenStatusEnabled} {
		ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/token/?status_only=true", map[string]any{"id": token.Id, "status": status, "group": "a"}, user.Id)
		UpdateToken(ctx)
		response := decodeAPIResponse(t, rec)
		assert.Equal(t, status == common.TokenStatusDisabled, response.Success, response.Message)
		require.NoError(t, db.First(token, token.Id).Error)
		assert.Equal(t, common.TokenStatusDisabled, token.Status)
		assert.Equal(t, "b", token.Group)
	}
}

func TestTokenGroupPermissionOmittedSnapshotPreservesStoredScope(t *testing.T) {
	db, user := setupTokenModelsControllerTest(t)
	configureGroupPermissionScope(t, true)
	token := seedTokenModelsToken(t, db, user.Id, "legacy-auto", "auto", []string{"a", "b"})
	request := baseAutoTokenRequest("updated")
	request["id"] = token.Id
	ctx, rec := newAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	require.True(t, decodeAPIResponse(t, rec).Success)
	require.NoError(t, db.First(token, token.Id).Error)
	assert.JSONEq(t, `["a","b"]`, token.AutoGroups)
	groups, err := service.GetTokenEffectiveGroups(user.Group, token)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, groups)
	request["auto_groups"] = []string{"a", "b"}
	request["name"] = "must-not-persist"
	ctx, rec = newAuthenticatedContext(t, http.MethodPut, "/api/token/", request, user.Id)
	UpdateToken(ctx)
	require.False(t, decodeAPIResponse(t, rec).Success)
	require.NoError(t, db.First(token, token.Id).Error)
	assert.Equal(t, "updated", token.Name)
	assert.JSONEq(t, `["a","b"]`, token.AutoGroups)
}

func TestGroupPermissionEmptyRulesRoundTripAndHistoricalConflict(t *testing.T) {
	db := setupGroupRenameControllerTest(t)
	const key = "group_ratio_setting.group_special_usable_group"
	const original = `{"empty1":{},"empty2":{},"empty3":{},"empty4":{},"empty5":{},"empty6":{}}`
	const updated = `{"empty1":{},"empty2":{},"empty3":{},"empty4":{},"empty5":{},"empty6":{},"enterprise":{"+:default":"Default"}}`
	require.NoError(t, db.Create(&model.Option{Key: key, Value: original}).Error)
	_, err := model.ApplyGroupRename(model.GroupRenameApplyRequest{Options: map[string]string{key: updated}, Expected: map[string]string{key: original}})
	require.NoError(t, err)
	var option model.Option
	require.NoError(t, db.Where(&model.Option{Key: key}).First(&option).Error)
	assert.Equal(t, updated, option.Value)
	// Unrelated writes do not normalize or silently expand an old ambiguous rule.
	const legacy = `{"enterprise":{"+:default":"Default","-:default":"Hidden"}}`
	require.NoError(t, db.Model(&model.Option{}).Where(&model.Option{Key: key}).Update("value", legacy).Error)
	_, err = model.ApplyGroupRename(model.GroupRenameApplyRequest{Options: map[string]string{"MaxTokenAutoGroups": "4"}})
	require.NoError(t, err)
	require.NoError(t, db.Where(&model.Option{Key: key}).First(&option).Error)
	assert.Equal(t, legacy, option.Value)
}
