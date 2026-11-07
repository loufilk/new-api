package controller

import (
	"errors"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type newUserBenefitSettingsResponse struct {
	Enabled         bool   `json:"enabled"`
	PayAmountCNY    string `json:"pay_amount_cny"`
	CreditAmountCNY string `json:"credit_amount_cny"`
	Version         int64  `json:"version"`
}

func newUserBenefitSettingsView(config *model.NewUserBenefitConfig) newUserBenefitSettingsResponse {
	return newUserBenefitSettingsResponse{
		Enabled: config.Enabled, Version: config.Version,
		PayAmountCNY:    model.FormatNewUserBenefitCNY(config.PayAmountCents),
		CreditAmountCNY: model.FormatNewUserBenefitCNY(config.CreditAmountCents),
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
	common.ApiSuccess(c, newUserBenefitSettingsView(config))
}

func UpdateNewUserBenefitSettings(c *gin.Context) {
	var request struct {
		Enabled         *bool   `json:"enabled"`
		PayAmountCNY    *string `json:"pay_amount_cny"`
		CreditAmountCNY *string `json:"credit_amount_cny"`
		Version         *int64  `json:"version"`
	}
	if err := common.DecodeJsonStrict(http.MaxBytesReader(c.Writer, c.Request.Body, 2048), &request); err != nil ||
		request.Enabled == nil || request.PayAmountCNY == nil || request.CreditAmountCNY == nil || request.Version == nil {
		newUserBenefitSettingsError(c, http.StatusBadRequest, "new_user_benefit_invalid_config", model.ErrNewUserBenefitInvalidConfig.Error())
		return
	}
	payCents, payErr := model.ParseNewUserBenefitCNY(*request.PayAmountCNY)
	creditCents, creditErr := model.ParseNewUserBenefitCNY(*request.CreditAmountCNY)
	if payErr != nil || creditErr != nil {
		newUserBenefitSettingsError(c, http.StatusBadRequest, "new_user_benefit_invalid_config", model.ErrNewUserBenefitInvalidConfig.Error())
		return
	}
	config, err := model.UpdateNewUserBenefitConfig(c.Request.Context(), *request.Version, c.GetInt("id"), model.NewUserBenefitConfig{
		Enabled: *request.Enabled, PayAmountCents: payCents, CreditAmountCents: creditCents,
	})
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
	common.ApiSuccess(c, newUserBenefitSettingsView(config))
}
