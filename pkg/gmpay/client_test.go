package gmpay

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestUSDTGMPayOfficialSignatureAndValidation(t *testing.T) {
	f := map[string]string{
		"pid": "1000", "order_id": "ORD202605230001", "currency": "cny", "token": "usdt", "network": "tron", "amount": "100",
		"name": "VIP", "notify_url": "https://merchant.example/notify", "redirect_url": "https://merchant.example/return",
	}
	if got := hex.EncodeToString(sign(f, "epusdt_secret_key")); got != "6f874b1919d95081835e2809b620e354a5866f5a6dbb2e432d1627f1eb10059d" {
		t.Fatal("official protocol vector mismatch")
	}
	for _, raw := range []string{"https://user@epusdt.example", "https://epusdt.example/pay", "https://epusdt.example?", "http://epusdt.example"} {
		if got := HTTPSOrigin(raw); got != "" {
			t.Fatalf("accepted unpinned origin %q", got)
		}
	}
	c, err := New("https://epusdt.example", "1000", "test", "https://merchant.example")
	if err != nil {
		t.Fatal(err)
	}
	if c.Checkout("abc", "https://foreign.example/pay/checkout-counter/abc") != "" {
		t.Fatal("accepted foreign checkout URL")
	}
	if got := Address("tron", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"); got == "" {
		t.Fatal("failed to parse checksum-valid TRON address")
	}
	if got := Address("tron", "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6a"); got != "" {
		t.Fatal("accepted broken TRON checksum")
	}
	if _, err := c.VerifyCallback(strings.NewReader(`{"pid":"1000","pid":"1000"}`)); err == nil {
		t.Fatal("duplicate keys accepted")
	}
	if _, err := c.VerifyCallback(strings.NewReader(`{"pid":"1000&status=2"}`)); err == nil {
		t.Fatal("non-canonical scalar accepted")
	}
}
