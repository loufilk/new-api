package paymentcore

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// RequireCompliance is the generic creation-time payment compliance gate used
// by every concrete payment provider.
func RequireCompliance(c *gin.Context) bool {
	if !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorT(c, "Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
		return false
	}
	return true
}

// ReturnPath resolves dashboard payment return targets from the configured
// public server address.
func ReturnPath(suffix string) string {
	return strings.TrimRight(system_setting.ServerAddress, "/") + suffix
}

// CallbackAddress preserves the generic payment callback override contract.
func CallbackAddress() string {
	if operation_setting.CustomCallbackAddress == "" {
		return system_setting.ServerAddress
	}
	return operation_setting.CustomCallbackAddress
}

func TopUpQuota(amount int64) (int, error) {
	quota := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		quota = decimal.NewFromInt(quota.Div(quotaPerUnit).IntPart()).Mul(quotaPerUnit)
	} else {
		quota = quota.Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	return common.WalletQuotaFromDecimalStrict(quota)
}

func MaxTopUpAmount() int64 {
	if common.QuotaPerUnit <= 0 {
		return 0
	}
	quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	maxStoredAmount := decimal.NewFromInt(common.MaxWalletQuota).Div(quotaPerUnit).Floor()
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return maxStoredAmount.Add(decimal.NewFromInt(1)).Mul(quotaPerUnit).Ceil().Sub(decimal.NewFromInt(1)).IntPart()
	}
	return maxStoredAmount.IntPart()
}

func ValidateCreditedQuota(quota decimal.Decimal) (int, error) {
	value, err := common.WalletQuotaFromDecimalStrict(quota)
	if err != nil {
		return 0, common.NewMessage("Top-up quota exceeds the range the system can represent")
	}
	if value <= 0 {
		return 0, common.NewMessage("Top-up quota must be greater than 0")
	}
	return value, nil
}

func ValidateTopUpQuota(amount int64) (int, error) {
	quota, err := TopUpQuota(amount)
	if err == nil && quota > 0 {
		return quota, nil
	}
	maxAmount := MaxTopUpAmount()
	if maxAmount > 0 && amount > maxAmount {
		return 0, common.NewMessage("Top-up amount cannot exceed {{max}}", map[string]any{"max": maxAmount})
	}
	return 0, common.NewMessage("Invalid top-up amount")
}

func RejectInvalidCreditedQuota(c *gin.Context, userID int, quota decimal.Decimal) bool {
	creditedQuota, err := ValidateCreditedQuota(quota)
	if err == nil {
		err = model.ValidateTopUpQuotaCapacity(userID, creditedQuota)
	}
	if err != nil {
		WriteError(c, err)
		return true
	}
	return false
}

func RejectInvalidTopUpQuota(c *gin.Context, userID int, amount int64) bool {
	creditedQuota, err := ValidateTopUpQuota(amount)
	if err == nil {
		err = model.ValidateTopUpQuotaCapacity(userID, creditedQuota)
	}
	if err != nil {
		WriteError(c, err)
		return true
	}
	return false
}

// PayloadSummary gives operators a stable correlation value without persisting
// payment payloads, customer data, or webhook signatures in logs.
func PayloadSummary(payload []byte) string {
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("bytes=%d sha256=%x", len(payload), digest[:8])
}

// WriteError preserves the provider response contract and adds translation data.
func WriteError(c *gin.Context, err error) {
	body := gin.H{"message": "error", "data": err.Error()}
	var message *common.Message
	if errors.As(err, &message) {
		maps.Copy(body, message.Fields())
	}
	c.JSON(http.StatusOK, body)
}

var orderLocks sync.Map
var createLock sync.Mutex

type refCountedMutex struct {
	mu       sync.Mutex
	refCount int
}

func LockOrder(tradeNo string) {
	createLock.Lock()
	var lock *refCountedMutex
	if value, ok := orderLocks.Load(tradeNo); ok {
		lock = value.(*refCountedMutex)
	} else {
		lock = &refCountedMutex{}
		orderLocks.Store(tradeNo, lock)
	}
	lock.refCount++
	createLock.Unlock()
	lock.mu.Lock()
}

func UnlockOrder(tradeNo string) {
	value, ok := orderLocks.Load(tradeNo)
	if !ok {
		return
	}
	lock := value.(*refCountedMutex)
	lock.mu.Unlock()

	createLock.Lock()
	lock.refCount--
	if lock.refCount == 0 {
		orderLocks.Delete(tradeNo)
	}
	createLock.Unlock()
}
