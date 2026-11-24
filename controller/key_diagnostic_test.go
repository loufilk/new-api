package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeyDiagnosticKeyEligibilityDeniesOtherOwnerExpiredIPAndExhausted(t *testing.T) {
	ip := "192.0.2.0/24"
	token := model.Token{UserId: 7, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100, AllowIps: &ip}
	assert.True(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
	assert.False(t, usableDiagnosticToken(&token, 8, "192.0.2.4"))
	assert.False(t, usableDiagnosticToken(&token, 7, "203.0.113.4"))
	token.ExpiredTime = time.Now().Add(-time.Minute).Unix()
	assert.False(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
	token.ExpiredTime = -1
	token.RemainQuota = 0
	assert.False(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
	token.UnlimitedQuota = true
	token.Status = common.TokenStatusDisabled
	assert.False(t, usableDiagnosticToken(&token, 7, "192.0.2.4"))
}

func TestKeyDiagnosticCandyClearWrongAndUnrecognized(t *testing.T) {
	for answer, want := range map[string]string{
		"答案：8 颗。最坏情形是先摸完 7 颗红糖。": "correct",
		"**八颗。**":         "correct",
		"答案：7 颗。":         "incorrect",
		"可能是 8 颗也可能是 9 颗": "unrecognized",
		"最坏情形是先拿 7 颗红糖，再拿一种其它颜色。": "unrecognized",
		"": "unrecognized",
	} {
		assert.Equal(t, want, classifyCandyAnswer(answer), answer)
	}
}

func TestKeyDiagnosticPostgresOneConfirmedRequestUsesOwnedKeyAndReplaysReceipt(t *testing.T) {
	db, userID := setupModelTestSwitchExecution(t)
	require.NoError(t, db.AutoMigrate(&model.UserSession{}))
	identity := model.AuthSessionIdentity{UserID: userID, SessionID: "local-browser", UserAuthVersion: 1, SessionVersion: 1}
	session := model.UserSession{SID: identity.SessionID, UserID: userID, UserAuthVersion: 1, Version: 1,
		Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Create(&session).Error)
	var token model.Token
	require.NoError(t, db.Where("user_id = ?", userID).First(&token).Error)
	require.NoError(t, db.Model(&token).Updates(map[string]any{"key": "syntheticownercredential", "allow_ips": "192.0.2.0/24"}).Error)
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.ModelTestEnabledKey).Update("value", "false").Error)
	oldDebug, oldConsume := common.DebugEnabled, common.LogConsumeEnabled
	oldWriter, oldErrorWriter := gin.DefaultWriter, gin.DefaultErrorWriter
	var capturedLogs bytes.Buffer
	common.DebugEnabled, common.LogConsumeEnabled = true, true
	common.LogWriterMu.Lock()
	gin.DefaultWriter, gin.DefaultErrorWriter = &capturedLogs, &capturedLogs
	common.LogWriterMu.Unlock()
	defer func() {
		common.DebugEnabled, common.LogConsumeEnabled = oldDebug, oldConsume
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldErrorWriter
		common.LogWriterMu.Unlock()
	}()
	var calls atomic.Int32
	var forceFailure atomic.Bool
	upstreamStarted := make(chan struct{}, 10)
	allowUpstream := make(chan struct{})
	var release sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		upstreamStarted <- struct{}{}
		<-allowUpstream
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Empty(t, r.Header.Get("Cookie"))
		assert.NotContains(t, r.Header.Get("Authorization"), "syntheticownercredential")
		var input struct {
			Model     string
			MaxTokens int `json:"max_tokens"`
			Messages  []struct{ Role, Content string }
		}
		if assert.NoError(t, common.DecodeJson(r.Body, &input)) && assert.Len(t, input.Messages, 1) {
			assert.Equal(t, "gpt-4o-mini", input.Model)
			assert.Equal(t, 8192, input.MaxTokens)
			assert.Contains(t, input.Messages[0].Content, "糖")
		}
		w.Header().Set("Content-Type", "application/json")
		if forceFailure.Load() {
			w.Header().Set("X-Request-ID", "synthetic-not-a-real-key")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"type":"server_error","message":"fixture error body echo synthetic-not-a-real-key syntheticownercredential"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"local-response","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"答案：8 颗。 Secret echo: syntheticownercredential"},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":3,"total_tokens":83}}`)
	}))
	defer upstream.Close()
	defer release.Do(func() { close(allowUpstream) })
	require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
	group := lifecycle.NewGroup()
	previous := keyDiagnosticWorkers.Swap(group)
	defer func() {
		keyDiagnosticWorkers.Store(previous)
		group.BeginDrain()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, group.Wait(ctx))
	}()
	engine := gin.New()
	require.NoError(t, engine.SetTrustedProxies(nil))
	engine.Use(middleware.RequestId(), middleware.BodyStorageCleanup(), func(c *gin.Context) {
		owner := userID
		if c.Request.Header.Get("X-Synthetic-Viewer") == "other" {
			owner++
		}
		c.Set("id", owner)
		c.Set("session_id", identity.SessionID)
		c.Set("auth_version", identity.UserAuthVersion)
		c.Set("session_version", identity.SessionVersion)
		c.Next()
	})
	engine.GET("/options", GetKeyDiagnosticOptions)
	engine.GET("/runs", ListKeyDiagnostics)
	engine.GET("/runs/:id", GetKeyDiagnostic)
	engine.POST("/runs", PrepareKeyDiagnostic, SaveKeyDiagnostic)
	request := func(ctx context.Context, method, path, body, ip string, other bool) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(ctx, method, path, strings.NewReader(body))
		r.RemoteAddr = ip + ":1234"
		r.Header.Set("X-Forwarded-For", "192.0.2.4")
		if other {
			r.Header.Set("X-Synthetic-Viewer", "other")
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Cookie", "sentinel=not-for-provider")
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, r)
		return rec
	}
	get := request(context.Background(), "GET", "/options", "", "192.0.2.4", false)
	require.Equal(t, 200, get.Code, get.Body.String())
	assert.NotContains(t, get.Body.String(), "syntheticownercredential")
	assert.Contains(t, get.Body.String(), `"max_output_tokens":32000`)
	assert.Zero(t, calls.Load())
	id := uuid.NewString()
	selection := keyDiagnosticRequest{TokenID: token.Id, Model: "gpt-4o-mini", Question: "candy-logic-v1", ClientID: id, Confirmed: true}
	post := func(ctx context.Context, s keyDiagnosticRequest, ip string) *httptest.ResponseRecorder {
		data, err := common.Marshal(s)
		require.NoError(t, err)
		return request(ctx, "POST", "/runs", string(data), ip, false)
	}
	denied := selection
	denied.Confirmed = false
	assert.Equal(t, 400, post(context.Background(), denied, "192.0.2.4").Code)
	denied = selection
	denied.Model = "not-in-group"
	assert.Equal(t, 403, post(context.Background(), denied, "192.0.2.4").Code)
	assert.Equal(t, 403, post(context.Background(), selection, "203.0.113.4").Code, "an untrusted forwarded header cannot bypass selected Key IP policy")
	assert.Zero(t, calls.Load())

	client, cancel := context.WithCancel(context.Background())
	out := make(chan *httptest.ResponseRecorder, 1)
	go func() { out <- post(client, selection, "192.0.2.4") }()
	select {
	case rec := <-out:
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), id)
		require.Contains(t, rec.Body.String(), `"status":"queued"`)
	case <-time.After(6 * time.Second):
		t.Fatal("POST waited for the blocked provider instead of committing and returning a task ID")
	}
	cancel()
	select {
	case <-upstreamStarted:
	case <-time.After(7 * time.Second):
		t.Fatal("saved task never reached synthetic provider")
	}
	assert.Equal(t, http.StatusNotFound, request(context.Background(), "GET", "/runs/"+id, "", "192.0.2.4", true).Code)
	otherList := request(context.Background(), "GET", "/runs", "", "192.0.2.4", true)
	assert.Contains(t, otherList.Body.String(), `"data":[]`)
	assert.Equal(t, 200, post(context.Background(), selection, "203.0.113.4").Code, "receipt status recovery does not require the old dispatch IP")
	denied = selection
	denied.Question = "pelican-svg-v1"
	assert.Equal(t, 409, post(context.Background(), denied, "192.0.2.4").Code)
	denied = selection
	denied.ClientID = uuid.NewString()
	assert.Equal(t, 429, post(context.Background(), denied, "192.0.2.4").Code, "the current paid call occupies the same owner concurrency slot")
	release.Do(func() { close(allowUpstream) })

	var completed *httptest.ResponseRecorder
	require.Eventually(t, func() bool {
		completed = request(context.Background(), "GET", "/runs/"+id, "", "192.0.2.4", false)
		return strings.Contains(completed.Body.String(), `"status":"completed"`)
	}, 7*time.Second, 30*time.Millisecond)
	assert.Contains(t, completed.Body.String(), `"candy_result":"correct"`)
	assert.Contains(t, completed.Body.String(), `"http_status":200`)
	assert.Contains(t, completed.Body.String(), `"elapsed_ms":`)
	assert.Contains(t, completed.Body.String(), `"billing":{"status":"settled","scope":"generation","quota":`)
	assert.Contains(t, completed.Body.String(), `"usage":{"prompt_tokens":80,"completion_tokens":3}`)
	assert.Contains(t, completed.Body.String(), "Secret echo: [redacted]")
	assert.NotContains(t, completed.Body.String(), "syntheticownercredential")
	assert.NotContains(t, completed.Body.String(), "192.0.2.4")
	assert.EqualValues(t, 1, calls.Load())
	var run model.ModelTestRun
	require.NoError(t, db.First(&run, "id = ?", model.KeyDiagnosticRowID(userID, id)).Error)
	assert.NotContains(t, run.SamplesJSON, "192.0.2.4")
	assert.NotContains(t, run.SamplesJSON, "local-browser")
	assert.NotContains(t, run.ArtworkHTML, "syntheticownercredential")
	var receipt model.BillingFinalization
	require.NoError(t, db.Where("request_id = ?", run.RequestID).Take(&receipt).Error)
	assert.Equal(t, userID, receipt.UserID)
	assert.Equal(t, token.Id, receipt.TokenID)
	assert.Equal(t, model.BillingSettled, receipt.State)
	assert.Positive(t, receipt.ActualQuota)
	var consume model.Log
	require.NoError(t, db.Where("request_id = ? AND type = ?", run.RequestID, model.LogTypeConsume).Take(&consume).Error)
	detail := func() string {
		return request(context.Background(), "GET", "/runs/"+id, "", "203.0.113.4", false).Body.String()
	}
	assert.Contains(t, detail(), `"usage":{"prompt_tokens":80,"completion_tokens":3}`, "polling origin is not the execution authority")
	originalLog := consume
	for _, mismatch := range []map[string]any{
		{"quota": consume.Quota + 1}, {"token_id": consume.TokenId + 1},
		{"other": `{"admin_info":{"usage_billing_path":"billing-usage-openai-estimated"}}`},
	} {
		require.NoError(t, db.Model(&consume).Updates(mismatch).Error)
		assert.NotContains(t, detail(), `"usage":`)
		assert.Contains(t, detail(), `"status":"settled"`)
		require.NoError(t, db.Model(&consume).Updates(map[string]any{"quota": receipt.ActualQuota, "token_id": token.Id, "other": originalLog.Other}).Error)
	}
	duplicated := consume
	duplicated.Id = 0
	require.NoError(t, db.Create(&duplicated).Error)
	assert.NotContains(t, detail(), `"usage":`)
	require.NoError(t, db.Delete(&duplicated).Error)

	// A later disabled Key must not hide the same accepted receipt.
	require.NoError(t, db.Model(&token).Update("status", common.TokenStatusDisabled).Error)
	replay := post(context.Background(), selection, "192.0.2.4")
	assert.Equal(t, 200, replay.Code, replay.Body.String())
	assert.Contains(t, replay.Body.String(), id)
	history := request(context.Background(), "GET", "/runs", "", "192.0.2.4", false)
	assert.Contains(t, history.Body.String(), id)
	assert.NotContains(t, history.Body.String(), "Secret echo")
	assert.EqualValues(t, 1, calls.Load())

	// An explicit provider error is not an incorrect candy answer and debug
	// must not record the arbitrary provider error/credential text.
	require.NoError(t, db.Model(&token).Update("status", common.TokenStatusEnabled).Error)
	failure := selection
	failure.ClientID = uuid.NewString()
	forceFailure.Store(true)
	require.Equal(t, http.StatusAccepted, post(context.Background(), failure, "192.0.2.4").Code)
	var failed string
	require.Eventually(t, func() bool {
		failed = request(context.Background(), "GET", "/runs/"+failure.ClientID, "", "203.0.113.4", false).Body.String()
		return strings.Contains(failed, `"status":"failed"`)
	}, 5*time.Second, 25*time.Millisecond)
	assert.NotContains(t, failed, `"candy_result"`)
	assert.NotContains(t, failed, "fixture error body")
	assert.EqualValues(t, 2, calls.Load())
	common.LogWriterMu.Lock()
	captured := capturedLogs.String()
	common.LogWriterMu.Unlock()
	assert.NotContains(t, captured, "fixture error body")
	assert.NotContains(t, captured, "Secret echo")
	assert.NotContains(t, captured, "syntheticownercredential")
	assert.NotContains(t, captured, "synthetic-not-a-real-key")
	var allLogs []model.Log
	require.NoError(t, db.Where("user_id = ?", userID).Find(&allLogs).Error)
	for _, row := range allLogs {
		assert.NotContains(t, row.Content+row.Other+row.UpstreamRequestId, "syntheticownercredential")
		assert.NotContains(t, row.Content+row.Other+row.UpstreamRequestId, "synthetic-not-a-real-key")
	}
}

func TestKeyDiagnosticPostgresClaimsNeverRedispatchAfterInterruptedProcess(t *testing.T) {
	db, owner := setupModelTestSwitchExecution(t)
	require.NoError(t, db.AutoMigrate(&model.UserSession{}))
	session := model.UserSession{SID: "fixture-session", UserID: owner, UserAuthVersion: 1, Version: 1,
		Status: model.UserSessionStatusActive, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	require.NoError(t, db.Create(&session).Error)
	identity := model.AuthSessionIdentity{UserID: owner, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1}

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()
	require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("base_url", upstream.URL).Error)
	var token model.Token
	require.NoError(t, db.Where("user_id = ?", owner).First(&token).Error)
	question := keyDiagnosticQuestions[1]
	now := time.Now()
	id := uuid.NewString()
	row := model.ModelTestRun{UserID: owner, TokenID: token.Id, Model: "gpt-4o-mini", BenchmarkID: question.ID,
		CreatedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), MaxOutputTokens: question.Limit, RequestID: "diag-" + id}
	replayed, err := model.CreateKeyDiagnosticRun(context.Background(), &row, id)
	require.NoError(t, err)
	require.False(t, replayed)

	claims := make(chan bool, 2)
	for range 2 {
		go func() {
			ok, err := model.ClaimKeyDiagnosticRun(context.Background(), row, identity, time.Now().Add(time.Minute).Unix())
			assert.NoError(t, err)
			claims <- ok
		}()
	}
	require.NotEqual(t, <-claims, <-claims, "one process owns the persistent irreversible claim")
	require.NoError(t, db.Model(&model.ModelTestRun{}).Where("id = ?", row.ID).Update("expires_at", now.Add(-time.Second).Unix()).Error)
	// A worker invoked on the same in-memory data after loss of the previous
	// process/acknowledgement cannot claim again or reach billing/provider.
	executeKeyDiagnostic(context.Background(), keyDiagnosticWorkerJob{row, keyDiagnosticAdmission{identity: identity, question: question, ip: "192.0.2.4"}, token.CacheRevision})
	saved, err := model.GetKeyDiagnosticRun(context.Background(), owner, id)
	require.NoError(t, err)
	view := diagnosticView(*saved, model.KeyDiagnosticBill{})
	assert.Equal(t, "unknown", view.Status)
	assert.Equal(t, "worker_interrupted", view.ErrorCode)
	assert.Equal(t, "unconfirmed", view.Billing.Status)
	assert.Nil(t, view.Billing.Quota)
	assert.Zero(t, calls.Load())

	// A revoked session before claim proves no attempt was dispatched.
	secondID := uuid.NewString()
	second := row
	second.RequestID, second.ExpiresAt = "diag-"+secondID, now.Add(time.Minute).Unix()
	_, err = model.CreateKeyDiagnosticRun(context.Background(), &second, secondID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&session).Update("revoked_at", now.Unix()).Error)
	executeKeyDiagnostic(context.Background(), keyDiagnosticWorkerJob{second, keyDiagnosticAdmission{identity: identity, question: question, ip: "192.0.2.4"}, token.CacheRevision})
	saved, err = model.GetKeyDiagnosticRun(context.Background(), owner, secondID)
	require.NoError(t, err)
	view = diagnosticView(*saved, model.KeyDiagnosticBill{})
	assert.Equal(t, "failed", view.Status)
	assert.Equal(t, "session_unavailable", view.ErrorCode)
	assert.Zero(t, calls.Load())

	// Freeze the trusted original client while queued, then change only its
	// Key allowlist (not its revision) to isolate the second IP check.
	require.NoError(t, db.Model(&session).Update("revoked_at", 0).Error)
	thirdID := uuid.NewString()
	third := row
	third.RequestID, third.ExpiresAt = "diag-"+thirdID, time.Now().Add(time.Minute).Unix()
	_, err = model.CreateKeyDiagnosticRun(context.Background(), &third, thirdID)
	require.NoError(t, err)
	require.NoError(t, db.Model(&token).Update("allow_ips", "203.0.113.0/24").Error)
	executeKeyDiagnostic(context.Background(), keyDiagnosticWorkerJob{third, keyDiagnosticAdmission{identity: identity, question: question, ip: "192.0.2.4"}, token.CacheRevision})
	saved, err = model.GetKeyDiagnosticRun(context.Background(), owner, thirdID)
	require.NoError(t, err)
	view = diagnosticView(*saved, model.KeyDiagnosticBill{})
	assert.Equal(t, "failed", view.Status)
	assert.Equal(t, "token_unavailable", view.ErrorCode)
	assert.Zero(t, calls.Load())
	var billCount int64
	require.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ?", owner).Count(&billCount).Error)
	assert.Zero(t, billCount)
}
