package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/modelfingerprint"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/net/html"
)

const modelTestVersion = "local-fingerprint-v1"

// Original task text, dedicated to the public domain under CC0-1.0. This is a
// public visual programming exercise, not a secret identity test or an IQ test.
const modelTestPrompt = "Create one self-contained HTML document showing a cheerful pelican riding a bicycle beside a pond. Include two wheels, a visible beak, pedals and a blue pond. Use only inline SVG and CSS; no JavaScript, links, fonts, images, external resources, forms, embedded documents or network requests. Output only the complete HTML, without markdown fences. Keep the illustration readable at 360 pixels wide."
const modelTestSampleTimeout = 120 * time.Second
const modelTestTimeout = modelTestSampleTimeout + 30*time.Second
const modelTestResponseLimit = 64 << 10

type modelTestSelection struct {
	TokenID         int    `json:"token_id"`
	Model           string `json:"model"`
	Group           string `json:"group"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}
type modelTestRequest struct {
	modelTestSelection
	ConfirmationToken string `json:"confirmation_token"`
	Confirmed         bool   `json:"confirmed"`
	RequestID         string `json:"request_id"`
}
type modelTestConfirmation struct {
	modelTestSelection
	UserID          int                          `json:"user_id"`
	Version         string                       `json:"version"`
	ExpiresAt       int64                        `json:"expires_at"`
	EstimatedQuota  *int                         `json:"estimated_quota"`
	OutputUnbounded bool                         `json:"output_unbounded"`
	Challenges      []modelfingerprint.Challenge `json:"challenges"`
	TokenIdentity   string                       `json:"token_identity"`
	Nonce           string                       `json:"nonce"`
	PricingHash     string                       `json:"pricing_hash"`
}

// Bind confirmation to current configuration, not merely a displayed amount.
// No sensitive pricing headers or credentials are placed in this hash input.
func modelTestPricingHash(userID int, selected modelTestSelection) (string, error) {
	user, err := model.GetUserCache(userID)
	if err != nil {
		return "", err
	}
	originalSelection := selected
	selected.Model = helper.ResolveBillingModelName(selected.Model)
	price, priced := ratio_setting.GetModelPrice(selected.Model, false)
	ratio, ratioExists, _ := ratio_setting.GetModelRatio(selected.Model)
	expression, expressionExists := billing_setting.GetBillingExpr(selected.Model)
	cache, _ := ratio_setting.GetCacheRatio(selected.Model)
	creation, _ := ratio_setting.GetCreateCacheRatio(selected.Model)
	body, err := common.Marshal(gin.H{"model": originalSelection.Model, "billing_model": selected.Model, "group": selected.Group, "group_ratio": service.GetUserGroupRatio(user.Group, selected.Group), "mode": billing_setting.GetBillingMode(selected.Model), "expression": expression, "expression_exists": expressionExists, "price": price, "priced": priced, "ratio": ratio, "ratio_exists": ratioExists, "completion": ratio_setting.GetCompletionRatio(selected.Model), "cache": cache, "creation": creation, "quota_per_unit": common.QuotaPerUnit})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	return fmt.Sprintf("%x", hash[:]), nil
}

func modelTestError(c *gin.Context, status int, code string) {
	c.AbortWithStatusJSON(status, gin.H{"success": false, "message": "Model test request could not be completed", "code": code})
}

func modelTestCatalogue(userID int) (map[string][]string, error) {
	// The fingerprint catalogue is an allowlist, not the complete relay catalogue.
	// Apply the administrator's enabled targets before exposing models to users
	// or admitting manual probes through validateModelTestSelection.
	config, err := model.GetModelFingerprintSiteConfig(context.Background())
	if err != nil {
		return nil, err
	}
	allowedTargets := make(map[string]map[string]struct{})
	for _, target := range config.EnabledTargets() {
		if allowedTargets[target.Group] == nil {
			allowedTargets[target.Group] = make(map[string]struct{})
		}
		allowedTargets[target.Group][target.Model] = struct{}{}
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return nil, err
	}
	groups := service.GetUserUsableGroups(user.Group)
	pricingSet := make(map[string]struct{})
	for _, p := range model.GetPricing() {
		if slices.Contains(p.SupportedEndpointTypes, constant.EndpointTypeOpenAI) && len(p.BillingUsageSchema) == 0 {
			pricingSet[p.ModelName] = struct{}{}
		}
	}
	result := make(map[string][]string)
	var allowedGroups []string
	for group := range groups {
		if service.IsUserSelectableGroup(user.Group, group) && len(allowedTargets[group]) > 0 {
			allowedGroups = append(allowedGroups, group)
			result[group] = []string{}
		}
	}
	abilities, err := model.GetEnabledAbilitiesWithChannelsForGroups(allowedGroups)
	if err != nil {
		return nil, err
	}
	for _, ability := range abilities {
		if _, priced := pricingSet[ability.Model]; priced && allowedTargets[ability.Group] != nil && !slices.Contains(result[ability.Group], ability.Model) {
			if _, enabled := allowedTargets[ability.Group][ability.Model]; !enabled {
				continue
			}
			result[ability.Group] = append(result[ability.Group], ability.Model)
		}
	}
	for group := range result {
		slices.Sort(result[group])
	}
	return result, nil
}

func validateModelTestSelection(userID int, selected modelTestSelection) error {
	if len(selected.Model) == 0 || len(selected.Model) > 255 || len(selected.Group) == 0 || len(selected.Group) > 64 || selected.MaxOutputTokens != 0 {
		return errors.New("invalid_selection")
	}
	catalogue, err := modelTestCatalogue(userID)
	if err != nil {
		return err
	}
	if !slices.Contains(catalogue[selected.Group], selected.Model) {
		return errors.New("model_or_group_unavailable")
	}
	return nil
}

func modelTestBody(selected modelTestSelection, prompt string) []byte {
	body, _ := common.Marshal(gin.H{"model": selected.Model, "stream": false, "messages": []gin.H{{"role": "user", "content": prompt}}})
	return body
}

// With no output limit token-priced and dynamic-expression totals are unknown.
// Only fixed per-call prices remain estimable; nil never means zero or free.
// This is not a monetary ceiling or a reserve. Ordinary relay owns settlement.
func estimateModelTestQuota(userID int, selected modelTestSelection, challenges []modelfingerprint.Challenge) *int {
	if len(challenges) != 3 {
		return nil
	}
	user, err := model.GetUserCache(userID)
	if err != nil {
		return nil
	}
	billingModel := helper.ResolveBillingModelName(selected.Model)
	if billing_setting.GetBillingMode(billingModel) == billing_setting.BillingModeTieredExpr {
		return nil
	}
	price, known := ratio_setting.GetModelPrice(billingModel, false)
	if !known {
		return nil
	}
	value := price * common.QuotaPerUnit * service.GetUserGroupRatio(user.Group, selected.Group)
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	quota, err := common.QuotaFromFloatStrict(3 * math.Ceil(value))
	if err != nil {
		return nil
	}
	return &quota
}

// Unknown totals match only unknown totals. Pricing fingerprints independently
// bind the actual rate configuration before every dispatch.
func modelTestQuotaEstimateMatches(current, confirmed *int) bool {
	if current == nil || confirmed == nil {
		return current == nil && confirmed == nil
	}
	return *current == *confirmed
}

func signModelTestConfirmation(ticket modelTestConfirmation) string {
	body, _ := common.Marshal(ticket)
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	mac.Write([]byte("model-test-confirmation-v1\x00"))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyModelTestConfirmation(value string, userID int, selection modelTestSelection) (*modelTestConfirmation, bool) {
	if len(value) > 16384 {
		return nil, false
	}
	bodyPart, signaturePart, ok := strings.Cut(value, ".")
	if !ok {
		return nil, false
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyPart)
	if err != nil {
		return nil, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return nil, false
	}
	mac := hmac.New(sha256.New, []byte(common.SessionSecret))
	mac.Write([]byte("model-test-confirmation-v1\x00"))
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, false
	}
	var ticket modelTestConfirmation
	if common.Unmarshal(body, &ticket) != nil || !ticket.OutputUnbounded || ticket.MaxOutputTokens != 0 || ticket.UserID != userID || ticket.Version != modelTestVersion || ticket.modelTestSelection != selection || ticket.ExpiresAt < time.Now().Unix() {
		return nil, false
	}
	return &ticket, true
}

func GetModelTests(c *gin.Context) {
	items, err := model.ListModelTestRuns(c.GetInt("id"))
	if err != nil {
		modelTestError(c, 503, "history_unavailable")
		return
	}
	effectivePoints, effectiveErr := model.ListEffectiveModelTestRuns(c.GetInt("id"))
	if effectiveErr != nil {
		modelTestError(c, 503, "history_unavailable")
		return
	}
	enabled, settingsErr := model.ModelTestEnabled(c.Request.Context())
	availabilityError := ""
	if settingsErr != nil {
		availabilityError = "model_test_settings_unavailable"
	}
	catalogue := map[string][]string{}
	if enabled {
		// Owner history does not depend on routing configuration. A catalogue
		// does, so acquire a fresh primary-store guard without an auth snapshot.
		guard, guardErr := model.BeginGroupConfigRead(c.Request.Context())
		if guardErr == nil {
			catalogue, guardErr = modelTestCatalogue(c.GetInt("id"))
			if finishErr := guard.Finish(); guardErr == nil {
				guardErr = finishErr
			}
		}
		if guardErr != nil {
			enabled = false
			catalogue = map[string][]string{}
			availabilityError = "model_test_settings_unavailable"
		}
	}
	tokens, tokenErr := modelTestTokens(c, catalogue)
	if tokenErr != nil {
		enabled = false
		availabilityError = "token_catalogue_unavailable"
	}
	common.ApiSuccess(c, gin.H{"effective_points": effectivePoints, "eligible_tokens_by_group": tokens, "enabled": enabled, "availability_error": availabilityError, "benchmark": gin.H{"id": modelTestVersion, "name": "Local statistical model fingerprint", "public_prompt": true, "assessment": "statistical-not-proof", "sample_count": 3}, "limits": gin.H{"max_output_tokens": 0, "default_output_tokens": 0, "allowed_output_tokens": []int{0}, "timeout_seconds": int(modelTestTimeout.Seconds()), "sample_timeout_seconds": int(modelTestSampleTimeout.Seconds()), "runs_per_hour": 3, "concurrency_per_user": 1, "concurrency_global": 4}, "eligible_models_by_group": catalogue, "site_results": []any{}, "items": items})
}

// Reads primary storage every time; neither a signed quote nor an administrator
// session overrides the opt-in switch. History/reconciliation/cancel are separate.
func requireModelTestEnabled(c *gin.Context) bool {
	err := model.RequireModelTestEnabled(c.Request.Context())
	if err == nil {
		return true
	}
	code, status := "model_tests_disabled", http.StatusForbidden
	if !errors.Is(err, model.ErrModelTestsDisabled) {
		code, status = "model_test_settings_unavailable", http.StatusServiceUnavailable
	}
	c.Set("model_test_error_code", code)
	modelTestError(c, status, code)
	return false
}

func EstimateModelTest(c *gin.Context) {
	if !requireModelTestEnabled(c) {
		return
	}
	if modelFingerprintScorer.ready() != nil {
		modelTestError(c, 503, "fingerprint_unavailable")
		return
	}
	var selected modelTestSelection
	if common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 4096), &selected) != nil {
		modelTestError(c, 400, "invalid_selection")
		return
	}
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()

	if err := validateModelTestSelection(c.GetInt("id"), selected); err != nil {
		modelTestError(c, 400, "model_or_group_unavailable")
		return
	}
	_, identity, tokenErr := selectedModelTestToken(c, selected)
	if tokenErr != nil {
		modelTestError(c, 400, "token_unavailable")
		return
	}
	pricingHash, err := modelTestPricingHash(c.GetInt("id"), selected)
	if err != nil {
		modelTestError(c, 503, "catalogue_unavailable")
		return
	}
	if !requireModelTestEnabled(c) {
		return
	}
	challenges, err := modelFingerprintScorer.challenges()
	if err != nil || len(challenges) != 3 {
		modelTestError(c, 503, "fingerprint_unavailable")
		return
	}
	ticket := modelTestConfirmation{OutputUnbounded: true, Challenges: challenges, TokenIdentity: identity, Nonce: uuid.NewString(), modelTestSelection: selected, UserID: c.GetInt("id"), Version: modelTestVersion, ExpiresAt: time.Now().Add(5 * time.Minute).Unix(), EstimatedQuota: estimateModelTestQuota(c.GetInt("id"), selected, challenges), PricingHash: pricingHash}
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	common.ApiSuccess(c, gin.H{"token_id": selected.TokenID, "sample_count": 3, "model": selected.Model, "group": selected.Group, "max_output_tokens": selected.MaxOutputTokens, "estimated_quota": ticket.EstimatedQuota, "quota_per_unit": common.QuotaPerUnit, "estimate_is_cap": false, "output_unbounded": true, "cost_unknown": ticket.EstimatedQuota == nil, "timeout_seconds": int(modelTestTimeout.Seconds()), "sample_timeout_seconds": int(modelTestSampleTimeout.Seconds()), "benchmark_id": modelTestVersion, "confirmation_token": signModelTestConfirmation(ticket), "expires_at": ticket.ExpiresAt})
}

func GetModelTest(c *gin.Context) {
	run, err := model.GetModelTestRun(c.GetInt("id"), c.Param("id"))
	if err != nil {
		modelTestError(c, 404, "run_not_found")
		return
	}
	prompt := ""
	if run.BenchmarkID == "pelican-static-v1" {
		prompt = modelTestPrompt
	}
	common.ApiSuccess(c, struct {
		*model.ModelTestRun
		ArtworkHTML string `json:"artwork_html"`
		Prompt      string `json:"prompt,omitempty"`
	}{run, run.ArtworkHTML, prompt})
}

func CancelModelTest(c *gin.Context) {
	run, err := model.CancelModelTestRun(c.GetInt("id"), c.Param("id"))
	if err != nil {
		modelTestError(c, 404, "run_not_found")
		return
	}
	common.ApiSuccess(c, run)
}

// PrepareModelTest uses a validated existing owned API token with normal relay
// accounting. It replaces all client-controlled input before interception and
// distribution. No key is minted, returned, or accepted from the browser.
func PrepareModelTest(c *gin.Context) {
	var request modelTestRequest
	if common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 24576), &request) != nil || !request.Confirmed {
		modelTestError(c, 400, "confirmation_required")
		return
	}
	id, err := uuid.Parse(request.RequestID)
	if err != nil || id.String() != request.RequestID {
		modelTestError(c, 400, "invalid_request_id")
		return
	}
	userID := c.GetInt("id")
	// Reconciliation of an owned, already-created request never initiates work.
	// It remains possible after a confirmation expires or prices change.
	if previous, lookupErr := model.GetModelTestRun(userID, request.RequestID); lookupErr == nil {
		if previous.TokenID != request.TokenID || previous.Model != request.Model || previous.Group != request.Group || previous.MaxOutputTokens != request.MaxOutputTokens || previous.BenchmarkID != modelTestVersion {
			modelTestError(c, 409, "request_id_conflict")
			return
		}
		c.Abort()
		common.ApiSuccess(c, previous)
		return
	}
	if !requireModelTestEnabled(c) {
		return
	}
	if modelFingerprintScorer.ready() != nil {
		modelTestError(c, 503, "fingerprint_unavailable")
		return
	}
	ticket, ok := verifyModelTestConfirmation(request.ConfirmationToken, userID, request.modelTestSelection)
	if !ok {
		modelTestError(c, 400, "confirmation_expired_or_invalid")
		return
	}
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()

	if err := validateModelTestSelection(userID, request.modelTestSelection); err != nil {
		modelTestError(c, 400, "model_or_group_unavailable")
		return
	}
	token, identity, tokenErr := selectedModelTestToken(c, request.modelTestSelection)
	if tokenErr != nil || identity != ticket.TokenIdentity {
		modelTestError(c, 409, "confirmation_token_changed")
		return
	}
	if ticket.Nonce == "" || len(ticket.Challenges) != 3 {
		modelTestError(c, 400, "confirmation_expired_or_invalid")
		return
	}
	userCache, cacheErr := model.GetUserCache(userID)
	if cacheErr != nil {
		modelTestError(c, 503, "token_unavailable")
		return
	}
	userCache.WriteContext(c)
	if middleware.SetupContextForToken(c, token) != nil {
		modelTestError(c, 400, "token_unavailable")
		return
	}
	common.SetContextKey(c, constant.ContextKeyUsingGroup, request.Group)
	currentEstimate := estimateModelTestQuota(userID, request.modelTestSelection, ticket.Challenges)
	if !modelTestQuotaEstimateMatches(currentEstimate, ticket.EstimatedQuota) {
		modelTestError(c, 409, "confirmation_price_changed")
		return
	}
	pricingHash, err := modelTestPricingHash(userID, request.modelTestSelection)
	if err != nil || pricingHash != ticket.PricingHash {
		modelTestError(c, 409, "confirmation_price_changed")
		return
	}
	now := time.Now()
	confirmationID := fmt.Sprintf("%x", sha256.Sum256([]byte(request.ConfirmationToken)))
	run := &model.ModelTestRun{Source: "user", TokenID: request.TokenID, ConfirmationID: &confirmationID, ID: request.RequestID, UserID: userID, Model: request.Model, Group: request.Group, BenchmarkID: modelTestVersion, Status: "running", Assessment: "unassessed", CreatedAt: now.Unix(), ExpiresAt: now.Add(modelTestTimeout + 30*time.Second).Unix(), MaxOutputTokens: request.MaxOutputTokens, EstimatedQuota: ticket.EstimatedQuota, RequestID: c.GetString(common.RequestIdKey)}
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	replayed, err := model.CreateModelTestRun(run)
	if errors.Is(err, model.ErrModelTestTargetUnavailable) {
		modelTestError(c, http.StatusForbidden, "model_or_group_unavailable")
		return
	}
	if errors.Is(err, model.ErrModelTestsDisabled) {
		modelTestError(c, http.StatusForbidden, "model_tests_disabled")
		return
	}
	if errors.Is(err, model.ErrModelTestSettingsUnavailable) {
		modelTestError(c, http.StatusServiceUnavailable, "model_test_settings_unavailable")
		return
	}
	if errors.Is(err, model.ErrModelTestLimit) {
		modelTestError(c, 429, "rate_or_concurrency_limit")
		return
	}
	if errors.Is(err, model.ErrModelTestConflict) {
		modelTestError(c, 409, "request_id_conflict")
		return
	}
	if err != nil {
		modelTestError(c, 503, "history_unavailable")
		return
	}
	if replayed {
		c.Abort()
		common.ApiSuccess(c, run)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), modelTestTimeout)
	defer cancel()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current, err := model.GetModelTestRun(userID, run.ID)
				if err != nil || current.CancelRequested {
					cancel()
					return
				}
			}
		}
	}()
	originalWriter := c.Writer
	buffer := &modelTestWriter{ResponseWriter: originalWriter, header: make(http.Header), status: 200, cancel: cancel}
	c.Writer = buffer
	defer func() { c.Writer = originalWriter }()
	body := modelTestBody(request.modelTestSelection, ticket.Challenges[0].Prompt)
	common.CleanupBodyStorage(c)
	c.Set(common.KeyRequestBody, body)
	c.Request = c.Request.WithContext(ctx)
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	// Authentication is complete. A controlled task accepts no user-selected
	// upstream or pricing headers, and does not retain browser credentials.
	c.Set("model_test_client_ip", c.ClientIP())
	c.Request.Header = make(http.Header)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.URL.Path = "/v1/chat/completions"
	common.SetContextKey(c, constant.ContextKeyModelTestSingleAttempt, true)
	common.SetContextKey(c, constant.ContextKeyTokenNoRetryWithinGroup, true)
	common.SetContextKey(c, constant.ContextKeyTokenCrossGroupRetry, false)
	common.SetContextKey(c, constant.ContextKeyModelTestOutputLimit, request.MaxOutputTokens)
	c.Set("model_test_confirmed_pricing_hash", ticket.PricingHash)
	c.Set("model_test_selection", request.modelTestSelection)
	c.Set("model_test_run", run)
	c.Set("model_test_token_identity", identity)
	c.Set("model_test_challenges", ticket.Challenges)
	c.Set("model_test_estimated_quota", ticket.EstimatedQuota)
	c.Next()
	requestErr := ctx.Err()
	cancel()
	<-watchDone
	c.Writer = originalWriter
	run.CompletedAt = time.Now().Unix()
	current, currentErr := model.GetModelTestRun(userID, run.ID)
	if currentErr != nil {
		run.Status, run.ErrorCode = "unknown", "history_unavailable"
	} else if current.CancelRequested {
		run.Status, run.ErrorCode, run.CancelRequested = "cancelled", "cancel_requested", true
	} else if buffer.header.Get("X-Newapi-Call-Intercepted") == "true" {
		run.Status, run.ErrorCode = "intercepted", "user_call_intercepted"
	} else if errors.Is(requestErr, context.DeadlineExceeded) || c.GetBool("model_test_sample_timeout") {
		run.Status, run.ErrorCode = "timeout", "request_timeout"
	} else if buffer.overflow {
		run.Status, run.ErrorCode = "failed", "response_too_large"
	} else if requestErr != nil {
		run.Status, run.ErrorCode = "cancelled", "request_cancelled"
	} else if c.GetBool("model_test_uncertain") {
		run.Status, run.ErrorCode = "unknown", "upstream_outcome_uncertain"
	} else if code := c.GetString("model_test_error_code"); code != "" {
		run.Status, run.ErrorCode = "failed", code
	} else if buffer.status < 200 || buffer.status >= 300 {
		run.Status, run.ErrorCode = "failed", "relay_failed"
	} else {
		result, exists := c.Get("model_test_fingerprint_result")
		if !exists {
			run.Status, run.ErrorCode = "failed", "incomplete_response"
		} else {
			data, marshalErr := common.Marshal(result)
			if marshalErr != nil {
				run.Status, run.ErrorCode = "failed", "invalid_result"
			} else {
				run.Status, run.Assessment, run.FingerprintResult = "completed", "statistical", data
				if fingerprint, ok := result.(modelfingerprint.Result); ok {
					run.Assessment = fingerprint.Status
				}
			}
		}
	}
	if result, exists := c.Get("model_test_fingerprint_result"); exists {
		if data, err := common.Marshal(result); err == nil {
			run.FingerprintResult = data
		}
	}
	if model.FinishModelTestRun(run) != nil {
		modelTestError(c, 503, "result_persistence_failed")
		return
	}
	c.Header("Cache-Control", "private, no-store")
	common.ApiSuccess(c, run)
}

// Channel parameter overrides could change the fixed fingerprint challenge.
// Refuse those routes rather than silently claiming a controlled comparison.
func ModelTestPlayground(c *gin.Context) {
	guard, guardOK := beginControllerGroupConfigRead(c)
	if !guardOK {
		return
	}
	defer guard.Close()

	selected, ok := c.MustGet("model_test_selection").(modelTestSelection)
	if !ok {
		c.AbortWithStatus(400)
		return
	}
	hash, err := modelTestPricingHash(c.GetInt("id"), selected)
	if err != nil || hash != c.GetString("model_test_confirmed_pricing_hash") {
		c.Set("model_test_error_code", "confirmation_price_changed")
		c.AbortWithStatusJSON(409, gin.H{"error": gin.H{"code": "confirmation_price_changed"}})
		return
	}
	if settings, ok := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting); ok && (settings.SystemPrompt != "" || settings.SystemPromptOverride || settings.ThinkingToContent || settings.PassThroughBodyEnabled) {
		c.Set("model_test_error_code", "channel_overrides_not_supported")
		c.AbortWithStatus(400)
		return
	}
	if raw, exists := common.GetContextKey(c, constant.ContextKeyChannelParamOverride); exists {
		if overrides, ok := raw.(map[string]any); !ok || len(overrides) != 0 {
			c.Set("model_test_error_code", "channel_overrides_not_supported")
			c.AbortWithStatusJSON(400, gin.H{"error": gin.H{"code": "channel_overrides_not_supported"}})
			return
		}
	}
	if raw, exists := common.GetContextKey(c, constant.ContextKeyChannelHeaderOverride); exists {
		if overrides, ok := raw.(map[string]any); !ok || len(overrides) != 0 {
			c.Set("model_test_error_code", "channel_overrides_not_supported")
			c.AbortWithStatus(400)
			return
		}
	}
	if !finishControllerGroupConfigRead(c, guard) {
		return
	}
	if !requireModelTestEnabled(c) {
		return
	}
	executeModelFingerprint(c)
}

type modelTestWriter struct {
	gin.ResponseWriter
	header   http.Header
	body     bytes.Buffer
	status   int
	written  bool
	overflow bool
	cancel   context.CancelFunc
}

func (w *modelTestWriter) Header() http.Header { return w.header }
func (w *modelTestWriter) WriteHeader(code int) {
	if !w.written {
		w.status = code
	}
}
func (w *modelTestWriter) WriteHeaderNow() { w.written = true }
func (w *modelTestWriter) Write(value []byte) (int, error) {
	w.written = true
	if w.body.Len()+len(value) > modelTestResponseLimit {
		w.overflow = true
		w.cancel()
		return 0, errors.New("model test response exceeds limit")
	}
	return w.body.Write(value)
}
func (w *modelTestWriter) WriteString(value string) (int, error) { return w.Write([]byte(value)) }
func (w *modelTestWriter) Status() int                           { return w.status }
func (w *modelTestWriter) Size() int {
	if !w.written {
		return -1
	}
	return w.body.Len()
}
func (w *modelTestWriter) Written() bool { return w.written }
func (w *modelTestWriter) Flush()        { w.written = true }

// Strict static projection, plus opaque-origin iframe/CSP on the client. Raw
// model HTML is never served from an HTML endpoint and is not stored on failure.
func staticModelTestArtwork(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		if _, rest, ok := strings.Cut(value, "\n"); ok {
			value = strings.TrimSuffix(strings.TrimSpace(rest), "```")
		}
	}
	if len(value) == 0 || len(value) > 128<<10 {
		return "", errors.New("invalid artwork")
	}
	doc, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return "", err
	}
	allowed := strings.Fields("html head body title style div span p h1 h2 h3 section main figure figcaption svg g path circle ellipse rect line polyline polygon text tspan defs linearGradient radialGradient stop clipPath mask pattern")
	allowedAttrs := strings.Fields("class id style width height viewBox xmlns fill stroke stroke-width d x y x1 x2 y1 y2 cx cy r rx ry points transform opacity fill-opacity stroke-opacity offset stop-color stop-opacity font-size font-family font-weight text-anchor dominant-baseline preserveAspectRatio role aria-label")
	visual := false
	var walk func(*html.Node) error
	walk = func(n *html.Node) error {
		if n.Type == html.ElementNode {
			if n.Data == "meta" {
				// Permit encoding/viewport metadata, never refresh or an upstream
				// security policy. The preview supplies its own restrictive CSP.
				for _, a := range n.Attr {
					if a.Namespace != "" || !slices.Contains([]string{"charset", "name", "content"}, a.Key) {
						return errors.New("unsupported metadata")
					}
				}
				return nil
			}
			if !slices.Contains(allowed, n.Data) {
				return fmt.Errorf("unsupported element")
			}
			if n.Data == "svg" || n.Data == "style" {
				visual = true
			}
			for _, a := range n.Attr {
				if a.Namespace != "" || !slices.Contains(allowedAttrs, a.Key) {
					return fmt.Errorf("unsupported attribute")
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err = walk(doc); err != nil || !visual {
		return "", errors.New("invalid static illustration")
	}
	var output strings.Builder
	if err = html.Render(&output, doc); err != nil {
		return "", err
	}
	return output.String(), nil
}
