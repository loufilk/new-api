package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	PartnerApplicationSubmitted = "submitted"
	PartnerApplicationReviewing = "reviewing"
	PartnerApplicationApproved  = "approved"
	PartnerApplicationRejected  = "rejected"
	PartnerApplicationCancelled = "cancelled"
)

const (
	PartnerContactWeChat   = "wechat"
	PartnerContactQQ       = "qq"
	PartnerContactEmail    = "email"
	PartnerContactPhone    = "phone"
	PartnerContactOther    = "other"
	PartnerContactTelegram = "telegram"

	PartnerPlatformOpenAI    = "openai"
	PartnerPlatformAnthropic = "anthropic"
	PartnerPlatformGoogle    = "google"
	PartnerPlatformDeepSeek  = "deepseek"
	PartnerPlatformAlibaba   = "alibaba"
	PartnerPlatformByteDance = "bytedance"
	PartnerPlatformZhipu     = "zhipu"
	PartnerPlatformMoonshot  = "moonshot"
	PartnerPlatformOther     = "other"
)

// Supplier platform codes label quotes; they do not select relay channels.
var partnerQuotePlatforms = []string{
	PartnerPlatformOpenAI, PartnerPlatformAnthropic, PartnerPlatformGoogle, "aws",
	PartnerPlatformDeepSeek, "azure", "vertex", PartnerPlatformAlibaba, PartnerPlatformByteDance,
	PartnerPlatformMoonshot, "zhipu-glm", "tencent", "xai", "newapi", "advanced-custom",
	"sub2api", "mistral", "cohere", "openrouter", "ollama", "vllm", "siliconflow", "perplexity",
	"baidu", "baidu-v2", "xunfei", "lingyiwanwu", "minimax", "coze", "360", "xinference",
	"dify", "jina", "cloudflare", "custom", "codex", "fastgpt", "mokaai", "mjproxy",
	"mjproxy-plus", "sunoapi", "kling", "jimeng", "vidu", "submodel", "doubao", "sora",
	"replicate", "ohmygpt", PartnerPlatformZhipu, "sglang", PartnerPlatformOther,
}

// The previous candidate (0dff956) accepts exactly these persisted codes.
// Keep the allowlists independent from future additions to the live catalogue.
func isLegacyPartnerContactType(value string) bool {
	switch value {
	case PartnerContactWeChat, PartnerContactQQ, PartnerContactEmail, PartnerContactPhone, PartnerContactOther:
		return true
	default:
		return false
	}
}

func isLegacyPartnerQuotePlatform(value string) bool {
	switch value {
	case PartnerPlatformOpenAI, PartnerPlatformAnthropic, PartnerPlatformGoogle, PartnerPlatformDeepSeek,
		PartnerPlatformAlibaba, PartnerPlatformByteDance, PartnerPlatformZhipu, PartnerPlatformMoonshot, PartnerPlatformOther:
		return true
	default:
		return false
	}
}

var (
	ErrPartnerApplicationInvalid   = errors.New("Invalid partner application")
	ErrPartnerApplicationTooLarge  = errors.New("Partner application request is too large")
	ErrPartnerApplicationConflict  = errors.New("This application changed. Reload and try again.")
	ErrPartnerApplicationForbidden = errors.New("Unauthorized, insufficient privileges")
	ErrPartnerApplicationsClosed   = errors.New("Partner applications are currently closed")
	partnerQuoteRatePattern        = regexp.MustCompile(`^\d{1,4}(?:\.\d{1,6})?$`)
)

type PartnerApplicationSettings struct {
	ID           int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Revision     int64  `json:"revision" gorm:"not null"`
	Enabled      bool   `json:"partner_enabled" gorm:"not null"`
	Instructions string `json:"partner_instructions" gorm:"type:text;not null"`
	UpdatedAt    int64  `json:"updated_at" gorm:"not null"`
	UpdatedBy    int    `json:"updated_by" gorm:"not null"`
}

type PartnerQuote struct {
	Platform    string `json:"platform"`
	Group       string `json:"group"`
	Models      string `json:"models"`
	Rate        string `json:"rate"`
	Description string `json:"description"`
}

type PartnerApplication struct {
	ID           int64                     `json:"id" gorm:"primaryKey"`
	UserID       int                       `json:"user_id" gorm:"not null;index:idx_partner_application_user_status,priority:1;uniqueIndex:idx_partner_application_request,priority:1"`
	RequestKey   string                    `json:"-" gorm:"size:96;not null;uniqueIndex:idx_partner_application_request,priority:2"`
	RequestHash  string                    `json:"-" gorm:"size:64;not null"`
	ActiveUserID *int                      `json:"-" gorm:"uniqueIndex:idx_partner_application_active_user"`
	SiteName     string                    `json:"site_name" gorm:"size:120;not null"`
	SiteURL      string                    `json:"site_url" gorm:"size:2048;not null"`
	ContactType  string                    `json:"contact_type" gorm:"size:24;not null"`
	Contact      string                    `json:"contact" gorm:"size:200;not null"`
	Description  string                    `json:"description" gorm:"type:text;not null"`
	QuotesJSON   string                    `json:"-" gorm:"type:text;not null"`
	Quotes       []PartnerQuote            `json:"quotes" gorm:"-"`
	Status       string                    `json:"status" gorm:"size:24;not null;index:idx_partner_application_user_status,priority:2;index:idx_partner_application_status"`
	Revision     int64                     `json:"revision" gorm:"not null"`
	ReviewNote   string                    `json:"review_note" gorm:"size:1000;not null"`
	ReviewedBy   int                       `json:"reviewed_by" gorm:"not null"`
	CreatedAt    int64                     `json:"created_at" gorm:"not null;autoCreateTime:false"`
	UpdatedAt    int64                     `json:"updated_at" gorm:"not null;autoUpdateTime:false"`
	Events       []PartnerApplicationEvent `json:"events,omitempty" gorm:"-"`
}

type PartnerApplicationEvent struct {
	ID            int64  `json:"id" gorm:"primaryKey"`
	ApplicationID int64  `json:"application_id" gorm:"not null;index:idx_partner_application_events,priority:1"`
	ActorID       int    `json:"actor_id" gorm:"not null"`
	UserID        int    `json:"user_id" gorm:"not null;index"`
	Action        string `json:"action" gorm:"size:24;not null"`
	FromStatus    string `json:"from_status" gorm:"size:24;not null"`
	ToStatus      string `json:"to_status" gorm:"size:24;not null"`
	Note          string `json:"note,omitempty" gorm:"size:1000;not null"`
	CreatedAt     int64  `json:"created_at" gorm:"not null;autoCreateTime:false;index:idx_partner_application_events,priority:2"`
}

type PartnerApplicationInput struct {
	SiteName    string         `json:"site_name"`
	SiteURL     string         `json:"site_url"`
	ContactType string         `json:"contact_type"`
	Contact     string         `json:"contact"`
	Description string         `json:"description"`
	Quotes      []PartnerQuote `json:"quotes"`
}

type PartnerApplicationReview struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Action           string `json:"action"`
	Note             string `json:"note"`
}

type PartnerAdminPermissionCheck func(tx *gorm.DB, actor *User) error

func DefaultPartnerApplicationSettings() PartnerApplicationSettings {
	return PartnerApplicationSettings{ID: 1, Enabled: true}
}

func SeedPartnerApplicationSettings(db *gorm.DB) error {
	if db == nil {
		return errors.New("partner application settings database is required")
	}
	settings := DefaultPartnerApplicationSettings()
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&settings).Error
}

func GetPartnerApplicationSettings(ctx context.Context) (*PartnerApplicationSettings, error) {
	settings := DefaultPartnerApplicationSettings()
	if err := DB.WithContext(ctx).Where("id = ?", settings.ID).Limit(1).Find(&settings).Error; err != nil {
		return nil, err
	}
	return &settings, nil
}

func SavePartnerApplicationSettings(ctx context.Context, actorID int, input PartnerApplicationSettings, checks ...PartnerAdminPermissionCheck) (*PartnerApplicationSettings, error) {
	input.Instructions = strings.TrimSpace(input.Instructions)
	if input.Revision < 0 || !validPartnerText(input.Instructions, 0, 3000) {
		return nil, ErrPartnerApplicationInvalid
	}
	input.ID = 1
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := partnerApplicationActor(tx, actorID, true, checks...); err != nil {
			return err
		}
		if err := SeedPartnerApplicationSettings(tx); err != nil {
			return err
		}
		current := DefaultPartnerApplicationSettings()
		if err := lockForUpdate(tx).First(&current, 1).Error; err != nil {
			return err
		}
		if input.Revision != current.Revision {
			return ErrPartnerApplicationConflict
		}
		input.Revision = current.Revision + 1
		input.UpdatedAt = common.GetTimestamp()
		input.UpdatedBy = actorID
		return tx.Save(&input).Error
	})
	if err != nil {
		return nil, err
	}
	return &input, nil
}

func normalizePartnerApplication(input PartnerApplicationInput) (PartnerApplicationInput, error) {
	input.SiteName = strings.TrimSpace(input.SiteName)
	input.SiteURL = strings.TrimSpace(input.SiteURL)
	input.ContactType = strings.TrimSpace(input.ContactType)
	input.Contact = strings.TrimSpace(input.Contact)
	input.Description = strings.TrimSpace(input.Description)
	if !validPartnerSingleLine(input.SiteName, 2, 120) ||
		input.SiteURL != "" && !validPartnerHTTPS(input.SiteURL) ||
		!slices.Contains([]string{PartnerContactWeChat, PartnerContactQQ, PartnerContactEmail, PartnerContactPhone, PartnerContactOther, PartnerContactTelegram}, input.ContactType) ||
		!validPartnerSingleLine(input.Contact, 1, 200) || !validPartnerText(input.Description, 0, 3000) ||
		len(input.Quotes) == 0 || len(input.Quotes) > 20 {
		return PartnerApplicationInput{}, ErrPartnerApplicationInvalid
	}
	if input.ContactType == PartnerContactEmail && !validPartnerEmail(input.Contact) {
		return PartnerApplicationInput{}, ErrPartnerApplicationInvalid
	}

	seenGroups := make(map[string]struct{}, len(input.Quotes))
	for i := range input.Quotes {
		quote := &input.Quotes[i]
		quote.Platform = strings.TrimSpace(quote.Platform)
		quote.Group = strings.TrimSpace(quote.Group)
		quote.Models = strings.TrimSpace(quote.Models)
		quote.Rate = strings.TrimSpace(quote.Rate)
		quote.Description = strings.TrimSpace(quote.Description)
		if !slices.Contains(partnerQuotePlatforms, quote.Platform) ||
			!partnerQuoteRatePattern.MatchString(quote.Rate) || !validPartnerSingleLine(quote.Group, 1, 120) ||
			!validPartnerSingleLine(quote.Models, 0, 500) || !validPartnerText(quote.Description, 0, 500) {
			return PartnerApplicationInput{}, ErrPartnerApplicationInvalid
		}
		rate, err := decimal.NewFromString(quote.Rate)
		if err != nil || rate.IsNegative() || rate.GreaterThan(decimal.NewFromInt(1000)) {
			return PartnerApplicationInput{}, ErrPartnerApplicationInvalid
		}
		key := strings.ToLower(quote.Group)
		if _, exists := seenGroups[key]; exists {
			return PartnerApplicationInput{}, ErrPartnerApplicationInvalid
		}
		seenGroups[key] = struct{}{}
		quote.Rate = rate.String()
	}
	return input, nil
}

func validPartnerText(value string, minLength, maxLength int) bool {
	length := len([]rune(value))
	if length < minLength || length > maxLength {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool {
		return unicode.IsControl(char) && char != '\n' && char != '\t'
	})
}

func validPartnerSingleLine(value string, minLength, maxLength int) bool {
	return validPartnerText(value, minLength, maxLength) && !strings.ContainsAny(value, "\r\n\t")
}

func validPartnerEmail(value string) bool {
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value && len(value) <= 254
}

func validPartnerHTTPS(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Hostname() != "" && parsed.User == nil && parsed.Fragment == "" && len(value) <= 2048
}

func partnerRequestKeyValid(value string) bool {
	if len(value) < 8 || len(value) > 96 {
		return false
	}
	return !strings.ContainsFunc(value, func(char rune) bool {
		return !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_')
	})
}

func partnerApplicationActor(tx *gorm.DB, actorID int, admin bool, checks ...PartnerAdminPermissionCheck) error {
	var actor User
	if err := lockForUpdate(tx).Select("id", "role", "status").First(&actor, actorID).Error; err != nil {
		return ErrPartnerApplicationForbidden
	}
	if actor.Status != common.UserStatusEnabled || !common.IsValidateRole(actor.Role) || admin && actor.Role < common.RoleAdminUser {
		return ErrPartnerApplicationForbidden
	}
	for _, check := range checks {
		if check == nil {
			return ErrPartnerApplicationForbidden
		}
		if err := check(tx, &actor); err != nil {
			return err
		}
	}
	return nil
}

func CreatePartnerApplication(ctx context.Context, userID int, requestKey string, input PartnerApplicationInput) (*PartnerApplication, error) {
	input, err := normalizePartnerApplication(input)
	if err != nil || !partnerRequestKeyValid(requestKey) {
		return nil, ErrPartnerApplicationInvalid
	}
	// Run ordinary validation first; invalid inputs stay invalid in either mode.
	// Do not translate new codes to "other" or silently drop a quote.
	if !common.IM21RolloutActive() && (!isLegacyPartnerContactType(input.ContactType) ||
		slices.ContainsFunc(input.Quotes, func(q PartnerQuote) bool { return !isLegacyPartnerQuotePlatform(q.Platform) })) {
		return nil, common.ErrIM21RolloutLocked
	}
	encoded, err := common.Marshal(input)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(encoded)
	quotes, err := common.Marshal(input.Quotes)
	if err != nil {
		return nil, err
	}
	var row PartnerApplication
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := partnerApplicationActor(tx, userID, false); err != nil {
			return err
		}
		found := tx.Where("user_id = ? AND request_key = ?", userID, requestKey).First(&row)
		if found.Error == nil {
			if row.RequestHash != hex.EncodeToString(hash[:]) {
				return ErrPartnerApplicationConflict
			}
			return nil
		}
		if !errors.Is(found.Error, gorm.ErrRecordNotFound) {
			return found.Error
		}
		settings := DefaultPartnerApplicationSettings()
		if err := lockForUpdate(tx).First(&settings, 1).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if !settings.Enabled {
			return ErrPartnerApplicationsClosed
		}
		var active int64
		if err := tx.Model(&PartnerApplication{}).Where("user_id = ? AND status IN ?", userID, []string{PartnerApplicationSubmitted, PartnerApplicationReviewing}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return ErrPartnerApplicationConflict
		}
		now := common.GetTimestamp()
		row = PartnerApplication{
			UserID: userID, RequestKey: requestKey, RequestHash: hex.EncodeToString(hash[:]), ActiveUserID: &userID,
			SiteName: input.SiteName, SiteURL: input.SiteURL, ContactType: input.ContactType, Contact: input.Contact,
			Description: input.Description, QuotesJSON: string(quotes),
			Status: PartnerApplicationSubmitted, Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Create(&PartnerApplicationEvent{
			ApplicationID: row.ID, ActorID: userID, UserID: userID, Action: "submitted",
			ToStatus: row.Status, CreatedAt: now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return GetPartnerApplication(ctx, userID, row.ID, false)
}

func GetPartnerApplication(ctx context.Context, userID int, id int64, admin bool) (*PartnerApplication, error) {
	var row PartnerApplication
	query := DB.WithContext(ctx).Where("id = ?", id)
	if !admin {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.First(&row).Error; err != nil {
		return nil, err
	}
	if err := common.UnmarshalJsonStr(row.QuotesJSON, &row.Quotes); err != nil {
		return nil, err
	}
	if err := DB.WithContext(ctx).Where("application_id = ?", id).Order("id").Find(&row.Events).Error; err != nil {
		return nil, err
	}
	if !admin {
		row.ReviewNote = ""
		for i := range row.Events {
			row.Events[i].Note = ""
		}
	}
	return &row, nil
}

func ListPartnerApplications(ctx context.Context, userID int, admin bool, status string, offset, limit int) ([]PartnerApplication, int64, error) {
	if status != "" && !slices.Contains([]string{PartnerApplicationSubmitted, PartnerApplicationReviewing, PartnerApplicationApproved, PartnerApplicationRejected, PartnerApplicationCancelled}, status) {
		return nil, 0, ErrPartnerApplicationInvalid
	}
	query := DB.WithContext(ctx).Model(&PartnerApplication{})
	if !admin {
		query = query.Where("user_id = ?", userID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []PartnerApplication
	if err := query.Order("id DESC").Offset(max(0, offset)).Limit(min(max(1, limit), 100)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	for i := range rows {
		if err := common.UnmarshalJsonStr(rows[i].QuotesJSON, &rows[i].Quotes); err != nil {
			return nil, 0, err
		}
		if !admin {
			rows[i].ReviewNote = ""
		}
	}
	return rows, total, nil
}

func ReviewPartnerApplication(ctx context.Context, actorID int, id int64, admin bool, input PartnerApplicationReview, checks ...PartnerAdminPermissionCheck) (*PartnerApplication, error) {
	input.Note = strings.TrimSpace(input.Note)
	if input.ExpectedRevision < 1 || !validPartnerText(input.Note, 0, 1000) {
		return nil, ErrPartnerApplicationInvalid
	}
	var row PartnerApplication
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := partnerApplicationActor(tx, actorID, admin, checks...); err != nil {
			return err
		}
		query := lockForUpdate(tx).Where("id = ?", id)
		if !admin {
			query = query.Where("user_id = ?", actorID)
		}
		if err := query.First(&row).Error; err != nil {
			return err
		}
		if row.Revision != input.ExpectedRevision || !slices.Contains([]string{PartnerApplicationSubmitted, PartnerApplicationReviewing}, row.Status) {
			return ErrPartnerApplicationConflict
		}
		before := row.Status
		switch input.Action {
		case "cancel":
			if admin {
				return ErrPartnerApplicationForbidden
			}
			row.Status = PartnerApplicationCancelled
		case "review":
			if !admin || row.Status != PartnerApplicationSubmitted {
				return ErrPartnerApplicationConflict
			}
			row.Status = PartnerApplicationReviewing
		case "approve", "reject":
			if !admin || input.Note == "" {
				return ErrPartnerApplicationInvalid
			}
			row.Status = PartnerApplicationApproved
			if input.Action == "reject" {
				row.Status = PartnerApplicationRejected
			}
		default:
			return ErrPartnerApplicationInvalid
		}
		if admin {
			row.ReviewNote, row.ReviewedBy = input.Note, actorID
		}
		row.Revision++
		row.UpdatedAt = common.GetTimestamp()
		if row.Status == PartnerApplicationApproved || row.Status == PartnerApplicationRejected || row.Status == PartnerApplicationCancelled {
			row.ActiveUserID = nil
		}
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return tx.Create(&PartnerApplicationEvent{
			ApplicationID: row.ID, ActorID: actorID, UserID: row.UserID, Action: input.Action,
			FromStatus: before, ToStatus: row.Status, Note: input.Note, CreatedAt: row.UpdatedAt,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return GetPartnerApplication(ctx, actorID, row.ID, admin)
}
