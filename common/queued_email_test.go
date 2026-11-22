package common

import (
	"net/smtp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueuedSMTPConfigurationRequiresSecureRemoteAndValidEnvelope(t *testing.T) {
	base := SMTPDeliveryConfig{Server: "smtp.example.invalid", Port: 587, From: "notify@example.invalid", StartTLS: true}
	require.True(t, base.Valid())
	tests := []struct {
		name string
		edit func(*SMTPDeliveryConfig)
	}{
		{"no TLS", func(c *SMTPDeliveryConfig) { c.StartTLS = false }},
		{"insecure certificate", func(c *SMTPDeliveryConfig) { c.InsecureSkipVerify = true }},
		{"invalid port", func(c *SMTPDeliveryConfig) { c.Port = 0 }},
		{"missing token", func(c *SMTPDeliveryConfig) { c.Account = "account" }},
		{"missing account", func(c *SMTPDeliveryConfig) { c.Token = "synthetic" }},
		{"sender injection", func(c *SMTPDeliveryConfig) { c.From = "notify@example.invalid\r\nBcc:evil@example.invalid" }},
		{"multiple senders", func(c *SMTPDeliveryConfig) { c.From = "notify@example.invalid;evil@example.invalid" }},
		{"server injection", func(c *SMTPDeliveryConfig) { c.Server = "smtp.example.invalid\r\n" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { copy := base; test.edit(&copy); assert.False(t, copy.Valid()) })
	}
	loopback := base
	loopback.Server = "127.0.0.1"
	loopback.StartTLS = false
	assert.True(t, loopback.Valid())
	implicit := base
	implicit.StartTLS = false
	implicit.Port = 465
	assert.True(t, implicit.Valid())
}

func TestQueuedEmailStableMessageIDAndSingleRecipientHeaders(t *testing.T) {
	config := SMTPDeliveryConfig{From: "notify@example.invalid"}
	first, err := buildQueuedEmail(config, "Notice\r\nBcc:wrong", "one@example.invalid", "safe text", "<p>safe</p>", "<stable@notification.invalid>")
	require.NoError(t, err)
	second, err := buildQueuedEmail(config, "Notice", "one@example.invalid", "safe text", "<p>safe</p>", "<stable@notification.invalid>")
	require.NoError(t, err)
	for _, data := range [][]byte{first, second} {
		assert.Contains(t, string(data), "Message-ID: <stable@notification.invalid>\r\n")
		assert.NotContains(t, string(data), "\r\nBcc:")
		assert.NotContains(t, string(data), "Cc:")
	}
	for _, recipient := range []string{"one@example.invalid;two@example.invalid", "one@example.invalid\r\nBcc:two@example.invalid", "Name <one@example.invalid>"} {
		_, err := buildQueuedEmail(config, "Notice", recipient, "safe", "safe", "<stable@notification.invalid>")
		assert.Error(t, err)
	}
	_, err = buildQueuedEmail(config, "Notice", "one@example.invalid", "safe", "safe", "<stable>\r\nBcc:evil")
	assert.Error(t, err)
}

func TestQueuedSMTPAuthSnapshotDoesNotUseMutableGlobalHostnameOrLoginMode(t *testing.T) {
	oldServer, oldForce := SMTPServer, SMTPForceAuthLogin
	t.Cleanup(func() { SMTPServer = oldServer; SMTPForceAuthLogin = oldForce })
	SMTPServer = "wrong-global.invalid"
	SMTPForceAuthLogin = true
	auth := &smtpAutoAuth{username: "user", password: "synthetic", configuration: &smtpAuthConfiguration{server: "snapshot.invalid"}}
	mechanism, response, err := auth.Start(&smtp.ServerInfo{Name: "snapshot.invalid", TLS: true, Auth: []string{"PLAIN"}})
	require.NoError(t, err)
	assert.Equal(t, "PLAIN", mechanism)
	assert.True(t, strings.HasSuffix(string(response), "synthetic"))
}
