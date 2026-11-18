package model

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type PartnerQuote struct {
	Group       string `json:"group"`
	Models      string `json:"models"`
	Rate        string `json:"rate"`
	Description string `json:"description"`
}

var partnerQuoteRatePattern = regexp.MustCompile(`^\d{1,4}(?:\.\d{1,6})?$`)

type PartnerApplication struct {
	ID            int64           `json:"id" gorm:"primaryKey"`
	UserID        int             `json:"user_id" gorm:"index;uniqueIndex:idx_partner_user_request,priority:1"`
	RequestKey    string          `json:"-" gorm:"type:varchar(96);uniqueIndex:idx_partner_user_request,priority:2"`
	RequestHash   string          `json:"-" gorm:"type:varchar(64)"`
	SiteName      string          `json:"site_name" gorm:"type:varchar(120)"`
	SiteURL       string          `json:"site_url" gorm:"type:varchar(2048)"`
	ContactName   string          `json:"contact_name" gorm:"type:varchar(120)"`
	ContactEmail  string          `json:"contact_email" gorm:"type:varchar(254)"`
	ContactMethod string          `json:"contact_method" gorm:"type:varchar(200)"`
	Description   string          `json:"description" gorm:"type:text"`
	QuotesJSON    string          `json:"-" gorm:"type:text"`
	Quotes        []PartnerQuote  `json:"quotes" gorm:"-"`
	Status        string          `json:"status" gorm:"type:varchar(24);index"`
	Revision      int64           `json:"revision"`
	ReviewNote    string          `json:"review_note" gorm:"type:varchar(1000)"`
	ReviewedBy    int             `json:"reviewed_by"`
	CreatedAt     int64           `json:"created_at" gorm:"autoCreateTime:false"`
	UpdatedAt     int64           `json:"updated_at" gorm:"autoUpdateTime:false"`
	Events        []CommerceAudit `json:"events,omitempty" gorm:"-"`
}

type PartnerCreate struct {
	SiteName      string         `json:"site_name"`
	SiteURL       string         `json:"site_url"`
	ContactName   string         `json:"contact_name"`
	ContactEmail  string         `json:"contact_email"`
	ContactMethod string         `json:"contact_method"`
	Description   string         `json:"description"`
	Quotes        []PartnerQuote `json:"quotes"`
}

func CreatePartnerApplication(ctx context.Context, userID int, requestKey string, input PartnerCreate) (*PartnerApplication, error) {
	input.SiteName, input.SiteURL = strings.TrimSpace(input.SiteName), strings.TrimSpace(input.SiteURL)
	input.ContactName, input.ContactEmail = strings.TrimSpace(input.ContactName), strings.TrimSpace(input.ContactEmail)
	input.ContactMethod, input.Description = strings.TrimSpace(input.ContactMethod), strings.TrimSpace(input.Description)
	if !commerceRequestKey(requestKey) || !commerceText(input.SiteName, 2, 120) || !commerceHTTPS(input.SiteURL) || !commerceText(input.ContactName, 1, 120) || !commerceEmail(input.ContactEmail) || !commerceText(input.ContactMethod, 0, 200) || !commerceText(input.Description, 10, 3000) || len(input.Quotes) < 1 || len(input.Quotes) > 20 {
		return nil, ErrCommerceInvalid
	}
	seen := map[string]bool{}
	for i := range input.Quotes {
		quote := &input.Quotes[i]
		quote.Group, quote.Models, quote.Description, quote.Rate = strings.TrimSpace(quote.Group), strings.TrimSpace(quote.Models), strings.TrimSpace(quote.Description), strings.TrimSpace(quote.Rate)
		if !partnerQuoteRatePattern.MatchString(quote.Rate) {
			return nil, ErrCommerceInvalid
		}
		rate, err := decimal.NewFromString(quote.Rate)
		if err != nil || rate.LessThan(decimal.Zero) || rate.GreaterThan(decimal.NewFromInt(1000)) || !commerceText(quote.Group, 1, 120) || !commerceText(quote.Models, 1, 500) || !commerceText(quote.Description, 0, 500) || seen[quote.Group] {
			return nil, ErrCommerceInvalid
		}
		quote.Rate = rate.String()
		seen[quote.Group] = true
	}
	hash, err := commerceRequestHash(input)
	if err != nil {
		return nil, err
	}
	quotes, err := common.Marshal(input.Quotes)
	if err != nil {
		return nil, err
	}
	var row PartnerApplication
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := commerceActor(tx, userID, false); err != nil {
			return err
		}
		found := tx.Where("user_id = ? AND request_key = ?", userID, requestKey).First(&row)
		if found.Error == nil {
			if row.RequestHash != hash {
				return ErrCommerceConflict
			}
			return nil
		}
		if !errors.Is(found.Error, gorm.ErrRecordNotFound) {
			return found.Error
		}
		config := DefaultCommerceConfig()
		if err := tx.Where("id = ?", 1).Limit(1).Find(&config).Error; err != nil {
			return err
		}
		if !config.PartnerEnabled {
			return ErrCommerceDisabled
		}
		var pending int64
		if err := tx.Model(&PartnerApplication{}).Where("user_id = ? AND status IN ?", userID, []string{"submitted", "reviewing"}).Count(&pending).Error; err != nil {
			return err
		}
		if pending > 0 {
			return ErrCommerceConflict
		}
		now := common.GetTimestamp()
		row = PartnerApplication{UserID: userID, RequestKey: requestKey, RequestHash: hash, SiteName: input.SiteName, SiteURL: input.SiteURL, ContactName: input.ContactName, ContactEmail: input.ContactEmail, ContactMethod: input.ContactMethod, Description: input.Description, QuotesJSON: string(quotes), Status: "submitted", Revision: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Create(&CommerceAudit{Kind: "partner", ResourceID: row.ID, ActorID: userID, UserID: userID, Action: "submitted", ToStatus: row.Status, CreatedAt: now}).Error
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
	if err := DB.WithContext(ctx).Where("kind = ? AND resource_id = ?", "partner", id).Order("id").Find(&row.Events).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func ListPartnerApplications(ctx context.Context, userID int, admin bool, status string, offset, limit int) ([]PartnerApplication, int64, error) {
	items := make([]PartnerApplication, 0)
	query := DB.WithContext(ctx).Model(&PartnerApplication{})
	if !admin {
		query = query.Where("user_id = ?", userID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id DESC").Offset(max(0, offset)).Limit(min(max(1, limit), 100)).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	for i := range items {
		if err := common.UnmarshalJsonStr(items[i].QuotesJSON, &items[i].Quotes); err != nil {
			return nil, 0, err
		}
	}
	return items, count, nil
}

func ReviewPartnerApplication(ctx context.Context, actorID int, id int64, admin bool, input CommerceReview, permissions ...AdminUserPermissionCheck) (*PartnerApplication, error) {
	input.Note = strings.TrimSpace(input.Note)
	if input.ExpectedRevision < 1 || !commerceText(input.Note, 0, 1000) {
		return nil, ErrCommerceInvalid
	}
	var row PartnerApplication
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := commerceActor(tx, actorID, admin, permissions...); err != nil {
			return err
		}
		query := lockForUpdate(tx).Where("id = ?", id)
		if !admin {
			query = query.Where("user_id = ?", actorID)
		}
		if err := query.First(&row).Error; err != nil {
			return err
		}
		if row.Revision != input.ExpectedRevision {
			return ErrCommerceConflict
		}
		if !slices.Contains([]string{"submitted", "reviewing"}, row.Status) {
			return ErrCommerceConflict
		}
		before := row.Status
		switch input.Action {
		case "cancel":
			if admin {
				return ErrCommerceForbidden
			}
			row.Status = "cancelled"
		case "review":
			if !admin || row.Status != "submitted" {
				return ErrCommerceConflict
			}
			row.Status = "reviewing"
		case "approve", "reject":
			if !admin || input.Note == "" {
				return ErrCommerceInvalid
			}
			row.Status = "approved"
			if input.Action == "reject" {
				row.Status = "rejected"
			}
		default:
			return ErrCommerceInvalid
		}
		if admin {
			row.ReviewNote, row.ReviewedBy = input.Note, actorID
		}
		row.Revision++
		row.UpdatedAt = common.GetTimestamp()
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return tx.Create(&CommerceAudit{Kind: "partner", ResourceID: row.ID, ActorID: actorID, UserID: row.UserID, Action: input.Action, FromStatus: before, ToStatus: row.Status, Note: input.Note, CreatedAt: row.UpdatedAt}).Error
	})
	if err != nil {
		return nil, err
	}
	return GetPartnerApplication(ctx, actorID, row.ID, admin)
}
