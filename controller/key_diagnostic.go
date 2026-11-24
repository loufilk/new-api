package controller

import (
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// The prompt, generation limit and protocol are all server policy, not request fields.
type keyDiagnosticQuestion struct {
	ID     string `json:"id"`
	Prompt string `json:"prompt"`
	Limit  int    `json:"max_output_tokens"`
}

var keyDiagnosticQuestions = []keyDiagnosticQuestion{
	{"pelican-svg-v1", "请用一个独立的 SVG 动画描绘一只鹈鹕骑着自行车前进。让车轮转动，脚和踏板配合，长嘴、身体和自行车清晰可辨。只返回完整 SVG 源码，不使用 JavaScript、外链或外部资源。", 32000},
	{"candy-logic-v1", "袋中有 7 颗红糖、5 颗蓝糖和 3 颗绿糖。闭眼一次摸出一颗，不放回。至少要摸出多少颗，才能保证其中既有两颗同色的糖，又有两颗不同色的糖？请给出答案，并用最坏情形说明为什么不能更少。", 8192},
}

func usableDiagnosticToken(token *model.Token, owner int, ip string) bool {
	if token == nil || owner <= 0 || token.UserId != owner || token.EffectiveStatus(time.Now().Unix()) != common.TokenStatusEnabled {
		return false
	}
	allowed := token.GetIpLimits()
	return len(allowed) == 0 || (net.ParseIP(ip) != nil && common.IsIpInCIDRList(net.ParseIP(ip), allowed))
}

func GetKeyDiagnosticOptions(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	var tokens []model.Token
	// Read only non-secret columns, scoped to the dashboard identity.
	if err := model.DB.WithContext(c.Request.Context()).Select("id", "user_id", "name", "status", "expired_time", "remain_quota", "unlimited_quota", "allow_ips", "group", "smart_routing", "routing_strategy", "auto_groups").Where("user_id = ? AND status = ?", identity.UserID, common.TokenStatusEnabled).Order("id DESC").Find(&tokens).Error; err != nil {
		keyDiagnosticError(c, 503, "catalogue_unavailable")
		return
	}
	type keyOption struct {
		ID              int      `json:"id"`
		Name            string   `json:"name"`
		Group           string   `json:"group"`
		SmartRouting    bool     `json:"smart_routing"`
		RoutingStrategy string   `json:"routing_strategy"`
		AutoGroups      []string `json:"auto_groups"`
	}
	keys := make([]keyOption, 0, len(tokens))
	for i := range tokens {
		if usableDiagnosticToken(&tokens[i], identity.UserID, c.ClientIP()) {
			token := &tokens[i]
			groups, err := token.GetAutoGroups()
			if err != nil || groups == nil {
				groups = []string{}
			}
			keys = append(keys, keyOption{token.Id, token.Name, token.Group, token.SmartRouting, token.GetRoutingStrategy(), groups})
		}
	}
	common.ApiSuccess(c, gin.H{"keys": keys, "questions": keyDiagnosticQuestions})
}

func keyDiagnosticModels(c *gin.Context, tokenID, owner int) (*model.Token, []string, bool) {
	guard, ok := beginControllerGroupConfigRead(c)
	if !ok {
		return nil, nil, false
	}
	defer guard.Close()
	token := &model.Token{}
	err := model.DB.WithContext(c.Request.Context()).Omit("key").Where("id = ? AND user_id = ?", tokenID, owner).First(token).Error
	if err != nil || !usableDiagnosticToken(token, owner, c.ClientIP()) {
		keyDiagnosticError(c, 403, "token_unavailable")
		return nil, nil, false
	}
	user, err := model.GetUserCache(owner)
	if err != nil || user.Status != common.UserStatusEnabled {
		keyDiagnosticError(c, 403, "token_unavailable")
		return nil, nil, false
	}
	groups, err := service.GetTokenEffectiveGroupsForUser(c.Request.Context(), owner, user.Group, token)
	if err != nil {
		keyDiagnosticError(c, 503, "catalogue_unavailable")
		return nil, nil, false
	}
	allowsChat := model.NewPlaygroundChannelFilter(constant.EndpointTypeOpenAI)
	limits := token.GetModelLimitsMap()
	models, err := getRoutableModelsForGroups(user, groups, func(ch *model.Channel, name string) bool {
		return diagnosticChannelSupported(ch.Type) && allowsChat(ch, name) && (!token.ModelLimitsEnabled || middleware.TokenModelLimitAllows(limits, name))
	})
	if err != nil {
		keyDiagnosticError(c, 503, "catalogue_unavailable")
		return nil, nil, false
	}
	if !finishControllerGroupConfigRead(c, guard) {
		return nil, nil, false
	}
	return token, models, true
}

// Only transports whose final outbound body can enforce the displayed limit.
// An SDK, WebSocket or custom provider must not silently drop the cap.
func diagnosticChannelSupported(channelType int) bool {
	switch channelType {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeAzure, constant.ChannelTypeAnthropic, constant.ChannelTypeOpenRouter, constant.ChannelTypeDeepSeek, constant.ChannelTypeGemini, constant.ChannelTypeMoonshot, constant.ChannelTypeSiliconFlow, constant.ChannelTypeXai, constant.ChannelTypeVLLM, constant.ChannelTypeSGLang, constant.ChannelTypeSub2API, constant.ChannelTypeNewAPI, constant.ChannelTypeCodex, constant.ChannelTypeAws:
		return true
	default:
		return false
	}
}

func GetKeyDiagnosticModels(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	tokenID, err := strconv.Atoi(c.Param("id"))
	if err != nil || tokenID <= 0 {
		keyDiagnosticError(c, 400, "invalid_selection")
		return
	}
	_, models, ok := keyDiagnosticModels(c, tokenID, identity.UserID)
	if ok {
		common.ApiSuccess(c, models)
	}
}

type keyDiagnosticRequest struct {
	TokenID   int    `json:"token_id"`
	Model     string `json:"model"`
	Question  string `json:"question"`
	Confirmed bool   `json:"confirmed"`
	ClientID  string `json:"client_request_id"`
}

type keyDiagnosticAdmission struct {
	input    keyDiagnosticRequest
	identity service.AuthIdentity
	question keyDiagnosticQuestion
	ip       string
}

type keyDiagnosticView struct {
	ID          string                    `json:"id"`
	TokenID     int                       `json:"token_id"`
	Model       string                    `json:"model"`
	Question    string                    `json:"question"`
	Status      string                    `json:"status"`
	ErrorCode   string                    `json:"error_code,omitempty"`
	RequestID   string                    `json:"request_id"`
	Answer      string                    `json:"answer,omitempty"`
	CreatedAt   int64                     `json:"created_at"`
	CompletedAt int64                     `json:"completed_at,omitempty"`
	ElapsedMS   *int64                    `json:"elapsed_ms,omitempty"`
	HTTPStatus  int                       `json:"http_status,omitempty"`
	CandyResult string                    `json:"candy_result,omitempty"`
	Billing     model.KeyDiagnosticBill   `json:"billing"`
	Usage       *model.KeyDiagnosticUsage `json:"usage,omitempty"`
}

func diagnosticView(run model.ModelTestRun, bill model.KeyDiagnosticBill) keyDiagnosticView {
	d := model.ReadKeyDiagnosticDetails(run)
	status, code := run.Status, run.ErrorCode
	if run.ExpiresAt <= time.Now().Unix() {
		switch status {
		case model.KeyDiagnosticQueued:
			status, code = "failed", "worker_not_dispatched"
		case model.KeyDiagnosticRunning:
			status, code = "unknown", "worker_interrupted"
		}
	}
	if bill.Status == "" {
		bill.Status = "unconfirmed"
	}
	bill.Scope = "generation"
	var elapsed *int64
	if d.Version == 1 && run.CompletedAt > 0 && status != "queued" && status != "running" {
		elapsed = &d.ElapsedMS
	}
	return keyDiagnosticView{ID: d.ClientID, TokenID: run.TokenID, Model: run.Model, Question: run.BenchmarkID,
		Status: status, ErrorCode: code, RequestID: run.RequestID, Answer: run.ArtworkHTML,
		CreatedAt: run.CreatedAt, CompletedAt: run.CompletedAt, ElapsedMS: elapsed, HTTPStatus: d.HTTPStatus,
		CandyResult: d.CandyResult, Billing: bill, Usage: bill.Usage}
}

func diagnosticResponse(c *gin.Context, status int, run *model.ModelTestRun) {
	var bill model.KeyDiagnosticBill
	if status == http.StatusOK {
		bill = model.ReadKeyDiagnosticBills(c.Request.Context(), run.UserID, []model.ModelTestRun{*run})[run.RequestID]
	}
	c.JSON(status, gin.H{"success": true, "message": "", "data": diagnosticView(*run, bill)})
}

func parseKeyDiagnosticID(raw string) bool {
	id, err := uuid.Parse(raw)
	return err == nil && id != uuid.Nil && id.String() == raw
}

func PrepareKeyDiagnostic(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		c.Abort()
		return
	}
	var input keyDiagnosticRequest
	// client_request_id is intentionally distinct from the prior synchronous
	// handler's request_id. Its strict decoder won't dispatch it on old nodes.
	if c.ContentType() != "application/json" || common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &input) != nil || !input.Confirmed {
		keyDiagnosticError(c, 400, "confirmation_required")
		return
	}
	qi := slices.IndexFunc(keyDiagnosticQuestions, func(q keyDiagnosticQuestion) bool { return q.ID == input.Question })
	if !parseKeyDiagnosticID(input.ClientID) || qi < 0 || input.TokenID <= 0 || strings.TrimSpace(input.Model) == "" || len(input.Model) > 255 {
		keyDiagnosticError(c, 400, "invalid_selection")
		return
	}
	question := keyDiagnosticQuestions[qi]
	if prior, err := model.GetKeyDiagnosticRun(c.Request.Context(), identity.UserID, input.ClientID); err == nil {
		if !model.KeyDiagnosticSameInput(prior, input.TokenID, input.Model, input.Question, question.Limit) {
			keyDiagnosticError(c, 409, "request_id_conflict")
			return
		}
		c.Abort()
		diagnosticResponse(c, http.StatusOK, prior)
		return
	} else if errors.Is(err, model.ErrModelTestConflict) {
		keyDiagnosticError(c, 409, "request_id_conflict")
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		keyDiagnosticError(c, 503, "history_unavailable")
		return
	}
	ip := net.ParseIP(c.ClientIP())
	if ip == nil {
		keyDiagnosticError(c, 403, "client_ip_unavailable")
		return
	}
	c.Set("key_diagnostic_admission", keyDiagnosticAdmission{input, identity, question, ip.String()})
	c.Next()
}

func SaveKeyDiagnostic(c *gin.Context) {
	accepted, ok := c.Get("key_diagnostic_admission")
	admit, valid := accepted.(keyDiagnosticAdmission)
	if !ok || !valid {
		keyDiagnosticError(c, 400, "invalid_selection")
		return
	}
	if keyDiagnosticWorkers.Load() == nil {
		keyDiagnosticError(c, 503, "worker_unavailable")
		return
	}
	token, models, ok := keyDiagnosticModels(c, admit.input.TokenID, admit.identity.UserID)
	if !ok {
		return
	}
	if !slices.Contains(models, admit.input.Model) {
		keyDiagnosticError(c, 403, "model_or_group_unavailable")
		return
	}
	group := token.Group
	if token.SmartRouting {
		group = "auto"
	}
	now := time.Now()
	run := model.ModelTestRun{UserID: admit.identity.UserID, TokenID: admit.input.TokenID, Model: admit.input.Model, Group: group,
		BenchmarkID: admit.question.ID, Assessment: "unassessed", CreatedAt: now.Unix(),
		ExpiresAt: now.Add(keyDiagnosticAdmissionTTL).Unix(), MaxOutputTokens: admit.question.Limit, RequestID: "diag-" + model.KeyDiagnosticRowID(admit.identity.UserID, admit.input.ClientID)}
	replayed, err := model.CreateKeyDiagnosticRun(c.Request.Context(), &run, admit.input.ClientID)
	switch {
	case errors.Is(err, model.ErrModelTestConflict):
		keyDiagnosticError(c, 409, "request_id_conflict")
	case errors.Is(err, model.ErrModelTestLimit):
		keyDiagnosticError(c, 429, "rate_or_concurrency_limit")
	case errors.Is(err, model.ErrKeyDiagnosticNotAuthorized):
		keyDiagnosticError(c, 403, "token_unavailable")
	case err != nil:
		keyDiagnosticError(c, 503, "history_unavailable")
	case replayed:
		diagnosticResponse(c, http.StatusOK, &run)
	default:
		// No goroutine owns the caller's request, response, credentials or
		// cancellation. Only the issuer of this committed new row can submit.
		if !submitKeyDiagnostic(run, admit, token.CacheRevision) {
			run = finishKeyDiagnosticBeforeClaim(run, "worker_not_dispatched")
		}
		diagnosticResponse(c, http.StatusAccepted, &run)
	}
}

func keyDiagnosticError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"success": false, "message": "Key diagnostic request could not be completed", "code": code})
}

func RunKeyDiagnostic(c *gin.Context) {
	if !diagnosticChannelSupported(common.GetContextKeyInt(c, constant.ContextKeyChannelType)) {
		keyDiagnosticError(c, 400, "output_budget_not_supported")
		c.Set("model_test_error_code", "output_budget_not_supported")
		return
	}
	Relay(c, types.RelayFormatOpenAI)
}

func KeyDiagnosticTokenAdmitted(c *gin.Context) {
	// The in-process resolver never creates an Authorization header; keep this
	// boundary explicit before any channel overrides or plugins run.
	c.Request.Header.Del("Authorization")
	if id := common.GetContextKeyInt(c, constant.ContextKeyKeyDiagnosticTokenID); id <= 0 || id != c.GetInt("token_id") || c.GetInt("key_diagnostic_owner") != c.GetInt("id") {
		keyDiagnosticError(c, 403, "token_unavailable")
		return
	}
	c.Next()
}

func RetiredModelTests(c *gin.Context) { modelTestError(c, http.StatusGone, "fingerprint_retired") }

// Reconcile an interrupted response with a read, never another paid dispatch.
func GetKeyDiagnostic(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	clientID := c.Param("id")
	if !parseKeyDiagnosticID(clientID) {
		keyDiagnosticError(c, 404, "run_not_found")
		return
	}
	run, err := model.GetKeyDiagnosticRun(c.Request.Context(), identity.UserID, clientID)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, model.ErrModelTestConflict) {
		keyDiagnosticError(c, 404, "run_not_found")
		return
	}
	if err != nil {
		keyDiagnosticError(c, 503, "history_unavailable")
		return
	}
	diagnosticResponse(c, http.StatusOK, run)
}

func ListKeyDiagnostics(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		return
	}
	runs, err := model.ListKeyDiagnosticRuns(c.Request.Context(), identity.UserID)
	if err != nil {
		keyDiagnosticError(c, 503, "history_unavailable")
		return
	}
	bills := model.ReadKeyDiagnosticBills(c.Request.Context(), identity.UserID, runs)
	views := make([]keyDiagnosticView, 0, len(runs))
	for _, row := range runs {
		views = append(views, diagnosticView(row, bills[row.RequestID]))
	}
	common.ApiSuccess(c, views)
}
