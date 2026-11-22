package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newUserBenefitZpayQueryFixture() (ZpayBenefitOrderExpectation, map[string]any) {
	return ZpayBenefitOrderExpectation{
		OutTradeNo: "BENEFIT001", TradeNo: "20261108123456789", PaymentMethod: "alipay", PaidCents: 10,
	}, map[string]any{
		"code": 1, "status": 1, "pid": "201901151314084206659771", "out_trade_no": "BENEFIT001",
		"trade_no": "20261108123456789", "type": "alipay", "money": "0.10", "buyer": "2088102130896433",
	}
}

func TestNewUserBenefitZpayQueryBindsPaidOrderAndPayer(t *testing.T) {
	for _, test := range []struct {
		name   string
		field  string
		value  any
		reason string
	}{
		{name: "matching order"},
		{name: "exact numeric merchant ID", field: "pid", value: common.RawMessage("201901151314084206659771")},
		{name: "string status", field: "status", value: "1"},
		{name: "equivalent decimal money", field: "money", value: "0.1"},
		{name: "query rejected", field: "code", value: -1, reason: ZpayBenefitQueryUnavailable},
		{name: "query error text", field: "code", value: "error", reason: ZpayBenefitQueryUnavailable},
		{name: "unpaid", field: "status", value: 0, reason: ZpayBenefitOrderMismatch},
		{name: "merchant mismatch", field: "pid", value: "201901151314084206659772", reason: ZpayBenefitOrderMismatch},
		{name: "missing merchant", field: "pid", value: nil, reason: ZpayBenefitOrderMismatch},
		{name: "local order mismatch", field: "out_trade_no", value: "OTHER", reason: ZpayBenefitOrderMismatch},
		{name: "provider order mismatch", field: "trade_no", value: "OTHER", reason: ZpayBenefitOrderMismatch},
		{name: "payment method mismatch", field: "type", value: "wxpay", reason: ZpayBenefitOrderMismatch},
		{name: "amount mismatch", field: "money", value: "0.01", reason: ZpayBenefitOrderMismatch},
		{name: "negative amount", field: "money", value: "-0.10", reason: ZpayBenefitOrderMismatch},
		{name: "scientific amount", field: "money", value: "1e-1", reason: ZpayBenefitOrderMismatch},
		{name: "fractional cent", field: "money", value: "0.101", reason: ZpayBenefitOrderMismatch},
		{name: "amount overflow", field: "money", value: "9999999999999999999999999999999.00", reason: ZpayBenefitOrderMismatch},
		{name: "empty payer", field: "buyer", value: "", reason: ZpayBenefitIdentityUnavailable},
		{name: "null payer", field: "buyer", value: nil, reason: ZpayBenefitIdentityUnavailable},
		{name: "masked payer", field: "buyer", value: "20881021****6433", reason: ZpayBenefitIdentityUnavailable},
		{name: "payer login account", field: "buyer", value: "payer@example.com", reason: ZpayBenefitIdentityUnavailable},
		{name: "openid without namespace", field: "buyer", value: "074a1cctg1lelxke4xqc0zgndid0nxi95", reason: ZpayBenefitIdentityUnavailable},
		{name: "payer wrong prefix", field: "buyer", value: "2089102130896433", reason: ZpayBenefitIdentityUnavailable},
		{name: "payer non digit", field: "buyer", value: "208810213089643x", reason: ZpayBenefitIdentityUnavailable},
		{name: "payer whitespace", field: "buyer", value: "2088102130896433 ", reason: ZpayBenefitIdentityUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			expected, result := newUserBenefitZpayQueryFixture()
			if test.field != "" {
				result[test.field] = test.value
			}
			body, err := common.Marshal(result)
			require.NoError(t, err)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/gateway/api.php", r.URL.Path)
				assert.Equal(t, "order", r.URL.Query().Get("act"))
				assert.Equal(t, "201901151314084206659771", r.URL.Query().Get("pid"))
				assert.Equal(t, "fixture key & value", r.URL.Query().Get("key"))
				assert.Equal(t, expected.OutTradeNo, r.URL.Query().Get("out_trade_no"))
				assert.Len(t, r.URL.Query(), 4)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			buyer, reason := queryNewUserBenefitZpayOrder(context.Background(), server.Client(), server.URL+"/gateway/", "201901151314084206659771", "fixture key & value", expected)
			assert.Equal(t, test.reason, reason)
			if test.reason == "" {
				assert.Equal(t, "2088102130896433", buyer)
			} else {
				assert.Empty(t, buyer)
			}
		})
	}
}

func TestNewUserBenefitZpayQueryRejectsUnsafeTransportAndMalformedResponses(t *testing.T) {
	expected, result := newUserBenefitZpayQueryFixture()
	validBody, err := common.Marshal(result)
	require.NoError(t, err)
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "upstream error", status: http.StatusBadGateway, body: "secret from upstream"},
		{name: "invalid json", status: http.StatusOK, body: "invalid"},
		{name: "trailing content", status: http.StatusOK, body: string(validBody) + "{}"},
		{name: "oversized response", status: http.StatusOK, body: strings.Repeat(" ", (64<<10)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			buyer, reason := queryNewUserBenefitZpayOrder(context.Background(), server.Client(), server.URL, "201901151314084206659771", "fixture-secret", expected)
			assert.Empty(t, buyer)
			assert.Equal(t, ZpayBenefitQueryUnavailable, reason)
		})
	}
	t.Run("untrusted TLS certificate", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("an untrusted TLS connection must not send credentials")
		}))
		defer server.Close()
		buyer, reason := queryNewUserBenefitZpayOrder(context.Background(), &http.Client{}, server.URL, "201901151314084206659771", "fixture-secret", expected)
		assert.Empty(t, buyer)
		assert.Equal(t, ZpayBenefitQueryUnavailable, reason)
	})
	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		buyer, reason := queryNewUserBenefitZpayOrder(ctx, &http.Client{}, "https://gateway.invalid", "201901151314084206659771", "fixture-secret", expected)
		assert.Empty(t, buyer)
		assert.Equal(t, ZpayBenefitQueryUnavailable, reason)
	})
}

func TestNewUserBenefitZpayQueryNeverFollowsRedirects(t *testing.T) {
	expected, _ := newUserBenefitZpayQueryFixture()
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a redirect must not forward the credential-bearing query")
	}))
	defer target.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"?"+r.URL.RawQuery, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	buyer, reason := queryNewUserBenefitZpayOrder(context.Background(), origin.Client(), origin.URL, "201901151314084206659771", "fixture-secret", expected)
	assert.Empty(t, buyer)
	assert.Equal(t, ZpayBenefitQueryUnavailable, reason)
}

func TestNewUserBenefitZpayQueryRejectsUnsupportedConfigurationBeforeRequest(t *testing.T) {
	expected, _ := newUserBenefitZpayQueryFixture()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid query configuration must not make a network request")
	}))
	defer server.Close()
	for _, gateway := range []string{
		strings.Replace(server.URL, "https:", "http:", 1),
		strings.Replace(server.URL, "https://", "https://user:password@", 1),
		server.URL + "?existing=value", server.URL + "?", server.URL + "#fragment", server.URL + "#",
		" " + server.URL, "https:///no-host",
	} {
		t.Run(gateway, func(t *testing.T) {
			buyer, reason := queryNewUserBenefitZpayOrder(context.Background(), server.Client(), gateway, "201901151314084206659771", "fixture-secret", expected)
			assert.Empty(t, buyer)
			assert.Equal(t, ZpayBenefitQueryUnavailable, reason)
		})
	}
	expected.PaymentMethod = "wxpay"
	buyer, reason := queryNewUserBenefitZpayOrder(context.Background(), server.Client(), server.URL, "201901151314084206659771", "fixture-secret", expected)
	assert.Empty(t, buyer)
	assert.Equal(t, ZpayBenefitIdentityUnavailable, reason)
}
