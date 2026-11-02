package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Each fixture has a different primary schema. Production group versions are
// monotonic, so isolate their runtime state rather than permitting a rollback.
func modelTestGroupRenameIsolatedProcess(t *testing.T) bool {
	t.Helper()
	require.Equal(t, "1", os.Getenv("NO_AGENT_FIXTURE_OWNED"))
	require.NotEmpty(t, os.Getenv("TEST_POSTGRES_DSN"))
	if os.Getenv("MODEL_TEST_GROUP_JOINT_CHILD") == t.Name() {
		return false
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.count=1", "-test.timeout=3m")
	cmd.Env = append(os.Environ(), "MODEL_TEST_GROUP_JOINT_CHILD="+t.Name())
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	t.Log(string(output))
	return true
}

func setupModelTestGroupRename(t *testing.T) (*gorm.DB, int, *gin.Engine) {
	t.Helper()
	db, userID := setupModelTestSwitchExecution(t)
	require.NoError(t, db.AutoMigrate(&model.GroupRenameOperation{}, &model.UserSession{}, &model.AuditLog{}, &model.UserCallInterceptionAudit{}))
	for key, value := range map[string]string{
		"GroupRatio":       `{"old":0.25,"premium":2}`,
		"UserUsableGroups": `{"old":"Old","premium":"Premium"}`,
		"group_ratio_setting.group_special_usable_group": `{"old":{"-:premium":"denied"}}`,
		"GroupConfigVersion":                             "1",
	} {
		require.NoError(t, db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&model.Option{Key: key, Value: value}).Error)
	}
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", userID).Update("group", "old").Error)
	var channel model.Channel
	require.NoError(t, db.Where("models = ?", "gpt-4o-mini").First(&channel).Error)
	require.NoError(t, db.Model(&channel).Update("group", "old,premium").Error)
	require.NoError(t, db.Model(&model.Ability{}).Where("channel_id = ?", channel.Id).Update("group", "old").Error)
	require.NoError(t, db.Create(&model.Ability{ChannelId: channel.Id, Group: "premium", Model: "gpt-4o-mini", Enabled: true}).Error)
	guard, err := model.BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	require.NoError(t, guard.Finish())
	model.InvalidatePricingCache()
	engine := gin.New()
	engine.Use(middleware.RequestId(), middleware.BodyStorageCleanup(), func(c *gin.Context) {
		guard, err := model.BeginGroupConfigRead(c.Request.Context())
		if !assert.NoError(t, err) {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		var user model.User
		require.NoError(t, db.First(&user, userID).Error)
		require.NoError(t, guard.Finish())
		c.Set("id", userID)
		c.Set(model.GroupConfigVersionContextKey, guard.Version)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, user.Group)
		common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
		c.Next()
	})
	engine.POST("/estimate", EstimateModelTest)
	engine.POST("/runs", PrepareModelTest, middleware.UserCallInterception(), middleware.Distribute(), ModelTestPlayground)
	return db, userID, engine
}

func modelTestGroupRenameQuotedRequest(t *testing.T, engine *gin.Engine, group, requestID string) ([]byte, int) {
	t.Helper()
	selection := modelTestSelection{Model: "gpt-4o-mini", Group: group, MaxOutputTokens: 2048}
	input, err := common.Marshal(selection)
	require.NoError(t, err)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/estimate", bytes.NewReader(input)))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var quote struct {
		Data struct {
			ConfirmationToken string `json:"confirmation_token"`
			EstimatedQuota    *int   `json:"estimated_quota"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &quote))
	require.NotEmpty(t, quote.Data.ConfirmationToken)
	require.NotNil(t, quote.Data.EstimatedQuota)
	body, err := common.Marshal(modelTestRequest{modelTestSelection: selection, ConfirmationToken: quote.Data.ConfirmationToken, Confirmed: true, RequestID: requestID})
	require.NoError(t, err)
	return body, *quote.Data.EstimatedQuota
}

func TestModelTestGroupRenamePostgresOldQuoteCannotUseNewScopeOrPrice(t *testing.T) {
	if modelTestGroupRenameIsolatedProcess(t) {
		return
	}
	db, userID, engine := setupModelTestGroupRename(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
	model.InvalidatePricingCache()
	body, before := modelTestGroupRenameQuotedRequest(t, engine, "old", uuid.NewString())
	require.Positive(t, before)
	_, err := model.ApplyGroupRename(model.GroupRenameApplyRequest{Mappings: []model.GroupRenameMapping{{From: "old", To: "new"}}, Options: map[string]string{"GroupRatio": `{"new":0.75,"premium":2}`}})
	require.NoError(t, err)
	for range 2 {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body)))
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "model_or_group_unavailable")
	}
	_, after := modelTestGroupRenameQuotedRequest(t, engine, "new", uuid.NewString())
	assert.Equal(t, before*3, after, "a new quote uses the renamed group's new price, not the old confirmation")
	denied := httptest.NewRecorder()
	engine.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/estimate", strings.NewReader(`{"model":"gpt-4o-mini","group":"premium","max_output_tokens":2048}`)))
	assert.Equal(t, http.StatusBadRequest, denied.Code, denied.Body.String())
	assert.Contains(t, denied.Body.String(), "model_or_group_unavailable", "the old exclusion must follow the owner, not broaden permission")
	for _, table := range []any{&model.ModelTestRun{}, &model.BillingFinalization{}, &model.BalanceLedger{}} {
		var count int64
		require.NoError(t, db.Model(table).Where("user_id = ?", userID).Count(&count).Error)
		assert.Zero(t, count)
	}
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	assert.Equal(t, 1_000_000, user.QuotaPaid)
	assert.Zero(t, calls.Load())
}

func TestModelTestGroupRenamePostgresDisabledHistorySurvivesNewerGroupSnapshot(t *testing.T) {
	if modelTestGroupRenameIsolatedProcess(t) {
		return
	}
	db, userID, _ := setupModelTestGroupRename(t)
	run := &model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "old", BenchmarkID: modelTestVersion, Status: "running", MaxOutputTokens: 2048, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	_, err := model.CreateModelTestRun(run)
	require.NoError(t, err)
	admitted, err := model.BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	require.NoError(t, admitted.Finish())
	_, err = model.ApplyGroupRename(model.GroupRenameApplyRequest{Mappings: []model.GroupRenameMapping{{From: "old", To: "new"}}})
	require.NoError(t, err)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", userID); c.Set(model.GroupConfigVersionContextKey, admitted.Version) })
	engine.GET("/history", GetModelTests)
	engine.POST("/runs/:id/cancel", CancelModelTest)
	history := httptest.NewRecorder()
	engine.ServeHTTP(history, httptest.NewRequest(http.MethodGet, "/history", nil))
	require.Equal(t, http.StatusOK, history.Code, history.Body.String())
	assert.Contains(t, history.Body.String(), run.ID)
	assert.Contains(t, history.Body.String(), `"enabled":false`)
	assert.Contains(t, history.Body.String(), `"eligible_models_by_group":{}`)
	for range 2 {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs/"+run.ID+"/cancel", nil))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"cancel_requested":true`)
	}
	saved, err := model.GetModelTestRun(userID, run.ID)
	require.NoError(t, err)
	assert.Equal(t, "old", saved.Group, "rename never rewrites historical pricing identity")
}

func TestModelTestGroupRenamePostgresEnabledHistorySurvivesUnavailableCatalogue(t *testing.T) {
	if modelTestGroupRenameIsolatedProcess(t) {
		return
	}
	db, userID, _ := setupModelTestGroupRename(t)
	run := &model.ModelTestRun{ID: uuid.NewString(), UserID: userID, Model: "gpt-4o-mini", Group: "old", BenchmarkID: modelTestVersion, Status: "running", MaxOutputTokens: 2048, CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()}
	_, err := model.CreateModelTestRun(run)
	require.NoError(t, err)
	enabled, err := model.ModelTestEnabled(t.Context())
	require.NoError(t, err)
	require.True(t, enabled, "this must exercise the enabled catalogue branch, not the disabled shortcut")
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "GroupConfigVersion").Update("value", "malformed-primary-version").Error)
	engine := gin.New()
	engine.GET("/history", func(c *gin.Context) {
		c.Set("id", userID)
		_, present := c.Get(model.GroupConfigVersionContextKey)
		require.False(t, present, "the history auth route deliberately has no routing admission snapshot")
		GetModelTests(c)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/history", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), run.ID)
	assert.Contains(t, response.Body.String(), `"enabled":false`)
	assert.Contains(t, response.Body.String(), `"eligible_models_by_group":{}`)
	assert.Contains(t, response.Body.String(), `"availability_error":"model_test_settings_unavailable"`)
}

func TestModelTestGroupRenamePostgresDisableAfterReservationRefundsFrozenAmountOnce(t *testing.T) {
	if modelTestGroupRenameIsolatedProcess(t) {
		return
	}
	db, userID, engine := setupModelTestGroupRename(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
	model.InvalidatePricingCache()
	id := uuid.NewString()
	body, _ := modelTestGroupRenameQuotedRequest(t, engine, "old", id)
	var cut atomic.Bool
	var reservedBalance int
	const callback = "test:joint-rename-disable-after-held"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "options" || cut.Load() {
			return
		}
		// Only the final feature authority read can inject the write. A group
		// version read may own the policy RLock and must not synchronously rename.
		where, ok := tx.Statement.Clauses["WHERE"].Expression.(clause.Where)
		if !ok || len(where.Exprs) != 1 {
			return
		}
		condition, ok := where.Exprs[0].(clause.Eq)
		if !ok || condition.Value != model.ModelTestEnabledKey {
			return
		}
		column, ok := condition.Column.(clause.Column)
		if !ok || column.Name != "key" {
			return
		}
		var held int64
		if !assert.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", userID, model.BillingHeld).Count(&held).Error) || held == 0 {
			return
		}
		if !cut.CompareAndSwap(false, true) {
			return
		}
		var user model.User
		require.NoError(t, db.First(&user, userID).Error)
		reservedBalance = user.QuotaPaid
		_, err := model.ApplyGroupRename(model.GroupRenameApplyRequest{Mappings: []model.GroupRenameMapping{{From: "old", To: "new"}}, Options: map[string]string{"GroupRatio": `{"new":99,"premium":2}`}})
		require.NoError(t, err)
		require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
	}))
	t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body)))
	require.True(t, cut.Load(), "the committed-hold/final-dispatch boundary must be reached")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, response.Body.String(), "model_tests_disabled")
	waitTaskSubmissionFixtureQuiescence(t)
	assert.Equal(t, 1_000_000-125, reservedBalance)
	assert.Zero(t, calls.Load())
	run, err := model.GetModelTestRun(userID, id)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status)
	assert.Equal(t, "old", run.Group)
	for range 2 {
		replay := httptest.NewRecorder()
		engine.ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/runs", bytes.NewReader(body)))
		require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	}
	var user model.User
	require.NoError(t, db.First(&user, userID).Error)
	assert.Equal(t, "new", user.Group)
	assert.Equal(t, 1_000_000, user.QuotaPaid)
	assert.Equal(t, user.QuotaPaid, user.Quota)
	var owners []model.BillingFinalization
	require.NoError(t, db.Where("user_id = ?", userID).Find(&owners).Error)
	require.Len(t, owners, 1)
	assert.Equal(t, model.BillingRefunded, owners[0].State)
	assert.Equal(t, run.RequestID, owners[0].RequestID)
	var ledger []model.BalanceLedger
	require.NoError(t, db.Where("user_id = ?", userID).Order("id").Find(&ledger).Error)
	require.Len(t, ledger, 2)
	assert.Equal(t, model.LedgerReasonBillingPreConsume, ledger[0].Reason)
	assert.Equal(t, -125, ledger[0].Delta)
	assert.Equal(t, model.LedgerReasonBillingRefund, ledger[1].Reason)
	assert.Equal(t, 125, ledger[1].Delta)
	assert.Zero(t, calls.Load(), "reconciliation must not reserve or dispatch again")
}
