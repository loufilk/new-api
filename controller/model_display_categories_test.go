package controller

import (
	"bytes"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestModelDisplayCategoriesPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to run the PostgreSQL display category contract")
	}
	for _, mode := range []string{"fresh", "upgrade"} {
		t.Run(mode, func(t *testing.T) {
			db := modelManagementDB(t, "postgres", dsn)
			if mode == "upgrade" {
				// Representative pre-feature schema: identical Model table,
				// except the newly introduced nullable TEXT column is absent.
				require.NoError(t, db.Migrator().DropColumn(&model.Model{}, "DisplayCategories"))
			}
			existing := model.Model{ModelName: "synthetic-display-category", Status: 1, SyncOfficial: 1,
				InputModalities: `["text","image"]`, OutputModalities: `["text"]`, Capabilities: `["vision"]`,
				ContextLength: common.GetPointer(128000), MaxOutputTokens: common.GetPointer(4096),
				KnowledgeCutoff: "2025-01", ReleaseDate: "2025-06", ParameterCount: "8B", BonusEligible: common.GetPointer(false)}
			require.NoError(t, db.Omit("DisplayCategories").Create(&existing).Error)
			var indexesBefore []string
			require.NoError(t, db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'models' ORDER BY indexname").Scan(&indexesBefore).Error)
			require.NoError(t, db.AutoMigrate(&model.Model{}))
			var migrations bytes.Buffer
			require.NoError(t, db.Session(&gorm.Session{Logger: logger.New(log.New(&migrations, "", 0), logger.Config{LogLevel: logger.Info})}).AutoMigrate(&model.Model{}))
			assert.NotContains(t, strings.ToUpper(migrations.String()), "ALTER TABLE", "second migration must not repeat DDL")
			var indexesAfter []string
			require.NoError(t, db.Raw("SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'models' ORDER BY indexname").Scan(&indexesAfter).Error)
			assert.Equal(t, indexesBefore, indexesAfter)
			var loaded model.Model
			require.NoError(t, db.First(&loaded, existing.Id).Error)
			assert.Equal(t, existing, loaded, "migration must not alter pre-existing metadata")
			require.Nil(t, loaded.DisplayCategories)
			channel := model.Channel{Id: 501, Name: "synthetic-display-channel", Models: existing.ModelName, Group: "default", Status: common.ChannelStatusEnabled, Type: constant.ChannelTypeOpenAI}
			require.NoError(t, db.Create(&channel).Error)
			require.NoError(t, channel.AddAbilities(db))
			beforePricing := model.GetPricing()
			require.Len(t, beforePricing, 1)
			beforeEndpoints := model.GetModelSupportEndpointTypes(existing.ModelName)

			payload := map[string]any{}
			encoded, err := common.Marshal(existing)
			require.NoError(t, err)
			require.NoError(t, common.Unmarshal(encoded, &payload))
			payload["display_categories"] = []string{"video", "images", "images"}
			var response struct {
				Success bool
				Data    model.Model
				Message string
			}
			modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", payload, &response)
			require.True(t, response.Success, response.Message)
			assert.Equal(t, model.ModelDisplayCategories{"images", "video"}, response.Data.DisplayCategories)
			require.NoError(t, db.First(&loaded, existing.Id).Error)
			want := existing
			want.UpdatedTime = loaded.UpdatedTime
			want.DisplayCategories = model.ModelDisplayCategories{"images", "video"}
			assert.Equal(t, want, loaded, "a display-only save preserves all source metadata, including bonus eligibility")
			afterPricing := model.GetPricing()
			require.Len(t, afterPricing, 1)
			assert.Equal(t, want.DisplayCategories, afterPricing[0].DisplayCategories)
			afterPricing[0].DisplayCategories = nil
			assert.Equal(t, beforePricing, afterPricing, "display override must not affect pricing, capabilities or protocols")
			assert.Equal(t, beforeEndpoints, model.GetModelSupportEndpointTypes(existing.ModelName))

			for _, omitted := range []bool{true, false} {
				if omitted {
					delete(payload, "display_categories")
				} else {
					payload["display_categories"] = nil
				}
				modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", payload, &response)
				require.True(t, response.Success, response.Message)
				assert.Equal(t, want.DisplayCategories, response.Data.DisplayCategories, "omitted and null preserve existing override")
			}
			// Catalog synchronization must leave this local-only field alone.
			require.NoError(t, db.First(&loaded, existing.Id).Error)
			_, err = model.ApplyMetadataSync([]model.MetadataSyncUpdate{{MetadataSyncSelection: model.MetadataSyncSelection{
				ModelName: existing.ModelName, RecordVersion: model.MetadataRecordVersion(&loaded, nil, nil), Fields: []string{"description"}}, Values: model.MetadataValues{Description: "synced description", Status: 1}}}, nil)
			require.NoError(t, err)
			require.NoError(t, db.First(&loaded, existing.Id).Error)
			assert.Equal(t, want.DisplayCategories, loaded.DisplayCategories)
			assert.Equal(t, "synced description", loaded.Description)

			for _, invalid := range []any{[]string{"all"}, []string{"unknown"}, []string{"text", "images", "video", "audio", "vectors", "decision", "text"}, "text", []int{1}} {
				payload["display_categories"] = invalid
				modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", payload, &response)
				assert.False(t, response.Success)
				require.NoError(t, db.First(&loaded, existing.Id).Error)
				assert.Equal(t, want.DisplayCategories, loaded.DisplayCategories, "rejected updates cannot clear or change override")
			}
			payload["display_categories"] = []string{}
			modelManagementRequest(t, UpdateModelMeta, http.MethodPut, "/api/models/", payload, &response)
			require.True(t, response.Success, response.Message)
			require.NoError(t, db.First(&loaded, existing.Id).Error)
			assert.Empty(t, loaded.DisplayCategories)
			assert.Empty(t, model.GetPricing()[0].DisplayCategories)
			assert.Equal(t, []string{"text"}, model.GetPricing()[0].OutputModalities)
			// Corrupt persisted text cannot break reads or invent capabilities.
			require.NoError(t, db.Model(&model.Model{}).Where("id = ?", existing.Id).Update("display_categories", `["all"]`).Error)
			require.NoError(t, db.First(&loaded, existing.Id).Error)
			assert.Nil(t, loaded.DisplayCategories)
			model.RefreshPricing()
			assert.Empty(t, model.GetPricing()[0].DisplayCategories)

			create := map[string]any{"model_name": "synthetic-new-display", "status": 1, "display_categories": []string{"audio", "vectors", "decision"}}
			modelManagementRequest(t, CreateModelMeta, http.MethodPost, "/api/models/", create, &response)
			require.True(t, response.Success, response.Message)
			var created model.Model
			require.NoError(t, db.First(&created, response.Data.Id).Error)
			assert.Equal(t, model.ModelDisplayCategories{"audio", "vectors", "decision"}, created.DisplayCategories)
			create["model_name"], create["display_categories"] = "synthetic-invalid-create", []string{"all"}
			modelManagementRequest(t, CreateModelMeta, http.MethodPost, "/api/models/", create, &response)
			assert.False(t, response.Success)
			var count int64
			require.NoError(t, db.Model(&model.Model{}).Where("model_name = ?", "synthetic-invalid-create").Count(&count).Error)
			assert.Zero(t, count)

			// GetModelMeta has path parameters; exercise the real detail handler.
			recorder := modelManagementRequest(t, func(c *gin.Context) {
				c.Params = []gin.Param{{Key: "id", Value: strconv.Itoa(existing.Id)}}
				GetModelMeta(c)
			}, http.MethodGet, "/api/models/1", nil, &response)
			assert.Equal(t, http.StatusOK, recorder.Code)
			require.True(t, response.Success, response.Message)
		})
	}
}
