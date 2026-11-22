package common

import "strings"

// IsGatewayCredentialHeader identifies inbound headers that must not cross the
// relay's client-to-upstream boundary. Adaptors set their own upstream auth.
func IsGatewayCredentialHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "x-goog-api-key",
		"mj-api-secret", "sec-websocket-protocol":
		return true
	default:
		return false
	}
}
