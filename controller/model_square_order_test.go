package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelSquareOrderPatchPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN is not configured")
	}
	db := modelManagementDB(t, "postgres", dsn)
	require.NoError(t, db.AutoMigrate(&model.SystemSyncTopic{}, &model.SystemTask{}))
	original := operation_setting.ModelSquareSettingSnapshot()
	t.Cleanup(func() { *operation_setting.GetModelSquareSetting() = original })

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("role", common.RoleRootUser)
		c.Next()
	})
	const endpoint = "/api/option/model-square-order"
	engine.GET(endpoint, GetModelSquareOrderOptions)
	engine.PATCH(endpoint, PatchModelSquareOrderOptions)
	engine.PUT("/api/option/", UpdateOption)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	read := func() operation_setting.ModelSquareSetting {
		rec := request(http.MethodGet, endpoint, "")
		require.Equal(t, http.StatusOK, rec.Code)
		var response struct {
			Success bool                                 `json:"success"`
			Data    operation_setting.ModelSquareSetting `json:"data"`
		}
		require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &response), rec.Body.String())
		require.True(t, response.Success, rec.Body.String())
		return response.Data
	}
	write := func(body string, status int, ok bool) {
		rec := request(http.MethodPatch, endpoint, body)
		require.Equal(t, status, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), map[bool]string{true: `"success":true`, false: `"success":false`}[ok])
	}

	require.NoError(t, db.Create(&model.Option{Key: "UnrelatedFixture", Value: "preserve me"}).Error)
	want := operation_setting.DefaultModelSquareSetting()
	assert.Equal(t, want, read())
	old := request(http.MethodPut, "/api/option/", `{"key":"model_square_setting.sort_models","value":"[\"alpha\"]"}`)
	require.Contains(t, old.Body.String(), `"success":true`)
	want.SortModels = []string{"alpha"}
	assert.Equal(t, want, read())

	for _, invalid := range []string{
		`{"changes":{"sort_mode":"mixed","UnrelatedFixture":"overwrite"},"expected":{"sort_mode":"manual","UnrelatedFixture":"preserve me"}}`,
		`{"changes":{"sort_mode":"mixed","sort_metric":"cost"},"expected":{"sort_mode":"manual","sort_metric":"tokens"}}`,
		`{"changes":{"sort_mode":"mixed","sort_days":3},"expected":{"sort_mode":"manual","sort_days":7}}`,
		`{"changes":{"sort_models":[null]},"expected":{"sort_models":["alpha"]}}`,
		`{"changes":{"sort_mode":"mixed"},"expected":{"sort_metric":"tokens"}}`,
		`{"changes":{"sort_mode":"mixed"},"expected":{"sort_mode":"manual"},"extra":true}`,
	} {
		write(invalid, http.StatusBadRequest, false)
		assert.Equal(t, want, read(), invalid)
	}

	// Reject the second of two valid application-level writes at the database.
	// This distinguishes a true transaction rollback from request validation.
	require.NoError(t, db.Exec(`ALTER TABLE options ADD CONSTRAINT square_fixture_reject CHECK ("key" <> 'model_square_setting.sort_models' OR value <> '["reject"]')`).Error)
	write(`{"changes":{"sort_mode":"mixed","sort_models":["reject"]},"expected":{"sort_mode":"manual","sort_models":["alpha"]}}`, http.StatusInternalServerError, false)
	assert.Equal(t, want, read())
	assert.Equal(t, want, operation_setting.ModelSquareSettingSnapshot())

	// Another editor has committed a pin change after the first editor opened.
	// Reject both writes, including the valid mode written first in key order.
	write(`{"changes":{"sort_models":["gamma"]},"expected":{"sort_models":["alpha"]}}`, http.StatusOK, true)
	want.SortModels = []string{"gamma"}
	write(`{"changes":{"sort_mode":"mixed","sort_models":["beta"]},"expected":{"sort_mode":"manual","sort_models":["alpha"]}}`, http.StatusConflict, false)
	assert.Equal(t, want, read())
	assert.Equal(t, want, operation_setting.ModelSquareSettingSnapshot())

	write(`{"changes":{"sort_mode":"mixed","sort_metric":"requests"},"expected":{"sort_mode":"manual","sort_metric":"tokens"}}`, http.StatusOK, true)
	want.SortMode, want.SortMetric = "mixed", "requests"
	assert.Equal(t, want, read())
	assert.Equal(t, want, operation_setting.ModelSquareSettingSnapshot())

	var other model.Option
	require.NoError(t, db.Where(`"key" = ?`, "UnrelatedFixture").First(&other).Error)
	assert.Equal(t, "preserve me", other.Value)
}
