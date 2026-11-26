package controller

import (
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/Calcium-Ion/go-epay/epay"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var errEpayIntentConflict = errors.New("epay checkout intent conflicts with its frozen quote")

// Fixed-length, merchant-safe order number, deterministically scoped to the
// authenticated local user. Legacy order numbers use a different prefix.
func epayIntentTradeNo(owner int, raw string) (string, error) {
	rawUUID, gateway, found := strings.Cut(raw, ".")
	gatewayBytes, decodeErr := hex.DecodeString(gateway)
	if !found || decodeErr != nil || len(gatewayBytes) != 16 || gateway != strings.ToLower(gateway) {
		return "", errors.New("invalid checkout gateway")
	}
	id, err := uuid.Parse(rawUUID)
	if err != nil || id.Version() != 4 || id.Variant() != uuid.RFC4122 || id.String() != rawUUID || owner <= 0 {
		return "", errors.New("invalid checkout intent")
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("epay:v1:%d:%s", owner, raw)))
	return "EI" + strings.TrimRight(base32.StdEncoding.EncodeToString(sum[:16]), "="), nil
}

// The public checkout destination (not the signing secret) is part of the
// logical intent ID. A gateway rotation cannot recreate the old order under
// a different merchant endpoint, even when both merchants use the same PID.
func epayEndpointRef(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	u.Fragment = ""
	u.RawFragment = ""
	h := sha256.Sum256([]byte("epay:endpoint:v1:" + u.String()))
	return hex.EncodeToString(h[:16])
}
func epayQuoteGatewayRef(address, signType string) string {
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	submitPath := epay.PurchaseUrl
	switch strings.ToUpper(signType) {
	case "", epay.SignTypeMD5:
	case epay.SignTypeRSA:
		submitPath = epay.PurchaseRSAUrl
	default:
		return ""
	}
	u.Path = path.Join(u.Path, submitPath)
	return epayEndpointRef(u.String())
}

// No new table, expiry job, or process lock. The existing globally unique
// trade_no is the durable receipt; its original terms can never be overwritten.
func persistEpayIntent(ctx context.Context, order *model.TopUp) (model.TopUp, error) {
	db := model.DB.WithContext(ctx)
	var stored model.TopUp
	if err := db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "trade_no"}}, DoNothing: true}).Create(order).Error; err != nil {
		return stored, err
	}
	if err := db.Where("trade_no = ? AND user_id = ?", order.TradeNo, order.UserId).Take(&stored).Error; err != nil {
		return stored, err
	}
	if stored.PaymentProvider != model.PaymentProviderEpay || stored.Amount != order.Amount ||
		stored.PaymentMethod != order.PaymentMethod || stored.QuotedWalletQuota != order.QuotedWalletQuota ||
		stored.QuotedPaymentMinor != order.QuotedPaymentMinor || stored.QuotedPaymentScope != order.QuotedPaymentScope ||
		stored.QuotedPaymentCurrency != order.QuotedPaymentCurrency || stored.QuotedPaymentEnvironment != order.QuotedPaymentEnvironment {
		return stored, errEpayIntentConflict
	}
	return stored, nil
}

func epayIntentError(c *gin.Context, status int, code string) {
	c.JSON(status, gin.H{"success": false, "code": code})
}

// Only use for a definitive rejection before this HTTP invocation entered the
// order insert path. It cannot rule out a different invocation with this ID.
func rejectEpayIntentBeforeCreate(c *gin.Context, code string) {
	c.JSON(http.StatusConflict, gin.H{"success": false, "code": code, "intent_id": c.Param("intent"), "insert_attempted": false})
}

// Read only. Never signs a checkout form, sends a provider request or credits an account.
func GetEpayIntent(c *gin.Context) {
	id := c.GetInt("id")
	tradeNo, err := epayIntentTradeNo(id, c.Param("intent"))
	if err != nil {
		epayIntentError(c, http.StatusBadRequest, "invalid_intent")
		return
	}
	var order model.TopUp
	err = model.DB.WithContext(c.Request.Context()).Select("status", "trade_no", "payment_method", "quoted_payment_minor").
		Where("user_id = ? AND trade_no = ? AND payment_provider = ?", id, tradeNo, model.PaymentProviderEpay).Take(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "code": "epay_intent_not_found", "reference": tradeNo})
		return
	}
	if err != nil {
		epayIntentError(c, http.StatusServiceUnavailable, "epay_intent_unavailable")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "trade_no": order.TradeNo, "order_status": order.Status, "payment_method": order.PaymentMethod, "payment_minor": order.QuotedPaymentMinor})
}
