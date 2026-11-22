package model

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/mail"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const SupportEmailMaxAttempts = 6
const SupportEmailLifetime = 7 * 24 * time.Hour

var ErrSupportEmailInput = errors.New("Invalid support email settings")
var ErrSupportEmailState = errors.New("Support email notification is no longer eligible")

// Settings are a versioned singleton, read from the primary DB on every enqueue
// and dispatch. No process cache can reopen disabled delivery on a sibling node.
type SupportEmailSettings struct {
	ID                  int    `json:"-" gorm:"primaryKey"`
	Enabled             bool   `json:"enabled"`
	AdminRecipientsJSON string `json:"-" gorm:"type:text"`
	SiteOrigin          string `json:"site_origin" gorm:"size:255"`
	Version             int64  `json:"-"`
}

type SupportEmailPreference struct {
	UserID   int   `json:"-" gorm:"primaryKey"`
	Disabled bool  `json:"-"`
	Version  int64 `json:"-"`
}

// Outbox contains no ticket subject, message, request, attachment or credential.
// Recipient snapshots are private server data, never returned by diagnostics.
type SupportEmailOutbox struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	MessageID         int64  `json:"-" gorm:"uniqueIndex:idx_support_email_event,priority:1"`
	TicketID          int64  `json:"ticket_id" gorm:"index"`
	UserID            int    `json:"-" gorm:"index"`
	Event             string `json:"event" gorm:"size:24"`
	Recipient         string `json:"-" gorm:"size:254"`
	RecipientKey      string `json:"-" gorm:"size:64;uniqueIndex:idx_support_email_event,priority:2;index"`
	ConfigVersion     int64  `json:"-"`
	EmailVersion      int64  `json:"-"`
	PreferenceVersion int64  `json:"-"`
	Status            string `json:"status" gorm:"size:16;index:idx_support_email_due,priority:1"`
	Attempts          int    `json:"attempts"`
	RetryGeneration   int    `json:"-"`
	NextAttemptAt     int64  `json:"next_attempt_at" gorm:"index:idx_support_email_due,priority:2"`
	LeaseUntil        int64  `json:"-"`
	LeaseToken        string `json:"-" gorm:"size:64"`
	LastErrorCode     string `json:"last_error_code" gorm:"size:48"`
	CreatedAt         int64  `json:"created_at"`
	SentAt            int64  `json:"-"`
}

// The unique recipient/category row serializes cross-node rate reservations.
type SupportEmailRate struct {
	Key      string `gorm:"primaryKey;size:64"`
	NextAt   int64
	DayStart int64
	DayCount int
}

func ValidSupportEmailAddress(raw string) (string, bool) {
	value := NormalizeEmail(raw)
	if len(value) > 254 || strings.ContainsAny(value, "\r\n\x00;,") {
		return "", false
	}
	address, err := mail.ParseAddress(value)
	return value, err == nil && address.Name == "" && address.Address == value && strings.Contains(value, "@")
}

func ValidateSupportEmailOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(origin, "\r\n\x00\\*?#") || strings.HasSuffix(u.Host, ":") || len(origin) > 255 {
		return false
	}
	if raw := u.Port(); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	return true
}

func (s SupportEmailSettings) AdminRecipients() []string {
	var recipients []string
	if common.UnmarshalJsonStr(s.AdminRecipientsJSON, &recipients) != nil {
		return nil
	}
	return recipients
}

func ReadSupportEmailSettings(ctx context.Context) (SupportEmailSettings, error) {
	var settings SupportEmailSettings
	err := DB.WithContext(ctx).Where("id = ?", 1).Limit(1).Find(&settings).Error
	return settings, err
}

func SaveSupportEmailSettings(ctx context.Context, enabled bool, recipients []string, origin string) error {
	origin = strings.TrimSpace(origin)
	if len(recipients) > 5 || (origin != "" && !ValidateSupportEmailOrigin(origin)) {
		return ErrSupportEmailInput
	}
	normalized := make([]string, 0, len(recipients))
	for _, raw := range recipients {
		address, ok := ValidSupportEmailAddress(raw)
		if !ok {
			return ErrSupportEmailInput
		}
		if !slices.Contains(normalized, address) {
			normalized = append(normalized, address)
		}
	}
	slices.Sort(normalized)
	if enabled && (len(normalized) == 0 || !ValidateSupportEmailOrigin(origin)) {
		return ErrSupportEmailInput
	}
	encoded, err := common.Marshal(normalized)
	if err != nil {
		return err
	}
	return DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SupportEmailSettings{ID: 1}).Error; err != nil {
			return err
		}
		var current SupportEmailSettings
		if err := lockForUpdate(tx).First(&current, 1).Error; err != nil {
			return err
		}
		if current.Enabled == enabled && current.AdminRecipientsJSON == string(encoded) && current.SiteOrigin == origin {
			return nil
		}
		if err := tx.Model(&current).Updates(map[string]any{"enabled": enabled, "admin_recipients_json": string(encoded), "site_origin": origin, "version": current.Version + 1}).Error; err != nil {
			return err
		}
		// A new configuration is not consent to send a backlog to new recipients.
		return tx.Model(&SupportEmailOutbox{}).Where("status IN ?", []string{"pending", "retry", "leased", "dead"}).Updates(map[string]any{"status": "cancelled", "last_error_code": "settings_changed", "lease_token": "", "lease_until": 0}).Error
	})
}

func ReadSupportEmailPreference(ctx context.Context, userID int) (SupportEmailPreference, error) {
	var preference SupportEmailPreference
	err := DB.WithContext(ctx).Where("user_id = ?", userID).Limit(1).Find(&preference).Error
	return preference, err
}

func SaveSupportEmailPreference(ctx context.Context, userID int, enabled bool) error {
	return DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SupportEmailPreference{UserID: userID}).Error; err != nil {
			return err
		}
		var preference SupportEmailPreference
		if err := lockForUpdate(tx).First(&preference, userID).Error; err != nil {
			return err
		}
		if preference.Disabled == !enabled {
			return nil
		}
		if err := tx.Model(&preference).Updates(map[string]any{"disabled": !enabled, "version": preference.Version + 1}).Error; err != nil {
			return err
		}
		return tx.Model(&SupportEmailOutbox{}).Where("user_id = ? AND event = ? AND status IN ?", userID, "admin_reply", []string{"pending", "retry", "leased", "dead"}).Updates(map[string]any{"status": "cancelled", "last_error_code": "preference_changed", "lease_token": "", "lease_until": 0}).Error
	})
}

func supportRecipientKey(event, recipient string) string {
	category := "admin"
	if event == "admin_reply" {
		category = "customer"
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(category+"\x00"+recipient)))
}

// Enqueue is part of the ticket transaction. SMTP is never performed here. A
// failed DB enqueue prevents reporting a committed message without its event.
func enqueueSupportEmail(tx *gorm.DB, ticket *SupportTicket, message *SupportTicketMessage, created bool) error {
	var settings SupportEmailSettings
	if err := tx.Where("id = ?", 1).Limit(1).Find(&settings).Error; err != nil {
		return err
	}
	if !settings.Enabled {
		return nil
	}
	event := "user_reply"
	recipients := settings.AdminRecipients()
	emailVersion, preferenceVersion := int64(0), int64(0)
	switch message.AuthorRole {
	case SupportTicketAuthorUser:
		if created {
			event = "created"
		}
	case SupportTicketAuthorAdmin:
		event = "admin_reply"
		var user User
		if err := tx.First(&user, ticket.UserID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled || !VerifiedSupportEmail(&user) {
			return nil
		}
		var preference SupportEmailPreference
		if err := tx.Where("user_id = ?", user.Id).Limit(1).Find(&preference).Error; err != nil {
			return err
		}
		if preference.Disabled {
			return nil
		}
		recipients = []string{NormalizeEmail(user.Email)}
		emailVersion, preferenceVersion = user.EmailVerificationVersion, preference.Version
	default:
		return nil // future internal notes / system events are never mail.
	}
	for _, recipient := range recipients {
		normalized, valid := ValidSupportEmailAddress(recipient)
		if !valid {
			continue
		}
		key := supportRecipientKey(event, normalized)
		row := SupportEmailOutbox{MessageID: message.ID, TicketID: ticket.ID, UserID: ticket.UserID, Event: event, Recipient: normalized, RecipientKey: key, ConfigVersion: settings.Version, EmailVersion: emailVersion, PreferenceVersion: preferenceVersion, Status: "pending", NextAttemptAt: message.CreatedAt, CreatedAt: message.CreatedAt}
		// Existing pending notification already links to the live thread; keep its
		// original eligibility snapshot rather than moving old events to a new email.
		var pending int64
		if err := tx.Model(&SupportEmailOutbox{}).Where("ticket_id = ? AND recipient_key = ? AND config_version = ? AND email_version = ? AND preference_version = ? AND status IN ?", ticket.ID, key, settings.Version, emailVersion, preferenceVersion, []string{"pending", "retry", "leased", "sending"}).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			row.Status = "coalesced"
		}
		if err := tx.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

// CAS claiming works without SKIP LOCKED (including older supported engines).
// Lease duration exceeds the SMTP hard deadline; a crash recovers after expiry.
func ClaimSupportEmail(ctx context.Context, now int64) (*SupportEmailOutbox, error) {
	var rows []SupportEmailOutbox
	db := DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	eligible := "((status IN ? AND next_attempt_at <= ?) OR (status IN ? AND lease_until <= ?))"
	if err := db.Where(eligible, []string{"pending", "retry"}, now, []string{"leased", "sending"}, now).Order("next_attempt_at ASC, id ASC").Limit(20).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		token := common.GetRandomString(40)
		result := db.Model(&SupportEmailOutbox{}).Where("id = ?", row.ID).Where(eligible, []string{"pending", "retry"}, now, []string{"leased", "sending"}, now).Updates(map[string]any{"status": "leased", "lease_token": token, "lease_until": now + 120})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 1 {
			row.Status = "leased"
			row.LeaseToken = token
			row.LeaseUntil = now + 120
			return &row, nil
		}
	}
	return nil, nil
}

type SupportEmailAdmission struct {
	Origin   string
	Language string
	Deadline time.Time
}

// Admission is the last authoritative DB check before SMTP. Changes committed
// after this boundary cannot retract an SMTP transaction already in progress.
func AdmitSupportEmail(ctx context.Context, row *SupportEmailOutbox, now int64) (*SupportEmailAdmission, error) {
	var admission *SupportEmailAdmission
	err := DB.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).Transaction(func(tx *gorm.DB) error {

		// Match all configuration/preference mutators: settings -> user ->
		// preference -> outbox. Locking the outbox first would deadlock against
		// opt-out, and unlocked identity reads could admit an obsolete address.
		var settings SupportEmailSettings
		if err := lockForUpdate(tx).Where("id = ?", 1).Limit(1).Find(&settings).Error; err != nil {
			return err
		}
		var user User
		if err := lockForUpdate(tx).Where("id = ?", row.UserID).Limit(1).Find(&user).Error; err != nil {
			return err
		}
		var preference SupportEmailPreference
		if row.Event == "admin_reply" {
			if err := lockForUpdate(tx).Where("user_id = ?", row.UserID).Limit(1).Find(&preference).Error; err != nil {
				return err
			}
		}
		var current SupportEmailOutbox
		if err := lockForUpdate(tx).Where("id = ? AND status = ? AND lease_token = ?", row.ID, "leased", row.LeaseToken).First(&current).Error; err != nil {
			return err
		}
		now = max(now, time.Now().Unix())
		if current.LeaseUntil <= now {
			return nil
		}
		cancel := func(reason string) error {
			return tx.Model(&current).Updates(map[string]any{"status": "cancelled", "last_error_code": reason, "lease_token": "", "lease_until": 0}).Error
		}
		if now-current.CreatedAt >= int64(SupportEmailLifetime/time.Second) {
			return cancel("expired")
		}
		if !settings.Enabled || settings.Version != current.ConfigVersion || !ValidateSupportEmailOrigin(settings.SiteOrigin) {
			return cancel("settings_changed")
		}
		if user.Id == 0 || user.Status != common.UserStatusEnabled {
			return cancel("account_unavailable")
		}
		language := ""
		if current.Event == "admin_reply" {
			if preference.Disabled || preference.Version != current.PreferenceVersion || !VerifiedSupportEmail(&user) || user.EmailVerificationVersion != current.EmailVersion || NormalizeEmail(user.Email) != current.Recipient {
				return cancel("recipient_changed")
			}
			var owners int64
			if err := tx.Unscoped().Model(&User{}).Where("LOWER(email) = ?", current.Recipient).Count(&owners).Error; err != nil {
				return err
			}
			if owners != 1 {
				return cancel("recipient_ambiguous")
			}
			language = user.GetSetting().Language
		} else if !slices.Contains(settings.AdminRecipients(), current.Recipient) {
			return cancel("recipient_changed")
		}
		if current.Attempts >= SupportEmailMaxAttempts*(current.RetryGeneration+1) {
			return tx.Model(&current).Updates(map[string]any{"status": "dead", "last_error_code": "attempts_exhausted", "lease_token": "", "lease_until": 0}).Error
		}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&SupportEmailRate{Key: current.RecipientKey}).Error; err != nil {
			return err
		}
		var rate SupportEmailRate
		if err := lockForUpdate(tx).Where(map[string]any{"key": current.RecipientKey}).First(&rate).Error; err != nil {
			return err
		}
		// Waiting for a recipient's throttle row can outlive the original
		// claim. Never dispatch under an expired lease; reserve a fresh lease
		// only after every potentially blocking row lock has been acquired.
		now = max(now, time.Now().Unix())
		if current.LeaseUntil <= now {
			return nil
		}
		if now-rate.DayStart >= 86400 {
			rate.DayStart = now
			rate.DayCount = 0
		}
		limit := 120
		if current.Event == "admin_reply" {
			limit = 30
		}
		next := rate.NextAt
		if rate.DayCount >= limit {
			next = max(next, rate.DayStart+86400)
		}
		if next > now {
			return tx.Model(&current).Updates(map[string]any{"status": "retry", "next_attempt_at": next, "lease_token": "", "lease_until": 0, "last_error_code": "rate_limited"}).Error
		}
		if err := tx.Model(&rate).Updates(map[string]any{"next_at": now + 60, "day_start": rate.DayStart, "day_count": rate.DayCount + 1}).Error; err != nil {
			return err
		}
		attempts := current.Attempts + 1
		if err := tx.Model(&current).Updates(map[string]any{"attempts": attempts, "status": "sending", "lease_until": now + 120}).Error; err != nil {
			return err
		}
		row.LeaseUntil = now + 120
		row.Attempts = attempts
		row.RetryGeneration = current.RetryGeneration
		admission = &SupportEmailAdmission{Origin: settings.SiteOrigin, Language: language, Deadline: time.Unix(now+30, 0)}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return admission, err
}

func FinishSupportEmail(ctx context.Context, row *SupportEmailOutbox, now int64, errorCode string) error {
	updates := map[string]any{"lease_token": "", "lease_until": 0, "last_error_code": errorCode}
	if errorCode == "" {
		updates["status"] = "sent"
		updates["sent_at"] = now
	} else {
		updates["status"] = "retry"
		round := max(1, row.Attempts-SupportEmailMaxAttempts*row.RetryGeneration)
		updates["next_attempt_at"] = now + min(int64(3600), int64(30)<<min(round-1, 7))
		if round >= SupportEmailMaxAttempts {
			updates["status"] = "dead"
		}
	}
	return DB.WithContext(ctx).Model(&SupportEmailOutbox{}).Where("id = ? AND status = ? AND lease_token = ?", row.ID, "sending", row.LeaseToken).Updates(updates).Error
}

func ManageSupportEmail(ctx context.Context, id int64, retry bool) error {
	updates := map[string]any{"status": "cancelled", "last_error_code": "operator_cancelled", "lease_token": "", "lease_until": 0}
	states := []string{"pending", "retry", "leased", "dead"}
	if retry {
		states = []string{"dead"}
		updates = map[string]any{"status": "retry", "last_error_code": "", "next_attempt_at": time.Now().Unix(), "retry_generation": gorm.Expr("retry_generation + 1")}
	}
	result := DB.WithContext(ctx).Model(&SupportEmailOutbox{}).Where("id = ? AND status IN ?", id, states).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrSupportEmailState
	}
	return nil
}
