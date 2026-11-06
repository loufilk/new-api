package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/modules/chuanapi/console/accountpool"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func poolHandlerRequest(handler gin.HandlerFunc, userID int) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/account-pool", nil)
	c.Set("id", userID)
	handler(c)
	return recorder
}

func TestAccountPoolPostgresToggleDeniesCachedSnapshotsAcrossInstances(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	oldOptions := common.OptionMap
	t.Cleanup(func() { common.OptionMap = oldOptions })
	common.OptionMap = map[string]string{model.AccountPoolEnabledKey: "true"}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "/api/v1/admin/accounts", r.URL.Path)
		assert.Equal(t, "synthetic-admin-key", r.Header.Get("x-api-key"))
		fmt.Fprint(w, `{"code":0,"data":{"page":1,"total":1,"items":[{"id":909,"platform":"openai","type":"oauth","status":"active","schedulable":true,"group_ids":[7],"credentials":{"plan_type":"plus","access_token":"synthetic-secret"}}]}}`)
	}))
	defer upstream.Close()
	channel := model.Channel{Name: "switch-fixture", Type: constant.ChannelTypeSub2API, Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, Key: "synthetic-relay-key"}
	require.NoError(t, db.Create(&channel).Error)
	t.Setenv("SUB2_POOL_CHANNELS", fmt.Sprintf(`[{"channel_id":%d,"origin":%q,"group_map":{"7":"default"}}]`, channel.Id, upstream.URL))
	t.Setenv(fmt.Sprintf("SUB2_POOL_ADMIN_KEY_%d", channel.Id), "synthetic-admin-key")
	first, second := accountpool.New(service.GetUserUsableGroups, service.GetUserGroupRatio), accountpool.New(service.GetUserUsableGroups, service.GetUserGroupRatio)
	off := poolHandlerRequest(first.List, userID)
	assert.Equal(t, 403, off.Code)
	assert.Contains(t, off.Body.String(), "ACCOUNT_POOL_DISABLED")
	assert.NotContains(t, off.Body.String(), "items")
	assert.Equal(t, "no-store", off.Header().Get("Cache-Control"))
	assert.Zero(t, calls.Load())
	require.NoError(t, model.UpdateOption(model.AccountPoolEnabledKey, "true"))
	for _, instance := range []*accountpool.Service{first, second} {
		response := poolHandlerRequest(instance.List, userID)
		require.Equal(t, 200, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), "AC-")
		assert.NotContains(t, response.Body.String(), "synthetic-secret")
	}
	assert.EqualValues(t, 2, calls.Load())
	require.NoError(t, model.UpdateOption(model.AccountPoolEnabledKey, "false"))
	common.OptionMap[model.AccountPoolEnabledKey] = "true" // stale sibling process
	for _, instance := range []*accountpool.Service{first, second, accountpool.New(service.GetUserUsableGroups, service.GetUserGroupRatio)} {
		response := poolHandlerRequest(instance.List, userID)
		assert.Equal(t, 403, response.Code)
		assert.NotContains(t, response.Body.String(), "AC-")
		diagnostic := poolHandlerRequest(instance.AdminStatus, userID)
		assert.Equal(t, 200, diagnostic.Code)
		assert.Contains(t, diagnostic.Body.String(), `"enabled":false`)
		assert.Contains(t, diagnostic.Body.String(), `"nodes":[]`)
	}
	assert.EqualValues(t, 2, calls.Load(), "disabled requests and root diagnostics do not fetch upstream")
	status := poolHandlerRequest(GetStatus, userID)
	assert.Contains(t, status.Body.String(), `"account_pool_enabled":false`)
	assert.NotContains(t, status.Body.String(), "synthetic-admin-key")
}

func TestAccountPoolPostgresDisableDuringCollectionDiscardsResponse(t *testing.T) {
	db, userID := setupModelTestsPostgres(t)
	oldOptions := common.OptionMap
	t.Cleanup(func() { common.OptionMap = oldOptions })
	common.OptionMap = map[string]string{}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		fmt.Fprint(w, `{"code":0,"data":{"page":1,"total":1,"items":[{"id":909,"platform":"openai","status":"active","schedulable":true,"group_ids":[7]}]}}`)
	}))
	defer upstream.Close()
	channel := model.Channel{Name: "inflight-switch-fixture", Type: constant.ChannelTypeSub2API, Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL}
	require.NoError(t, db.Create(&channel).Error)
	t.Setenv("SUB2_POOL_CHANNELS", fmt.Sprintf(`[{"channel_id":%d,"origin":%q,"group_map":{"7":"default"}}]`, channel.Id, upstream.URL))
	t.Setenv(fmt.Sprintf("SUB2_POOL_ADMIN_KEY_%d", channel.Id), "synthetic-key")
	require.NoError(t, model.UpdateOption(model.AccountPoolEnabledKey, "true"))
	instance := accountpool.New(service.GetUserUsableGroups, service.GetUserGroupRatio)
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- poolHandlerRequest(instance.List, userID) }()
	<-started
	err := model.UpdateOption(model.AccountPoolEnabledKey, "false")
	close(release)
	require.NoError(t, err)
	response := <-finished
	assert.Equal(t, 403, response.Code, response.Body.String())
	assert.NotContains(t, response.Body.String(), "AC-")
	assert.EqualValues(t, 1, calls.Load())
	assert.Equal(t, 403, poolHandlerRequest(instance.List, userID).Code)
	assert.EqualValues(t, 1, calls.Load())
}

func TestAccountPoolPostgresUpdateRejectsNonBooleanValues(t *testing.T) {
	_, _ = setupModelTestsPostgres(t)
	oldOptions := common.OptionMap
	t.Cleanup(func() { common.OptionMap = oldOptions })
	common.OptionMap = map[string]string{}
	for _, raw := range []string{`1`, `null`, `{}`, `[]`, `"TRUE"`, `""`} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"AccountPoolEnabled","value":`+raw+`}`))
		UpdateOption(c)
		assert.Contains(t, recorder.Body.String(), `"success":false`, raw)
	}
}
