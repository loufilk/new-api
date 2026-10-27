package common

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"
)

func generateMessageID() (string, error) {
	split := strings.Split(SMTPFrom, "@")
	if len(split) < 2 {
		return "", fmt.Errorf("invalid SMTP account")
	}
	domain := strings.Split(SMTPFrom, "@")[1]
	return fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), GetRandomString(12), domain), nil
}

func shouldUseSMTPLoginAuth() bool {
	if SMTPForceAuthLogin {
		return true
	}
	return isOutlookServer(SMTPAccount) || slices.Contains(EmailLoginAuthServerList, SMTPServer)
}

func getSMTPAuth() smtp.Auth {
	return AutoSMTPAuth(SMTPAccount, SMTPToken)
}

func shouldAuthenticateSMTP() bool {
	return SMTPAccount != "" && SMTPToken != ""
}

func smtpTLSConfig() *tls.Config {
	return &tls.Config{
		ServerName:         SMTPServer,
		InsecureSkipVerify: SMTPInsecureSkipVerify, // #nosec G402 -- admin-controlled SMTP compatibility option.
	}
}

func newSMTPClient(addr string) (*smtp.Client, error) {
	if SMTPSSLEnabled || (SMTPPort == 465 && !SMTPStartTLSEnabled) {
		conn, err := tls.Dial("tcp", addr, smtpTLSConfig())
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, SMTPServer)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}

	client, err := smtp.Dial(addr)
	if err != nil {
		return nil, err
	}

	if SMTPStartTLSEnabled {
		startTLSSupported, _ := client.Extension("STARTTLS")
		if !startTLSSupported {
			_ = client.Close()
			return nil, fmt.Errorf("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(smtpTLSConfig()); err != nil {
			_ = client.Close()
			return nil, err
		}
	}

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

func buildEmailMessage(subject string, receiver string, plainText string, htmlContent string) ([]byte, error) {
	id, err := generateMessageID()
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

	fromHeader := (&mail.Address{Name: SystemName, Address: SMTPFrom}).String()
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
	if SMTPFrom == "" { // for compatibility
		SMTPFrom = SMTPAccount
	}
	if SMTPServer == "" && SMTPAccount == "" {
		return NewMessage("SMTP server is not configured")
	}
	message, err := buildEmailMessage(subject, receiver, plainText, htmlContent)
	if err != nil {
		return err
	}
	auth := getSMTPAuth()
	addr := fmt.Sprintf("%s:%d", SMTPServer, SMTPPort)
	to := strings.Split(receiver, ";")
	client, err := newSMTPClient(addr)
	if err != nil {
		return err
	}
	defer client.Close()
	if shouldAuthenticateSMTP() {
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	if err = client.Mail(SMTPFrom); err != nil {
		return err
	}
	for _, recipient := range to {
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
	err = client.Quit()
	if err != nil {
		SysError(LogText("failed to send email to %s: %v", receiver, err))
	}
	return err
}

func SendEmail(subject string, receiver string, content string) error {
	return SendEmailWithPlainText(subject, receiver, htmlToPlainText(content), content)
}
