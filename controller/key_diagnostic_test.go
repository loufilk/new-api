package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestKeyDiagnosticReferenceCandyOnlyGradesFinalLine(t *testing.T) {
	for answer, want := range map[string]string{
		"在任意抽取的情况下是29，但这里可选择形状。\n最终答案：21": "correct",
		"推导略。\r\n**最终答案：二十一颗。**\r\n":      "correct",
		"最终答案：29":          "incorrect",
		"**最终答案：二十九 颗**":   "incorrect",
		"最终答案：8 颗":         "incorrect",
		"最终答案：+０２１":        "correct",
		"最终答案：1000":        "incorrect",
		"最终答案：十一一":         "unrecognized",
		"结论：21 颗。":         "unrecognized",
		"最终答案：21或29":       "unrecognized",
		"最终答案：21\n还有另一种可能": "unrecognized",
		"21": "unrecognized",
	} {
		assert.Equal(t, want, classifyReferenceCandyAnswer(answer), answer)
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
			questionID := keyDiagnosticCandyV2
			if forceFailure.Load() {
				questionID = keyDiagnosticPelicanV2
			}
			expected, active := lookupKeyDiagnosticQuestion(questionID)
			assert.True(t, active)
			assert.Equal(t, expected.Limit, input.MaxTokens)
			assert.Equal(t, "user", input.Messages[0].Role)
			assert.Equal(t, expected.Prompt, input.Messages[0].Content)
		}
		w.Header().Set("Content-Type", "application/json")
		if forceFailure.Load() {
			w.Header().Set("X-Request-ID", "synthetic-not-a-real-key")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"error":{"type":"server_error","message":"fixture error body echo synthetic-not-a-real-key syntheticownercredential"}}`)
			return
		}
		fmt.Fprint(w, `{"id":"local-response","object":"chat.completion","model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"Secret echo: syntheticownercredential\n最终答案：21"},"finish_reason":"stop"}],"usage":{"prompt_tokens":80,"completion_tokens":3,"total_tokens":83}}`)
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
	var published struct {
		Data struct {
			Questions []keyDiagnosticQuestion
		}
	}
	require.NoError(t, common.Unmarshal(get.Body.Bytes(), &published))
	assert.Equal(t, keyDiagnosticQuestions, published.Data.Questions)
	require.Len(t, published.Data.Questions, 2)
	assert.Equal(t, 32000, published.Data.Questions[0].Limit)
	assert.Equal(t, 8192, published.Data.Questions[1].Limit)
	// Digests pin the exact two transcriptions, including punctuation.
	assert.Equal(t, "3b8cb2b57eae95d991f2954afcb66a24f3b1a7d02d68f2a60bb23f36b9b7b8ab", fmt.Sprintf("%x", sha256.Sum256([]byte(published.Data.Questions[0].Prompt))))
	assert.Equal(t, "371cc073a59ba4e6d086de24edb2dc796cf69435559b7628c892124c6b7fbb9d", fmt.Sprintf("%x", sha256.Sum256([]byte(published.Data.Questions[1].Prompt))))
	assert.Zero(t, calls.Load())
	id := uuid.NewString()
	selection := keyDiagnosticRequest{TokenID: token.Id, Model: "gpt-4o-mini", Question: keyDiagnosticCandyV2, ClientID: id, Confirmed: true}
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

	// The earlier question can be replayed only if this exact receipt exists.
	// An old browser cannot spend against its retired prompt or use that UUID
	// for a different revision. The raw previous outcome is not recomputed.
	legacy := selection
	legacy.Question, legacy.ClientID = keyDiagnosticCandyV1, uuid.NewString()
	stale := post(context.Background(), legacy, "192.0.2.4")
	assert.Equal(t, http.StatusBadRequest, stale.Code)
	assert.Contains(t, stale.Body.String(), "question_retired")
	assert.Equal(t, http.StatusNotFound, request(context.Background(), "GET", "/runs/"+legacy.ClientID, "", "192.0.2.4", false).Code)
	historicalTime := time.Now().Add(-2 * time.Hour).Unix()
	legacyRow := model.ModelTestRun{UserID: userID, TokenID: token.Id, Model: legacy.Model, Group: token.Group,
		BenchmarkID: legacy.Question, MaxOutputTokens: 8192, CreatedAt: historicalTime, ExpiresAt: historicalTime + 60, RequestID: "synthetic-v1-receipt"}
	_, err := model.CreateKeyDiagnosticRun(context.Background(), &legacyRow, legacy.ClientID)
	require.NoError(t, err)
	legacyRow.Status, legacyRow.CompletedAt, legacyRow.ArtworkHTML = "completed", historicalTime+2, "答案：8 颗。"
	legacyDetails := model.ReadKeyDiagnosticDetails(legacyRow)
	legacyDetails.CandyResult = "correct"
	require.NoError(t, model.FinishSavedKeyDiagnostic(context.Background(), legacyRow, model.KeyDiagnosticQueued, legacyDetails))
	oldResult := post(context.Background(), legacy, "203.0.113.4")
	assert.Equal(t, http.StatusOK, oldResult.Code, oldResult.Body.String())
	assert.Contains(t, oldResult.Body.String(), `"question":"candy-logic-v1"`)
	assert.Contains(t, oldResult.Body.String(), `"candy_result":"correct"`)
	assert.Contains(t, oldResult.Body.String(), "答案：8 颗。")
	crossVersion := legacy
	crossVersion.Question = keyDiagnosticCandyV2
	assert.Equal(t, http.StatusConflict, post(context.Background(), crossVersion, "192.0.2.4").Code)
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
	denied.Question = keyDiagnosticPelicanV2
	assert.Equal(t, 409, post(context.Background(), denied, "192.0.2.4").Code)
	denied = selection
	denied.ClientID = uuid.NewString()
	busy := post(context.Background(), denied, "192.0.2.4")
	require.Equal(t, http.StatusTooManyRequests, busy.Code, "the current paid call occupies the same owner concurrency slot")
	assert.Contains(t, busy.Body.String(), `"limit_reason":"user_active"`)
	assert.Empty(t, busy.Header().Get("Retry-After"))
	require.Equal(t, http.StatusNotFound, request(context.Background(), "GET", "/runs/"+denied.ClientID, "", "192.0.2.4", false).Code)
	assert.EqualValues(t, 1, calls.Load())
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
	failure.Question = keyDiagnosticPelicanV2
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
	// Third saved task is allowed, then a new UUID is refused before there
	// is anything to poll or charge. Existing receipts remain replayable.
	forceFailure.Store(false)
	third := selection
	third.ClientID = uuid.NewString()
	require.Equal(t, http.StatusAccepted, post(context.Background(), third, "192.0.2.4").Code)
	require.Eventually(t, func() bool {
		get := request(context.Background(), "GET", "/runs/"+third.ClientID, "", "192.0.2.4", false)
		return get.Code == http.StatusOK && strings.Contains(get.Body.String(), `"status":"completed"`)
	}, 5*time.Second, 25*time.Millisecond)
	fourth := selection
	fourth.ClientID = uuid.NewString()
	// Keep two new runs and a third expiring in about a minute. A fourth
	// historical row just inside the window must not determine this wait.
	now := time.Now().Unix()
	require.NoError(t, db.Model(&model.ModelTestRun{}).Where("id = ?", run.ID).Update("created_at", now-3540).Error)
	require.NoError(t, db.Model(&model.ModelTestRun{}).Where("id = ?", legacyRow.ID).Update("created_at", now-3590).Error)
	limited := post(context.Background(), fourth, "192.0.2.4")
	require.Equal(t, http.StatusTooManyRequests, limited.Code, limited.Body.String())
	var reply struct {
		Success bool
		Code    string
		Reason  string `json:"limit_reason"`
		Seconds int    `json:"retry_after_seconds"`
	}
	require.NoError(t, common.Unmarshal(limited.Body.Bytes(), &reply))
	assert.False(t, reply.Success)
	assert.Equal(t, "rate_or_concurrency_limit", reply.Code)
	assert.Equal(t, "hourly", reply.Reason)
	assert.InDelta(t, 61, reply.Seconds, 3)
	assert.Equal(t, strconv.Itoa(reply.Seconds), limited.Header().Get("Retry-After"))
	assert.Equal(t, http.StatusNotFound, request(context.Background(), "GET", "/runs/"+fourth.ClientID, "", "192.0.2.4", false).Code)
	assert.Equal(t, http.StatusOK, post(context.Background(), third, "192.0.2.4").Code)
	assert.EqualValues(t, 3, calls.Load())

	// Simulate the sliding window advancing: repeat the refused UUID under
	// fresh explicit consent, prove only one paid task is ever dispatched.
	require.NoError(t, db.Model(&model.ModelTestRun{}).Where("id IN ?", []string{run.ID, legacyRow.ID}).Update("created_at", now-3602).Error)
	require.Equal(t, http.StatusAccepted, post(context.Background(), fourth, "192.0.2.4").Code)
	require.Eventually(t, func() bool {
		reply := request(context.Background(), "GET", "/runs/"+fourth.ClientID, "", "192.0.2.4", false)
		return reply.Code == http.StatusOK && strings.Contains(reply.Body.String(), `"status":"completed"`)
	}, 5*time.Second, 25*time.Millisecond)
	assert.Equal(t, http.StatusOK, post(context.Background(), fourth, "192.0.2.4").Code)
	assert.EqualValues(t, 4, calls.Load())
	var settlements int64
	require.NoError(t, db.Model(&model.BillingFinalization{}).Where("request_id = ?", "diag-"+model.KeyDiagnosticRowID(userID, fourth.ClientID)).Count(&settlements).Error)
	assert.EqualValues(t, 1, settlements)

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

func TestKeyDiagnosticPostgresCapacityClearsAndCannotDoubleAdmit(t *testing.T) {
	db, owner := setupModelTestsPostgres(t)
	now := time.Now().Unix()
	for n := 1; n <= 4; n++ {
		row := model.ModelTestRun{ID: uuid.NewString(), UserID: owner + n, Source: model.KeyDiagnosticSource,
			Status: model.KeyDiagnosticRunning, CreatedAt: now, ExpiresAt: now + 120}
		require.NoError(t, db.Create(&row).Error)
	}
	id := uuid.NewString()
	row := model.ModelTestRun{UserID: owner, TokenID: 1, Model: "fixture", BenchmarkID: keyDiagnosticCandyV2, CreatedAt: now, ExpiresAt: now + 30}
	_, err := model.CreateKeyDiagnosticRun(t.Context(), &row, id)
	var limit *model.KeyDiagnosticLimit
	require.ErrorAs(t, err, &limit)
	assert.ErrorIs(t, err, model.ErrModelTestLimit)
	assert.Equal(t, "capacity", limit.Reason)
	assert.Zero(t, limit.RetryAfterSeconds)
	require.NoError(t, db.Model(&model.ModelTestRun{}).Where("user_id = ?", owner+1).Update("expires_at", now-1).Error)
	replay, err := model.CreateKeyDiagnosticRun(t.Context(), &row, id)
	require.NoError(t, err)
	require.False(t, replay)
	replay, err = model.CreateKeyDiagnosticRun(t.Context(), &row, id)
	require.NoError(t, err)
	require.True(t, replay)
}
