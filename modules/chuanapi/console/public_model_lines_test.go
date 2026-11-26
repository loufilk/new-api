package console

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPublicModelLinesDoNotMergeEqualRatiosAndWithdrawLive(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousUsable, previousRates := setting.UserUsableGroups2JSONString(), ratio_setting.GroupRatio2JSONString()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	db, err := gorm.Open(sqlite.Open("file:public-model-lines?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousUsable))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousRates))
		_ = db.Where("key = ?", "GroupCatalogMetadata").Delete(&model.Option{}).Error
		_ = model.LoadOptionsFromDatabase()
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMainType, previousLogType)
		model.InvalidatePricingCache()
		connection, err := db.DB()
		if err == nil {
			_ = connection.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.Model{}, &model.Vendor{}, &model.Option{}))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"real-one":"One","real-two":"Two","customer-only":"Customer","compat-test":"Test"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"real-one":1,"real-two":1,"customer-only":1,"compat-test":1}`))
	require.NoError(t, db.Create(&model.Option{Key: "GroupCatalogMetadata", Value: `{"customer-only":{"exclusive":true}}`}).Error)
	require.NoError(t, model.LoadOptionsFromDatabase())

	groupNames := []string{"real-one", "real-two", "customer-only", "compat-test"}
	labels := []string{"Standard US", "Gemini direct", "Customer label", "Test label"}
	ids := make([]string, 0, len(groupNames))
	channelTypes := []int{constant.ChannelTypeOpenAI, constant.ChannelTypeGemini, constant.ChannelTypeOpenAI, constant.ChannelTypeOpenAI}
	config := make(map[string]model.PublicModelLineRecord)
	for i, group := range groupNames {
		id := fmt.Sprintf("line-%032x", i+1)
		ids = append(ids, id)
		config[group] = model.PublicModelLineRecord{
			ID: id, PublishedAt: time.Now().Unix(),
			PublicModelLineSettings: model.PublicModelLineSettings{Label: labels[i]},
		}
		require.NoError(t, db.Create(&model.Channel{
			Id: 700 + i, Type: channelTypes[i], Status: common.ChannelStatusEnabled, Name: "internal-upstream", Key: "never-output", Models: "published-model", Group: model.ChannelGroupList(group),
		}).Error)
		require.NoError(t, db.Create(&model.Ability{Group: group, Model: "published-model", ChannelId: 700 + i, Enabled: true}).Error)
	}
	putConfig := func() {
		t.Helper()
		raw, err := common.Marshal(config)
		require.NoError(t, err)
		require.NoError(t, db.Save(&model.Option{Key: "GroupCatalogPublicLines", Value: string(raw)}).Error)
	}
	putConfig()
	model.InvalidatePricingCache()
	gin.SetMode(gin.TestMode)
	routes := gin.New()
	routes.GET("/public-api/model-lines", GetPublicModelLines)
	doRequest := func() (publishedModelCatalog, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/public-api/model-lines", nil)
		req.Header.Set("Authorization", "Bearer irrelevant")
		req.Header.Set("Cookie", "private=irrelevant")
		recorder := httptest.NewRecorder()
		routes.ServeHTTP(recorder, req)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		assert.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
		var catalog publishedModelCatalog
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &catalog))
		return catalog, recorder.Body.String()
	}
	first, wire := doRequest()
	require.Len(t, first.Lines, 2)
	require.Len(t, first.Models, 1)
	assert.ElementsMatch(t, ids[:2], first.Models[0].LineIDs)
	assert.Contains(t, first.Models[0].EndpointType[ids[0]], constant.EndpointTypeOpenAI)
	assert.Contains(t, first.Models[0].EndpointType[ids[1]], constant.EndpointTypeGemini)
	assert.NotContains(t, first.Models[0].EndpointType[ids[0]], constant.EndpointTypeGemini)
	for _, secret := range append(append(groupNames, labels[2:]...), "internal-upstream", "never-output") {
		assert.NotContains(t, wire, secret)
	}
	assert.NotContains(t, performPublicPricingRequest(t, nil).Body.String(), labels[0])
	delete(config, groupNames[0])
	putConfig()
	second, wire := doRequest()
	require.Len(t, second.Lines, 1)
	assert.Equal(t, ids[1], second.Lines[0].ID)
	assert.NotContains(t, wire, ids[0])
	require.NoError(t, db.Create(&model.Model{ModelName: "published-model", Status: 1}).Error)
	require.NoError(t, db.Model(&model.Model{}).Where("model_name = ?", "published-model").Update("status", 0).Error)
	third, wire := doRequest()
	assert.Empty(t, third.Lines)
	assert.NotContains(t, strings.TrimSpace(wire), "published-model")
}
