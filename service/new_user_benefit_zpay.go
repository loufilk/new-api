package service

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	ZpayBenefitQueryUnavailable    = "query_unavailable"
	ZpayBenefitOrderMismatch       = "order_mismatch"
	ZpayBenefitIdentityUnavailable = "identity_unavailable"
)

// ZpayBenefitOrderExpectation binds the query to a locally persisted order and
// its already verified notification. PaidCents is the actual CNY payment.
type ZpayBenefitOrderExpectation struct {
	OutTradeNo    string
	TradeNo       string
	PaymentMethod string
	PaidCents     int64
}

// QueryNewUserBenefitZpayOrder accepts only a complete legacy Alipay UID from a
// matching, paid server-side query. ZPAY does not guarantee that buyer contains
// this identifier. The caller must fingerprint a successful UID immediately;
// neither the UID nor gateway credentials belong in logs or public responses.
func QueryNewUserBenefitZpayOrder(ctx context.Context, gatewayURL, partnerID, key string, expected ZpayBenefitOrderExpectation) (buyer string, reason string) {
	return queryNewUserBenefitZpayOrder(ctx, GetHttpClient(), gatewayURL, partnerID, key, expected)
}

func queryNewUserBenefitZpayOrder(ctx context.Context, client *http.Client, gatewayURL, partnerID, key string, expected ZpayBenefitOrderExpectation) (buyer string, reason string) {
	if expected.PaymentMethod != "alipay" {
		return "", ZpayBenefitIdentityUnavailable
	}
	if client == nil || partnerID == "" || partnerID != strings.TrimSpace(partnerID) || key == "" ||
		expected.OutTradeNo == "" || expected.TradeNo == "" || expected.PaidCents <= 0 {
		return "", ZpayBenefitQueryUnavailable
	}
	endpoint, err := url.Parse(gatewayURL)
	if err != nil || gatewayURL != strings.TrimSpace(gatewayURL) || endpoint == nil ||
		endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.Hostname() == "" ||
		endpoint.Opaque != "" || endpoint.User != nil || endpoint.RawQuery != "" ||
		endpoint.ForceQuery || endpoint.Fragment != "" || strings.Contains(gatewayURL, "#") {
		return "", ZpayBenefitQueryUnavailable
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/api.php"
	endpoint.RawPath = ""
	endpoint.RawQuery = url.Values{
		"act": {"order"}, "pid": {partnerID}, "key": {key}, "out_trade_no": {expected.OutTradeNo},
	}.Encode()
	// ZPAY expects notification acknowledgement within five seconds. Leave time
	// for the caller's database settlement after the auxiliary query.
	queryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(queryCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", ZpayBenefitQueryUnavailable
	}
	request.Header.Set("Accept", "application/json")
	response, err := DoRequestWithoutRedirects(client, request)
	if err != nil {
		// Transport errors can contain the full credential-bearing URL.
		return "", ZpayBenefitQueryUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", ZpayBenefitQueryUnavailable
	}
	const maxResponseBytes = 64 << 10
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return "", ZpayBenefitQueryUnavailable
	}
	var result struct {
		Code       common.RawMessage `json:"code"`
		Status     common.RawMessage `json:"status"`
		PID        common.RawMessage `json:"pid"`
		OutTradeNo string            `json:"out_trade_no"`
		TradeNo    string            `json:"trade_no"`
		Type       string            `json:"type"`
		Money      string            `json:"money"`
		Buyer      string            `json:"buyer"`
	}
	if err := common.Unmarshal(data, &result); err != nil || common.JsonRawMessageToString(result.Code) != "1" {
		return "", ZpayBenefitQueryUnavailable
	}
	// The protocol documents pid as an integer, although some compatible
	// gateways return a string. RawMessage preserves every digit in both forms.
	paidCents, amountErr := model.ParseNewUserBenefitCNY(result.Money)
	if common.JsonRawMessageToString(result.Status) != "1" ||
		common.JsonRawMessageToString(result.PID) != partnerID ||
		result.OutTradeNo != expected.OutTradeNo || result.TradeNo != expected.TradeNo ||
		result.Type != expected.PaymentMethod || amountErr != nil || paidCents != expected.PaidCents {
		return "", ZpayBenefitOrderMismatch
	}
	// Alipay documents its legacy account UID as 16 ASCII digits starting 2088.
	// OpenIDs need an application namespace that ZPAY's query does not provide.
	if len(result.Buyer) != 16 || !strings.HasPrefix(result.Buyer, "2088") {
		return "", ZpayBenefitIdentityUnavailable
	}
	for _, digit := range result.Buyer {
		if digit < '0' || digit > '9' {
			return "", ZpayBenefitIdentityUnavailable
		}
	}
	return result.Buyer, ""
}
