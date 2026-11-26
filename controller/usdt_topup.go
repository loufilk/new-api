package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/gmpay"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var usdtNetworks = [...]struct {
	ID, Name, Provider string
	Precision          int
}{
	{"bsc", "BNB Smart Chain (BEP20)", "binance", 18},
	{"polygon", "Polygon PoS", "polygon", 6},
	{"tron", "TRON (TRC20)", "tron", 6},
}
var usdtOrderID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var usdtRequestID = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,80}$`)
var errUSDTConfig = common.NewMessage("Invalid USDT recharge configuration")
var errUSDTDisabled = common.NewMessage("USDT recharge is unavailable")
var errUSDTIncompatible = common.NewMessage("USDT order could not be verified")

type usdtNetworkConfig struct {
	ID       string `json:"id"`
	Enabled  bool   `json:"enabled"`
	BonusBPS int    `json:"bonus_bps"`
}
type usdtConfig struct {
	Enabled   bool                `json:"enabled"`
	MinAmount string              `json:"min_amount"`
	MaxAmount string              `json:"max_amount"`
	Networks  []usdtNetworkConfig `json:"networks"`
}
type usdtOptionsNetwork struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	BonusBPS int    `json:"bonus_bps"`
}

func loadUSDTPayClient() (*gmpay.Client, error) {
	return gmpay.New(os.Getenv("USDT_GMPAY_ORIGIN"), os.Getenv("USDT_GMPAY_PID"), os.Getenv("USDT_GMPAY_SECRET"), os.Getenv("USDT_CALLBACK_ORIGIN"))
}
func normalizeUSDTConfig(cfg usdtConfig) (usdtConfig, error) {
	min, err1 := gmpay.ParseAmount(cfg.MinAmount, 2)
	max, err2 := gmpay.ParseAmount(cfg.MaxAmount, 2)
	if err1 != nil || err2 != nil || min.LessThan(decimal.New(2, -2)) || max.GreaterThan(decimal.NewFromInt(1000000)) || min.GreaterThan(max) || len(cfg.Networks) != len(usdtNetworks) {
		return usdtConfig{}, errUSDTConfig
	}
	byID := make(map[string]usdtNetworkConfig, len(cfg.Networks))
	for _, n := range cfg.Networks {
		if _, ok := byID[n.ID]; ok || n.BonusBPS < 0 || n.BonusBPS > 10000 {
			return usdtConfig{}, errUSDTConfig
		}
		byID[n.ID] = n
	}
	cfg.Networks = make([]usdtNetworkConfig, 0, len(usdtNetworks))
	for _, n := range usdtNetworks {
		item, ok := byID[n.ID]
		if !ok {
			return usdtConfig{}, errUSDTConfig
		}
		cfg.Networks = append(cfg.Networks, item)
	}
	cfg.MinAmount, cfg.MaxAmount = min.StringFixed(2), max.StringFixed(2)
	return cfg, nil
}
func readUSDTConfig(ctx context.Context) (usdtConfig, error) {
	var cfg usdtConfig
	found, err := model.LoadUSDTTopUpConfig(ctx, &cfg)
	if err != nil {
		return usdtConfig{}, err
	}
	if !found {
		return usdtConfig{MinAmount: "10.00", MaxAmount: "10000.00", Networks: []usdtNetworkConfig{
			{ID: "bsc"}, {ID: "polygon"}, {ID: "tron"},
		}}, nil
	}
	return normalizeUSDTConfig(cfg)
}
func GetUSDTConfig(c *gin.Context) {
	cfg, err := readUSDTConfig(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errUSDTConfig)
		return
	}
	_, gatewayErr := loadUSDTPayClient()
	replyUSDTConfig(c, cfg, gatewayErr == nil)
}
func replyUSDTConfig(c *gin.Context, cfg usdtConfig, configured bool) {
	common.ApiSuccess(c, gin.H{"enabled": cfg.Enabled, "min_amount": cfg.MinAmount, "max_amount": cfg.MaxAmount, "networks": cfg.Networks, "configured": configured})
}
func PutUSDTConfig(c *gin.Context) {
	var cfg usdtConfig
	if c.ShouldBindJSON(&cfg) != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errUSDTConfig)
		return
	}
	cfg, err := normalizeUSDTConfig(cfg)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusBadRequest, errUSDTConfig)
		return
	}
	_, gatewayErr := loadUSDTPayClient()
	if cfg.Enabled {
		var enabled bool
		for _, n := range cfg.Networks {
			enabled = enabled || n.Enabled
		}
		if !enabled || gatewayErr != nil || !operation_setting.IsPaymentComplianceConfirmed() {
			common.ApiErrorStatus(c, http.StatusConflict, errUSDTDisabled)
			return
		}
	}
	if model.SaveUSDTTopUpConfig(c.Request.Context(), cfg) != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errUSDTConfig)
		return
	}
	common.SysLog(common.LogText("USDT recharge policy updated by operator id=%d", c.GetInt("id")))
	replyUSDTConfig(c, cfg, gatewayErr == nil)
}
func GetUSDTOptions(c *gin.Context) {
	cfg, err := readUSDTConfig(c.Request.Context())
	if err != nil {
		common.ApiErrorStatus(c, http.StatusInternalServerError, errUSDTConfig)
		return
	}
	_, gatewayErr := loadUSDTPayClient()
	items := make([]usdtOptionsNetwork, 0, len(usdtNetworks))
	if gatewayErr == nil && cfg.Enabled && operation_setting.IsPaymentComplianceConfirmed() {
		for i, n := range cfg.Networks {
			if n.Enabled {
				items = append(items, usdtOptionsNetwork{ID: n.ID, Name: usdtNetworks[i].Name, BonusBPS: n.BonusBPS})
			}
		}
	}
	common.ApiSuccess(c, gin.H{"enabled": len(items) > 0, "networks": items, "min_amount": cfg.MinAmount, "max_amount": cfg.MaxAmount, "currency": "CNY"})
}
func providerNetwork(id string) (network string, places int) {
	for _, n := range usdtNetworks {
		if n.ID == id {
			return n.Provider, n.Precision
		}
	}
	return "", 0
}
func newUSDTOrderID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
func USDTCreateOrder(c *gin.Context) {
	var req struct {
		Network    string `json:"network"`
		Amount     string `json:"amount"`
		RequestKey string `json:"idempotency_key"`
	}
	uid := c.GetInt("id")
	if uid <= 0 {
		c.Status(http.StatusUnauthorized)
		return
	}
	if c.ShouldBindJSON(&req) != nil || !usdtRequestID.MatchString(req.RequestKey) {
		common.ApiErrorStatus(c, http.StatusBadRequest, errUSDTIncompatible)
		return
	}
	amount, err := gmpay.ParseAmount(req.Amount, 2)
	network, precision := providerNetwork(req.Network)
	if err != nil || network == "" || amount.GreaterThan(decimal.NewFromInt(1000000)) {
		common.ApiErrorStatus(c, http.StatusBadRequest, errUSDTIncompatible)
		return
	}
	minor := amount.Shift(2).IntPart()
	ctx := c.Request.Context()
	prior, err := model.FindUSDTTopUpRequest(ctx, uid, req.RequestKey)
	if err == nil {
		replyUSDTExisting(c, prior, req.Network, minor)
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errUSDTDisabled)
		return
	}
	cfg, err := readUSDTConfig(ctx)
	client, gatewayErr := loadUSDTPayClient()
	if err != nil || gatewayErr != nil || !operation_setting.IsPaymentComplianceConfirmed() {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errUSDTDisabled)
		return
	}
	bonusBPS := -1
	for _, n := range cfg.Networks {
		if n.ID == req.Network && n.Enabled {
			bonusBPS = n.BonusBPS
		}
	}
	min, _ := gmpay.ParseAmount(cfg.MinAmount, 2)
	max, _ := gmpay.ParseAmount(cfg.MaxAmount, 2)
	if !cfg.Enabled || bonusBPS < 0 || amount.LessThan(min) || amount.GreaterThan(max) {
		replyUSDTTermsRejected(c, req.RequestKey, req.Network, req.Amount)
		return
	}
	// 1 CNY principal buys 1 displayed wallet unit. Bonus rounds down to fen.
	bonusMinor := minor * int64(bonusBPS) / 10000
	quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	paid, err1 := common.WalletQuotaFromDecimalStrict(amount.Mul(quotaPerUnit))
	bonus, err2 := common.WalletQuotaFromDecimalStrict(decimal.New(bonusMinor, -2).Mul(quotaPerUnit))
	if err1 != nil || err2 != nil || paid <= 0 || paid > common.MaxWalletQuota-bonus || model.ValidateTopUpQuotaCapacity(uid, paid+bonus) != nil {
		common.ApiErrorStatus(c, http.StatusConflict, errUSDTIncompatible)
		return
	}
	id, err := newUSDTOrderID()
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errUSDTDisabled)
		return
	}
	now := time.Now().Unix()
	order := model.USDTTopUpOrder{ID: id, UserID: uid, RequestKey: req.RequestKey, CreatedAt: now, Network: req.Network, AmountMinor: minor, PrincipalQuota: paid, BonusQuota: bonus, BonusMinor: bonusMinor, Status: model.USDTCreating, GatewayOrigin: client.Origin, GatewayMerchant: client.Merchant}
	created, err := model.InsertUSDTTopUpOrder(ctx, &order, cfg)
	if !created || err != nil {
		prior, findErr := model.FindUSDTTopUpRequest(ctx, uid, req.RequestKey)
		if errors.Is(err, model.ErrUSDTConflict) && errors.Is(findErr, gorm.ErrRecordNotFound) {
			// The config changed inside the admission transaction, before INSERT.
			replyUSDTTermsRejected(c, req.RequestKey, req.Network, req.Amount)
			return
		}
		if findErr != nil {
			common.ApiErrorStatus(c, http.StatusServiceUnavailable, errUSDTDisabled)
			return
		}
		replyUSDTExisting(c, prior, req.Network, minor)
		return
	}
	// Ambiguous provider outcomes cannot be automatically re-created.
	createCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 14*time.Second)
	defer cancel()
	snap, err := client.Create(createCtx, order.ID, network, amount)
	price, priceErr := gmpay.ParseAmount(snap.ActualAmount.String(), precision)
	exactAmount, amountErr := gmpay.ParseAmount(snap.Amount.String(), 2)
	address := gmpay.Address(network, snap.Address)
	checkout := client.Checkout(snap.TradeID, snap.CheckoutURL)
	if err == nil && (priceErr != nil || amountErr != nil || !amount.Equal(exactAmount) || snap.Status != 1 ||
		snap.OrderID != order.ID || !strings.EqualFold(snap.Currency, "cny") || !strings.EqualFold(snap.Token, "usdt") ||
		address == "" || checkout == "" || snap.Expires <= time.Now().Unix() || snap.Expires > now+3600) {
		err = gmpay.ErrProtocol
	}
	if err == nil {
		order.GatewayTradeID = &snap.TradeID
		order.Address, order.CheckoutURL, order.USDTAmount, order.ExpiresAt = address, checkout, price.String(), snap.Expires
		details, detailErr := client.Detail(createCtx, snap.TradeID)
		if detailErr != nil || !usdtMatchesDetail(order, details) {
			err = gmpay.ErrProtocol
		} else {
			err = model.SaveUSDTTopUpQuote(createCtx, order)
		}
	}
	if err != nil {
		state, reason := model.USDTReview, "create_uncertain"
		if errors.Is(err, gmpay.ErrRejected) {
			state, reason = model.USDTFailed, "gateway_rejected"
		}
		markCtx, cancelMark := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancelMark()
		_ = model.SetUSDTTopUpState(markCtx, order.ID, state, reason, model.USDTCreating)
		order = model.USDTTopUpOrder{ID: order.ID, Network: order.Network, AmountMinor: order.AmountMinor, BonusMinor: order.BonusMinor, Status: state, CreatedAt: order.CreatedAt}
	} else {
		order.Status = model.USDTPending
	}
	common.ApiSuccess(c, usdtOrderReply(order))
}
func usdtMatchesDetail(order model.USDTTopUpOrder, d gmpay.Snapshot) bool {
	network, precision := providerNetwork(order.Network)
	amount, e1 := gmpay.ParseAmount(d.Amount.String(), 2)
	actual, e2 := gmpay.ParseAmount(d.ActualAmount.String(), precision)
	quote, e3 := gmpay.ParseAmount(order.USDTAmount, precision)
	// Checkout details use milliseconds; GMPay create uses seconds.
	return e1 == nil && e2 == nil && e3 == nil && order.GatewayTradeID != nil && *order.GatewayTradeID == d.TradeID &&
		(d.OrderID == "" || d.OrderID == order.ID) && d.Network == network &&
		strings.EqualFold(d.Currency, "cny") && strings.EqualFold(d.Token, "usdt") && strings.EqualFold(d.PaymentType, "gmpay") &&
		amount.Equal(decimal.New(order.AmountMinor, -2)) && actual.Equal(quote) &&
		gmpay.Address(network, d.Address) == order.Address && d.Expires > 0 && d.Expires/1000 == order.ExpiresAt
}

// This describes only this invocation. A proxy error, insertion failure or
// conflicting pre-existing request must never advertise a pre-insert refusal.
func replyUSDTTermsRejected(c *gin.Context, key, network, amount string) {
	common.ApiErrorStatus(c, http.StatusBadRequest, errUSDTIncompatible, gin.H{
		"code": "usdt_terms_rejected_before_insert", "idempotency_key": key, "network": network, "amount": amount,
	})
}

func GetUSDTOrderRequest(c *gin.Context) {
	uid, key := c.GetInt("id"), c.Param("key")
	if uid <= 0 {
		c.Status(http.StatusUnauthorized)
		return
	}
	if !usdtRequestID.MatchString(key) {
		c.Status(http.StatusNotFound)
		return
	}
	order, err := model.FindUSDTTopUpRequest(c.Request.Context(), uid, key)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// Not visible yet is not evidence that a timed-out POST never arrived.
		c.Status(http.StatusNotFound)
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errUSDTDisabled)
		return
	}
	common.ApiSuccess(c, gin.H{"idempotency_key": key, "order": usdtOrderReply(order)})
}

func replyUSDTExisting(c *gin.Context, o model.USDTTopUpOrder, network string, amount int64) {
	if o.Network != network || o.AmountMinor != amount {
		common.ApiErrorStatus(c, http.StatusConflict, model.ErrUSDTConflict)
		return
	}
	common.ApiSuccess(c, usdtOrderReply(o))
}
func usdtOrderReply(o model.USDTTopUpOrder) gin.H {
	state := o.DisplayStatus(time.Now().Unix())
	expiry := ""
	if o.ExpiresAt > 0 {
		expiry = time.Unix(o.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	txID := ""
	if o.ChainTransaction != nil {
		_, txID, _ = strings.Cut(*o.ChainTransaction, ":")
	}
	addr := ""
	if state == model.USDTPending || state == model.USDTConfirming {
		addr = o.Address
	}
	return gin.H{
		"id": o.ID, "network": o.Network, "amount": decimal.New(o.AmountMinor, -2).StringFixed(2),
		"usdt_amount": o.USDTAmount, "wallet_credit": decimal.New(o.AmountMinor, -2).StringFixed(2), "bonus_credit": decimal.New(o.BonusMinor, -2).StringFixed(2),
		"currency": "CNY", "address": addr, "status": state, "expires_at": expiry, "tx_id": txID,
	}
}
func ListUSDTOrders(c *gin.Context) {
	uid := c.GetInt("id")
	if uid <= 0 {
		c.Status(http.StatusUnauthorized)
		return
	}
	orders, err := model.ListUSDTTopUpOrders(c.Request.Context(), uid)
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errUSDTDisabled)
		return
	}
	reply := make([]gin.H, 0, len(orders))
	for _, o := range orders {
		reply = append(reply, usdtOrderReply(o))
	}
	common.ApiSuccess(c, reply)
}
func GetUSDTOrder(c *gin.Context) {
	uid, id := c.GetInt("id"), c.Param("id")
	if uid <= 0 {
		c.Status(http.StatusUnauthorized)
		return
	}
	if !usdtOrderID.MatchString(id) {
		c.Status(http.StatusNotFound)
		return
	}
	ctx := c.Request.Context()
	order, err := model.FindUSDTTopUpOrder(ctx, id, uid)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(http.StatusNotFound)
		return
	}
	if err != nil {
		common.ApiErrorStatus(c, http.StatusServiceUnavailable, errUSDTDisabled)
		return
	}
	now := time.Now().Unix()
	if (order.Status == model.USDTPending || order.Status == model.USDTConfirming) && order.GatewayTradeID != nil && now <= order.ExpiresAt {
		client, err := loadUSDTPayClient()
		if err == nil && client.Origin == order.GatewayOrigin && client.Merchant == order.GatewayMerchant {
			claimed, _ := model.ClaimUSDTTopUpPoll(ctx, order, now)
			if claimed {
				status, err := client.Status(ctx, *order.GatewayTradeID)
				if err == nil {
					var state, reason string
					switch status {
					case 3:
						state = model.USDTExpired
					case 2, 5:
						state = model.USDTConfirming
					case 6:
						state, reason = model.USDTReview, "gateway_review"
					}
					// Polling never credits: provider success can still be awaiting the webhook.
					if state != "" {
						_ = model.SetUSDTTopUpState(ctx, id, state, reason, model.USDTPending, model.USDTConfirming)
					}
				}
				if latest, err := model.FindUSDTTopUpOrder(ctx, id, uid); err == nil {
					order = latest
				}
			}
		}
	}
	common.ApiSuccess(c, usdtOrderReply(order))
}
func USDTNotify(c *gin.Context) {
	receivedAt := time.Now().Unix()
	client, err := loadUSDTPayClient()
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	payment, err := client.VerifyCallback(c.Request.Body)
	if err != nil || !usdtOrderID.MatchString(payment.OrderID) {
		c.Status(http.StatusBadRequest)
		return
	}
	ctx := c.Request.Context()
	order, err := model.FindUSDTTopUpOrder(ctx, payment.OrderID, 0)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.Status(http.StatusNotFound)
		return
	}
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if order.Status == model.USDTCreating {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	network, precision := providerNetwork(order.Network)
	quote, err := gmpay.ParseAmount(order.USDTAmount, precision)
	markReview := func(reason string) {
		_ = model.SetUSDTTopUpState(ctx, order.ID, model.USDTReview, reason, model.USDTPending, model.USDTConfirming, model.USDTExpired)
		c.Status(http.StatusConflict)
	}
	if err != nil || order.GatewayTradeID == nil || *order.GatewayTradeID != payment.TradeID ||
		order.GatewayOrigin != client.Origin || order.GatewayMerchant != payment.Merchant ||
		(payment.Currency != "" && !strings.EqualFold(payment.Currency, "cny")) || payment.Network != network ||
		gmpay.Address(network, payment.Address) != order.Address ||
		!payment.Amount.Equal(decimal.New(order.AmountMinor, -2)) || !payment.ActualAmount.Equal(quote) {
		markReview("callback_mismatch")
		return
	}
	if order.Status == model.USDTPaid {
		if order.ChainTransaction != nil && *order.ChainTransaction == order.Network+":"+payment.TxID {
			c.String(http.StatusOK, "ok")
			return
		}
		c.Status(http.StatusConflict)
		return
	}
	if (order.Status != model.USDTPending && order.Status != model.USDTConfirming) || receivedAt > order.ExpiresAt {
		markReview("late_or_held")
		return
	}
	snap, err := client.Detail(ctx, payment.TradeID)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if !usdtMatchesDetail(order, snap) {
		markReview("gateway_mismatch")
		return
	}
	status, err := client.Status(ctx, payment.TradeID)
	if err != nil || status == 1 || status == 5 {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if status != 2 {
		markReview("gateway_unsettled")
		return
	}
	if err := model.SettleUSDTTopUp(ctx, order, payment.TxID, receivedAt); err != nil {
		if errors.Is(err, model.ErrUSDTConflict) || errors.Is(err, model.ErrUSDTReview) || errors.Is(err, model.ErrWalletQuotaLimitExceeded) {
			markReview("settlement_review")
			return
		}
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.String(http.StatusOK, "ok")
}
