package controller

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HTTP/WS handler completion does not join the asynchronous atomic refund.
// Pause the admitted refund before any source/token writes, then release it only
// AFTER the first accounting read observes the held token row. A one-shot check
// must not mistake durable admission or handler completion for returned funds.
func TestResponsesAccountingWaitsForAsynchronousTokenRefund(t *testing.T) {
	fixture := newResponsesWSBillingTest(t, `tier("request", fixed(0.002))`, func(*websocket.Conn, *http.Request) {})
	oldRetry := common.RetryTimes
	common.RetryTimes = 0
	common.RedisEnabled = false
	t.Cleanup(func() { common.RetryTimes = oldRetry })
	fixture.httpUpstream = func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"synthetic unavailable","type":"upstream_error"}}`)
	}
	// A second read-only connection observes the admitted owner and held balances
	// before the refund transaction writes either account. Pausing after a source
	// write would instead lock SQLite readers, not expose a committed partial refund.
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(2)
	t.Cleanup(func() { sqlDB.SetMaxOpenConns(1) })
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var pauseClaimed atomic.Bool
	var accountingRead, firstReadHeldToken atomic.Bool
	var updateHits atomic.Int32
	requestStarted, handlerJoined := false, false
	releaseRefund := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseRefund)
	const pauseHook = "test:pause-responses-admitted-refund"
	const updateHook = "test:count-responses-token-refund"
	const queryHook = "test:release-responses-refund-after-read"
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(pauseHook, func(tx *gorm.DB) {
		owner, ok := tx.Statement.Dest.(*model.BillingFinalization)
		if tx.Error != nil || !ok || owner.UserID != fixture.user.Id || owner.RequestID != "responses-http-billing" || owner.State != model.BillingRefundReady {
			return
		}
		// The worker is the first reader of this newly admitted owner. Later
		// verification reads must remain non-blocking while it is paused.
		if !pauseClaimed.CompareAndSwap(false, true) {
			return
		}
		close(entered)
		<-release
	}))
	require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(updateHook, func(tx *gorm.DB) {
		if tx.Statement.Table != "tokens" {
			return
		}
		updates, ok := tx.Statement.Dest.(map[string]any)
		if !ok {
			return
		}
		delta, ok := updates["remain_quota"].(clause.Expr)
		if !ok || delta.SQL != "remain_quota + ?" || len(delta.Vars) != 1 || delta.Vars[0] != 1000 {
			return
		}
		updateHits.Add(1)
	}))
	require.NoError(t, model.DB.Callback().Query().After("gorm:query").Register(queryHook, func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" && accountingRead.Load() {
			if token, ok := tx.Statement.Dest.(*model.Token); ok && token.Id == fixture.token.Id && token.RemainQuota == 2000 && token.UsedQuota == 1000 {
				firstReadHeldToken.Store(true)
			}
			releaseRefund()
		}
	}))
	t.Cleanup(func() {
		releaseRefund()
		if requestStarted && !handlerJoined {
			select {
			case <-fixture.httpDone:
				handlerJoined = true
			case <-time.After(3 * time.Second):
				t.Error("HTTP handler did not join before refund cleanup")
			}
		}
		fixture.closeAndWait(t)
		// The handler has stopped producing work and Redis is disabled. Unlike
		// balances or refunded=true, zero joins the actual finite pool callbacks.
		require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond,
			"refund callbacks must return before removing hooks or restoring fixture globals")
		require.NoError(t, model.DB.Callback().Query().Remove(pauseHook))
		require.NoError(t, model.DB.Callback().Update().Remove(updateHook))
		require.NoError(t, model.DB.Callback().Query().Remove(queryHook))
	})

	request, err := http.NewRequest(http.MethodPost, fixture.gatewayURL+"/v1/responses", strings.NewReader(`{"model":"ws-billing","input":"synthetic"}`))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer sk-"+fixture.token.Key)
	request.Header.Set("Content-Type", "application/json")
	requestStarted = true
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(request)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	select {
	case <-fixture.httpDone:
		handlerJoined = true
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not finish")
	}
	fixture.closeAndWait(t)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("asynchronous token refund was not admitted")
	}
	var token model.Token
	var user model.User
	require.NoError(t, model.DB.First(&token, fixture.token.Id).Error)
	require.NoError(t, model.DB.First(&user, fixture.user.Id).Error)
	require.Equal(t, 2000, token.RemainQuota, "handler completion precedes token refund")
	require.Equal(t, 1000, token.UsedQuota)
	require.Equal(t, 99000, user.Quota, "durable refund admission has not yet returned either held balance")
	require.Equal(t, 99000, user.QuotaPaid)
	require.Zero(t, user.QuotaBonus)
	require.Zero(t, user.QuotaReward)
	var owners int64
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).
		Where("user_id = ? AND request_id = ?", fixture.user.Id, "responses-http-billing").Count(&owners).Error)
	require.EqualValues(t, 1, owners, "refund admission must retain the original durable owner")
	var admitted model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", "responses-http-billing").First(&admitted).Error)
	require.Equal(t, model.BillingRefundReady, admitted.State)
	require.Equal(t, fixture.user.Id, admitted.UserID)
	require.Equal(t, "wallet", admitted.Source)
	require.Equal(t, fixture.token.Id, admitted.TokenID)
	require.Equal(t, model.BillingTokenFingerprint(fixture.token.Key), admitted.TokenFingerprint)
	require.Equal(t, 1000, admitted.PreConsumed)
	require.Equal(t, 1000, admitted.SourceHeld)
	require.Equal(t, 1000, admitted.TokenHeld)
	require.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 1000}}, admitted.Shares())
	require.Zero(t, updateHits.Load(), "no token credit may precede release of the admitted refund")
	accountingRead.Store(true)
	assertResponsesWSAccounting(t, fixture, nil)
	require.True(t, firstReadHeldToken.Load(), "the first accounting read must observe the paused original token row")
	require.Equal(t, int32(1), updateHits.Load())
	require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
	var completed model.BillingFinalization
	require.NoError(t, model.DB.Where("request_id = ?", admitted.RequestID).First(&completed).Error)
	require.Equal(t, model.BillingRefunded, completed.State)
	require.Equal(t, admitted.RequestID, completed.RequestID)
	require.Equal(t, admitted.UserID, completed.UserID)
	require.Equal(t, admitted.Source, completed.Source)
	require.Equal(t, admitted.TokenID, completed.TokenID)
	require.Equal(t, admitted.TokenFingerprint, completed.TokenFingerprint)
	require.Equal(t, admitted.PreConsumed, completed.PreConsumed)
	require.Equal(t, admitted.SourceHeld, completed.SourceHeld)
	require.Equal(t, admitted.TokenHeld, completed.TokenHeld)
	require.Equal(t, admitted.Shares(), completed.Shares())
	require.Zero(t, completed.ActualQuota)
	require.NoError(t, model.DB.Model(&model.BillingFinalization{}).
		Where("user_id = ? AND request_id = ?", fixture.user.Id, admitted.RequestID).Count(&owners).Error)
	require.EqualValues(t, 1, owners)
}
