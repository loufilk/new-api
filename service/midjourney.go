package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func CovertMjpActionToModelName(mjAction string) string {
	modelName := "mj_" + strings.ToLower(mjAction)
	if mjAction == constant.MjActionSwapFace {
		modelName = "swap_face"
	}
	return modelName
}

// PrepareMidjourneyTaskBilling captures the original authorized token before
// insertion. The empty receipt is an uncharged marker, never a refund source.
func PrepareMidjourneyTaskBilling(relayInfo *relaycommon.RelayInfo, task *model.Midjourney, quota int, shouldBill bool) (bool, error) {
	if task == nil {
		return false, errors.New("Midjourney task is nil")
	}
	task.Quota = 0
	task.TokenId = 0
	task.TokenFingerprint = model.BillingTokenFingerprint("no-token-v2")
	task.BillingAccounted = false
	task.BillingRequestID = ""
	task.BillingOriginalQuota = 0
	task.BillingChannelId = 0
	task.WalletDebit = []model.LedgerShare{}
	if !shouldBill {
		return false, nil
	}
	if relayInfo == nil || relayInfo.RequestId == "" || len(relayInfo.RequestId) > 64 || strings.TrimSpace(relayInfo.RequestId) != relayInfo.RequestId || quota < 0 || quota > common.MaxQuota {
		return false, errors.New("invalid Midjourney billing input")
	}
	if relayInfo.BillingSource == BillingSourceSubscription {
		return false, errors.New("legacy Midjourney billing does not support subscriptions")
	}
	if !relayInfo.IsPlayground {
		if relayInfo.TokenId <= 0 || relayInfo.TokenKey == "" {
			return false, errors.New("original Midjourney token identity is missing")
		}
		task.TokenId = relayInfo.TokenId
		task.TokenFingerprint = model.BillingTokenFingerprint(relayInfo.TokenKey)
	}
	task.Quota = quota
	task.BillingRequestID = relayInfo.RequestId
	task.BillingOriginalQuota = quota
	task.BillingChannelId = task.ChannelId
	if relayInfo.ChannelMeta != nil && relayInfo.ChannelId > 0 {
		task.BillingChannelId = relayInfo.ChannelId
	}
	return true, nil
}

// SettleMidjourneyTaskBilling supports explicitly identified legacy unreserved rows.
// New submissions must use ReserveMidjourneyBilling and PersistMidjourneyBilling.
// It returns true only for a newly acknowledged commit.
// A replay must not repeat the caller's display log. The row is locked before
// the source, so settlement and failure observers use the same lock order.
func SettleMidjourneyTaskBilling(relayInfo *relaycommon.RelayInfo, task *model.Midjourney, prepared bool) (bool, error) {
	if !prepared {
		return false, nil
	}
	if relayInfo == nil || task == nil || task.Id <= 0 || relayInfo.UserId != task.UserId || relayInfo.RequestId == "" {
		return false, errors.New("invalid persisted Midjourney billing identity")
	}
	order := WalletDebitOrder(relayInfo)
	var persisted *model.Midjourney
	var receipt *model.LedgerResult
	applied := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		persisted, err = model.LoadMidjourneyFinancialTx(tx, task.Id, task.UserId)
		if err != nil {
			return err
		}
		if persisted.MjId != task.MjId || !persisted.HasCurrentBillingIdentity() || persisted.WalletDebit == nil || persisted.Quota < 0 || persisted.Quota > common.MaxQuota || persisted.BillingRequestID != relayInfo.RequestId || persisted.BillingOriginalQuota != task.BillingOriginalQuota || persisted.BillingRequestID != task.BillingRequestID || persisted.Quota != 0 && persisted.Quota != persisted.BillingOriginalQuota {
			return model.ErrTaskFinancialReview
		}
		tokenID, fingerprint := 0, model.BillingTokenFingerprint("no-token-v2")
		if !relayInfo.IsPlayground {
			tokenID, fingerprint = relayInfo.TokenId, model.BillingTokenFingerprint(relayInfo.TokenKey)
		}
		if persisted.TokenId != tokenID || persisted.TokenFingerprint != fingerprint {
			return fmt.Errorf("%w: original Midjourney token identity changed", model.ErrTaskFinancialReview)
		}
		if persisted.BillingAccounted {
			if _, err := model.ClaimMidjourneyBillingOwnerTx(tx, persisted, false, persisted.BillingOriginalQuota); err != nil {
				return err
			}
			return model.ValidateTaskWalletReceipt(persisted.WalletDebit, persisted.Quota)
		}
		if len(persisted.WalletDebit) > 0 {
			return fmt.Errorf("%w: Midjourney receipt exists without accounting marker", model.ErrTaskFinancialReview)
		}
		if persisted.Quota == 0 && persisted.BillingOriginalQuota > 0 {
			_, err := model.ClaimMidjourneyBillingOwnerTx(tx, persisted, false, 0)
			return err // charging was canceled before it committed
		}
		if persisted.Status == "FAILURE" {
			return errors.New("Midjourney task failed before charging")
		}
		owned, err := model.ClaimMidjourneyBillingOwnerTx(tx, persisted, true, persisted.BillingOriginalQuota)
		if err != nil {
			return err
		}
		if owned {
			return fmt.Errorf("%w: request owner exists without Midjourney accounting", model.ErrTaskFinancialReview)
		}
		entry := model.LedgerEntry{UserId: persisted.UserId, Account: model.AccountPaid, Delta: persisted.Quota, Reason: model.LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: relayInfo.RequestId, RequestId: relayInfo.RequestId, IdempotencyKey: fmt.Sprintf("billing:settle:%s:%d", relayInfo.RequestId, persisted.Quota)}
		if err := model.RejectTaskSourceOnlyReplayTx(tx, persisted.UserId, entry.IdempotencyKey); err != nil {
			return err
		}
		if persisted.Quota > 0 {
			receipt, err = model.DebitQuotaTx(tx, entry, order...)
			if err != nil {
				return err
			}
			if receipt.Replayed {
				return fmt.Errorf("%w: source-only Midjourney charge", model.ErrTaskFinancialReview)
			}
		}
		if err := model.ApplyBillingTokenDeltaTx(tx, &model.BillingFinalization{UserID: persisted.UserId, TokenID: persisted.TokenId, TokenFingerprint: persisted.TokenFingerprint}, -persisted.Quota); err != nil {
			return err
		}
		if err := model.ApplyTaskUsageDeltaTx(tx, persisted.UserId, persisted.GetBillingChannelId(), persisted.Quota, 1); err != nil {
			return err
		}
		persisted.WalletDebit = appendTaskShares([]model.LedgerShare{}, receipt)
		persisted.BillingAccounted = true
		if err := persisted.UpdateBillingStateTx(tx); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		// Never erase a marker after an uncertain COMMIT: the next caller must
		// reread the row to establish whether the entire transaction committed.
		return false, err
	}
	*task = *persisted
	if receipt != nil {
		model.RefreshUserQuotaCache(task.UserId, receipt.TotalAfter)
	}
	if applied {
		checkAndSendQuotaNotify(relayInfo, task.Quota, 0)
	}
	return applied, nil
}

// RefundMidjourneyQuota uses the locked, current outstanding receipt. The same
// transaction owns source, original token, usage and the zero-quota marker.
func RefundMidjourneyQuota(ctx context.Context, task *model.Midjourney, reason string) bool {
	if task == nil || task.Id <= 0 {
		return false
	}
	var persisted *model.Midjourney
	var receipt *model.LedgerResult
	var display *model.RecordTaskBillingLogParams
	err := model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		persisted, err = model.LoadMidjourneyFinancialTx(tx, task.Id, task.UserId)
		if err != nil {
			return err
		}
		if persisted.MjId != task.MjId || persisted.RequiresFinancialReview() {
			return model.ErrTaskFinancialReview
		}
		quota := persisted.Quota
		if quota < 0 || quota > common.MaxQuota || !persisted.HasCurrentBillingIdentity() {
			return model.ErrTaskFinancialReview
		}
		if quota == 0 {
			if persisted.WalletDebit == nil || len(persisted.WalletDebit) != 0 {
				return model.ErrTaskFinancialReview
			}
			actual, allowNew := persisted.BillingOriginalQuota, false
			if !persisted.BillingAccounted {
				actual = 0
				// A first explicit cancellation of a zero-price preparation owns
				// the request without incrementing usage. A later charge sees this
				// owner and cannot mistake the missing accounting flag for new work.
				allowNew = persisted.BillingOriginalQuota == 0 && persisted.Status != "SUCCESS"
			}
			_, err := model.ClaimMidjourneyBillingOwnerTx(tx, persisted, allowNew, actual)
			return err
		}
		if quota != persisted.BillingOriginalQuota || persisted.Status == "SUCCESS" {
			return model.ErrTaskFinancialReview
		}
		if !persisted.BillingAccounted {
			if persisted.WalletDebit == nil || len(persisted.WalletDebit) != 0 {
				return model.ErrTaskFinancialReview
			}
			owned, err := model.ClaimMidjourneyBillingOwnerTx(tx, persisted, true, 0)
			if err != nil {
				return err
			}
			if owned {
				return fmt.Errorf("%w: Midjourney accounting marker conflicts with its owner", model.ErrTaskFinancialReview)
			}
			key := fmt.Sprintf("billing:settle:%s:%d", persisted.BillingRequestID, persisted.BillingOriginalQuota)
			if err := model.RejectTaskSourceOnlyReplayTx(tx, persisted.UserId, key); err != nil {
				return err
			}
			// Only a new, explicitly identified uncharged row can be cleared.
			persisted.Quota = 0
			return persisted.UpdateBillingStateTx(tx)
		}
		if _, err := model.ClaimMidjourneyBillingOwnerTx(tx, persisted, false, persisted.BillingOriginalQuota); err != nil {
			return err
		}
		if err := model.ValidateTaskWalletReceipt(persisted.WalletDebit, quota); err != nil {
			return err
		}
		referenceID, legacyKey := persisted.MjId, "midjourney:refund:"+persisted.MjId
		if referenceID == "" {
			referenceID = strconv.Itoa(persisted.Id)
			legacyKey = "midjourney:refund-local:" + referenceID
		}
		operationID := fmt.Sprintf("midjourney:financial-refund:%d", persisted.Id)
		if err := model.RejectTaskSourceOnlyReplayTx(tx, persisted.UserId, operationID, legacyKey); err != nil {
			return err
		}
		entry := model.LedgerEntry{UserId: persisted.UserId, Account: model.AccountPaid, Reason: model.LedgerReasonTaskRefund, ReferenceType: "mj_task", ReferenceId: referenceID, RequestId: persisted.MjId, IdempotencyKey: operationID}
		entry.SetMetadata(map[string]any{"reason": reason})
		receipt, err = model.CreditQuotaSharesTx(tx, entry, persisted.WalletDebit)
		if err != nil {
			return err
		}
		if receipt.Replayed {
			return fmt.Errorf("%w: source-only Midjourney refund", model.ErrTaskFinancialReview)
		}
		if err := model.ApplyBillingTokenDeltaTx(tx, &model.BillingFinalization{UserID: persisted.UserId, TokenID: persisted.TokenId, TokenFingerprint: persisted.TokenFingerprint}, quota); err != nil {
			return err
		}
		if err := model.ApplyTaskUsageDeltaTx(tx, persisted.UserId, persisted.GetBillingChannelId(), -quota, 0); err != nil {
			return err
		}
		other := model.NewLogOther()
		other.SetPublic("task_id", persisted.MjId)
		other.SetPublic("reason", reason)
		params := model.RecordTaskBillingLogParams{UserId: persisted.UserId, LogType: model.LogTypeRefund, ChannelId: persisted.GetBillingChannelId(), ModelName: CovertMjpActionToModelName(persisted.Action), Quota: quota, TokenId: persisted.TokenId, Other: other, OperationID: operationID}
		logged, err := model.RecordTaskBillingLogTx(tx, params)
		if err != nil {
			return err
		}
		if !logged {
			display = &params
		}
		persisted.Quota = 0
		persisted.WalletDebit = []model.LedgerShare{}
		return persisted.UpdateBillingStateTx(tx)
	})
	if err != nil {
		logger.LogWarn(ctx, "Midjourney refund requires retry or reconciliation: "+err.Error())
		return false
	}
	*task = *persisted
	if receipt != nil {
		model.RefreshUserQuotaCache(task.UserId, receipt.TotalAfter)
	}
	if display != nil {
		model.RecordTaskBillingLog(*display)
	}
	return true
}

func GetMjRequestModel(relayMode int, midjRequest *dto.MidjourneyRequest) (string, *dto.MidjourneyResponse, bool) {
	action := ""
	if relayMode == relayconstant.RelayModeMidjourneyAction {
		// plus request
		err := CoverPlusActionToNormalAction(midjRequest)
		if err != nil {
			return "", err, false
		}
		action = midjRequest.Action
	} else {
		switch relayMode {
		case relayconstant.RelayModeMidjourneyImagine:
			action = constant.MjActionImagine
		case relayconstant.RelayModeMidjourneyVideo:
			action = constant.MjActionVideo
		case relayconstant.RelayModeMidjourneyEdits:
			action = constant.MjActionEdits
		case relayconstant.RelayModeMidjourneyDescribe:
			action = constant.MjActionDescribe
		case relayconstant.RelayModeMidjourneyBlend:
			action = constant.MjActionBlend
		case relayconstant.RelayModeMidjourneyShorten:
			action = constant.MjActionShorten
		case relayconstant.RelayModeMidjourneyChange:
			action = midjRequest.Action
		case relayconstant.RelayModeMidjourneyModal:
			action = constant.MjActionModal
		case relayconstant.RelayModeSwapFace:
			action = constant.MjActionSwapFace
		case relayconstant.RelayModeMidjourneyUpload:
			action = constant.MjActionUpload
		case relayconstant.RelayModeMidjourneySimpleChange:
			params := ConvertSimpleChangeParams(midjRequest.Content)
			if params == nil {
				return "", MidjourneyErrorWrapper(constant.MjRequestError, "invalid_request"), false
			}
			action = params.Action
		case relayconstant.RelayModeMidjourneyTaskFetch, relayconstant.RelayModeMidjourneyTaskFetchByCondition, relayconstant.RelayModeMidjourneyNotify:
			return "", nil, true
		default:
			return "", MidjourneyErrorWrapper(constant.MjRequestError, "unknown_relay_action"), false
		}
	}
	modelName := CovertMjpActionToModelName(action)
	return modelName, nil, true
}

func CoverPlusActionToNormalAction(midjRequest *dto.MidjourneyRequest) *dto.MidjourneyResponse {
	// "customId": "MJ::JOB::upsample::2::3dbbd469-36af-4a0f-8f02-df6c579e7011"
	customId := midjRequest.CustomId
	if customId == "" {
		return MidjourneyErrorWrapper(constant.MjRequestError, "custom_id_is_required")
	}
	splits := strings.Split(customId, "::")
	var action string
	if splits[1] == "JOB" {
		action = splits[2]
	} else {
		action = splits[1]
	}

	if action == "" {
		return MidjourneyErrorWrapper(constant.MjRequestError, "unknown_action")
	}
	if strings.Contains(action, "upsample") {
		index, err := strconv.Atoi(splits[3])
		if err != nil {
			return MidjourneyErrorWrapper(constant.MjRequestError, "index_parse_failed")
		}
		midjRequest.Index = index
		midjRequest.Action = constant.MjActionUpscale
	} else if strings.Contains(action, "variation") {
		midjRequest.Index = 1
		if action == "variation" {
			index, err := strconv.Atoi(splits[3])
			if err != nil {
				return MidjourneyErrorWrapper(constant.MjRequestError, "index_parse_failed")
			}
			midjRequest.Index = index
			midjRequest.Action = constant.MjActionVariation
		} else if action == "low_variation" {
			midjRequest.Action = constant.MjActionLowVariation
		} else if action == "high_variation" {
			midjRequest.Action = constant.MjActionHighVariation
		}
	} else if strings.Contains(action, "pan") {
		midjRequest.Action = constant.MjActionPan
		midjRequest.Index = 1
	} else if strings.Contains(action, "reroll") {
		midjRequest.Action = constant.MjActionReRoll
		midjRequest.Index = 1
	} else if action == "Outpaint" {
		midjRequest.Action = constant.MjActionZoom
		midjRequest.Index = 1
	} else if action == "CustomZoom" {
		midjRequest.Action = constant.MjActionCustomZoom
		midjRequest.Index = 1
	} else if action == "Inpaint" {
		midjRequest.Action = constant.MjActionInPaint
		midjRequest.Index = 1
	} else {
		return MidjourneyErrorWrapper(constant.MjRequestError, "unknown_action:"+customId)
	}
	return nil
}

func ConvertSimpleChangeParams(content string) *dto.MidjourneyRequest {
	split := strings.Split(content, " ")
	if len(split) != 2 {
		return nil
	}

	action := strings.ToLower(split[1])
	changeParams := &dto.MidjourneyRequest{}
	changeParams.TaskId = split[0]

	if action[0] == 'u' {
		changeParams.Action = "UPSCALE"
	} else if action[0] == 'v' {
		changeParams.Action = "VARIATION"
	} else if action == "r" {
		changeParams.Action = "REROLL"
		return changeParams
	} else {
		return nil
	}

	index, err := strconv.Atoi(action[1:2])
	if err != nil || index < 1 || index > 4 {
		return nil
	}
	changeParams.Index = index
	return changeParams
}

// RecordMidjourneyPolicyResponse distinguishes accepted tasks from errors inside
// HTTP 200 responses. Submissions are single-attempt: an ambiguous transport
// failure must never create a duplicate task on another channel.
func RecordMidjourneyPolicyResponse(c *gin.Context, response *dto.MidjourneyResponseWithStatusCode, requestErr error) bool {
	if response == nil {
		response = MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "empty_response", http.StatusBadGateway)
	}
	accepted := requestErr == nil && response.StatusCode == http.StatusOK && (response.Response.Code == 1 || response.Response.Code == 21 || response.Response.Code == 22)
	if accepted {
		properties, _ := response.Response.Properties.(map[string]any)
		if properties["status"] != "FAILURE" {
			return true
		}
	}
	state := RequestPolicy(c)
	event := PolicyEvent{ChannelID: c.GetInt("channel_id"), Status: response.StatusCode, ErrorCode: strconv.Itoa(response.Response.Code), ErrorSource: "upstream", Decision: PolicyDecision{Action: "failure", Reason: "upstream_failure", Source: "upstream"}}
	state.AddEvent(event)
	event.Decision, event.Health = PolicyDecision{Action: "stop", Reason: "non_retryable_error", Source: "system"}, "unchanged"
	if accepted {
		event.Decision.Reason = "task_accepted"
	}
	state.AddEvent(event)
	return false
}

func DoMidjourneyHttpRequest(c *gin.Context, timeout time.Duration, fullRequestURL string) (*dto.MidjourneyResponseWithStatusCode, []byte, error) {
	// Until dispatch starts, all local parsing/build failures are known-unsent.
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, false)
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestUnsent, true)
	var nullBytes []byte
	//var requestBody io.Reader
	//requestBody = c.Request.Body
	// read request body to json, delete accountFilter and notifyHook
	var mapResult map[string]any
	// if get request, no need to read request body
	if c.Request.Method != "GET" {
		err := common.DecodeJson(c.Request.Body, &mapResult)
		if err != nil {
			return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "read_request_body_failed", http.StatusInternalServerError), nullBytes, err
		}
		if !setting.MjAccountFilterEnabled {
			delete(mapResult, "accountFilter")
		}
		if !setting.MjNotifyEnabled {
			delete(mapResult, "notifyHook")
		}
		//req, err := http.NewRequest(c.Request.Method, fullRequestURL, requestBody)
		// make new request with mapResult
	}
	if setting.MjModeClearEnabled {
		if prompt, ok := mapResult["prompt"].(string); ok {
			prompt = strings.Replace(prompt, "--fast", "", -1)
			prompt = strings.Replace(prompt, "--relax", "", -1)
			prompt = strings.Replace(prompt, "--turbo", "", -1)

			mapResult["prompt"] = prompt
		}
	}
	reqBody, err := common.Marshal(mapResult)
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "marshal_request_body_failed", http.StatusInternalServerError), nullBytes, err
	}
	req, err := http.NewRequest(c.Request.Method, fullRequestURL, strings.NewReader(string(reqBody)))
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "create_request_failed", http.StatusInternalServerError), nullBytes, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	// 使用带有超时的 context 创建新的请求
	req = req.WithContext(ctx)
	req.Header.Set("Content-Type", c.Request.Header.Get("Content-Type"))
	req.Header.Set("Accept", c.Request.Header.Get("Accept"))
	auth := common.GetContextKeyString(c, constant.ContextKeyChannelKey)
	if auth != "" {
		auth = strings.TrimPrefix(auth, "Bearer ")
		req.Header.Set("mj-api-secret", auth)
	}
	defer cancel()
	client := GetHttpClient()
	if client == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || req.URL.Host == "" {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "create_request_failed", http.StatusInternalServerError), nullBytes, errors.New("invalid Midjourney HTTP client or URL")
	}
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, true)
	common.SetContextKey(c, constant.ContextKeyUpstreamRequestUnsent, false)
	resp, err := DoRequestWithoutRedirects(client, req)
	if err != nil {
		var dialErr *net.OpError
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) || errors.As(err, &dialErr) && dialErr.Op == "dial" {
			common.SetContextKey(c, constant.ContextKeyUpstreamRequestUnsent, true)
		}
		common.SysLog(common.LogText("do request failed: %s", err.Error()))
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "do_request_failed", http.StatusInternalServerError), nullBytes, err
	}
	statusCode := resp.StatusCode
	//if statusCode != 200  {
	//	return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "bad_response_status_code", statusCode), nullBytes, nil
	//}
	err = req.Body.Close()
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "close_request_body_failed", statusCode), nullBytes, err
	}
	err = c.Request.Body.Close()
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "close_request_body_failed", statusCode), nullBytes, err
	}
	var midjResponse dto.MidjourneyResponse
	var midjourneyUploadsResponse dto.MidjourneyUploadResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "read_response_body_failed", statusCode), nullBytes, err
	}
	CloseResponseBodyGracefully(resp)
	logger.LogDebug(c, "midjourney response body: %s", responseBody)
	if len(responseBody) == 0 {
		return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "empty_response_body", statusCode), responseBody, nil
	} else {
		err = common.Unmarshal(responseBody, &midjResponse)
		if err != nil {
			err2 := common.Unmarshal(responseBody, &midjourneyUploadsResponse)
			if err2 != nil {
				return MidjourneyErrorWithStatusCodeWrapper(constant.MjErrorUnknown, "unmarshal_response_body_failed", statusCode), responseBody, err
			}
		}
	}
	//for k, v := range resp.Header {
	//	c.Writer.Header().Set(k, v[0])
	//}
	return &dto.MidjourneyResponseWithStatusCode{
		StatusCode: statusCode,
		Response:   midjResponse,
	}, responseBody, nil
}
