package common

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func generateMessageID(from string) (string, error) {
	_, domain, ok := strings.Cut(from, "@")
	if !ok {
		return "", fmt.Errorf("invalid SMTP account")
	}
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

// smtpConfigSnapshotLocked reads SMTP globals while holding OptionMapRWMutex.
// The sender fallback is local; a concurrent settings refresh cannot mutate
// the configuration halfway through one delivery.
func smtpConfigSnapshotLocked() SMTPDeliveryConfig {
	from := SMTPFrom
	if from == "" {
		from = SMTPAccount
	}
	return SMTPDeliveryConfig{
		Server: SMTPServer, Port: SMTPPort, Account: SMTPAccount, Token: SMTPToken, From: from,
		SSL: SMTPSSLEnabled, StartTLS: SMTPStartTLSEnabled,
		InsecureSkipVerify: SMTPInsecureSkipVerify, ForceAuthLogin: SMTPForceAuthLogin,
	}
}

func newSMTPClient(ctx context.Context, addr string, config SMTPDeliveryConfig) (*smtp.Client, error) {
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	ready := false
	defer func() {
		if !ready {
			stop()
			_ = rawConn.Close()
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	tlsConfig := &tls.Config{
		ServerName:         config.Server,
		InsecureSkipVerify: config.InsecureSkipVerify, // #nosec G402 -- preserve explicit legacy admin compatibility.
	}
	implicitTLS := config.SSL || config.Port == 465 && !config.StartTLS
	if implicitTLS {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secure
	}
	client, err := smtp.NewClient(conn, config.Server)
	if err != nil {
		return nil, err
	}
	if config.StartTLS && !implicitTLS {
		if supported, _ := client.Extension("STARTTLS"); !supported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			_ = client.Close()
			return nil, err
		}
	}
	ready = true
	return client, nil
}

func htmlToPlainText(content string) string {
	doc, err := html.Parse(strings.NewReader(content))
	if err != nil {
		return strings.TrimSpace(content)
	}

	var builder strings.Builder
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, skip bool) {
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style") {
			skip = true
		}
		if !skip && node.Type == html.TextNode {
			text := strings.Join(strings.Fields(node.Data), " ")
			if text != "" {
				if builder.Len() > 0 {
					builder.WriteByte(' ')
				}
				builder.WriteString(text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}
		if !skip && node.Type == html.ElementNode {
			switch node.Data {
			case "br", "p", "div", "li", "h1", "h2", "h3", "h4", "h5", "h6", "tr":
				builder.WriteByte('\n')
			}
		}
	}
	walk(doc, false)

	lines := strings.Split(builder.String(), "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

func createAlternativePart(writer *multipart.Writer, contentType string, content string) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Type", contentType+"; charset=UTF-8")
	header.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	quotedPrintableWriter := quotedprintable.NewWriter(part)
	if _, err = io.WriteString(quotedPrintableWriter, content); err != nil {
		_ = quotedPrintableWriter.Close()
		return err
	}
	return quotedPrintableWriter.Close()
}

func buildEmailMessage(from, subject, receiver, plainText, htmlContent string) ([]byte, error) {
	id, err := generateMessageID(from)
	if err != nil {
		return nil, err
	}

	var body bytes.Buffer
	alternativeWriter := multipart.NewWriter(&body)
	if err = createAlternativePart(alternativeWriter, "text/plain", plainText); err != nil {
		return nil, err
	}
	if err = createAlternativePart(alternativeWriter, "text/html", htmlContent); err != nil {
		return nil, err
	}
	if err = alternativeWriter.Close(); err != nil {
		return nil, err
	}

	recipients := strings.Split(receiver, ";")
	toHeaders := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		toHeaders = append(toHeaders, (&mail.Address{Address: recipient}).String())
	}
	if len(toHeaders) == 0 {
		return nil, fmt.Errorf("invalid email receiver")
	}

	fromHeader := (&mail.Address{Name: SystemName, Address: from}).String()
	var message bytes.Buffer
	fmt.Fprintf(&message, "To: %s\r\n", strings.Join(toHeaders, ", "))
	fmt.Fprintf(&message, "From: %s\r\n", fromHeader)
	fmt.Fprintf(&message, "Subject: %s\r\n", mime.BEncoding.Encode("UTF-8", subject))
	fmt.Fprintf(&message, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&message, "Message-ID: %s\r\n", id)
	fmt.Fprint(&message, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&message, "Content-Type: multipart/alternative; boundary=%q\r\n", alternativeWriter.Boundary())
	// Brevo consumes these SMTP headers and omits open/click tracking for this
	// transactional message, avoiding pixels and rewritten verification links.
	fmt.Fprint(&message, "X-Mailin-Track: false\r\n")
	fmt.Fprint(&message, "X-Mailin-Track-Clicks: false\r\n")
	fmt.Fprint(&message, "X-Mailin-Track-Opens: false\r\n")
	fmt.Fprint(&message, "\r\n")
	message.Write(body.Bytes())
	return message.Bytes(), nil
}

func SendEmailWithPlainText(subject string, receiver string, plainText string, htmlContent string) error {
	OptionMapRWMutex.RLock()
	provider := OptionMap["EmailProvider"]
	resendKey := OptionMap["ResendApiKey"]
	resendFrom := OptionMap["ResendFrom"]
	config := smtpConfigSnapshotLocked()
	OptionMapRWMutex.RUnlock()
	// One selector controls delivery. Never fall back to a second provider:
	// a failed response does not prove that the first provider did not send.
	switch provider {
	case "resend":
		return sendResendEmail(resendKey, resendFrom, subject, receiver, plainText, htmlContent)
	case "", "smtp": // Preserve existing installations without a selector.
	default:
		return NewMessage("Email sending method must be SMTP or Resend")
	}
	if config.Server == "" && config.Account == "" {
		return NewMessage("SMTP server is not configured")
	}
	message, err := buildEmailMessage(config.From, subject, receiver, plainText, htmlContent)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr := net.JoinHostPort(config.Server, strconv.Itoa(config.Port))
	client, err := newSMTPClient(ctx, addr, config)
	if err != nil {
		return err
	}
	defer client.Close()
	if config.Account != "" && config.Token != "" {
		auth := &smtpAutoAuth{username: config.Account, password: config.Token, configuration: &smtpAuthConfiguration{
			server: config.Server, forceLogin: config.ForceAuthLogin,
			preferLogin: isOutlookServer(config.Account) || slices.Contains(EmailLoginAuthServerList, config.Server),
		}}
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(config.From); err != nil {
		return err
	}
	for recipient := range strings.SplitSeq(receiver, ";") {
		recipient = strings.TrimSpace(recipient)
		if recipient == "" {
			continue
		}
		if err = client.Rcpt(recipient); err != nil {
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	_, err = w.Write(message)
	if err != nil {
		return err
	}
	err = w.Close()
	if err != nil {
		return err
	}
	// DATA was accepted. A later disconnect or QUIT error cannot undo it.
	_ = client.Quit()
	return nil
}

func SendEmail(subject string, receiver string, content string) error {
	return SendEmailWithPlainText(subject, receiver, htmlToPlainText(content), content)
}
