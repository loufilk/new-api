package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/smtp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SMTPDeliveryConfig is an immutable per-dispatch snapshot. Do not read mutable
// process-wide SMTP settings halfway through a durable notification delivery.
type SMTPDeliveryConfig struct {
	Server             string
	Port               int
	Account            string
	Token              string
	From               string
	SSL                bool
	StartTLS           bool
	InsecureSkipVerify bool
	ForceAuthLogin     bool
}

func (s SMTPDeliveryConfig) Valid() bool {
	from, err := mail.ParseAddress(s.From)
	loopback := strings.EqualFold(s.Server, "localhost")
	if ip := net.ParseIP(s.Server); ip != nil {
		loopback = ip.IsLoopback()
	}
	return s.Server != "" && !strings.ContainsAny(s.Server, "\r\n\x00 /\\") && s.Port > 0 && s.Port <= 65535 && err == nil && from.Name == "" && from.Address == s.From && !strings.ContainsAny(s.From, "\r\n\x00;,") && !s.InsecureSkipVerify && (loopback || s.SSL || s.StartTLS || s.Port == 465) && ((s.Account == "") == (s.Token == ""))
}

func buildQueuedEmail(config SMTPDeliveryConfig, subject, receiver, plainText, htmlContent, messageID string) ([]byte, error) {
	recipient, err := mail.ParseAddress(receiver)
	if err != nil || recipient.Name != "" || recipient.Address != receiver || strings.ContainsAny(receiver, "\r\n\x00;,") || strings.ContainsAny(messageID, "\r\n\x00") || !strings.HasPrefix(messageID, "<") || !strings.HasSuffix(messageID, ">") {
		return nil, errors.New("invalid notification envelope")
	}
	var body bytes.Buffer
	parts := multipart.NewWriter(&body)
	if err := createAlternativePart(parts, "text/plain", plainText); err != nil {
		return nil, err
	}
	if err := createAlternativePart(parts, "text/html", htmlContent); err != nil {
		return nil, err
	}
	if err := parts.Close(); err != nil {
		return nil, err
	}
	var message bytes.Buffer
	fmt.Fprintf(&message, "To: %s\r\nFrom: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\nX-Mailin-Track: false\r\nX-Mailin-Track-Clicks: false\r\nX-Mailin-Track-Opens: false\r\nAuto-Submitted: auto-generated\r\n\r\n", recipient.String(), (&mail.Address{Address: config.From}).String(), mime.BEncoding.Encode("UTF-8", subject), time.Now().Format(time.RFC1123Z), messageID, parts.Boundary())
	message.Write(body.Bytes())
	return message.Bytes(), nil
}

// SendQueuedSMTP never selects/falls back to another provider. It has one
// recipient, stable Message-ID and a hard deadline shorter than its DB lease.
// DATA 250 is success: a later QUIT failure must not schedule duplicate mail.
func SendQueuedSMTP(ctx context.Context, config SMTPDeliveryConfig, subject, receiver, plainText, htmlContent, messageID string) error {
	if !config.Valid() {
		return errors.New("SMTP notification configuration unavailable")
	}
	message, err := buildQueuedEmail(config, subject, receiver, plainText, htmlContent, messageID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(config.Server, strconv.Itoa(config.Port)))
	if err != nil {
		return err
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		return err
	}
	tlsConfig := &tls.Config{ServerName: config.Server, MinVersion: tls.VersionTLS12}
	if config.SSL || config.Port == 465 && !config.StartTLS {
		secure := tls.Client(conn, tlsConfig)
		if err = secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, config.Server)
	if err != nil {
		return err
	}
	defer client.Close()
	if config.StartTLS && !config.SSL {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP requires STARTTLS")
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if config.Account != "" {
		auth := &smtpAutoAuth{username: config.Account, password: config.Token, configuration: &smtpAuthConfiguration{server: config.Server, forceLogin: config.ForceAuthLogin, preferLogin: isOutlookServer(config.Account) || slices.Contains(EmailLoginAuthServerList, config.Server)}}
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(config.From); err != nil {
		return err
	}
	if err = client.Rcpt(receiver); err != nil {
		return err
	}
	data, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = data.Write(message); err != nil {
		return err
	}
	if err = data.Close(); err != nil {
		return err
	}
	_ = client.Quit()
	return nil
}
