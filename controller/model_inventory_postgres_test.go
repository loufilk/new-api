package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
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

func TestPublicModelInventoryRequiresLiveChannelsPostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires owned PostgreSQL inventory fixture")
	}
	db := setupModelListControllerTestDB(t)
	withSelfUseModeEnabled(t)
	for _, channel := range []model.Channel{
		{Id: 101, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled},
		{Id: 102, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusManuallyDisabled},
		{Id: 103, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusAutoDisabled},
	} {
		channel.Key, channel.Name = "synthetic-private-channel-key", "private-channel-name"
		require.NoError(t, db.Create(&channel).Error)
	}
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "default", Model: "public-live", ChannelId: 101, Enabled: true},
		{Group: "default", Model: "public-live", ChannelId: 102, Enabled: true},
		{Group: "default", Model: "manual-disabled", ChannelId: 102, Enabled: true},
		{Group: "default", Model: "auto-disabled", ChannelId: 103, Enabled: true},
		{Group: "default", Model: "missing-channel", ChannelId: 999, Enabled: true},
		{Group: "default", Model: "disabled-ability", ChannelId: 101, Enabled: false},
		{Group: "private", Model: "private-model", ChannelId: 101, Enabled: true},
	}).Error)
	contextFor := func(name string) (*gin.Context, *httptest.ResponseRecorder) {
		out := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(out)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models/"+name, nil)
		c.Params = gin.Params{{Key: "model", Value: name}}
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
		return c, out
	}
	for _, modelType := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAnthropic, constant.ChannelTypeGemini} {
		c, out := contextFor("")
		ListModels(c, modelType)
		require.Equal(t, http.StatusOK, out.Code)
		var body map[string]any
		require.NoError(t, common.Unmarshal(out.Body.Bytes(), &body))
		field := "data"
		if modelType == constant.ChannelTypeGemini {
			field = "models"
		}
		require.Len(t, body[field], 1)
		assert.Contains(t, out.Body.String(), "public-live")
		for _, hidden := range []string{"manual-disabled", "auto-disabled", "missing-channel", "disabled-ability", "private-model"} {
			assert.NotContains(t, out.Body.String(), hidden)
			c, detail := contextFor(hidden)
			RetrieveModel(c, modelType)
			assert.Equal(t, http.StatusNotFound, detail.Code, hidden)
			assert.Contains(t, detail.Body.String(), "model_not_found")
		}
		assert.NotContains(t, out.Body.String(), "private-channel")
	}
	// A channel disable must affect the next list/detail even if its ability is stale.
	require.NoError(t, db.Model(&model.Channel{}).Where("id = ?", 101).Update("status", common.ChannelStatusManuallyDisabled).Error)
	c, out := contextFor("")
	ListModels(c, constant.ChannelTypeOpenAI)
	assert.Empty(t, decodeListModelsPayload(t, out).Data)
	c, out = contextFor("public-live")
	RetrieveModel(c, constant.ChannelTypeOpenAI)
	assert.Equal(t, http.StatusNotFound, out.Code)
}

func TestPublicModelInventoryPreservesScopePricingAndOrderPostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires owned PostgreSQL inventory fixture")
	}
	db, _ := setupTokenModelsControllerTest(t)
	withSelfUseModeDisabled(t)
	originalRatios := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(originalRatios)) })
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"b-only":1,"shared":1,"a-only":1,"alias-model":1,"alias-model@thinking:on":1,"private-model":1,"not-allowed":1}`))
	for _, entry := range []struct{ group, name string }{
		{"a", "a-only"}, {"a", "shared"}, {"b", "b-only"}, {"b", "shared"},
		{"b", "alias-model@thinking:on"}, {"b", "unpriced-model"}, {"b", "not-allowed"}, {"private", "private-model"},
	} {
		seedTokenModelsAbility(t, db, entry.group, entry.name, constant.ChannelTypeOpenAI, true)
	}
	contextFor := func(smart bool, snapshot []string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "auto")
		common.SetContextKey(c, constant.ContextKeyTokenSmartRouting, smart)
		common.SetContextKey(c, constant.ContextKeyTokenAutoGroups, snapshot)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{
			"a-only": true, "b-only": true, "shared": true, "alias-model": true,
			"unpriced-model": true, "private-model": true,
		})
		return c
	}
	manual, err := visibleModelNames(contextFor(false, []string{"b", "a"}))
	require.NoError(t, err)
	require.Len(t, manual, 4)
	assert.ElementsMatch(t, []string{"b-only", "shared", "alias-model@thinking:on"}, manual[:3])
	assert.Equal(t, "a-only", manual[3])
	smart, err := visibleModelNames(contextFor(true, []string{"a"}))
	require.NoError(t, err)
	assert.ElementsMatch(t, manual, smart, "smart uses all authorized groups, not the manual draft")
	empty, err := visibleModelNames(contextFor(false, []string{}))
	require.NoError(t, err)
	assert.Empty(t, empty)
	// A group removed from selectable scope must disappear on the next read.
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","a":"A"}`))
	revoked, err := visibleModelNames(contextFor(true, []string{"b"}))
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"a-only", "shared"}, revoked)
}

func TestPublicModelInventoryDatabaseFailureIsNotEmptySuccessPostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" || os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires owned PostgreSQL inventory fixture")
	}
	db := setupModelListControllerTestDB(t)
	withSelfUseModeEnabled(t)
	// Drop only this test's isolated schema table, not a shared database fixture.
	require.NoError(t, db.Migrator().DropTable(&model.Ability{}))
	for _, detail := range []bool{false, true} {
		out := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(out)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/models/public-live", nil)
		c.Params = gin.Params{{Key: "model", Value: "public-live"}}
		common.SetContextKey(c, constant.ContextKeyTokenGroup, "default")
		if detail {
			RetrieveModel(c, constant.ChannelTypeOpenAI)
			assert.Equal(t, http.StatusInternalServerError, out.Code)
			assert.Contains(t, out.Body.String(), "model_lookup_failed")
		} else {
			ListModels(c, constant.ChannelTypeOpenAI)
			var body map[string]any
			require.NoError(t, common.Unmarshal(out.Body.Bytes(), &body))
			assert.Equal(t, false, body["success"])
			assert.NotContains(t, body, "data")
		}
		assert.NotContains(t, out.Body.String(), "SQLSTATE")
		assert.NotContains(t, out.Body.String(), "abilities")
	}
}
