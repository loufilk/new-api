package common

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

type privateKeyDiagnosticLogContextKey struct{}

// Only the server-owned Key Diagnostics worker sets this marker. A URL,
// header, or external caller cannot disable ordinary request diagnostics.
func WithPrivateKeyDiagnosticLog(ctx context.Context) context.Context {
	return context.WithValue(ctx, privateKeyDiagnosticLogContextKey{}, true)
}

func IsPrivateKeyDiagnosticLog(ctx context.Context) bool {
	if c, ok := ctx.(*gin.Context); ok {
		if c == nil || c.Request == nil {
			return false
		}
		ctx = c.Request.Context()
	}
	return ctx != nil && ctx.Value(privateKeyDiagnosticLogContextKey{}) == true
}

// No secret is stored on the execution record or context marker. Read known
// credentials only from the ordinary in-process TokenAuth/channel state.
func RedactPrivateKeyDiagnosticSecrets(c *gin.Context, value string) string {
	if !IsPrivateKeyDiagnosticLog(c) || value == "" {
		return value
	}
	channelKey := GetContextKeyString(c, constant.ContextKeyChannelKey)
	secrets := []string{c.GetString("token_key"), channelKey}
	if GetContextKeyInt(c, constant.ContextKeyChannelType) == constant.ChannelTypeAws {
		parts := strings.Split(channelKey, "|")
		if len(parts) == 2 || len(parts) == 3 {
			secrets = append(secrets, parts[:len(parts)-1]...)
		}
	}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		value = strings.ReplaceAll(value, secret, "[redacted]")
		encoded, _ := json.Marshal(secret)
		if len(encoded) > 2 {
			value = strings.ReplaceAll(value, string(encoded[1:len(encoded)-1]), "[redacted]")
		}
	}
	return value
}
