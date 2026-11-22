// Code generated from release 04f958fb model structs for migration tests; DO NOT EDIT.
package model

import "gorm.io/gorm"

type a07LegacyRedemption struct {
	CustomerUserID      int            `json:"customer_user_id" gorm:"not null;default:0;index"`
	DeliveryID          int            `json:"delivery_id" gorm:"not null;default:0;index"`
	SourceProtected     bool           `json:"source_protected" gorm:"not null;default:false"`
	BudgetAgentID       int            `json:"budget_agent_id" gorm:"not null;default:0;index"`
	BudgetReserved      bool           `json:"budget_reserved" gorm:"not null;default:false"`
	BudgetVersion       int            `json:"version" gorm:"not null;default:0"`
	CreationOperationID int            `json:"-" gorm:"not null;default:0;index"`
	Id                  int            `json:"id"`
	UserId              int            `json:"user_id"`
	Key                 string         `json:"key" gorm:"type:char(32);uniqueIndex"`
	Status              int            `json:"status" gorm:"default:1"`
	Name                string         `json:"name" gorm:"index"`
	Quota               int            `json:"quota" gorm:"default:100"`
	CreatedTime         int64          `json:"created_time" gorm:"bigint"`
	RedeemedTime        int64          `json:"redeemed_time" gorm:"bigint"`
	Count               int            `json:"count" gorm:"-:all"` // only for api request
	UsedUserId          int            `json:"used_user_id"`
	DeletedAt           gorm.DeletedAt `gorm:"index"`
	ExpiredTime         int64          `json:"expired_time" gorm:"bigint"` // 过期时间，0 表示不过期
}

func (a07LegacyRedemption) TableName() string { return "redemptions" }

type a07LegacyTopUp struct {
	PaymentReceiptID      int64   `json:"payment_receipt_id"`
	PaidProviderReference string  `json:"-" gorm:"type:varchar(255)"`
	PaidMerchantScope     string  `json:"-" gorm:"type:varchar(128)"`
	PaidEnvironment       string  `json:"-" gorm:"type:varchar(16)"`
	PaidCurrency          string  `json:"paid_currency" gorm:"type:varchar(3)"`
	PaidCurrencyExponent  int     `json:"paid_currency_exponent"`
	PaidAmountMinor       int64   `json:"paid_amount_minor,string"`
	Id                    int     `json:"id"`
	UserId                int     `json:"user_id" gorm:"index"`
	Amount                int64   `json:"amount"`
	Money                 float64 `json:"money"`
	TradeNo               string  `json:"trade_no" gorm:"unique;type:varchar(255);index"`
	PaymentMethod         string  `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider       string  `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	CreateTime            int64   `json:"create_time"`
	CompleteTime          int64   `json:"complete_time"`
	Status                string  `json:"status"`
}

func (a07LegacyTopUp) TableName() string { return "top_ups" }
