package accountpool

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccountPoolCollectorStopsPaginationAfterDisable(t *testing.T) {
	var enabled atomic.Bool
	enabled.Store(true)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		enabled.Store(false)
		fmt.Fprint(w, `{"code":0,"data":{"page":1,"total":2,"items":[{"id":1,"group_ids":[7]}]}}`)
	}))
	defer upstream.Close()
	s := New()
	s.enabled = func(context.Context) (bool, error) { return enabled.Load(), nil }
	_, err := s.collect(context.Background(), upstream.URL, "synthetic-key", Binding{ChannelID: 1, GroupMap: map[string]string{"7": "default"}})
	require.Error(t, err)
	assert.EqualValues(t, 1, calls.Load(), "disable prevents the next upstream page request")
}

func TestAccountPoolSettingsFailureDeniesAndDiscardsWarmCache(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, poolFixture)
	}))
	defer upstream.Close()
	s := enabledTestService()
	s.bindings = []Binding{{ChannelID: 3, Origin: upstream.URL, GroupMap: map[string]string{"7": "default"}}}
	s.getenv = func(string) string { return "synthetic-key" }
	s.channel = func(int) (*model.Channel, error) {
		return &model.Channel{Id: 3, Type: constant.ChannelTypeSub2API, Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL}, nil
	}
	require.Len(t, s.snapshot(context.Background()).items, 1)
	s.enabled = func(context.Context) (bool, error) { return false, errors.New("synthetic private DB details") }
	for _, handler := range []gin.HandlerFunc{s.List, s.AdminStatus} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/account-pool", nil)
		handler(c)
		assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))
		assert.NotContains(t, recorder.Body.String(), "items")
		assert.NotContains(t, recorder.Body.String(), "nodes")
		assert.NotContains(t, recorder.Body.String(), "private DB details")
	}
	assert.EqualValues(t, 1, calls.Load())
	s.enabled = func(context.Context) (bool, error) { return true, nil }
	require.Len(t, s.snapshot(context.Background()).items, 1)
	assert.EqualValues(t, 2, calls.Load(), "recovery must not return cache retained across failure")
}
