package service

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The writer also verifies that a slow log sink cannot retain the session lock.
// All test work is synchronous; no unowned refund worker is started.
type unknownDeliveryLogWriter struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	session  *BillingSession
	unlocked bool
}

func (w *unknownDeliveryLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	unlocked := w.session.mu.TryLock()
	if unlocked {
		w.session.mu.Unlock()
	}
	w.unlocked = w.unlocked && unlocked
	return w.buffer.Write(p)
}

func captureUnknownDeliveryLog(t *testing.T, s *BillingSession) *unknownDeliveryLogWriter {
	t.Helper()
	withDebugEnabled(t, false)
	w := &unknownDeliveryLogWriter{session: s, unlocked: true}
	common.LogWriterMu.Lock()
	previous := gin.DefaultErrorWriter
	gin.DefaultErrorWriter = w
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultErrorWriter = previous
		common.LogWriterMu.Unlock()
	})
	return w
}

func (w *unknownDeliveryLogWriter) requireUnresolved(t *testing.T, requestID, stage, code, class string) {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	line := w.buffer.String()
	require.Equal(t, 1, strings.Count(line, "event=billing_delivery_unknown_unresolved"))
	for _, field := range []string{"[ERR]", "priority=high", fmt.Sprintf("request_id=%q", requestID), fmt.Sprintf("request_id_unavailable=%t", requestID == ""), "stage=" + stage, "code=" + code, "class=" + class, "action=manual_reconciliation"} {
		require.Contains(t, line, field)
	}
	for _, forbidden := range []string{"synthetic-secret", "synthetic-settlement-deadline-token", model.BillingTokenFingerprint("synthetic-settlement-deadline-token"), "replacement-request", "injected lifecycle", "synthetic COMMIT", "synthetic committed", "rollback", "persisted=false"} {
		require.NotContains(t, line, forbidden)
	}
	require.True(t, w.unlocked, "all log writes must happen after releasing the billing session mutex")
}

type unknownDeliveryAccounting struct {
	lifecycleAccountingSnapshot
	UserUsedQuota, UserRequestCount int
}

func captureUnknownDeliveryAccounting(t *testing.T) unknownDeliveryAccounting {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.First(&user, 93201).Error)
	return unknownDeliveryAccounting{captureLifecycleAccounting(t), user.UsedQuota, user.RequestCount}
}

func requireUnknownDeliveryProtected(t *testing.T, s *BillingSession, before unknownDeliveryAccounting, requestID string) {
	t.Helper()
	require.True(t, s.taskOwned)
	require.False(t, s.NeedsRefund())
	s.Refund(testGinContext(t))
	require.Error(t, s.Settle(123), "ambiguous delivery must not admit later in-process settlement")
	ready, err := model.GetReadyBillingRefunds(t.Context(), 100)
	require.NoError(t, err)
	require.Empty(t, ready)
	executeBillingRefund(t.Context(), requestID)
	require.Equal(t, before, captureUnknownDeliveryAccounting(t), "source, token, ledger, statistics and the persisted owner must remain unchanged")
}

func requireUnknownDeliveryReport(t *testing.T, requestID, state, issue string) {
	t.Helper()
	page, err := model.ReadFinancialReviewReport(t.Context(), model.DB, model.FinancialReviewReportOptions{Entity: model.FinancialReviewBilling, Limit: 100})
	require.NoError(t, err)
	require.Equal(t, "available", page.Schema.Status)
	for _, item := range page.Items {
		if item.RequestID == requestID {
			require.Equal(t, state, item.State)
			require.Contains(t, item.Issues, issue)
			return
		}
	}
	t.Fatal("the original held/unknown evidence must remain discoverable by the read-only report")
}

func TestBillingUnknownDeliveryObservability(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, source := range []string{BillingSourceWallet, BillingSourceSubscription} {
		t.Run(source, func(t *testing.T) {
			t.Run("write_failure", func(t *testing.T) {
				s, _ := settlementDeadlineSession(t, "unknown-observe-write", true, source)
				requestID := s.billingIdentity.RequestID
				before := captureUnknownDeliveryAccounting(t)
				_, hits, remove := failLifecycleWrite(t, &model.BillingFinalization{}, false)
				logs := captureUnknownDeliveryLog(t, s)
				s.relayInfo.RequestId = "replacement-request"
				s.relayInfo.TokenKey = "synthetic-secret-replacement-token"
				PreserveUnknownBillingDelivery(s.relayInfo)
				require.EqualValues(t, 1, hits.Load(), "the real held-to-unknown UPDATE must reach the injected write fault")
				remove()
				logs.requireUnresolved(t, requestID, "mark_unknown", "recovery_error", "retryable")
				requireUnknownDeliveryProtected(t, s, before, requestID)
				requireUnknownDeliveryReport(t, requestID, model.BillingHeld, "held_not_refund_authorization")
			})

			t.Run("reservation_probe", func(t *testing.T) {
				s, _ := settlementDeadlineSession(t, "unknown-observe-probe", true, source)
				requestID := s.billingIdentity.RequestID
				flag := terminalCommitFault(t, 2)
				require.ErrorContains(t, s.Reserve(200), "synthetic committed ACK lost")
				require.Zero(t, flag.Load())
				require.True(t, s.reservationUncertain)
				require.Equal(t, 200, lifecycleOwner(t, requestID).SourceHeld)
				before := captureUnknownDeliveryAccounting(t)
				var hits atomic.Int32
				statement := &gorm.Statement{DB: model.DB}
				require.NoError(t, statement.Parse(&model.BillingFinalization{}))
				name := "unknown_delivery_probe_" + t.Name()
				require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == statement.Schema.Table {
						hits.Add(1)
						tx.AddError(errors.New("synthetic-secret-dsn://user:password@host token=synthetic-secret-token"))
					}
				}))
				var once sync.Once
				remove := func() { once.Do(func() { require.NoError(t, model.DB.Callback().Query().Remove(name)) }) }
				t.Cleanup(remove)
				logs := captureUnknownDeliveryLog(t, s)
				s.relayInfo.RequestId = "replacement-request"
				PreserveUnknownBillingDelivery(s.relayInfo)
				require.EqualValues(t, 1, hits.Load())
				remove()
				logs.requireUnresolved(t, requestID, "reservation_probe", "recovery_error", "retryable")
				requireUnknownDeliveryProtected(t, s, before, requestID)
				requireUnknownDeliveryReport(t, requestID, model.BillingHeld, "held_not_refund_authorization")
			})

			t.Run("receipt", func(t *testing.T) {
				s, _ := settlementDeadlineSession(t, "unknown-observe-receipt", true, source)
				requestID := s.billingIdentity.RequestID
				before := captureUnknownDeliveryAccounting(t)
				// Drop only the in-memory receipt; its real held row still exists.
				s.terminalReceipt = nil
				s.relayInfo.RequestId = "replacement-request"
				logs := captureUnknownDeliveryLog(t, s)
				PreserveUnknownBillingDelivery(s.relayInfo)
				logs.requireUnresolved(t, requestID, "receipt", "recovery_error", "retryable")
				requireUnknownDeliveryProtected(t, s, before, requestID)
				requireUnknownDeliveryReport(t, requestID, model.BillingHeld, "held_not_refund_authorization")
			})

			for _, mode := range []int32{1, 2} {
				t.Run(map[int32]string{1: "commit_rollback", 2: "commit_ack_lost"}[mode], func(t *testing.T) {
					s, _ := settlementDeadlineSession(t, "unknown-observe-commit", true, source)
					requestID := s.billingIdentity.RequestID
					before := captureUnknownDeliveryAccounting(t)
					flag := terminalCommitFault(t, mode)
					logs := captureUnknownDeliveryLog(t, s)
					PreserveUnknownBillingDelivery(s.relayInfo)
					require.Zero(t, flag.Load(), "the actual SQL COMMIT boundary must be exercised")
					logs.requireUnresolved(t, requestID, "mark_unknown", "recovery_error", "retryable")
					after := captureUnknownDeliveryAccounting(t)
					state, issue := model.BillingHeld, "held_not_refund_authorization"
					if mode == 2 {
						state, issue = model.BillingDeliveryUnknown, "delivery_evidence_unknown"
						expected := before.Owners[0]
						expected.State, expected.LastErrorCode, expected.LastErrorClass = state, "upstream_delivery_unknown", "review_required"
						expected.UpdatedAt = after.Owners[0].UpdatedAt
						before.Owners[0] = expected
					}
					require.Equal(t, state, lifecycleOwner(t, requestID).State)
					require.Equal(t, before, after, "only the unknown marker may commit; an error is not proof of rollback")
					requireUnknownDeliveryProtected(t, s, after, requestID)
					requireUnknownDeliveryReport(t, requestID, state, issue)
				})
			}

			t.Run("success_and_idempotent", func(t *testing.T) {
				s, _ := settlementDeadlineSession(t, "unknown-observe-success", true, source)
				requestID := s.billingIdentity.RequestID
				logs := captureUnknownDeliveryLog(t, s)
				PreserveUnknownBillingDelivery(s.relayInfo)
				require.Equal(t, model.BillingDeliveryUnknown, lifecycleOwner(t, requestID).State)
				before := captureUnknownDeliveryAccounting(t)
				PreserveUnknownBillingDelivery(s.relayInfo)
				requireUnknownDeliveryProtected(t, s, before, requestID)
				logs.mu.Lock()
				defer logs.mu.Unlock()
				require.NotContains(t, logs.buffer.String(), "event=billing_delivery_unknown_unresolved")
			})

			t.Run("terminal_conflict", func(t *testing.T) {
				s, _ := settlementDeadlineSession(t, "unknown-observe-settled", true, source)
				requestID := s.billingIdentity.RequestID
				require.NoError(t, s.Settle(100))
				before := captureUnknownDeliveryAccounting(t)
				logs := captureUnknownDeliveryLog(t, s)
				PreserveUnknownBillingDelivery(s.relayInfo)
				logs.requireUnresolved(t, requestID, "mark_unknown", "terminal_owner_conflict", "review_required")
				require.Equal(t, model.BillingSettled, lifecycleOwner(t, requestID).State)
				requireUnknownDeliveryProtected(t, s, before, requestID)
			})
		})
	}

	for _, invalid := range []struct{ name, requestID string }{{"missing", ""}, {"control", "synthetic-secret\nforged_event"}, {"oversized", strings.Repeat("x", 65)}} {
		t.Run("unavailable_identity_"+invalid.name, func(t *testing.T) {
			s, _ := settlementDeadlineSession(t, "unknown-observe-unavailable", true, BillingSourceWallet)
			requestID := s.billingIdentity.RequestID
			before := captureUnknownDeliveryAccounting(t)
			s.terminalReceipt = nil
			s.relayInfo.RequestId = "replacement-request"
			stage := "receipt"
			if invalid.requestID == "" {
				s.billingIdentity = nil
				s.reservationUncertain = true
				stage = "reservation_probe"
			} else {
				s.billingIdentity.RequestID = invalid.requestID
			}
			logs := captureUnknownDeliveryLog(t, s)
			PreserveUnknownBillingDelivery(s.relayInfo)
			logs.requireUnresolved(t, "", stage, "recovery_error", "retryable")
			requireUnknownDeliveryProtected(t, s, before, requestID)
			requireUnknownDeliveryReport(t, requestID, model.BillingHeld, "held_not_refund_authorization")
		})
	}
}
