package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

// ReserveMidjourneyBilling preserves the existing wallet-only MJ policy and
// original model eligibility, but atomically holds the full source + token
// amount before sending. Neither subscriptions nor trusted-user bypass apply.
func ReserveMidjourneyBilling(c *gin.Context, info *relaycommon.RelayInfo, quota int) error {
	if info == nil || info.Billing != nil || quota < 0 || quota > common.MaxQuota || !info.IsPlayground && (info.TokenId <= 0 || info.TokenKey == "") {
		return errors.New("invalid Midjourney reservation identity")
	}
	order := WalletDebitOrder(info)
	accounts := make([]string, 0, len(order))
	for _, account := range order {
		accounts = append(accounts, string(account))
	}
	funding, err := createFundingSource(moneyport.SourceWallet, moneyport.FactoryRequest{UserID: info.UserId, RequestID: info.RequestId, BillingModel: info.GetBillingModelName(), Amount: quota, AccountOrder: accounts})
	if err != nil {
		return err
	}
	session := &BillingSession{relayInfo: info, funding: funding, lang: i18n.StatedLang(c)}
	if !session.durableTerminalSource() {
		return errors.New("Midjourney requires atomic wallet reservation")
	}
	if err := session.reserveDurableLocked(quota, true); err != nil {
		return err
	}
	info.Billing = session
	return nil
}

func PersistMidjourneyBilling(info *relaycommon.RelayInfo, task *model.Midjourney) (bool, error) {
	if info == nil {
		return false, errors.New("missing Midjourney billing identity")
	}
	session, ok := info.Billing.(*BillingSession)
	if !ok {
		return false, errors.New("Midjourney has no original reservation")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	receipt, err := session.terminalReceiptLocked()
	if err != nil {
		return false, err
	}
	// Acceptance is already observed. A failed handoff cannot permit a later
	// request failure defer to refund this hold, including an uncertain COMMIT.
	session.taskOwned = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	applied, err := model.CommitMidjourneyBilling(ctx, receipt, task)
	if err != nil {
		code, class := model.BillingRefundErrorCode(err)
		logger.LogError(context.Background(), fmt.Sprintf("event=midjourney_handoff_unconfirmed request_id=%q code=%s class=%s action=manual_reconciliation", receipt.RequestID, code, class))
		return false, err
	}
	session.fundingSettled = true
	session.actualSettled = task.BillingOriginalQuota
	return applied, nil
}

// PreserveUnfinalizedMidjourneyBilling is a fail-closed defer for the interval
// between reservation and durable handoff. Panic, decode/DB errors, and lost
// acknowledgements cannot become automatic refund authority.
func PreserveUnfinalizedMidjourneyBilling(info *relaycommon.RelayInfo) {
	if info != nil && info.Billing != nil && info.Billing.NeedsRefund() {
		PreserveUnknownBillingDelivery(info)
	}
}

// RefundUnconsumedMidjourneySubmission only releases known-unsent requests or
// explicit MJ rejection codes. An HTTP error/unknown code alone proves neither
// non-delivery nor zero upstream cost.
func RefundUnconsumedMidjourneySubmission(c *gin.Context, info *relaycommon.RelayInfo, response *dto.MidjourneyResponseWithStatusCode, requestErr error) {
	if info == nil || info.Billing == nil {
		return
	}
	unconsumed := common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent)
	if requestErr == nil && response != nil && response.StatusCode == http.StatusOK {
		switch response.Response.Code {
		case 3, 23, 24: // no account instance, queue full, rejected prompt
			unconsumed = true
		case 1, 21, 22:
			properties, _ := response.Response.Properties.(map[string]any)
			unconsumed = properties["status"] == "FAILURE"
		}
	}
	if unconsumed {
		info.Billing.Refund(c)
	}
}
