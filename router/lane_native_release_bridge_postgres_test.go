//go:build lane_billing_integration_dev && !enterprise_lane_lab

package router

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestLaneNativeReleaseBridgeNormalRouterPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_LANE_INTEGRATION_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL TEST_LANE_INTEGRATION_POSTGRES_DSN")
	}
	const testName = "TestLaneNativeReleaseBridgeNormalRouterPostgres"
	const childEnv = "LANE_NATIVE_RELEASE_BRIDGE_ROUTER_TEST_CHILD"
	if os.Getenv(childEnv) != "1" {
		bin, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		child := exec.CommandContext(ctx, bin, "-test.v", "-test.run=^"+testName+"$", "-test.count=1", "-test.timeout=50s")
		child.Env = append(os.Environ(), childEnv+"=1")
		out, err := child.CombinedOutput()
		require.NoError(t, err, "%s", out)
		require.Contains(t, string(out), "--- PASS: "+testName)
		t.Log(string(out))
		return
	}
	t.Setenv(model.UserGroupPricingEnabledEnv, "false")
	db := laneRouterDevTestDatabase(t, dsn, false)
	release := model.LaneStoreReleaseV1{StoreID: strings.Repeat("a", 32), ReleaseID: strings.Repeat("b", 32), BinarySHA256: strings.Repeat("c", 64), Epoch: 1}
	require.NoError(t, model.InstallLaneStoreAuthorityV1(t.Context(), db, release))
	var sends atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		wire, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		assert.NoError(t, err)
		assert.Equal(t, "Bearer "+laneRouterDevUpstreamKey, r.Header.Get("Authorization"))
		assert.Equal(t, int64(6), gjson.GetBytes(wire, "max_completion_tokens").Int())
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, laneRouterDevSSEPrefix+laneRouterDevUsage+"}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(upstream.Close)
	user := model.User{Id: laneRouterDevUserID, Username: "synthetic-release-native", AffCode: "release-native", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: laneRouterDevPaid, QuotaPaid: laneRouterDevPaid, AuthVersion: 8, CacheRevision: 9}
	user.SetSetting(dto.UserSetting{BillingPreference: "wallet_only"})
	token := model.Token{Id: laneRouterDevTokenID, UserId: user.Id, Key: laneRouterDevToken, Status: common.TokenStatusEnabled,
		ExpiredTime: -1, RemainQuota: laneRouterDevPaid, Group: "default", CacheRevision: 11}
	patch := `{"max_completion_tokens":6}`
	ch := model.Channel{Id: laneRouterDevChannelID, Name: "synthetic-release-native", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Key: laneRouterDevUpstreamKey, Group: "default", Models: laneRouterDevModel, BaseURL: &upstream.URL, ParamOverride: &patch}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Create(&ch).Error)
	require.NoError(t, ch.AddAbilities(db))

	type result struct {
		captured bool
		evidence service.LaneNativeChatEvidence
	}
	results := make(chan result, 1)
	key := sha256.Sum256([]byte("inert synthetic release wire HMAC"))
	gin.SetMode(gin.TestMode)
	gw := gin.New()
	require.NoError(t, gw.SetTrustedProxies(nil))
	gw.Use(middleware.RequestId(), func(c *gin.Context) {
		// Private test fixture metadata, never production ingress selection.
		// The real TokenAuth/distributor are still inside SetRelayRouter.
		var snapshotUser model.User
		var snapshotToken model.Token
		if err := db.Take(&snapshotUser, user.Id).Error; err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		if err := db.Take(&snapshotToken, token.Id).Error; err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		plan := relay.LaneNativeReleaseObservationPlanV1{
			Input: model.LaneStoreQuoteInputV1{Release: release, RequestID: c.GetString(common.RequestIdKey), UserID: snapshotUser.Id, TokenID: snapshotToken.Id,
				TokenFingerprint: model.BillingTokenFingerprint(snapshotToken.Key), UserAuthVersion: snapshotUser.AuthVersion,
				UserCacheRevision: snapshotUser.CacheRevision, TokenCacheRevision: snapshotToken.CacheRevision, Group: "default"},
			Route: service.LaneNativeChatRoute{ChannelID: ch.Id, ChannelType: ch.Type, Group: string(ch.Group), Model: laneRouterDevModel,
				Path: laneRouterDevSendPath, AuthoritySHA256: strings.Repeat("d", 64)},
			ExpectedOrigin: upstream.URL, ExpectedProviderKeyTag: relay.LaneNativeReleaseProviderKeyTagV1(key, laneRouterDevUpstreamKey),
			WireHMACKey: key, Verifier: laneRouterDevPromptVerifier{},
		}
		requestMode := c.GetHeader("X-Synthetic-Test-Case")
		if requestMode == "ordinary" {
			c.Next()
			results <- result{}
			return
		}
		if requestMode == "wrong-origin" {
			plan.ExpectedOrigin += "/different"
		}
		ctx, observation := relay.WithLaneNativeReleaseObservationV1(c.Request.Context(), plan)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
		results <- result{observation.CapturedPrehold(), observation.SealedWire().Evidence()}
	})
	SetRelayRouter(gw)
	server := httptest.NewServer(gw)
	t.Cleanup(server.Close)

	post := func(mode, input string) (result, string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+laneRouterDevSendPath, strings.NewReader(input))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer sk-"+token.Key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Synthetic-Test-Case", mode)
		resp, err := server.Client().Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		wire, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, "%s", wire)
		require.Contains(t, string(wire), "fixture reply")
		require.Equal(t, 1, strings.Count(string(wire), "data: [DONE]"))
		var observed result
		select {
		case observed = <-results:
		case <-time.After(time.Second):
			t.Fatal("relay observer did not complete")
		}
		id := resp.Header.Get(common.RequestIdKey)
		var owner model.BillingFinalization
		require.NoError(t, db.Where("request_id=?", id).Take(&owner).Error)
		assert.Equal(t, model.BillingSettled, owner.State)
		assert.Equal(t, "wallet", owner.Source)
		return observed, id
	}
	ordinary, _ := post("ordinary", laneRouterDevWire)
	assert.Zero(t, ordinary)
	verified, id := post("verified", laneRouterDevWire)
	require.True(t, verified.captured, "typed release facts must be captured before ordinary prehold")
	assert.Equal(t, id, verified.evidence.RequestID)
	assert.Equal(t, 6, verified.evidence.CompletionCap, "proof saw the normal final parameter override")
	assert.NotEmpty(t, verified.evidence.FinalWireSHA256)
	encoded, err := common.Marshal(verified.evidence)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), laneRouterDevUpstreamKey)
	wrongBody, _ := post("verified", strings.Replace(laneRouterDevWire, "fixture", "tampered", 1))
	assert.True(t, wrongBody.captured)
	assert.Zero(t, wrongBody.evidence, "final unsupported body cannot produce sealed wire")
	wrongRoute, _ := post("wrong-origin", laneRouterDevWire)
	assert.Zero(t, wrongRoute)
	require.NoError(t, model.RevokeLaneStoreAuthorityV1(t.Context(), db, release))
	revoked, _ := post("verified", laneRouterDevWire)
	assert.Zero(t, revoked)
	assert.EqualValues(t, 5, sends.Load(), "always one ordinary generation per authenticated request")
	var finalUser model.User
	var finalToken model.Token
	require.NoError(t, db.Take(&finalUser, user.Id).Error)
	require.NoError(t, db.Take(&finalToken, token.Id).Error)
	assert.Equal(t, user.QuotaPaid-25, finalUser.QuotaPaid)
	assert.Equal(t, token.RemainQuota-25, finalToken.RemainQuota)
	assert.False(t, db.Migrator().HasTable("lane_billing_child_receipts_v1"), "no synthetic custody schema installed")
}
