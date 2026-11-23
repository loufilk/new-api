package common

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
)

var resendEmailClient = &http.Client{
	Timeout: 20 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ValidateResendConfiguration is shared by provider activation and delivery.
// It checks local configuration only; saving settings never sends an email.
func ValidateResendConfiguration(apiKey, from string) error {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n\x00") {
		return NewMessage("Resend API key is not configured or invalid")
	}
	if strings.ContainsAny(from, "\r\n\x00") {
		return NewMessage("Resend sender address is not configured or invalid")
	}
	if _, err := mail.ParseAddress(strings.TrimSpace(from)); err != nil {
		return NewMessage("Resend sender address is not configured or invalid")
	}
	return nil
}

// API contract: https://resend.com/docs/api-reference/emails/send-email
// OWASP Authentication / Session Management / Forgot Password cheat sheets:
// do not expose credentials, verification codes or reset links in errors/logs.
// This transport does not change token expiry, single-use or session rules.
func sendResendEmail(apiKey, from, subject, receiver, plainText, content string) error {
	return sendResendEmailContext(context.Background(), apiKey, from, subject, receiver, plainText, content, "")
}

// ResendHTTPError exposes only the status, never provider-controlled body text.
type ResendHTTPError struct{ Status int }

func (e *ResendHTTPError) Error() string {
	return fmt.Sprintf("Resend email delivery failed (HTTP %d)", e.Status)
}

// SendQueuedResend delivers one admitted notification under its original deadline.
// Keep the key stable across restarts, without sending a raw recipient or event ID.
func SendQueuedResend(ctx context.Context, apiKey, from, subject, receiver, plainText, content, eventID string) error {
	recipient, err := mail.ParseAddress(receiver)
	if err != nil || recipient.Name != "" || recipient.Address != receiver || strings.ContainsAny(receiver, "\r\n\x00;,") || eventID == "" {
		return NewMessage("Invalid email address")
	}
	key := fmt.Sprintf("support/%x", sha256.Sum256([]byte(eventID)))
	return sendResendEmailContext(ctx, apiKey, from, subject, receiver, plainText, content, key)
}

func sendResendEmailContext(ctx context.Context, apiKey, from, subject, receiver, plainText, content, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateResendConfiguration(apiKey, from); err != nil {
		return err
	}
	apiKey = strings.TrimSpace(apiKey)
	sender, _ := mail.ParseAddress(strings.TrimSpace(from))
	if sender.Name == "" && key == "" {
		sender.Name = SystemName
	}
	to := make([]string, 0)
	for address := range strings.SplitSeq(receiver, ";") {
		recipient, err := mail.ParseAddress(strings.TrimSpace(address))
		if err != nil || len(to) >= 50 {
			return NewMessage("Invalid email address")
		}
		to = append(to, recipient.Address)
	}
	var headers map[string]string
	if key != "" {
		headers = map[string]string{"Auto-Submitted": "auto-generated"}
	}
	payload, err := Marshal(struct {
		From    string            `json:"from"`
		To      []string          `json:"to"`
		Subject string            `json:"subject"`
		HTML    string            `json:"html"`
		Text    string            `json:"text,omitempty"`
		Headers map[string]string `json:"headers,omitempty"`
	}{sender.String(), to, subject, content, plainText, headers})
	if err != nil {
		return NewMessage("Resend request failed")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.resend.com/emails", bytes.NewReader(payload))
	if err != nil {
		return NewMessage("Resend request failed")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := resendEmailClient.Do(req)
	if err != nil {
		// Transport errors may quote request data or credentials. Keep the
		// failure observable without copying untrusted error text into logs.
		if key != "" && ctx.Err() != nil {
			return ctx.Err()
		}
		SysError(LogText("Resend request failed"))
		return NewMessage("Resend request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Provider diagnostics can echo credentials, verification codes or
		// reset links. Status is sufficient to diagnose delivery without
		// exposing those values to callers or server-log readers.
		SysError(LogText("Resend rejected the email: status=%d", resp.StatusCode))
		if key != "" {
			return &ResendHTTPError{Status: resp.StatusCode}
		}
		return NewMessage("Resend email delivery failed (HTTP {{status}})", map[string]any{"status": resp.StatusCode})
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := DecodeJson(io.LimitReader(resp.Body, 4096), &result); err != nil || strings.TrimSpace(result.ID) == "" {
		return NewMessage("Invalid Resend delivery response")
	}
	return nil
}
