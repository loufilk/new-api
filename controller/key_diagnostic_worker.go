package controller

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/QuantumNous/new-api/relay"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const (
	keyDiagnosticAdmissionTTL = 30 * time.Second
	keyDiagnosticFinishTTL    = 5 * time.Second
	keyDiagnosticRelayPath    = "/v1/chat/completions"
)

var keyDiagnosticWorkers atomic.Pointer[lifecycle.Group]

type keyDiagnosticWorkerJob struct {
	run           model.ModelTestRun
	admit         keyDiagnosticAdmission
	tokenRevision int64
}

type keyDiagnosticWorkerContextKey struct{}

type keyDiagnosticRelayReceipt struct {
	job             keyDiagnosticWorkerJob
	writer          *modelTestWriter
	deliveryUnknown bool
	errorCode       string
	answer          string
}

func submitKeyDiagnostic(run model.ModelTestRun, admit keyDiagnosticAdmission, rev int64) bool {
	group := keyDiagnosticWorkers.Load()
	if group == nil {
		return false
	}
	job := keyDiagnosticWorkerJob{run, admit, rev}
	return group.Go("key-diagnostic-dispatch", func(_, work context.Context) {
		executeKeyDiagnostic(work, job)
	})
}

func finishKeyDiagnosticBeforeClaim(run model.ModelTestRun, code string) model.ModelTestRun {
	return finishKeyDiagnostic(run, model.KeyDiagnosticQueued, "failed", code, model.ReadKeyDiagnosticDetails(run))
}

func finishKeyDiagnostic(run model.ModelTestRun, from, status, code string, details model.KeyDiagnosticDetails) model.ModelTestRun {
	original := run
	run.Status, run.ErrorCode, run.CompletedAt = status, code, time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.Background(), keyDiagnosticFinishTTL)
	defer cancel()
	if err := model.FinishSavedKeyDiagnostic(ctx, run, from, details); err != nil {
		if !errors.Is(err, model.ErrModelTestConflict) {
			common.SysError("key diagnostic result persistence was not confirmed")
		}
		// Do not promise an uncommitted terminal state to the caller.
		return original
	}
	return run
}

func executeKeyDiagnostic(parent context.Context, job keyDiagnosticWorkerJob) {
	run := job.run
	started := time.Now()
	claimCtx, cancelClaim := context.WithDeadline(parent, time.Unix(run.ExpiresAt, 0))
	ok, err := model.ClaimKeyDiagnosticRun(claimCtx, run, job.admit.identity, started.Add(modelTestTimeout+30*time.Second).Unix())
	cancelClaim()
	if errors.Is(err, model.ErrKeyDiagnosticNotAuthorized) {
		finishKeyDiagnosticBeforeClaim(run, "session_unavailable")
		return
	}
	if err != nil || !ok {
		return
	}
	// At this point any outward outcome may become ambiguous. Never run this
	// job again, even after timeout/shutdown/panic or a failed finish ACK.
	d := model.ReadKeyDiagnosticDetails(run)
	defer func() {
		if recover() != nil {
			d.ElapsedMS = time.Since(started).Milliseconds()
			finishKeyDiagnostic(run, model.KeyDiagnosticRunning, "unknown", "worker_interrupted", d)
		}
	}()
	ctx, cancel := context.WithDeadline(parent, started.Add(modelTestTimeout))
	defer cancel()
	body, err := common.Marshal(gin.H{"model": run.Model, "stream": false, "max_tokens": run.MaxOutputTokens,
		"messages": []gin.H{{"role": "user", "content": job.admit.question.Prompt}}})
	if err != nil {
		finishKeyDiagnostic(run, model.KeyDiagnosticRunning, "failed", "request_preparation_failed", d)
		return
	}
	buffer := &modelTestWriter{header: make(http.Header), status: http.StatusOK, cancel: cancel, limit: 512 << 10}
	receipt := &keyDiagnosticRelayReceipt{job: job, writer: buffer}
	ctx = common.WithPrivateKeyDiagnosticLog(ctx)
	ctx = context.WithValue(ctx, keyDiagnosticWorkerContextKey{}, receipt)
	ctx = context.WithValue(ctx, common.RequestIdKey, run.RequestID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, keyDiagnosticRelayPath, bytes.NewReader(body))
	if err != nil {
		finishKeyDiagnostic(run, model.KeyDiagnosticRunning, "failed", "request_preparation_failed", d)
		return
	}
	req.RemoteAddr = net.JoinHostPort(job.admit.ip, "0")
	req.Header.Set("Content-Type", "application/json")
	keyDiagnosticRelay().ServeHTTP(keyDiagnosticDiscardResponse{header: make(http.Header)}, req)
	d.ElapsedMS = time.Since(started).Milliseconds()
	if buffer.Written() {
		d.HTTPStatus = buffer.status
	}
	status, code := "failed", "incomplete_response"
	switch {
	case buffer.header.Get("X-Newapi-Call-Intercepted") == "true":
		status, code = "intercepted", "user_call_intercepted"
	case receipt.deliveryUnknown || ctx.Err() != nil || buffer.overflow:
		status, code = "unknown", "response_interrupted"
	case receipt.errorCode != "":
		code = receipt.errorCode
	case buffer.status < 200 || buffer.status >= 300:
		code = "relay_" + strconv.Itoa(buffer.status)
	default:
		if receipt.answer != "" {
			status, code = "completed", ""
			run.ArtworkHTML = receipt.answer
			if run.BenchmarkID == "candy-logic-v1" {
				d.CandyResult = classifyCandyAnswer(run.ArtworkHTML)
			}
		}
	}
	finishKeyDiagnostic(run, model.KeyDiagnosticRunning, status, code, d)
}

type keyDiagnosticDiscardResponse struct{ header http.Header }

func (r keyDiagnosticDiscardResponse) Header() http.Header         { return r.header }
func (r keyDiagnosticDiscardResponse) Write(b []byte) (int, error) { return len(b), nil }
func (r keyDiagnosticDiscardResponse) WriteHeader(int)             {}

// This Gin relay is never registered on the public router. It preserves the
// authoritative auth/model/routing/limit/charging chain on fresh state, with
// no dashboard cookies, credentials, query parameters, or proxy headers.
var keyDiagnosticRelay = sync.OnceValue(func() *gin.Engine {
	e := gin.New()
	e.ForwardedByClientIP = false
	_ = e.SetTrustedProxies(nil)
	e.POST(keyDiagnosticRelayPath, func(c *gin.Context) {
		receipt, _ := c.Request.Context().Value(keyDiagnosticWorkerContextKey{}).(*keyDiagnosticRelayReceipt)
		if receipt == nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		run := receipt.job.run
		receipt.writer.ResponseWriter = c.Writer
		c.Writer = receipt.writer
		c.Set("key_diagnostic_owner", run.UserID)
		c.Set(common.RequestIdKey, run.RequestID)
		common.SetContextKey(c, constant.ContextKeyGatewayIngressTime, time.Now())
		common.SetContextKey(c, constant.ContextKeyKeyDiagnosticTokenID, run.TokenID)
		common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
		common.SetContextKey(c, constant.ContextKeyModelTestOutputLimit, run.MaxOutputTokens)
		common.SetContextKey(c, constant.ContextKeyKeyDiagnosticAuthority, func() bool {
			return revalidateKeyDiagnosticAuthority(c.Request.Context(), receipt.job, c.ClientIP())
		})
		defer func() {
			receipt.deliveryUnknown = relay.RequestBillingDeliveryUnknown(c)
			receipt.errorCode = c.GetString("model_test_error_code")
			// Decode before checking for exact echoes, including JSON-escaped
			// characters. Neither credential nor raw error bodies can be saved.
			if receipt.writer.Status() >= 200 && receipt.writer.Status() < 300 && !receipt.writer.overflow {
				var result struct {
					Choices []struct {
						Message struct {
							Content string `json:"content"`
						} `json:"message"`
					} `json:"choices"`
				}
				if common.Unmarshal(receipt.writer.body.Bytes(), &result) == nil && len(result.Choices) > 0 &&
					strings.TrimSpace(result.Choices[0].Message.Content) != "" {
					receipt.answer = common.RedactPrivateKeyDiagnosticSecrets(c, result.Choices[0].Message.Content)
				}
			}
			c.Set("token_key", "")
			common.SetContextKey(c, constant.ContextKeyChannelKey, "")
		}()
		c.Next()
	}, middleware.BodyStorageCleanup(), middleware.SystemPerformanceCheck(),
		middleware.KeyDiagnosticTokenAuth(loadKeyDiagnosticToken), KeyDiagnosticTokenAdmitted,
		middleware.ModelRequestRateLimit(), middleware.Distribute(), RunKeyDiagnostic)
	return e
})

func loadKeyDiagnosticToken(c *gin.Context) (*model.Token, error) {
	receipt, _ := c.Request.Context().Value(keyDiagnosticWorkerContextKey{}).(*keyDiagnosticRelayReceipt)
	if receipt == nil {
		return nil, model.ErrTokenInvalid
	}
	job := receipt.job
	var token model.Token
	err := model.DB.WithContext(c.Request.Context()).Where("id = ? AND user_id = ?", job.run.TokenID, job.run.UserID).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && (!usableDiagnosticToken(&token, job.run.UserID, c.ClientIP()) || token.CacheRevision != job.tokenRevision || token.Key == "")) {
		c.Set("model_test_error_code", "token_unavailable")
		return nil, model.ErrTokenInvalid
	}
	if err != nil {
		return nil, model.ErrDatabase
	}
	return &token, nil
}

func revalidateKeyDiagnosticAuthority(ctx context.Context, job keyDiagnosticWorkerJob, ip string) bool {
	ctx, cancel := context.WithTimeout(ctx, keyDiagnosticFinishTTL)
	defer cancel()
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := model.ValidateAuthSessionWithTx(tx, job.admit.identity); err != nil {
			return err
		}
		var token model.Token
		if err := tx.Omit("key").Where("id = ? AND user_id = ?", job.run.TokenID, job.run.UserID).First(&token).Error; err != nil {
			return err
		}
		// The ordinary relay has already reserved the active attempt's
		// token quota by the last pre-send check; a zero remainder caused
		// by that same hold is not a permission or expiry change.
		token.UnlimitedQuota = true
		if !usableDiagnosticToken(&token, job.run.UserID, ip) || token.CacheRevision != job.tokenRevision {
			return model.ErrKeyDiagnosticNotAuthorized
		}
		return nil
	})
	return err == nil
}

// Only classify a clear answer in the opening bytes. Ambiguous replies or
// prose without a recognizable answer are not scored as wrong.
var candyAnswerPrefix = regexp.MustCompile(`^[\s#*]*(?:(?:最终答案|答案|结论|至少要摸出|至少需要摸出|至少摸出|至少需要|至少|需要摸出)\s*[:：]?\s*)?[*\s]*([0-9]{1,3}|[零一二三四五六七八九十两]+)[*\s]*(?:颗(?:糖果?|糖)?|粒(?:糖果?|糖)?)[*\s]*(?:[。.,，!！;；:：、\n]|$)`)

func classifyCandyAnswer(answer string) string {
	m := candyAnswerPrefix.FindStringSubmatch(strings.TrimSpace(answer))
	if len(m) != 2 {
		return "unrecognized"
	}
	if m[1] == "8" || m[1] == "八" {
		return "correct"
	}
	return "incorrect"
}
