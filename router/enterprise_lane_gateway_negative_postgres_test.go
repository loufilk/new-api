//go:build enterprise_lane_lab

package router

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type enterpriseLaneTerminalObserver struct {
	gin.ResponseWriter
	terminal chan<- struct{}
}

func (w enterpriseLaneTerminalObserver) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"finish_reason":"stop"`)) ||
		bytes.Contains(p, []byte(`"total_tokens":3`)) || bytes.Contains(p, []byte("[DONE]")) {
		select {
		case w.terminal <- struct{}{}:
		default:
		}
	}
	return w.ResponseWriter.Write(p)
}

func TestEnterpriseLaneGatewayNegativePostgres(t *testing.T) {
	dsn := os.Getenv("TEST_ENTERPRISE_LANE_ROUTER_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires fresh loopback enterprise_lane_ PostgreSQL fixture")
	}
	// Quoted columns, policy, and lane activation are process-wide. Keep this
	// fixture separate from other router tests rather than resetting internals.
	const childEnv = "ENTERPRISE_LANE_ROUTER_NEGATIVE_CHILD"
	if os.Getenv(childEnv) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		child := exec.CommandContext(ctx, binary, "-test.run=^TestEnterpriseLaneGatewayNegativePostgres$", "-test.v", "-test.count=1", "-test.timeout=45s")
		child.Env = append(os.Environ(), childEnv+"=1")
		out, err := child.CombinedOutput()
		require.NoError(t, err, "%s", out)
		require.Contains(t, string(out), "--- PASS: TestEnterpriseLaneGatewayNegativePostgres")
		t.Log(string(out))
		return
	}

	for key, val := range map[string]string{
		"SQL_DSN":                                dsn,
		"LOG_SQL_DSN":                            "",
		"REDIS_CONN_STRING":                      "",
		"HTTP_BIND_ADDRESS":                      "127.0.0.1",
		"TRUSTED_PROXIES":                        "none",
		"ENTERPRISE_LANE_LAB_ENABLED":            "true",
		"ENTERPRISE_LANE_LAB_STOP":               "false",
		"ENTERPRISE_LANE_LAB_TOKEN_ID":           "701",
		"ENTERPRISE_LANE_LAB_COUNT":              "1",
		"ENTERPRISE_LANE_LAB_PAID_PER_LANE":      "200",
		"ENTERPRISE_LANE_LAB_TOKEN_PER_LANE":     "200",
		"ENTERPRISE_LANE_LAB_CHANNEL_ID":         "702",
		"ENTERPRISE_LANE_LAB_MODEL":              "gpt-4o",
		"ENTERPRISE_LANE_LAB_ADMISSION_GLOBAL":   "1",
		"ENTERPRISE_LANE_LAB_ADMISSION_PER_LANE": "1",
	} {
		t.Setenv(key, val)
	}
	waitScenario := os.Getenv("TEST_ENTERPRISE_LANE_ROUTER_WAIT_SCENARIO")
	switch waitScenario {
	case "":
	case "positive", "revoked":
		// Stay within the same 50 ms runtime hard bound; establish actual
		// queuing with an admission counter and a PG lock barrier.
		t.Setenv("ENTERPRISE_LANE_LAB_ADMISSION_WAIT_MS", "50")
		t.Setenv("ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_GLOBAL", "1")
		t.Setenv("ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_PER_LANE", "1")
	default:
		t.Fatalf("unsupported isolated wait fixture")
	}
	// Validate all isolation preconditions BEFORE opening a database handle.
	require.NoError(t, middleware.PrepareEnterpriseLaneGateway(false))
	bootstrap, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	bootstrapSQL, err := bootstrap.DB()
	require.NoError(t, err)
	defer bootstrapSQL.Close()
	tables, err := bootstrap.Migrator().GetTables()
	require.NoError(t, err)
	require.Empty(t, tables, "refusing to use a nonempty database")
	require.NoError(t, bootstrap.AutoMigrate(&model.Option{}))
	common.IsMasterNode = false
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	common.CriticalRateLimitEnable = false
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	setting.ModelRequestRateLimitEnabled = false
	require.NoError(t, i18n.Init())
	require.NoError(t, model.InitDB())
	require.NoError(t, model.InitLogDB())
	t.Cleanup(func() { require.NoError(t, model.CloseDB()) })
	db := model.DB
	fixtures := []any{&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Option{}, &model.Model{}, &model.Vendor{}, &model.Log{},
		&model.SubscriptionPlan{}, &model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{},
		&model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.BillingFinalization{}}
	require.NoError(t, db.AutoMigrate(append(fixtures, model.BonusSourceModels()...)...))
	model.InitOptionMap()
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString("{}"))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o":1}`))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(`{"gpt-4o":1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	const body = `{"model":"gpt-4o","messages":[{"role":"user","content":"fixture"}],"max_tokens":6,"stream":true,"stream_options":{"include_usage":true}}`
	const output = `data: {"id":"chatcmpl-fixture","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"fixture"},"finish_reason":null}]}`
	const finish = `data: {"id":"chatcmpl-fixture","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	const usage = `data: {"id":"chatcmpl-fixture","object":"chat.completion.chunk","created":1,"model":"gpt-4o","choices":[],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "%s\n\n", output)
		w.(http.Flusher).Flush()
		if call == 1 {
			<-release
		}
		_, _ = fmt.Fprintf(w, "%s\n\n%s\n\n", finish, usage)
		if call == 1 {
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		}
		// Other calls deliberately EOF after otherwise exact usage.
	}))
	t.Cleanup(upstream.Close)
	t.Cleanup(unblock)

	user := model.User{Id: 700, Username: "lane-router-negative", AffCode: "lane-router-negative", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", Quota: 1000, QuotaPaid: 1000}
	token := model.Token{Id: 701, UserId: 700, Key: "syntheticenterpriselanerouterfinite", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000, Group: "default"}
	baseURL := upstream.URL
	channel := model.Channel{Id: 702, Name: "lane-router-fixture", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Key: "inert-fixture", Group: "default", Models: "gpt-4o", BaseURL: &baseURL}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(db))
	stop, err := middleware.ConfigureEnterpriseLaneGateway(t.Context())
	require.NoError(t, err)
	require.False(t, stop)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	terminalAttempt := make(chan struct{}, 1)
	engine.Use(middleware.RequestId(), func(c *gin.Context) {
		c.Writer = enterpriseLaneTerminalObserver{ResponseWriter: c.Writer, terminal: terminalAttempt}
		c.Next()
	})
	SetRelayRouter(engine)
	gateway := httptest.NewServer(engine)
	t.Cleanup(gateway.Close)

	post := func(path string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, gateway.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer sk-"+token.Key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := gateway.Client().Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	readOwner := func(id string) model.EnterpriseLaneRequest {
		t.Helper()
		require.NotEmpty(t, id)
		var owner model.EnterpriseLaneRequest
		require.NoError(t, db.First(&owner, "request_id = ?", id).Error)
		return owner
	}

	const chatPath = "/v1/chat/completions"
	unsupported := post("/v1/embeddings")
	unsupportedBody, err := io.ReadAll(unsupported.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, unsupported.StatusCode, "%s", unsupportedBody)
	assert.Zero(t, upstreamCalls.Load())

	if waitScenario != "" {
		// The holder holds the only gate permit, pending on this real lane
		// row. The second request must enter the separate pre-DB queue. The
		// gate counter proves it did not simply enter TokenAuth late.
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		blocker := db.WithContext(ctx).Begin()
		require.NoError(t, blocker.Error)
		defer blocker.Rollback()
		var lane model.EnterpriseLane
		require.NoError(t, blocker.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lane).Error)
		var pgPID int
		require.NoError(t, blocker.Raw("SELECT pg_backend_pid()").Scan(&pgPID).Error)
		type response struct {
			resp *http.Response
			err  error
		}
		start := func(ctx context.Context) <-chan response {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+chatPath, strings.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			req.Header.Set("Content-Type", "application/json")
			out := make(chan response, 1)
			go func() { r, err := gateway.Client().Do(req); out <- response{r, err} }()
			return out
		}
		holderCtx, cancelHolder := context.WithCancel(ctx)
		defer cancelHolder()
		holder := start(holderCtx)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			var blocked bool
			err := db.WithContext(ctx).Raw("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid)))", pgPID).Scan(&blocked).Error
			assert.NoError(c, err)
			assert.True(c, blocked)
		}, time.Second, time.Millisecond)
		waiter := start(ctx)
		require.Eventually(t, func() bool {
			stat := middleware.EnterpriseLaneAdmissionWaitSnapshot()
			return stat != nil && len(stat.Slots) == 1 && stat.Slots[0].Enqueued == 1
		}, time.Second, time.Millisecond)
		if waitScenario == "revoked" {
			require.NoError(t, db.WithContext(ctx).Model(&token).Update("status", common.TokenStatusDisabled).Error)
		}
		cancelHolder()
		select {
		case result := <-holder:
			if result.resp != nil {
				_ = result.resp.Body.Close()
			}
			require.ErrorIs(t, result.err, context.Canceled)
		case <-ctx.Done():
			t.Fatal("holder did not cancel")
		}
		require.NoError(t, blocker.Commit().Error)
		var result response
		select {
		case result = <-waiter:
			require.NoError(t, result.err)
			require.NotNil(t, result.resp)
		case <-ctx.Done():
			t.Fatal("queued HTTP request did not return")
		}
		defer result.resp.Body.Close()
		if waitScenario == "positive" {
			unblock()
		}
		full, err := io.ReadAll(result.resp.Body)
		require.NoError(t, err)
		rid := result.resp.Header.Get(common.RequestIdKey)
		require.NotEmpty(t, rid)
		waitMetric := middleware.EnterpriseLaneAdmissionWaitSnapshot()
		require.EqualValues(t, 1, waitMetric.Slots[0].Granted, "request must have actually waited and reached the controller freshness path")
		require.Zero(t, waitMetric.Slots[0].Timeout)
		require.Zero(t, waitMetric.Pending)
		require.Zero(t, waitMetric.Active)
		if waitScenario == "revoked" {
			require.Equal(t, http.StatusServiceUnavailable, result.resp.StatusCode, "%s", full)
			require.Contains(t, string(full), "enterprise_lane_admission_stale")
			require.Zero(t, upstreamCalls.Load())
			var owners int64
			require.NoError(t, db.Model(&model.EnterpriseLaneRequest{}).Count(&owners).Error)
			require.Zero(t, owners)
			require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&owners).Error)
			require.Zero(t, owners)
			require.NoError(t, db.First(&lane, "lane_key = ?", lane.LaneKey).Error)
			require.Zero(t, lane.Held)
			require.Zero(t, lane.Spent)
			require.Equal(t, 200, lane.PaidAvailable)
			require.Equal(t, 200, lane.TokenAvailable)
			return
		}
		require.Equal(t, http.StatusOK, result.resp.StatusCode, "%s", full)
		require.Contains(t, string(full), finish)
		require.Contains(t, string(full), usage)
		require.Contains(t, string(full), "data: [DONE]")
		require.EqualValues(t, 1, upstreamCalls.Load())
		owner := readOwner(rid)
		require.Equal(t, model.EnterpriseLaneSettled, owner.State)
		require.Equal(t, 3, owner.Actual)
		require.True(t, owner.UsageProjectionPending)
		require.NoError(t, db.First(&lane, "lane_key = ?", lane.LaneKey).Error)
		require.Zero(t, lane.Held)
		require.Equal(t, 3, lane.Spent)
		require.Equal(t, 197, lane.PaidAvailable)
		require.Equal(t, 197, lane.TokenAvailable)
		return
	}

	first := post(chatPath)
	require.Equal(t, http.StatusOK, first.StatusCode)
	rid := first.Header.Get(common.RequestIdKey)
	reader := bufio.NewReader(first.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, line, `"content":"fixture"`)
	pending := readOwner(rid)
	require.Equal(t, model.EnterpriseLaneDispatch, pending.State)
	require.Greater(t, pending.Held, 100, "the same second request cannot fit until the first settles")

	second := post(chatPath)
	denial, err := io.ReadAll(second.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, second.StatusCode, "%s", denial)
	assert.Contains(t, string(denial), "enterprise_lane_reservation_rejected")
	assert.EqualValues(t, 1, upstreamCalls.Load(), "insufficient escrow must not contact any provider")
	var rejectedOwners int64
	require.NoError(t, db.Model(&model.EnterpriseLaneRequest{}).Where("request_id = ?", second.Header.Get(common.RequestIdKey)).Count(&rejectedOwners).Error)
	assert.Zero(t, rejectedOwners)

	// Keep the final money transaction blocked after the provider has finished.
	// Wait for PostgreSQL evidence of that block, not a guessed stream timing.
	blockCtx, stopBlock := context.WithTimeout(t.Context(), 4*time.Second)
	defer stopBlock()
	blocker := db.WithContext(blockCtx).Begin()
	require.NoError(t, blocker.Error)
	defer blocker.Rollback()
	var lane model.EnterpriseLane
	require.NoError(t, blocker.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lane).Error)
	var pid int
	require.NoError(t, blocker.Raw("SELECT pg_backend_pid()").Scan(&pid).Error)
	// Keep another intake holding the only permit as it waits on the
	// financial row. New work must fail before its reservation transaction.
	reserveCtx, cancelReserve := context.WithCancel(blockCtx)
	defer cancelReserve()
	waiting, err := http.NewRequestWithContext(reserveCtx, http.MethodPost, gateway.URL+chatPath, strings.NewReader(body))
	require.NoError(t, err)
	waiting.Header.Set("Authorization", "Bearer sk-"+token.Key)
	waiting.Header.Set("Content-Type", "application/json")
	reserveDone := make(chan error, 1)
	go func() {
		response, err := gateway.Client().Do(waiting)
		if response != nil {
			_ = response.Body.Close()
		}
		reserveDone <- err
	}()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var blocked bool
		err := db.WithContext(blockCtx).Raw("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid)))", pid).Scan(&blocked).Error
		assert.NoError(c, err)
		assert.True(c, blocked)
	}, 2*time.Second, 10*time.Millisecond)
	busy := post(chatPath)
	busyBody, err := io.ReadAll(busy.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, busy.StatusCode, "%s", busyBody)
	require.Contains(t, string(busyBody), "enterprise_lane_admission_busy")
	assert.EqualValues(t, 1, upstreamCalls.Load())
	require.NoError(t, db.Model(&model.EnterpriseLaneRequest{}).Where("request_id = ?", busy.Header.Get(common.RequestIdKey)).Count(&rejectedOwners).Error)
	require.Zero(t, rejectedOwners)
	tail := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(reader)
		tail <- string(out)
	}()
	unblock()
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var blocked bool
		err := db.WithContext(blockCtx).Raw("SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE ? = ANY(pg_blocking_pids(pid)))", pid).Scan(&blocked).Error
		assert.NoError(c, err)
		assert.True(c, blocked)
	}, 2*time.Second, 10*time.Millisecond)
	// Even at full intake capacity, the existing stream can freeze usage.
	// Its final settlement is still correctly blocked on this same PG lock.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var owner model.EnterpriseLaneRequest
		err := db.WithContext(blockCtx).Where("request_id = ?", rid).Take(&owner).Error
		assert.NoError(c, err)
		assert.Equal(c, model.EnterpriseLaneObserved, owner.State)
	}, 2*time.Second, 10*time.Millisecond)
	cancelReserve()
	select {
	case err := <-reserveDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-blockCtx.Done():
		t.Fatal("canceled intake did not return")
	}
	select {
	case <-terminalAttempt:
		t.Fatal("the server attempted a terminal write before settlement")
	default:
	}
	require.NoError(t, blocker.Commit().Error)
	select {
	case out := <-tail:
		assert.Contains(t, out, finish)
		assert.Contains(t, out, usage)
		assert.Contains(t, out, "data: [DONE]")
	case <-blockCtx.Done():
		t.Fatal("terminal frames did not arrive after settlement released")
	}
	settled := readOwner(rid)
	require.Equal(t, model.EnterpriseLaneSettled, settled.State)
	require.Equal(t, 3, settled.Actual)
	require.True(t, settled.UsageProjectionPending)
	var totalOwners int64
	require.NoError(t, db.Model(&model.EnterpriseLaneRequest{}).Count(&totalOwners).Error)
	require.EqualValues(t, 1, totalOwners, "canceled or denied admissions must not commit an owner")

	missing := post(chatPath)
	missingBody, err := io.ReadAll(missing.Body)
	require.NoError(t, err)
	assert.Contains(t, string(missingBody), `"content":"fixture"`)
	assert.NotContains(t, string(missingBody), finish)
	assert.NotContains(t, string(missingBody), usage)
	assert.NotContains(t, string(missingBody), "data: [DONE]")
	assert.EqualValues(t, 2, upstreamCalls.Load())
	unknown := readOwner(missing.Header.Get(common.RequestIdKey))
	require.Equal(t, model.EnterpriseLaneUnknown, unknown.State)
	assert.Zero(t, unknown.Actual)
	assert.False(t, unknown.UsageProjectionPending)
	require.NoError(t, db.First(&lane, "lane_key = ?", lane.LaneKey).Error)
	assert.Equal(t, 3, lane.Spent)
	assert.Equal(t, 1, lane.OpenRequests)
	assert.Equal(t, unknown.Held, lane.Held)
	assert.Equal(t, 200-3-unknown.Held, lane.PaidAvailable)
	assert.Equal(t, lane.PaidAvailable, lane.TokenAvailable)
	_, err = model.StopEnterpriseLane(t.Context(), db, lane.EnterpriseLaneIdentity)
	require.NoError(t, err)
	_, err = model.DrainEnterpriseLane(t.Context(), db, lane.EnterpriseLaneIdentity)
	require.ErrorIs(t, err, model.ErrEnterpriseLaneState, "unresolved delivery blocks return")
	stopped := post(chatPath)
	stoppedBody, err := io.ReadAll(stopped.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, stopped.StatusCode, "%s", stoppedBody)
	assert.EqualValues(t, 2, upstreamCalls.Load(), "operator stop is a new-dispatch barrier")

	var legacyOwners int64
	require.NoError(t, db.Model(&model.BillingFinalization{}).Count(&legacyOwners).Error)
	assert.Zero(t, legacyOwners, "the dedicated key must not escape to legacy billing")
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 800, user.QuotaPaid)
	assert.Equal(t, 800, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)
	var ledger []model.BalanceLedger
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&ledger).Error)
	require.Len(t, ledger, 1, "only the funding debit touches the ordinary wallet")
	assert.Equal(t, -200, ledger[0].Delta)
	assert.Equal(t, "enterprise_lane_fund", ledger[0].Reason)
}
