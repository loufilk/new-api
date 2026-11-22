package controller

import (
	"bytes"
	"context"
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
	if err := model.DB.WithContext(c.Request.Context()).Select("id", "user_id", "name", "status", "expired_time", "remain_quota", "unlimited_quota", "allow_ips").Where("user_id = ? AND status = ?", identity.UserID, common.TokenStatusEnabled).Order("id DESC").Find(&tokens).Error; err != nil {
		modelTestError(c, 503, "catalogue_unavailable")
		return
	}
	type keyOption struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	keys := make([]keyOption, 0, len(tokens))
	for i := range tokens {
		if usableDiagnosticToken(&tokens[i], identity.UserID, c.ClientIP()) {
			keys = append(keys, keyOption{tokens[i].Id, tokens[i].Name})
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
	token, err := model.GetTokenByIds(tokenID, owner)
	if err != nil || !usableDiagnosticToken(token, owner, c.ClientIP()) {
		modelTestError(c, 403, "token_unavailable")
		return nil, nil, false
	}
	user, err := model.GetUserCache(owner)
	if err != nil || user.Status != common.UserStatusEnabled {
		modelTestError(c, 403, "token_unavailable")
		return nil, nil, false
	}
	groups, err := service.GetTokenEffectiveGroupsForUser(c.Request.Context(), owner, user.Group, token)
	if err != nil {
		modelTestError(c, 503, "catalogue_unavailable")
		return nil, nil, false
	}
	allowsChat := model.NewPlaygroundChannelFilter(constant.EndpointTypeOpenAI)
	limits := token.GetModelLimitsMap()
	models, err := getRoutableModelsForGroups(user, groups, func(ch *model.Channel, name string) bool {
		return diagnosticChannelSupported(ch.Type) && allowsChat(ch, name) && (!token.ModelLimitsEnabled || middleware.TokenModelLimitAllows(limits, name))
	})
	if err != nil {
		modelTestError(c, 503, "catalogue_unavailable")
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
		modelTestError(c, 400, "invalid_selection")
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
	RequestID string `json:"request_id"`
}

func diagnosticResponse(c *gin.Context, run *model.ModelTestRun) {
	status, code := run.Status, run.ErrorCode
	if status == "running" && run.ExpiresAt < time.Now().Unix() {
		status, code = "unknown", "worker_interrupted"
	}
	common.ApiSuccess(c, gin.H{"id": run.ID, "status": status, "error_code": code, "request_id": run.RequestID, "answer": run.ArtworkHTML})
}

func PrepareKeyDiagnostic(c *gin.Context) {
	identity, ok := requireBrowserSession(c)
	if !ok {
		c.Abort()
		return
	}
	var input keyDiagnosticRequest
	if c.ContentType() != "application/json" || common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &input) != nil || !input.Confirmed {
		modelTestError(c, 400, "confirmation_required")
		return
	}
	id, err := uuid.Parse(input.RequestID)
	qi := slices.IndexFunc(keyDiagnosticQuestions, func(q keyDiagnosticQuestion) bool { return q.ID == input.Question })
	if err != nil || id.String() != input.RequestID || id == uuid.Nil || qi < 0 || input.TokenID <= 0 || strings.TrimSpace(input.Model) == "" || len(input.Model) > 255 {
		modelTestError(c, 400, "invalid_selection")
		return
	}
	question := keyDiagnosticQuestions[qi]
	token, models, ok := keyDiagnosticModels(c, input.TokenID, identity.UserID)
	if !ok {
		return
	}
	if !slices.Contains(models, input.Model) {
		modelTestError(c, 403, "model_or_group_unavailable")
		return
	}
	group := token.Group
	if token.SmartRouting {
		group = "auto"
	}
	now := time.Now()
	run := &model.ModelTestRun{ID: input.RequestID, Source: model.KeyDiagnosticSource, UserID: identity.UserID, TokenID: input.TokenID, Model: input.Model, Group: group, BenchmarkID: question.ID, Status: "running", Assessment: "unassessed", CreatedAt: now.Unix(), ExpiresAt: now.Add(modelTestTimeout + 30*time.Second).Unix(), MaxOutputTokens: question.Limit, RequestID: c.GetString(common.RequestIdKey)}
	replayed, err := model.CreateModelTestRun(run)
	switch {
	case errors.Is(err, model.ErrModelTestConflict):
		modelTestError(c, 409, "request_id_conflict")
		return
	case errors.Is(err, model.ErrModelTestLimit):
		modelTestError(c, 429, "rate_or_concurrency_limit")
		return
	case err != nil:
		modelTestError(c, 503, "history_unavailable")
		return
	case replayed:
		c.Abort()
		diagnosticResponse(c, run)
		return
	}
	// Standard TokenAuth must see the actual originating IP. Build a new request,
	// not a clone: cookies, URL parameters and all client credentials are dropped.
	ctx, cancel := context.WithTimeout(c.Request.Context(), modelTestTimeout)
	defer cancel()
	body, _ := common.Marshal(gin.H{"model": input.Model, "stream": false, "max_tokens": question.Limit, "messages": []gin.H{{"role": "user", "content": question.Prompt}}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.RemoteAddr = net.JoinHostPort(c.ClientIP(), "0")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sk-"+token.Key)
	originalRequest, originalWriter := c.Request, c.Writer
	buffer := &modelTestWriter{ResponseWriter: originalWriter, header: make(http.Header), status: 200, cancel: cancel, limit: 512 << 10}
	common.CleanupBodyStorage(c)
	c.Set(common.KeyRequestBody, body)
	c.Request, c.Writer = req, buffer
	defer func() { c.Request, c.Writer = originalRequest, originalWriter }()
	c.Set("key_diagnostic_owner", identity.UserID)
	common.SetContextKey(c, constant.ContextKeyKeyDiagnosticTokenID, token.Id)
	common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
	common.SetContextKey(c, constant.ContextKeyModelTestOutputLimit, question.Limit)
	c.Next()
	c.Request, c.Writer = originalRequest, originalWriter
	run.CompletedAt = time.Now().Unix()
	switch {
	case buffer.header.Get("X-Newapi-Call-Intercepted") == "true":
		run.Status, run.ErrorCode = "intercepted", "user_call_intercepted"
	case ctx.Err() != nil || buffer.overflow:
		run.Status, run.ErrorCode = "unknown", "response_interrupted"
	case c.GetString("model_test_error_code") != "":
		run.Status, run.ErrorCode = "failed", c.GetString("model_test_error_code")
	case buffer.status < 200 || buffer.status >= 300:
		run.Status, run.ErrorCode = "failed", "relay_"+strconv.Itoa(buffer.status)
	default:
		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if common.Unmarshal(buffer.body.Bytes(), &result) != nil || len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
			run.Status, run.ErrorCode = "failed", "incomplete_response"
		} else {
			run.Status, run.ArtworkHTML = "completed", result.Choices[0].Message.Content
		}
	}
	if err := model.FinishKeyDiagnostic(run); err != nil {
		modelTestError(c, 503, "result_persistence_failed")
		return
	}
	diagnosticResponse(c, run)
}

func RunKeyDiagnostic(c *gin.Context) {
	if !diagnosticChannelSupported(common.GetContextKeyInt(c, constant.ContextKeyChannelType)) {
		modelTestError(c, 400, "output_budget_not_supported")
		c.Set("model_test_error_code", "output_budget_not_supported")
		return
	}
	Relay(c, types.RelayFormatOpenAI)
}

func KeyDiagnosticTokenAdmitted(c *gin.Context) {
	// TokenAuth has resolved the actual secret. Neither downstream channel
	// overrides nor request plugins should be able to read it from user headers.
	c.Request.Header.Del("Authorization")
	if id := common.GetContextKeyInt(c, constant.ContextKeyKeyDiagnosticTokenID); id <= 0 || id != c.GetInt("token_id") || c.GetInt("key_diagnostic_owner") != c.GetInt("id") {
		modelTestError(c, 403, "token_unavailable")
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
	var run model.ModelTestRun
	if err := model.DB.WithContext(c.Request.Context()).Where("id = ? AND user_id = ? AND source = ?", c.Param("id"), identity.UserID, model.KeyDiagnosticSource).First(&run).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			modelTestError(c, 503, "history_unavailable")
			return
		}
		modelTestError(c, 404, "run_not_found")
		return
	}
	diagnosticResponse(c, &run)
}
