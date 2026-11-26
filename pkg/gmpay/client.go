// Package gmpay implements the dedicated EPUSDT v2.1.5 merchant rail.
package gmpay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const maxPayload = 16 << 10

var (
	ErrUnavailable = errors.New("GMPay response unavailable")
	ErrProtocol    = errors.New("GMPay protocol mismatch")
	ErrRejected    = errors.New("GMPay rejected order creation")
	identifier     = regexp.MustCompile(`^[0-9a-zA-Z_-]{1,80}$`)
	amountPattern  = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,18})?$`)
	evmAddress     = regexp.MustCompile(`^0[xX][0-9A-Fa-f]{40}$`)
	hashPattern    = regexp.MustCompile(`^(?i:(0x)?[0-9a-f]{64})$`)
	lowerSignature = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Client struct {
	Origin, Merchant, CallbackOrigin string
	secret                           string
}

func HTTPSOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || strings.Contains(raw, "#") || len(u.Host) > 247 {
		return ""
	}
	return "https://" + u.Host
}
func New(origin, merchant, secret, callbackOrigin string) (*Client, error) {
	origin, callbackOrigin = HTTPSOrigin(origin), HTTPSOrigin(callbackOrigin)
	if origin == "" || callbackOrigin == "" || !identifier.MatchString(merchant) || strings.TrimSpace(secret) == "" || len(secret) > 512 {
		return nil, ErrProtocol
	}
	return &Client{Origin: origin, Merchant: merchant, secret: secret, CallbackOrigin: callbackOrigin}, nil
}
func sign(fields map[string]string, key string) []byte {
	keys := make([]string, 0, len(fields))
	for k, v := range fields {
		if k != "signature" && v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	mac := hmac.New(sha256.New, []byte(key))
	for i, k := range keys {
		if i > 0 {
			_, _ = io.WriteString(mac, "&")
		}
		_, _ = io.WriteString(mac, k+"="+fields[k])
	}
	return mac.Sum(nil)
}

type Snapshot struct {
	TradeID      string      `json:"trade_id"`
	OrderID      string      `json:"order_id"`
	Amount       json.Number `json:"amount"`
	ActualAmount json.Number `json:"actual_amount"`
	Currency     string      `json:"currency"`
	Token        string      `json:"token"`
	Network      string      `json:"network"`
	Address      string      `json:"receive_address"`
	Status       int         `json:"status"`
	Expires      int64       `json:"expiration_time"`
	CheckoutURL  string      `json:"payment_url"`
	PaymentType  string      `json:"payment_type"`
}
type Payment struct {
	Merchant, TradeID, OrderID, Currency, Network, Address, TxID string
	Amount, ActualAmount                                         decimal.Decimal
}

func ParseAmount(s string, places int) (decimal.Decimal, error) {
	if !amountPattern.MatchString(s) {
		return decimal.Zero, ErrProtocol
	}
	d, err := decimal.NewFromString(s)
	if err != nil || !d.IsPositive() || !d.Equal(d.Truncate(int32(places))) {
		return decimal.Zero, ErrProtocol
	}
	return d, nil
}
func Address(network, raw string) string {
	if network == "binance" || network == "polygon" {
		if !evmAddress.MatchString(raw) || strings.Trim(raw[2:], "0") == "" {
			return ""
		}
		return "0x" + strings.ToLower(raw[2:])
	}
	if network != "tron" || len(raw) != 34 || raw[0] != 'T' {
		return ""
	}
	const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	var n big.Int
	base := big.NewInt(58)
	for _, ch := range raw {
		v := strings.IndexRune(alphabet, ch)
		if v < 0 {
			return ""
		}
		n.Mul(&n, base)
		n.Add(&n, big.NewInt(int64(v)))
	}
	b := n.Bytes()
	if len(b) != 25 || b[0] != 0x41 {
		return ""
	}
	h1 := sha256.Sum256(b[:21])
	h2 := sha256.Sum256(h1[:])
	if !bytes.Equal(b[21:], h2[:4]) {
		return ""
	}
	return raw
}
func (c *Client) VerifyCallback(r io.Reader) (Payment, error) {
	// Reject duplicate names and values that make the canonical form ambiguous.
	d := json.NewDecoder(io.LimitReader(r, maxPayload+1))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return Payment{}, ErrProtocol
	}
	fields := map[string]string{}
	for d.More() {
		t, err := d.Token()
		key, ok := t.(string)
		if err != nil || !ok || !identifier.MatchString(key) {
			return Payment{}, ErrProtocol
		}
		if _, exists := fields[key]; exists {
			return Payment{}, ErrProtocol
		}
		var raw any
		if err := d.Decode(&raw); err != nil {
			return Payment{}, ErrProtocol
		}
		switch v := raw.(type) {
		case nil:
			fields[key] = ""
		case string:
			if len(v) > 512 || strings.ContainsAny(v, "&=\r\n") {
				return Payment{}, ErrProtocol
			}
			fields[key] = v
		case json.Number:
			n, err := strconv.ParseFloat(v.String(), 64)
			if err != nil {
				return Payment{}, ErrProtocol
			}
			fields[key] = strconv.FormatFloat(n, 'f', -1, 64)
		default:
			return Payment{}, ErrProtocol
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') || d.Decode(new(any)) != io.EOF {
		return Payment{}, ErrProtocol
	}
	sig := fields["signature"]
	if !lowerSignature.MatchString(sig) {
		return Payment{}, ErrProtocol
	}
	decoded, _ := hex.DecodeString(sig)
	if !hmac.Equal(decoded, sign(fields, c.secret)) {
		return Payment{}, ErrProtocol
	}
	amount, e1 := ParseAmount(fields["amount"], 2)
	actual, e2 := ParseAmount(fields["actual_amount"], 18)
	h := fields["block_transaction_id"]
	if e1 != nil || e2 != nil || fields["pid"] != c.Merchant || fields["status"] != "2" || !identifier.MatchString(fields["trade_id"]) || !identifier.MatchString(fields["order_id"]) || !strings.EqualFold(fields["token"], "usdt") || fields["network"] == "" || !hashPattern.MatchString(h) {
		return Payment{}, ErrProtocol
	}
	h = strings.TrimPrefix(strings.ToLower(h), "0x")
	if strings.Trim(h, "0") == "" {
		return Payment{}, ErrProtocol
	}
	return Payment{Merchant: fields["pid"], TradeID: fields["trade_id"], OrderID: fields["order_id"], Currency: fields["currency"], Network: fields["network"], Address: fields["receive_address"], TxID: h, Amount: amount, ActualAmount: actual}, nil
}
func (c *Client) Create(ctx context.Context, orderID, network string, amount decimal.Decimal) (Snapshot, error) {
	fields := map[string]string{
		"pid": c.Merchant, "order_id": orderID, "currency": "cny", "token": "usdt", "network": network, "amount": amount.String(),
		"notify_url": c.CallbackOrigin + "/api/user/usdt/notify", "redirect_url": c.CallbackOrigin + "/wallet",
		"name": "API wallet recharge", "callback_version": "2",
	}
	wire := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		wire[k] = v
	}
	wire["amount"] = json.Number(amount.String())
	wire["callback_version"] = 2
	wire["signature"] = hex.EncodeToString(sign(fields, c.secret))
	var snap Snapshot
	err := c.request(ctx, http.MethodPost, "/payments/gmpay/v1/order/create-transaction", wire, &snap)
	return snap, err
}
func (c *Client) Detail(ctx context.Context, id string) (Snapshot, error) {
	if !identifier.MatchString(id) {
		return Snapshot{}, ErrProtocol
	}
	var snap Snapshot
	err := c.request(ctx, http.MethodGet, "/pay/checkout-counter-resp/"+id, nil, &snap)
	return snap, err
}
func (c *Client) Status(ctx context.Context, id string) (int, error) {
	if !identifier.MatchString(id) {
		return 0, ErrProtocol
	}
	var resp struct {
		TradeID string `json:"trade_id"`
		Status  int    `json:"status"`
	}
	if err := c.request(ctx, http.MethodGet, "/pay/check-status/"+id, nil, &resp); err != nil {
		return 0, err
	}
	if resp.TradeID != id || resp.Status < 1 || resp.Status > 6 {
		return 0, ErrProtocol
	}
	return resp.Status, nil
}
func (c *Client) Checkout(id, got string) string {
	if !identifier.MatchString(id) {
		return ""
	}
	allowed := c.Origin + "/pay/checkout-counter/" + id
	if got != allowed {
		return ""
	}
	return allowed
}

var noRedirectClient = &http.Client{
	Timeout:       6 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (c *Client) request(ctx context.Context, method, path string, body any, result any) error {
	var data io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return ErrProtocol
		}
		data = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Origin+path, data)
	if err != nil {
		return ErrProtocol
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxPayload+1))
	if err != nil || len(b) > maxPayload {
		return ErrUnavailable
	}
	var envelope struct {
		Status int             `json:"status_code"`
		Data   json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil {
		return ErrUnavailable
	}
	if method == http.MethodPost && resp.StatusCode == http.StatusBadRequest {
		// Duplicate and generic errors may have created an order; do not retry.
		switch envelope.Status {
		case 10003, 10004, 10005, 10006, 10009, 10014, 10016, 10041, 10044:
			return ErrRejected
		}
	}
	if resp.StatusCode != http.StatusOK || envelope.Status != 200 || len(envelope.Data) == 0 || json.Unmarshal(envelope.Data, result) != nil {
		return ErrUnavailable
	}
	return nil
}
