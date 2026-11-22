package relay_test

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Called by the existing real-database Responses accounting matrix so the
// wallet runtime and frozen funding registry are configured exactly once.
func runBufferedResponsesAccountingCase(t *testing.T, db, logDB *gorm.DB, format types.RelayFormat, outcome string) {
	t.Helper()
	const requestedModel = "converted-stream-accounting"
	const paid, bonus, reward = 50000, 100, 70
	imageResponse := strings.HasPrefix(outcome, "image_")
	outcome = strings.TrimPrefix(outcome, "image_")
	jsonResponse := strings.HasPrefix(outcome, "json_")
	outcome = strings.TrimPrefix(outcome, "json_")
	success := outcome == "success" || outcome == "incomplete" || outcome == "fixed_success_missing_usage"
	unknown := outcome == "empty_eof" || outcome == "fixed_empty_eof" || outcome == "partial_unknown" || strings.Contains(outcome, "total_only")
	noUsage := strings.Contains(outcome, "total_only") || outcome == "partial_unknown" || strings.HasPrefix(outcome, "empty_") || strings.HasPrefix(outcome, "fixed_empty") || outcome == "fixed_success_missing_usage"
	fixed := strings.HasPrefix(outcome, "fixed_")
	wantCharge := 15
	if noUsage {
		wantCharge = 0
	}
	if fixed && (!noUsage || success) {
		wantCharge = 5000
	}
	if fixed {
		settings, ok := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
		require.True(t, ok)
		previous := *settings
		settings.BillingMode = maps.Clone(settings.BillingMode)
		settings.BillingExpr = maps.Clone(settings.BillingExpr)
		settings.BillingMode[requestedModel] = billing_setting.BillingModeTieredExpr
		settings.BillingExpr[requestedModel] = `tier("request", fixed(0.01))`
		t.Cleanup(func() { *settings = previous })
	}

	identity := strings.ReplaceAll(uuid.NewString(), "-", "")
	user := model.User{Username: "buffered_" + identity, AffCode: identity, Quota: paid + bonus + reward, QuotaPaid: paid, QuotaBonus: bonus, QuotaReward: reward, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "test-buffered-" + identity, RemainQuota: paid, Status: common.TokenStatusEnabled}
	require.NoError(t, db.Create(&token).Error)
	var calls atomic.Int32
	requests := make(chan string, 4)
	type heldSnapshot struct {
		accounting bufferedResponsesAccountingSnapshot
		err        error
	}
	heldSnapshots := make(chan heldSnapshot, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- r.URL.Path + "\n" + string(raw)
		if unknown {
			// Read the committed hold before upstream emits any response. The
			// final owner is not itself proof that the original debit survived.
			held, err := captureBufferedResponsesAccounting(db, identity, user.Id, token.Id)
			heldSnapshots <- heldSnapshot{held, err}
		}
		if imageResponse {
			w.Header().Set("Content-Type", "text/event-stream")
			if outcome == "empty_eof" {
				return
			}
			if outcome == "partial_unknown" {
				_, _ = fmt.Fprint(w, `data: {"type":"image_generation.partial_image","b64_json":"partial"}`+"\n\n")
			}
			usage := ""
			if !noUsage {
				usage = `,"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}`
			}
			_, _ = fmt.Fprint(w, `data: {"type":"error","error":{"type":"server_error","message":"provider-private failed"}`+usage+"}\n\n")
			return
		}
		if jsonResponse {
			w.Header().Set("Content-Type", "application/json")
			response := map[string]any{"id": "resp_partial", "model": "provider-private", "status": "failed", "error": map[string]any{"type": "server_error", "code": "overloaded", "message": "provider-private failed"}}
			if !noUsage {
				response["usage"] = map[string]int{"input_tokens": 11, "output_tokens": 4, "total_tokens": 15}
			}
			if strings.Contains(outcome, "total_only") {
				response["usage"] = map[string]int{"total_tokens": 15}
			}
			if outcome == "failed_status" {
				response["error"] = map[string]any{"code": "overloaded", "message": "provider-private failed"}
			}
			if outcome == "cancelled" || outcome == "cancelled_total_only" {
				response["status"] = "cancelled"
				delete(response, "error")
			}
			encoded, err := common.Marshal(response)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			_, _ = w.Write(encoded)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: "+`{"type":"response.created","response":{"id":"resp_partial","model":"provider-private","status":"in_progress"}}`+"\n\n")
		if !noUsage {
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.in_progress","response":{"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15}}}`+"\n\n")
			_, _ = fmt.Fprint(w, "data: "+`{"type":"response.output_text.delta","delta":"partial answer"}`+"\n\n")
		}
		ending := `{"type":"response.failed","response":{"status":"failed","error":{"type":"server_error","code":"overloaded","message":"provider-private failed"}}}`
		switch outcome {
		case "bare_error":
			ending = `{"type":"error","code":"overloaded","message":"provider-private failed"}`
		case "cancelled":
			ending = `{"type":"response.cancelled","response":{"status":"cancelled"}}`
		case "failed_status":
			ending = `{"type":"response.done","response":{"status":"failed","error":{"code":"overloaded","message":"provider-private failed"}}}`
		case "eof", "empty_eof", "fixed_empty_eof":
			return
		case "done_without_terminal":
			ending = "[DONE]"
		case "incomplete":
			ending = `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`
		case "success", "fixed_success_missing_usage":
			ending = `{"type":"response.completed","response":{"status":"completed"}}`
		}
		_, _ = fmt.Fprint(w, "data: "+ending+"\n\n")
	}))
	t.Cleanup(upstream.Close)
	channel := model.Channel{Name: "buffered-" + identity, Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled,
		Key: "test-only", BaseURL: common.GetPointer(upstream.URL), Models: requestedModel, Group: "default", AutoBan: common.GetPointer(0)}
	path := "/v1/chat/completions"
	body := `{"model":"` + requestedModel + `","messages":[{"role":"user","content":"Please return a helpful answer to this short test request"}],"stream":false}`
	if imageResponse {
		path = "/v1/images/generations"
		body = `{"model":"` + requestedModel + `","prompt":"draw a tree","n":1,"stream":true}`
	} else if format == types.RelayFormatClaude {
		path = "/v1/messages"
		body = `{"model":"` + requestedModel + `","messages":[{"role":"user","content":"Please return a helpful answer to this short test request"}],"max_tokens":32,"stream":false}`
	} else if format == types.RelayFormatGemini {
		path = "/v1beta/models/" + requestedModel + ":generateContent"
		body = `{"contents":[{"role":"user","parts":[{"text":"Please return a helpful answer to this short test request"}]}],"generationConfig":{"maxOutputTokens":32}}`
		channel.Type = constant.ChannelTypeAdvancedCustom
		channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1beta/models/{model}:generateContent", UpstreamPath: "/v1/responses", Converter: relayconvert.ConverterGeminiContentToOpenAIResponses,
		}}}})
	}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, channel.AddAbilities(db))
	newContext := func() (*gin.Context, *httptest.ResponseRecorder) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		for key, value := range map[constant.ContextKey]any{
			constant.ContextKeyUserId: user.Id, constant.ContextKeyUserQuota: paid + bonus + reward,
			constant.ContextKeyUserGroup: "default", constant.ContextKeyTokenGroup: "default", constant.ContextKeyUsingGroup: "default",
			constant.ContextKeyTokenId: token.Id, constant.ContextKeyTokenKey: token.Key,
			constant.ContextKeyUserSetting: dto.UserSetting{BillingPreference: "wallet_only"},
		} {
			common.SetContextKey(c, key, value)
		}
		c.Set(common.RequestIdKey, identity)
		c.Set("username", user.Username)
		c.Set("token_quota", paid)
		require.Nil(t, middleware.SetupContextForSelectedChannel(c, &channel, requestedModel))
		return c, recorder
	}
	c, recorder := newContext()
	completion := observeNativeResponsesRefundWithin(t, db, identity, wantCharge == 0 && !unknown, 5*time.Second, &token)
	var verifyBarrier func()
	if format == types.RelayFormatGemini && outcome == "empty_failure" {
		verifyBarrier = observeBufferedRefundCommitBarrier(t, db, completion, token.Id)
	}

	controller.Relay(c, format)
	if verifyBarrier != nil {
		verifyBarrier()
	}
	completion.join(t)

	require.Equal(t, int32(1), calls.Load(), "a buffered failure must not repeat a potentially billable upstream request")
	assert.Equal(t, 1, service.RequestPolicy(c).Attempts)
	assert.Equal(t, success && outcome != "incomplete", service.RequestPolicy(c).Successful, "partial billing must never publish a success outcome for failed or truncated upstream work")
	request := <-requests
	if imageResponse {
		assert.True(t, strings.HasPrefix(request, "/v1/images/generations\n"), request)
		assert.Contains(t, request, `"stream":true`)
	} else {
		assert.True(t, strings.HasPrefix(request, "/v1/responses\n"), request)
		assert.Contains(t, request, `"input":`)
		assert.NotContains(t, request, `"stream":true`)
		assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyStreamErrorHandled), "the outer controller must write the JSON error")
	}
	wire := recorder.Body.String()
	if !imageResponse {
		assert.NotContains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
		assert.NotContains(t, wire, "data:")
	}
	assert.NotContains(t, wire, "provider-private")
	if success {
		assert.Equal(t, http.StatusOK, recorder.Code, wire)
		assert.NotContains(t, wire, `"error":`)
		if outcome == "incomplete" {
			assert.NotContains(t, wire, `"finish_reason":"stop"`)
			assert.NotContains(t, wire, `"stop_reason":"end_turn"`)
			assert.NotContains(t, wire, `"finishReason":"STOP"`)
		}
	} else {
		if imageResponse && outcome != "empty_eof" {
			assert.Equal(t, http.StatusOK, recorder.Code, wire)
		} else {
			assert.Equal(t, http.StatusBadGateway, recorder.Code, wire)
		}
		assert.Equal(t, 1, strings.Count(wire, `"error":`))
		assert.NotContains(t, wire, `"finish_reason":`)
		assert.NotContains(t, wire, `"finishReason":`)
	}

	var logs []model.Log
	require.NoError(t, logDB.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
	if wantCharge == 0 {
		assert.Empty(t, logs)
	} else {
		require.Len(t, logs, 1, wire)
		assert.Equal(t, wantCharge, logs[0].Quota)
		assert.Equal(t, imageResponse, logs[0].IsStream)
		if !noUsage {
			assert.Equal(t, 11, logs[0].PromptTokens)
			assert.Equal(t, 4, logs[0].CompletionTokens)
		}
	}
	var owner model.BillingFinalization
	require.NoError(t, db.Where("request_id = ?", identity).First(&owner).Error)
	if unknown {
		held := <-heldSnapshots
		require.NoError(t, held.err)
		require.Equal(t, model.BillingHeld, held.accounting.Owner.State)
		require.Positive(t, held.accounting.Owner.SourceHeld)
		assert.Equal(t, held.accounting.Owner.SourceHeld, held.accounting.Owner.TokenHeld)
		assert.Equal(t, held.accounting.Owner.SourceHeld, held.accounting.Owner.PreConsumed)
		if fixed {
			assert.Equal(t, 5000, held.accounting.Owner.SourceHeld)
		}
		assert.Equal(t, model.BillingDeliveryUnknown, owner.State)
		assert.Zero(t, owner.ActualQuota, "unknown is not a zero-usage settlement")
		assert.Equal(t, held.accounting.Owner.SourceHeld, owner.SourceHeld)
		assert.Equal(t, held.accounting.Owner.TokenHeld, owner.TokenHeld)
		assert.Equal(t, held.accounting.Owner.PreConsumed, owner.PreConsumed)
		assert.Equal(t, held.accounting.Owner.Shares(), owner.Shares())
		assert.Equal(t, user.Id, owner.UserID)
		assert.Equal(t, token.Id, owner.TokenID)
		assert.Equal(t, model.BillingTokenFingerprint(token.Key), owner.TokenFingerprint)
		after, err := captureBufferedResponsesAccounting(db, identity, user.Id, token.Id)
		require.NoError(t, err)
		assert.Equal(t, held.accounting.User, after.User)
		assert.Equal(t, held.accounting.Token, after.Token)
		assert.Equal(t, held.accounting.Ledger, after.Ledger, "unknown delivery must neither credit nor add a guessed charge")
		require.NotEmpty(t, after.Ledger)
		net := 0
		accountNet := make(map[string]int)
		for _, row := range after.Ledger {
			assert.Negative(t, row.Delta)
			net += row.Delta
			accountNet[row.Account] += row.Delta
		}
		rewardHeld := min(reward, owner.SourceHeld)
		paidHeld := owner.SourceHeld - rewardHeld
		assert.Equal(t, -owner.SourceHeld, net)
		assert.Equal(t, paid+bonus+reward-owner.SourceHeld, after.User.Quota)
		assert.Equal(t, paid-paidHeld, after.User.QuotaPaid)
		assert.Equal(t, bonus, after.User.QuotaBonus)
		assert.Equal(t, reward-rewardHeld, after.User.QuotaReward)
		assert.Equal(t, -paidHeld, accountNet[string(model.AccountPaid)])
		assert.Zero(t, accountNet[string(model.AccountBonus)])
		assert.Equal(t, -rewardHeld, accountNet[string(model.AccountReward)])
		assert.Equal(t, paid-owner.TokenHeld, after.Token.RemainQuota)
		assert.Equal(t, owner.TokenHeld, after.Token.UsedQuota)

		// A fresh HTTP context with the same financial identity must not send
		// another generation, debit again, or reopen this hold for refund.
		replay, replayRecorder := newContext()
		controller.Relay(replay, format)
		assert.GreaterOrEqual(t, replayRecorder.Code, http.StatusBadRequest)
		assert.Equal(t, int32(1), calls.Load())
		_, err = model.RequestBillingRefund(t.Context(), held.accounting.Owner)
		require.ErrorIs(t, err, model.ErrBillingFinalized)
		ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
		require.NoError(t, err)
		for _, candidate := range ready {
			assert.NotEqual(t, identity, candidate.RequestID)
		}

		// The public read-only report scans all rows, so follow its pagination
		// instead of relying on a Where clause that the reader discards.
		found := false
		err = db.Transaction(func(tx *gorm.DB) error {
			for offset := 0; ; {
				page, err := model.ReadFinancialReviewReport(t.Context(), tx, model.FinancialReviewReportOptions{
					Entity: model.FinancialReviewBilling, Offset: offset, Limit: model.FinancialReviewMaxPageSize,
				})
				if err != nil {
					return err
				}
				assert.Equal(t, "available", page.Schema.Status)
				for _, item := range page.Items {
					if item.RequestID == identity {
						found = true
						assert.Equal(t, model.BillingDeliveryUnknown, item.State)
						assert.Contains(t, item.Issues, "delivery_evidence_unknown")
						assert.Equal(t, int64(owner.SourceHeld), item.Amounts["source_held"])
						assert.Equal(t, int64(owner.TokenHeld), item.Amounts["token_held"])
					}
				}
				if !page.HasMore {
					return nil
				}
				offset = page.NextOffset
			}
		}, &sql.TxOptions{ReadOnly: true})
		require.NoError(t, err)
		assert.True(t, found, "unknown delivery must remain visible for manual reconciliation")
		replayed, err := captureBufferedResponsesAccounting(db, identity, user.Id, token.Id)
		require.NoError(t, err)
		assert.Equal(t, after, replayed, "replay, rejected refund and read-only reporting must leave all financial evidence unchanged")
		var logCount int64
		require.NoError(t, logDB.Model(&model.Log{}).Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Count(&logCount).Error)
		assert.Zero(t, logCount)
		t.Logf("accounting: state=%s source_hold=%d token_hold=%d ledger_net=%d same_id_replay_and_readonly_report_unchanged", owner.State, owner.SourceHeld, owner.TokenHeld, net)
		return
	}
	wantState := model.BillingSettled
	if wantCharge == 0 {
		wantState = model.BillingRefunded
	}
	assert.Equal(t, wantState, owner.State)
	assert.Equal(t, wantCharge, owner.ActualQuota)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	rewardCharge := min(reward, wantCharge)
	paidCharge := wantCharge - rewardCharge
	assert.Equal(t, paid-paidCharge, user.QuotaPaid)
	assert.Equal(t, bonus, user.QuotaBonus)
	assert.Equal(t, reward-rewardCharge, user.QuotaReward)
	assert.Equal(t, paid+bonus+reward-wantCharge, user.Quota)
	assert.Equal(t, paid-wantCharge, token.RemainQuota)
	assert.Equal(t, wantCharge, token.UsedQuota)
	var ledger []model.BalanceLedger
	require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&ledger).Error)
	require.NotEmpty(t, ledger, "the test must exercise the real BillingSession reservation")
	assert.Negative(t, ledger[0].Delta)
	net := 0
	accountNet := make(map[string]int)
	for _, row := range ledger {
		net += row.Delta
		accountNet[row.Account] += row.Delta
	}
	assert.Equal(t, -wantCharge, net, "settlement followed by the outer refund must move money exactly once")
	assert.Equal(t, -rewardCharge, accountNet[string(model.AccountReward)])
	assert.Equal(t, -paidCharge, accountNet[string(model.AccountPaid)])
	assert.Zero(t, accountNet[string(model.AccountBonus)])
	t.Logf("accounting: state=%s actual=%d ledger_net=%d token_used=%d", owner.State, owner.ActualQuota, net, token.UsedQuota)
}

type bufferedResponsesAccountingSnapshot struct {
	User   model.User
	Token  model.Token
	Owner  model.BillingFinalization
	Ledger []model.BalanceLedger
}

func captureBufferedResponsesAccounting(db *gorm.DB, requestID string, userID, tokenID int) (bufferedResponsesAccountingSnapshot, error) {
	var snapshot bufferedResponsesAccountingSnapshot
	err := errors.Join(
		db.First(&snapshot.User, userID).Error,
		db.First(&snapshot.Token, tokenID).Error,
		db.Where("request_id = ?", requestID).First(&snapshot.Owner).Error,
		db.Where("user_id = ?", userID).Order("id").Find(&snapshot.Ledger).Error,
	)
	return snapshot, err
}

// Keep the Gemini explicit empty-failure refund at the original 5 s budget while
// deterministically exposing the old pre-debit signal and uncommitted refund.
func observeBufferedRefundCommitBarrier(t *testing.T, db *gorm.DB, completion *nativeResponsesRefundCompletion, tokenID int) func() {
	t.Helper()
	oldSignal := make(chan int, 1)
	blocked := make(chan *nativeResponsesRefundTransaction, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	completion.driverWait = make(chan *nativeResponsesRefundTransaction, 1)
	waitDone := make(chan struct{})
	waitStarted := false
	callback := "buffered_refund_commit_barrier_" + completion.requestID
	require.NoError(t, db.Callback().Update().After("native_refund_completion_"+completion.requestID).Register(callback, func(tx *gorm.DB) {
		delta, ok := nativeResponsesTokenQuotaDelta(tx, tokenID)
		if !ok {
			return
		}
		completion.mu.Lock()
		r := completion.records[tx.Statement.Context]
		matches := r != nil && r.matchesToken
		refund := matches && r.role == "refund"
		completion.mu.Unlock()
		if delta < 0 && matches {
			// The removed SQL-only predicate accepted this actual reservation.
			select {
			case oldSignal <- delta:
			default:
			}
		}
		if delta > 0 && refund {
			blocked <- r
			select {
			case <-release:
			case <-tx.Statement.Context.Done():
			}
		}
	}))
	// LIFO: release even on Fatal, join the once-owned wait, then remove hooks.
	// The observer's own cleanup subsequently restores model.DB and its hooks.
	t.Cleanup(func() {
		unblock()
		completion.join(t)
		if waitStarted {
			<-waitDone
		}
		require.NoError(t, db.Callback().Update().Remove(callback))
	})
	return func() {
		t.Helper()
		// The controller-return wait, barrier and final owner query share one deadline.
		completion.deadline = time.Now().Add(completion.budget)
		budget := time.NewTimer(time.Until(completion.deadline))
		defer budget.Stop()
		select {
		case delta := <-oldSignal:
			require.Negative(t, delta, "the old waiter accepted this request's pre-debit")
			t.Log("refund_barrier: initial_negative_observed")
		default:
			require.FailNow(t, "the original token reservation did not exercise the old false signal")
		}
		waitStarted = true
		go func() {
			completion.once.Do(func() { completion.joined, completion.err = completion.wait() })
			close(waitDone)
		}()
		var refund *nativeResponsesRefundTransaction
		select {
		case refund = <-blocked:
			t.Log("refund_barrier: same_owned_refund_precommit")
		case <-budget.C:
			require.FailNow(t, "the original token refund did not reach the pre-commit barrier")
		}
		select {
		case observed := <-completion.driverWait:
			require.Same(t, refund, observed, "wait must select this request's real refund transaction")
		case <-budget.C:
			require.FailNow(t, "completion did not reach the real driver wait")
		}
		select {
		case <-refund.finished:
			require.FailNow(t, "refund completed before the commit barrier was released")
		default:
		}
		select {
		case <-waitDone:
			require.FailNow(t, "the old reservation signal released the refund completion wait")
		default:
		}
		t.Log("refund_barrier: wait_selected_same_tx_not_finished")
		unblock()
		completion.join(t)
		<-waitDone
		require.NoError(t, completion.err)
		t.Log("refund_barrier: released_and_owned_tail_joined")
	}
}
