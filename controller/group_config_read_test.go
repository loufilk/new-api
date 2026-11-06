package controller

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type groupGuardBodyReader struct {
	reader     io.Reader
	once       sync.Once
	beforeRead func()
}

func (r *groupGuardBodyReader) Read(p []byte) (int, error) {
	r.once.Do(r.beforeRead)
	return r.reader.Read(p)
}
func (*groupGuardBodyReader) Close() error { return nil }

// A process boundary isolates production monotonic-version state from other
// tests' disposable schemas. Production never accepts a version rollback.
func TestGroupConfigAdmissionHTTP(t *testing.T) {
	if os.Getenv("TEST_POSTGRES_DSN") == "" {
		t.Skip("requires owned PostgreSQL fixture")
	}
	if os.Getenv("GROUP_CONFIG_HTTP_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestGroupConfigAdmissionHTTP$", "-test.v", "-test.timeout=2m")
		cmd.Env = append(os.Environ(), "GROUP_CONFIG_HTTP_CHILD=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))
		t.Log(string(output))
		return
	}
	require.NoError(t, i18n.Init())
	setupFingerprintScoringFixture(t)
	db := setupGroupRenameControllerTest(t)
	writeOptions := func(values map[string]string) {
		for key, value := range values {
			require.NoError(t, db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&model.Option{Key: key, Value: value}).Error)
		}
	}
	writeOptions(map[string]string{model.ModelTestEnabledKey: "true", "GroupRatio": `{"old":0.25,"premium":2}`, "UserUsableGroups": `{"old":"Old","premium":"Premium"}`, "group_ratio_setting.group_special_usable_group": `{"old":{"-:premium":"denied"}}`, "TopupGroupRatio": `{"old":0.5}`})
	user := model.User{Username: "group-guard-owner", Password: "synthetic-hash", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Group: "old", Quota: 20000, AffCode: "guard-owner"}
	require.NoError(t, db.Create(&user).Error)
	key := strings.Repeat("g", 48)
	token := model.Token{UserId: user.Id, Key: key, Name: "unchanged", Group: "old", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000}
	require.NoError(t, db.Create(&token).Error)
	forbidden := model.Token{UserId: user.Id, Key: strings.Repeat("p", 48), Group: "premium", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10000}
	require.NoError(t, db.Create(&forbidden).Error)
	disabled := model.Token{UserId: user.Id, Key: strings.Repeat("d", 48), Group: "old", Status: common.TokenStatusDisabled, ExpiredTime: -1, RemainQuota: 10000}
	require.NoError(t, db.Create(&disabled).Error)
	router := gin.New()
	router.GET("/runtime", middleware.TokenAuth(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"group": common.GetContextKeyString(c, constant.ContextKeyUsingGroup)})
	})
	perform := func(key string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/runtime", nil)
		req.Header.Set("Authorization", "Bearer sk-"+key)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	before := perform(key)
	require.Equal(t, http.StatusOK, before.Code, before.Body.String())
	require.Equal(t, http.StatusForbidden, perform(forbidden.Key).Code)
	// Simulate another node's committed rename with its Pub/Sub event lost.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("group", "new").Error)
	require.NoError(t, db.Model(&model.Token{}).Where("id IN ?", []int{token.Id, disabled.Id}).Update("group", "new").Error)
	writeOptions(map[string]string{"GroupRatio": `{"new":0.25,"premium":2}`, "UserUsableGroups": `{"new":"New","premium":"Premium"}`, "group_ratio_setting.group_special_usable_group": `{"new":{"-:premium":"denied"}}`, "TopupGroupRatio": `{"new":0.5}`, "GroupConfigVersion": "1"})
	require.NoError(t, service.ValidateTokenGroup("new", "premium"), "repro: the stale special policy incorrectly permits the renamed owner")
	denied := perform(forbidden.Key)
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	good := perform(key)
	require.Equal(t, http.StatusOK, good.Code, good.Body.String())
	assert.Contains(t, good.Body.String(), "new")
	assert.NotEqual(t, http.StatusOK, perform(disabled.Key).Code)
	assert.NotEqual(t, http.StatusOK, perform(strings.Repeat("z", 48)).Code)
	var stored model.Token
	require.NoError(t, db.First(&stored, token.Id).Error)
	assert.Equal(t, key, stored.Key)
	assert.Equal(t, 10000, stored.RemainQuota)
	assert.Equal(t, common.TokenStatusEnabled, stored.Status)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 20000, user.Quota)

	// A newer commit between authenticated identity and local model visibility
	// is rejected rather than resolving the old group under new policy.
	router.GET("/models-race", middleware.TokenAuth(), func(c *gin.Context) {
		writeOptions(map[string]string{"GroupConfigVersion": "2"})
		ListModels(c, constant.ChannelTypeOpenAI)
	})
	req := httptest.NewRequest(http.MethodGet, "/models-race", nil)
	req.Header.Set("Authorization", "Bearer sk-"+key)
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	assert.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
	assert.Contains(t, out.Body.String(), "group_config_changed")

	// The quote guard covers DB identity and numeric group multiplier, then
	// releases before any payment-provider I/O. Simulate a remote commit inside
	// the user lookup; no amount may be emitted from that mixed snapshot.
	current, err := model.BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	require.NoError(t, current.Finish())
	out = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(out)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/amount", bytes.NewBufferString(`{"amount":100}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", user.Id)
	c.Set(model.GroupConfigVersionContextKey, current.Version)
	oldMin := operation_setting.MinTopUp
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	operation_setting.MinTopUp = 1
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	t.Cleanup(func() {
		operation_setting.MinTopUp = oldMin
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
	})
	fired := false
	const cb = "group_quote_remote_commit"
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(cb, func(tx *gorm.DB) {
		if !fired && tx.Statement.Table == "users" {
			fired = true
			writeOptions(map[string]string{"GroupConfigVersion": "3"})
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(cb) })
	RequestAmount(c)
	require.True(t, fired)
	assert.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
	assert.Contains(t, out.Body.String(), "group_config_changed")
	require.NoError(t, db.Callback().Query().Remove(cb))
	require.NoError(t, db.AutoMigrate(&model.ModelTestRun{}))
	// A slow body must not retain a runtime read lock. On the first body read,
	// perform a remote commit and synchronous writer refresh. This would deadlock
	// if these handlers acquired their policy guard before decoding JSON.
	selection := modelTestSelection{TokenID: token.Id, Model: "synthetic-model", Group: "new"}
	ticket := signModelTestConfirmation(modelTestConfirmation{modelTestSelection: selection, OutputUnbounded: true, UserID: user.Id, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()})
	submitBody, err := common.Marshal(modelTestRequest{modelTestSelection: selection, Confirmed: true, RequestID: uuid.NewString(), ConfirmationToken: ticket})
	require.NoError(t, err)
	estimateBody, err := common.Marshal(selection)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, body string
		handler    gin.HandlerFunc
	}{
		{"token-preview", `{"group":"new"}`, PreviewTokenModels},
		{"model-estimate", string(estimateBody), EstimateModelTest},
		{"model-submit", string(submitBody), PrepareModelTest},
	} {
		current, err := model.BeginGroupConfigRead(t.Context())
		require.NoError(t, err)
		require.NoError(t, current.Finish())
		out := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(out)
		touched := false
		body := &groupGuardBodyReader{reader: strings.NewReader(tc.body), beforeRead: func() {
			touched = true
			writeOptions(map[string]string{"GroupConfigVersion": strconv.FormatInt(current.Version+1, 10)})
			refreshed, err := model.BeginGroupConfigRead(t.Context())
			require.NoError(t, err)
			require.NoError(t, refreshed.Finish())
		}}
		c.Request = httptest.NewRequest(http.MethodPost, "/synthetic/"+tc.name, body)
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("id", user.Id)
		c.Set(model.GroupConfigVersionContextKey, current.Version)
		tc.handler(c)
		require.True(t, touched, tc.name)
		require.Equal(t, http.StatusServiceUnavailable, out.Code, tc.name+": "+out.Body.String())
	}

	// A stale group may not fall back to the broader global limiter, including
	// when a stale node believes the limiter is disabled altogether.
	admitted, err := model.BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	require.NoError(t, admitted.Finish())
	writeOptions(map[string]string{"GroupConfigVersion": strconv.FormatInt(admitted.Version+1, 10)})
	originalRateEnabled := setting.ModelRequestRateLimitEnabled
	t.Cleanup(func() { setting.ModelRequestRateLimitEnabled = originalRateEnabled })
	for _, enabled := range []bool{false, true} {
		setting.ModelRequestRateLimitEnabled = enabled
		forwarded := false
		limiter := gin.New()
		limiter.GET("/rate", func(c *gin.Context) {
			c.Set("id", user.Id)
			c.Set(model.GroupConfigVersionContextKey, admitted.Version)
			common.SetContextKey(c, constant.ContextKeyUserGroup, "old")
		}, middleware.ModelRequestRateLimit(), func(c *gin.Context) { forwarded = true; c.Status(http.StatusOK) })
		out := httptest.NewRecorder()
		limiter.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/rate", nil))
		assert.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
		assert.False(t, forwarded)
	}

	// A failed identity lookup must not silently use anonymous/global grants.
	current, err = model.BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	require.NoError(t, current.Finish())
	const identityFailure = "group_visibility_identity_unavailable"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(identityFailure, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("synthetic identity lookup failure"))
		}
	}))
	for _, handler := range []gin.HandlerFunc{GetPricing, GetUserGroups} {
		out := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(out)
		c.Request = httptest.NewRequest(http.MethodGet, "/synthetic/groups", nil)
		c.Set("id", user.Id)
		c.Set(model.GroupConfigVersionContextKey, current.Version)
		handler(c)
		require.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
		require.NotContains(t, out.Body.String(), "premium")
	}
	require.NoError(t, db.Callback().Query().Remove(identityFailure))

}
