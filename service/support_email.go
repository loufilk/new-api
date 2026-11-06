package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"html"
	"net"
	"net/textproto"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Read only primary-DB SMTP options, never stale globals or request headers.
// Values/driver errors are not logged: this snapshot contains SMTP credentials.
func SupportEmailSMTPConfig(ctx context.Context) (common.SMTPDeliveryConfig, bool, error) {
	var rows []model.Option
	err := model.DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Where(map[string]any{"key": []string{"EmailProvider", "SMTPServer", "SMTPPort", "SMTPAccount", "SMTPToken", "SMTPFrom", "SMTPSSLEnabled", "SMTPStartTLSEnabled", "SMTPInsecureSkipVerify", "SMTPForceAuthLogin"}}).Find(&rows).Error
	if err != nil {
		return common.SMTPDeliveryConfig{}, false, err
	}
	options := map[string]string{}
	for _, row := range rows {
		options[row.Key] = row.Value
	}
	port := 587 // Match the existing SMTP default when no option row was saved.
	if raw := options["SMTPPort"]; raw != "" {
		port, _ = strconv.Atoi(raw)
	}
	config := common.SMTPDeliveryConfig{Server: options["SMTPServer"], Port: port, Account: options["SMTPAccount"], Token: options["SMTPToken"], From: options["SMTPFrom"], SSL: options["SMTPSSLEnabled"] == "true", StartTLS: options["SMTPStartTLSEnabled"] == "true", InsecureSkipVerify: options["SMTPInsecureSkipVerify"] == "true", ForceAuthLogin: options["SMTPForceAuthLogin"] == "true"}
	if config.From == "" {
		config.From = config.Account
	}
	return config, (options["EmailProvider"] == "" || options["EmailProvider"] == "smtp") && config.Valid(), nil
}

func SupportEmailContent(row *model.SupportEmailOutbox, admission *model.SupportEmailAdmission) (subject, plain, markup, messageID string) {
	eventKey := i18n.MsgSupportEmailCustomerReply
	if row.Event == "created" {
		eventKey = i18n.MsgSupportEmailCreated
	} else if row.Event == "admin_reply" {
		eventKey = i18n.MsgSupportEmailAdminReply
	}
	subject = i18n.Translate(admission.Language, i18n.MsgSupportEmailSubject, map[string]any{"ID": row.TicketID})
	event := i18n.Translate(admission.Language, eventKey)
	link := fmt.Sprintf("%s/support?ticket=%d", admission.Origin, row.TicketID)
	guidance := i18n.Translate(admission.Language, i18n.MsgSupportEmailGuidance)
	plain = fmt.Sprintf("%s\n%s\n\n%s\n%s", subject, event, link, guidance)
	markup = fmt.Sprintf("<p>%s</p><p>%s</p><p><a href=\"%s\">%s</a></p><p>%s</p>", html.EscapeString(subject), html.EscapeString(event), html.EscapeString(link), html.EscapeString(link), html.EscapeString(guidance))
	// Stable across retries/restarts/configuration, with no address or usable token.
	digest := sha256.Sum256([]byte(fmt.Sprintf("%d/%s", row.ID, row.RecipientKey)))
	messageID = fmt.Sprintf("<support-%x@notification.invalid>", digest)
	return
}

func supportEmailErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "delivery_cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "delivery_timeout"
	}
	var network net.Error
	if errors.As(err, &network) && network.Timeout() {
		return "delivery_timeout"
	}
	var smtp *textproto.Error
	if errors.As(err, &smtp) {
		if smtp.Code >= 500 {
			return "smtp_rejected"
		}
		return "smtp_temporary"
	}
	return "delivery_failed" // Never persist or log provider response/recipient/secret.
}

// RunSupportEmailOnce is synchronous only inside the worker, not in HTTP.
func RunSupportEmailOnce(ctx context.Context) (bool, error) {
	row, err := model.ClaimSupportEmail(ctx, time.Now().Unix())
	if err != nil || row == nil {
		return false, err
	}
	admission, err := model.AdmitSupportEmail(ctx, row, time.Now().Unix())
	if err != nil || admission == nil {
		return true, err
	}
	// The absolute deadline starts at admission, not when a paused worker wakes.
	// Reading SMTP after admission also avoids reusing a pre-admission snapshot
	// after an administrator has switched the selected delivery provider.
	sendCtx, cancelSend := context.WithDeadline(ctx, admission.Deadline)
	defer cancelSend()
	config, configured, configErr := SupportEmailSMTPConfig(sendCtx)
	code := "smtp_not_configured"
	if configErr != nil {
		code = "smtp_configuration_unavailable"
	} else if configured {
		subject, plain, markup, id := SupportEmailContent(row, admission)
		err = common.SendQueuedSMTP(sendCtx, config, subject, row.Recipient, plain, markup, id)
		code = ""
		if err != nil {
			code = supportEmailErrorCode(err)
		}
	}
	// Persist a bounded safe outcome even if the scheduling context was cancelled.
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return true, model.FinishSupportEmail(finishCtx, row, time.Now().Unix(), code)
}

func RunSupportEmailWorker(schedule, work context.Context) {
	lifecycle.Periodic(schedule, work, func() time.Duration { return 2 * time.Second }, false, func(ctx context.Context) {
		for range 5 {
			if schedule.Err() != nil || ctx.Err() != nil {
				return
			}
			handled, err := RunSupportEmailOnce(ctx)
			if err != nil {
				common.SysError("support email queue processing failed")
				return
			}
			if !handled {
				return
			}
		}
	})
}
