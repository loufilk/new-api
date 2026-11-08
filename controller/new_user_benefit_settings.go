package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type newUserBenefitSettingsResponse struct {
	RequireVerifiedEmail        bool `json:"require_verified_email"`
	RequireTurnstile            bool `json:"require_turnstile"`
	NetworkLimitEnabled         bool `json:"network_limit_enabled"`
	PaymentIdentityCheckEnabled bool `json:"payment_identity_check_enabled"`

	Enabled                bool   `json:"enabled"`
	PayAmountCNY           string `json:"pay_amount_cny"`
	CreditAmountCNY        string `json:"credit_amount_cny"`
	Version                int64  `json:"version"`
	MaxTotalOrders         int64  `json:"max_total_orders"`
	MaxOrdersPer24h        int64  `json:"max_orders_per_24h"`
	MaxOrdersPerNetwork24h int64  `json:"max_orders_per_network_24h"`
	TotalBonusBudgetCNY    string `json:"total_bonus_budget_cny"`
	ReservedOrders         int64  `json:"reserved_orders"`
	ReservedBonusCNY       string `json:"reserved_bonus_cny"`
	ReservedOrders24h      int64  `json:"reserved_orders_24h"`
	TurnstileReady         bool   `json:"turnstile_ready"`
}

func newUserBenefitSettingsView(config *model.NewUserBenefitConfig, usage *model.NewUserBenefitUsage) newUserBenefitSettingsResponse {
	return newUserBenefitSettingsResponse{
		RequireVerifiedEmail: config.RequireVerifiedEmail, RequireTurnstile: config.RequireTurnstile,
		NetworkLimitEnabled: config.NetworkLimitEnabled, PaymentIdentityCheckEnabled: config.PaymentIdentityCheckEnabled,
		Enabled: config.Enabled, Version: config.Version,
		PayAmountCNY:    model.FormatNewUserBenefitCNY(config.PayAmountCents),
		CreditAmountCNY: model.FormatNewUserBenefitCNY(config.CreditAmountCents),
		MaxTotalOrders:  config.MaxTotalOrders, MaxOrdersPer24h: config.MaxOrdersPer24h,
		MaxOrdersPerNetwork24h: config.MaxOrdersPerNetwork24h,
		TotalBonusBudgetCNY:    model.FormatNewUserBenefitCNY(config.TotalBonusBudgetCents),
		ReservedOrders:         usage.TotalOrders, ReservedBonusCNY: model.FormatNewUserBenefitCNY(usage.TotalBonusCents),
		ReservedOrders24h: usage.Orders24h, TurnstileReady: snapshotNewUserBenefitVerification().Ready,
	}
}

func newUserBenefitSettingsError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"success": false, "code": code, "message": message})
}

func GetNewUserBenefitSettings(c *gin.Context) {
	config, err := model.GetNewUserBenefitConfig(c.Request.Context())
	if err != nil {
		newUserBenefitSettingsError(c, http.StatusServiceUnavailable, "new_user_benefit_unavailable", "New user benefit is temporarily unavailable.")
		return
	}
	usage, err := model.GetNewUserBenefitUsage(c.Request.Context())
	if err != nil {
		newUserBenefitSettingsError(c, http.StatusServiceUnavailable, "new_user_benefit_unavailable", "New user benefit is temporarily unavailable.")
		return
	}
	common.ApiSuccess(c, newUserBenefitSettingsView(config, usage))
}

func UpdateNewUserBenefitSettings(c *gin.Context) {
	var request struct {
		RequireVerifiedEmail        *bool `json:"require_verified_email"`
		RequireTurnstile            *bool `json:"require_turnstile"`
		NetworkLimitEnabled         *bool `json:"network_limit_enabled"`
		PaymentIdentityCheckEnabled *bool `json:"payment_identity_check_enabled"`

		Enabled                *bool   `json:"enabled"`
		PayAmountCNY           *string `json:"pay_amount_cny"`
		CreditAmountCNY        *string `json:"credit_amount_cny"`
		Version                *int64  `json:"version"`
		MaxTotalOrders         *int64  `json:"max_total_orders"`
		MaxOrdersPer24h        *int64  `json:"max_orders_per_24h"`
		MaxOrdersPerNetwork24h *int64  `json:"max_orders_per_network_24h"`
		TotalBonusBudgetCNY    *string `json:"total_bonus_budget_cny"`
	}
	if err := common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 2048), &request); err != nil ||
		request.Enabled == nil || request.PayAmountCNY == nil || request.CreditAmountCNY == nil || request.Version == nil ||
		*request.Version < 0 || *request.Version >= 9_007_199_254_740_991 ||
		request.MaxTotalOrders == nil || request.MaxOrdersPer24h == nil || request.MaxOrdersPerNetwork24h == nil || request.TotalBonusBudgetCNY == nil {
		newUserBenefitSettingsError(c, http.StatusBadRequest, "new_user_benefit_invalid_config", model.ErrNewUserBenefitInvalidConfig.Error())
		return
	}
	payCents, payErr := model.ParseNewUserBenefitCNY(*request.PayAmountCNY)
	creditCents, creditErr := model.ParseNewUserBenefitCNY(*request.CreditAmountCNY)
	budgetCents, budgetErr := model.ParseNewUserBenefitCNY(*request.TotalBonusBudgetCNY)
	if payErr != nil || creditErr != nil || budgetErr != nil {
		newUserBenefitSettingsError(c, http.StatusBadRequest, "new_user_benefit_invalid_config", model.ErrNewUserBenefitInvalidConfig.Error())
		return
	}
	// Omitted new controls from a previously shipped administrator UI preserve
	// the existing behavior instead of silently switching risk checks off.
	requireEmail, requireTurnstile, networkLimit, paymentIdentity := true, true, true, false
	if request.RequireVerifiedEmail != nil {
		requireEmail = *request.RequireVerifiedEmail
	}
	if request.RequireTurnstile != nil {
		requireTurnstile = *request.RequireTurnstile
	}
	if request.NetworkLimitEnabled != nil {
		networkLimit = *request.NetworkLimitEnabled
	}
	if request.PaymentIdentityCheckEnabled != nil {
		paymentIdentity = *request.PaymentIdentityCheckEnabled
	}
	next := model.NewUserBenefitConfig{
		RequireVerifiedEmail: requireEmail, RequireTurnstile: requireTurnstile,
		NetworkLimitEnabled: networkLimit, PaymentIdentityCheckEnabled: paymentIdentity,
		Enabled: *request.Enabled, PayAmountCents: payCents, CreditAmountCents: creditCents,
		MaxTotalOrders: *request.MaxTotalOrders, MaxOrdersPer24h: *request.MaxOrdersPer24h,
		MaxOrdersPerNetwork24h: *request.MaxOrdersPerNetwork24h, TotalBonusBudgetCents: budgetCents,
	}
	if err := model.ValidateNewUserBenefitConfig(next); err != nil {
		newUserBenefitSettingsError(c, http.StatusBadRequest, "new_user_benefit_invalid_config", err.Error())
		return
	}
	if request.RequireVerifiedEmail == nil || request.RequireTurnstile == nil || request.NetworkLimitEnabled == nil || request.PaymentIdentityCheckEnabled == nil {
		current, err := model.GetNewUserBenefitConfig(c.Request.Context())
		if err != nil {
			newUserBenefitSettingsError(c, http.StatusServiceUnavailable, "new_user_benefit_unavailable", "New user benefit is temporarily unavailable.")
			return
		}
		if request.RequireVerifiedEmail == nil {
			next.RequireVerifiedEmail = current.RequireVerifiedEmail
		}
		if request.RequireTurnstile == nil {
			next.RequireTurnstile = current.RequireTurnstile
		}
		if request.NetworkLimitEnabled == nil {
			next.NetworkLimitEnabled = current.NetworkLimitEnabled
		}
		if request.PaymentIdentityCheckEnabled == nil {
			next.PaymentIdentityCheckEnabled = current.PaymentIdentityCheckEnabled
		}
	}
	if next.Enabled && next.RequireTurnstile && !snapshotNewUserBenefitVerification().Ready {
		newUserBenefitSettingsError(c, http.StatusConflict, "new_user_benefit_verification_unavailable", "Configure and enable Turnstile before enabling the new user benefit")
		return
	}
	if next.Enabled && next.PaymentIdentityCheckEnabled && !newUserBenefitIdentityGatewayReady(snapshotNewUserBenefitPayment()) {
		newUserBenefitSettingsError(c, http.StatusConflict, "new_user_benefit_payment_identity_unavailable", "New user benefit payment identity checks require an HTTPS Alipay gateway")
		return
	}
	// Collect the read-only usage snapshot before the mutation so a statistics
	// read failure cannot report an unsuccessful save after settings committed.
	usage, err := model.GetNewUserBenefitUsage(c.Request.Context())
	if err != nil {
		newUserBenefitSettingsError(c, http.StatusServiceUnavailable, "new_user_benefit_unavailable", "New user benefit is temporarily unavailable.")
		return
	}
	config, err := model.UpdateNewUserBenefitConfig(c.Request.Context(), *request.Version, c.GetInt("id"), next)
	if errors.Is(err, model.ErrNewUserBenefitConfigChanged) {
		newUserBenefitSettingsError(c, http.StatusConflict, "new_user_benefit_config_changed", err.Error())
		return
	}
	if errors.Is(err, model.ErrNewUserBenefitInvalidConfig) {
		newUserBenefitSettingsError(c, http.StatusBadRequest, "new_user_benefit_invalid_config", err.Error())
		return
	}
	if err != nil {
		newUserBenefitSettingsError(c, http.StatusServiceUnavailable, "new_user_benefit_unavailable", "New user benefit is temporarily unavailable.")
		return
	}
	common.ApiSuccess(c, newUserBenefitSettingsView(config, usage))
}
