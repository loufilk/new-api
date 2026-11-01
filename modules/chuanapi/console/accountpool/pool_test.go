package accountpool

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const poolFixture = `{"code":0,"data":{"page":1,"total":1,"items":[{"id":909,"name":"private@example.invalid","platform":"openai","type":"oauth","status":"active","schedulable":true,"concurrency":2,"current_concurrency":2,"group_ids":[7,99],"credentials":{"plan_type":"plus","access_token":"synthetic-do-not-expose"},"extra":{"codex_5h_used_percent":31.5,"codex_7d_used_percent":40},"error_message":"private upstream address"}]}}`

func TestCollectorUsesExplicitAdminCredentialAndOnlyAnonymousProjection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/admin/accounts", r.URL.Path)
		assert.Equal(t, "true", r.URL.Query().Get("lite"))
		assert.Equal(t, "synthetic-admin-key", r.Header.Get("x-api-key"))
		assert.Empty(t, r.Header.Get("Authorization"))
		fmt.Fprint(w, poolFixture)
	}))
	defer upstream.Close()
	s := New()
	items, err := s.collect(context.Background(), upstream.URL, "synthetic-admin-key", Binding{ChannelID: 3, GroupMap: map[string]string{"7": "default"}})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "available", items[0].Status, "full concurrency does not change remote schedulability")
	assert.Equal(t, "plus", items[0].Plan)
	assert.Equal(t, 2, *items[0].Concurrency)
	assert.Equal(t, 31.5, *items[0].Usage5hPercent)
	assert.Equal(t, []Group{{Name: "default"}}, items[0].Groups)
	encoded, err := common.Marshal(items)
	require.NoError(t, err)
	for _, secret := range []string{"909", "private@", "synthetic-do-not-expose", "error_message", "credentials", "99"} {
		assert.NotContains(t, string(encoded), secret)
	}
	assert.True(t, strings.HasPrefix(items[0].AnonymousID, "AC-"))
}

func TestCollectorRejectsPaginationDriftAndRedirects(t *testing.T) {
	for _, body := range []string{`{"code":0,"data":{"page":1,"total":1,"items":[]}}`, `{"code":0,"data":{"page":1,"total":5001,"items":[]}}`, `{"code":0,"data":{"page":2,"total":0,"items":[]}}`, `{"code":0,"data":{"page":1,"total":2,"items":[{"id":1},{"id":1}]}}`} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		s := New()
		_, err := s.collect(context.Background(), upstream.URL, "synthetic", Binding{ChannelID: 1, GroupMap: map[string]string{"1": "default"}})
		upstream.Close()
		require.Error(t, err)
	}
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 302) }))
	defer upstream.Close()
	s := New()
	_, err := s.collect(context.Background(), upstream.URL, "synthetic", Binding{})
	require.Error(t, err)
	assert.Zero(t, destinationCalls.Load())
}

func TestCacheFailureShowsBoundedStaleThenNoData(t *testing.T) {
	var calls atomic.Int32
	fail := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if fail {
			http.Error(w, "secret upstream error", 503)
			return
		}
		fmt.Fprint(w, poolFixture)
	}))
	defer upstream.Close()
	now := time.Unix(1800000000, 0)
	s := New()
	s.bindings = []Binding{{ChannelID: 3, Origin: upstream.URL, GroupMap: map[string]string{"7": "default"}}}
	s.now = func() time.Time { return now }
	s.getenv = func(string) string { return "synthetic" }
	s.channel = func(int) (*model.Channel, error) {
		return &model.Channel{Id: 3, Type: constant.ChannelTypeSub2API, Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL}, nil
	}
	first := s.snapshot(context.Background())
	require.Len(t, first.items, 1)
	assert.EqualValues(t, 1, calls.Load())
	s.snapshot(context.Background())
	assert.EqualValues(t, 1, calls.Load())
	fail = true
	now = now.Add(61 * time.Second)
	stale := s.snapshot(context.Background())
	require.Len(t, stale.items, 1)
	assert.NotZero(t, stale.retryAt)
	assert.EqualValues(t, 2, calls.Load())
	s.snapshot(context.Background())
	assert.EqualValues(t, 2, calls.Load(), "refresh clicks cannot bypass failure throttle")
	now = now.Add(5 * time.Minute)
	expired := s.snapshot(context.Background())
	assert.Empty(t, expired.items)
	assert.True(t, expired.fetched.IsZero())
}

func TestMissingAdminAccessNeverReusesRelayKey(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	s := New()
	s.bindings = []Binding{{ChannelID: 3, Origin: upstream.URL, GroupMap: map[string]string{"7": "default"}}}
	s.getenv = func(string) string { return "" }
	s.channel = func(int) (*model.Channel, error) {
		return &model.Channel{Id: 3, Type: constant.ChannelTypeSub2API, Status: common.ChannelStatusEnabled, Key: "synthetic-relay-key", BaseURL: &upstream.URL}, nil
	}
	result := s.snapshot(context.Background())
	assert.Empty(t, result.items)
	assert.Zero(t, calls.Load())
	assert.False(t, result.configured)
	require.Len(t, result.nodes, 1)
	assert.Equal(t, "management_read_access_not_configured", result.nodes[0].ErrorCode)
}

func TestChannelBaseURLChangeCannotExfiltratePinnedManagementKey(t *testing.T) {
	var trustedCalls, attackerCalls atomic.Int32
	trusted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { trustedCalls.Add(1) }))
	defer trusted.Close()
	attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attackerCalls.Add(1) }))
	defer attacker.Close()
	s := New()
	s.bindings = []Binding{{ChannelID: 3, Origin: trusted.URL, GroupMap: map[string]string{"7": "default"}}}
	s.getenv = func(string) string { return "synthetic-admin-credential" }
	s.channel = func(int) (*model.Channel, error) {
		return &model.Channel{Id: 3, Type: constant.ChannelTypeSub2API, Status: common.ChannelStatusEnabled, BaseURL: &attacker.URL}, nil
	}
	result := s.snapshot(context.Background())
	assert.Empty(t, result.items)
	require.Len(t, result.nodes, 1)
	assert.Equal(t, "pinned_origin_mismatch", result.nodes[0].ErrorCode)
	assert.Zero(t, trustedCalls.Load())
	assert.Zero(t, attackerCalls.Load(), "neither relay credentials nor admin credentials may be sent to a mutable destination")
}

func TestPoolOriginAndRemoteStatusFailClosed(t *testing.T) {
	for _, origin := range []string{"http://outside.invalid", "https://user:pass@host.invalid", "https://host.invalid?secret=yes", "https://host.invalid/admin"} {
		_, err := poolOrigin(origin)
		assert.Error(t, err)
	}
	origin, err := poolOrigin("http://127.0.0.1:9000/v1")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:9000", origin)
	now := time.Unix(1800000000, 0)
	future := now.Add(time.Minute)
	yes, no := true, false
	assert.Equal(t, "unknown", remoteStatus(remoteAccount{Status: "active"}, now))
	assert.Equal(t, "rate_limited", remoteStatus(remoteAccount{Status: "active", Schedulable: &yes, RateLimitResetAt: &future}, now))
	assert.Equal(t, "unavailable", remoteStatus(remoteAccount{Status: "active", Schedulable: &no}, now))
}
