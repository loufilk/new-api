//go:build enterprise_lane_lab

package model

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var ErrEnterpriseLaneUsage = errors.New("enterprise lane requires immutable, measured provider usage")

// EnterpriseLaneUsage records already validated provider facts on the original
// financial owner. The gateway is responsible for confirming physical final
// provider usage, routing and frozen pricing; this type isn't billing evidence
// merely because a caller constructed it. ObservedAt is server UTC Unix time.
// The zero value is reserved for the model-only lab lifecycle, not gateway use.
type EnterpriseLaneUsage struct {
	ChannelID        int    `gorm:"not null;default:0"`
	PromptTokens     int    `gorm:"type:bigint;not null;default:0"`
	CompletionTokens int    `gorm:"type:bigint;not null;default:0"`
	Model            string `gorm:"type:varchar(128);not null;default:''"`
	Group            string `gorm:"type:varchar(128);not null;default:''"`
	ObservedAt       int64  `gorm:"type:bigint;not null;default:0"`
}

func (usage EnterpriseLaneUsage) valid() bool {
	if usage.ChannelID <= 0 || usage.ObservedAt <= 0 || usage.PromptTokens < 0 || usage.PromptTokens > common.MaxQuota ||
		usage.CompletionTokens < 0 || usage.CompletionTokens > common.MaxQuota-usage.PromptTokens ||
		usage.PromptTokens+usage.CompletionTokens == 0 || len(usage.Model) == 0 || len(usage.Model) > 128 ||
		len(usage.Group) == 0 || len(usage.Group) > 128 || !utf8.ValidString(usage.Group) || strings.TrimSpace(usage.Group) != usage.Group {
		return false
	}
	for _, ch := range usage.Model {
		if ch < 33 || ch > 126 {
			return false
		}
	}
	for _, ch := range usage.Group {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

// SettleEnterpriseLaneWithUsage freezes the actual amount and measured facts
// before adjusting the escrow. A successful terminal transaction marks those
// exact facts pending for detached projection without touching usage counters
// on the shared user, channel or token rows.
func SettleEnterpriseLaneWithUsage(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string, actual int, usage EnterpriseLaneUsage) (EnterpriseLaneRequest, error) {
	if !usage.valid() {
		return EnterpriseLaneRequest{}, ErrEnterpriseLaneUsage
	}
	return settleEnterpriseLane(ctx, db, identity, requestID, actual, usage)
}

// ResumeEnterpriseLaneObservedUsage retries only a request with already frozen,
// valid measured usage. It cannot infer usage, dispatch or grant a refund from
// a held or delivery-unknown request. Same-ID retry also covers a lost terminal
// COMMIT acknowledgment, returning an unchanged settled owner.
func ResumeEnterpriseLaneObservedUsage(ctx context.Context, db *gorm.DB, identity EnterpriseLaneIdentity, requestID string) (EnterpriseLaneRequest, error) {
	return completeEnterpriseLaneObserved(ctx, db, identity, requestID, nil)
}
