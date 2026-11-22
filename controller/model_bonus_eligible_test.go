package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupBonusEligibleTestEnv 初始化测试 DB 与上下文（管理员身份）。
func setupBonusEligibleTestEnv(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()

	setupModelListControllerTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.ModelBonusEligibleAudit{}, &model.Log{}))
	model.InvalidateModelBonusEligibleCache()

	// 同一测试内多次调用共享同一内存库，先清空保证幂等
	require.NoError(t, model.DB.Exec("DELETE FROM models").Error)
	require.NoError(t, model.DB.Exec("DELETE FROM model_bonus_eligible_audits").Error)
	require.NoError(t, model.DB.Create(&model.Model{Id: 1, ModelName: "ctrl-model-a", Status: 1, SyncOfficial: 1}).Error)
	require.NoError(t, model.DB.Create(&model.Model{Id: 2, ModelName: "ctrl-model-b", Status: 1, SyncOfficial: 1}).Error)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 7)
	c.Set("username", "admin-test")
	return c, w
}

func TestBatchUpdateModelBonusEligibleAPI(t *testing.T) {
	c, w := setupBonusEligibleTestEnv(t)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/models/bonus_eligible/batch",
		strings.NewReader(`{"model_ids":[1,2],"bonus_eligible":true,"idempotency_key":"ctrl-batch-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")

	walletmodule.BatchUpdateModelBonusEligible(c)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Changed int `json:"changed"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, 2, resp.Data.Changed)

	// 独立审计记录已写入（operator / model / before / after / idempotency key / created_at）
	var audits []model.ModelBonusEligibleAudit
	require.NoError(t, model.DB.Order("id asc").Find(&audits).Error)
	require.Len(t, audits, 2)
	assert.Equal(t, 7, audits[0].OperatorId)
	assert.Equal(t, "admin-test", audits[0].OperatorName)
	assert.Equal(t, "ctrl-model-a", audits[0].ModelName)
	assert.Nil(t, audits[0].Before)
	assert.NotNil(t, audits[0].After)
	assert.True(t, *audits[0].After)
	assert.Equal(t, "ctrl-batch-1:1", audits[0].IdempotencyKey)
	assert.NotZero(t, audits[0].CreatedAt)

	// 无 model_ids / 幂等键缺失被拒绝
	c2, w2 := setupBonusEligibleTestEnv(t)
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/models/bonus_eligible/batch",
		strings.NewReader(`{"model_ids":[],"bonus_eligible":true}`))
	c2.Request.Header.Set("Content-Type", "application/json")
	walletmodule.BatchUpdateModelBonusEligible(c2)
	require.Equal(t, http.StatusOK, w2.Code)
	var respBad struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(w2.Body.Bytes(), &respBad))
	assert.False(t, respBad.Success)
}

func TestBatchUpdateModelBonusEligibleIdempotentReplayAPI(t *testing.T) {
	_, w := setupBonusEligibleTestEnv(t)
	_ = w
	// 直接经模型层重放同键请求，验证幂等
	changed, err := model.BatchUpdateModelBonusEligible([]int{1}, boolPtr(true), 7, "admin-test", "ctrl-batch-replay")
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	changed, err = model.BatchUpdateModelBonusEligible([]int{1}, boolPtr(true), 7, "admin-test", "ctrl-batch-replay")
	require.NoError(t, err)
	assert.Equal(t, 0, changed)
	var count int64
	require.NoError(t, model.DB.Model(&model.ModelBonusEligibleAudit{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestGetModelBonusEligibleAuditsAPI(t *testing.T) {
	c, w := setupBonusEligibleTestEnv(t)

	_, err := model.BatchUpdateModelBonusEligible([]int{1, 2}, boolPtr(true), 7, "admin-test", "ctrl-audit-1")
	require.NoError(t, err)
	_, err = model.BatchUpdateModelBonusEligible([]int{1}, boolPtr(false), 7, "admin-test", "ctrl-audit-2")
	require.NoError(t, err)

	c.Request = httptest.NewRequest(http.MethodGet, "/api/models/bonus_eligible/audits?model_id=1&page=1&page_size=10", nil)
	walletmodule.GetModelBonusEligibleAudits(c)

	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Items []model.ModelBonusEligibleAudit `json:"items"`
			Total int64                           `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, int64(2), resp.Data.Total)
	require.Len(t, resp.Data.Items, 2)
	// 倒序：最近的在前
	assert.Equal(t, "ctrl-audit-2:1", resp.Data.Items[0].IdempotencyKey)
	assert.Equal(t, "ctrl-model-a", resp.Data.Items[0].ModelName)

	// 按操作者过滤（复用同一测试库，不重置）
	recorder2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(recorder2)
	c2.Set("id", 7)
	c2.Set("username", "admin-test")
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/models/bonus_eligible/audits?operator_id=7", nil)
	walletmodule.GetModelBonusEligibleAudits(c2)
	var resp2 struct {
		Success bool `json:"success"`
		Data    struct {
			Total int64 `json:"total"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder2.Body.Bytes(), &resp2))
	assert.True(t, resp2.Success)
	assert.Equal(t, int64(3), resp2.Data.Total)
}

// boolPtr 测试辅助：控制器测试中构造 *bool。
func boolPtr(v bool) *bool { return &v }
