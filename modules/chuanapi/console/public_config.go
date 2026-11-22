package console

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// PublicConfig is the anonymous product configuration consumed by the public
// site. It carries only public product state: do not add authentication
// configuration, client identifiers, passkey origins, or secrets to this DTO.
type PublicConfig struct {
	RegisterEnabled      bool    `json:"register_enabled"`
	SystemName           string  `json:"system_name"`
	Version              string  `json:"version"`
	QuotaPerUnit         float64 `json:"quota_per_unit"`
	QuotaDisplayType     string  `json:"quota_display_type"`
	SignupBonusEnabled   bool    `json:"signup_bonus_enabled"`
	SignupBonusQuota     int     `json:"signup_bonus_quota"`
	SignupBonusProvider  string  `json:"signup_bonus_provider"`
	ReferralBonusEnabled bool    `json:"referral_bonus_enabled"`
	ReferralBonusRate    float64 `json:"referral_bonus_rate"`
}

// GetPublicConfig serves the anonymous configuration snapshot for the public
// site. It is the replacement for the product fields that used to be embedded
// in /api/status, which now only exposes console status.
func GetPublicConfig(c *gin.Context) {
	// Match the host status snapshot: option updates mutate these public values
	// under the same lock, so readers must not observe a partial update.
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": PublicConfig{
			RegisterEnabled:      common.RegisterEnabled,
			SystemName:           common.SystemName,
			Version:              common.Version,
			QuotaPerUnit:         common.QuotaPerUnit,
			QuotaDisplayType:     operation_setting.GetQuotaDisplayType(),
			SignupBonusEnabled:   true,
			SignupBonusQuota:     common.QuotaRound(common.LinuxDOSignupBonusUSD * common.QuotaPerUnit),
			SignupBonusProvider:  "linuxdo",
			ReferralBonusEnabled: common.AffiliateRewardEnabled,
			ReferralBonusRate:    common.AffiliateRewardRate,
		},
	})
}
