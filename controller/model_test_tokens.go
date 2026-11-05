package controller

import (
	"crypto/sha256"
	"fmt"
	"net"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type modelTestTokenMetadata struct {
	EligibleModels     []string `json:"eligible_models"`
	ID                 int      `json:"id"`
	Name               string   `json:"name"`
	Group              string   `json:"group"`
	ModelLimitsEnabled bool     `json:"model_limits_enabled"`
	ModelLimits        []string `json:"model_limits"`
}

// Concrete groups only: automatic/smart keys may select another group in the
// production router and are deliberately not coerced into a concrete key.
func modelTestTokenAllowed(token *model.Token, userID int, userGroup, group, modelName, clientIP string) bool {
	if token == nil || token.UserId != userID || token.Id <= 0 || token.Status != common.TokenStatusEnabled || token.ExpiredTime != -1 && token.ExpiredTime <= time.Now().Unix() || !token.UnlimitedQuota && token.RemainQuota <= 0 || token.SmartRouting || token.Group == "auto" {
		return false
	}
	effective := token.Group
	if effective == "" {
		effective = userGroup
	}
	if effective != group || !service.IsUserSelectableGroup(userGroup, group) || service.ValidateTokenRoutingScope(userGroup, token) != nil {
		return false
	}
	if token.ModelLimitsEnabled && modelName != "" && !middleware.TokenModelLimitAllows(token.GetModelLimitsMap(), modelName) {
		return false
	}
	if limits := token.GetIpLimits(); len(limits) > 0 {
		ip := net.ParseIP(clientIP)
		if ip == nil || !common.IsIpInCIDRList(ip, limits) {
			return false
		}
	}
	return true
}

func selectedModelTestToken(c *gin.Context, selected modelTestSelection) (*model.Token, string, error) {
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil || user == nil || user.Status != common.UserStatusEnabled {
		return nil, "", fmt.Errorf("token_unavailable")
	}
	clientIP := c.GetString("model_test_client_ip")
	if clientIP == "" {
		clientIP = c.ClientIP()
	}
	token, err := model.GetTokenByIds(selected.TokenID, user.Id)
	if err != nil || !modelTestTokenAllowed(token, user.Id, user.Group, selected.Group, selected.Model, clientIP) {
		return nil, "", fmt.Errorf("token_unavailable")
	}
	// Includes credential identity and authorization settings, never quota counters.
	identity, err := common.Marshal([]any{token.Id, token.UserId, token.Key, token.CacheRevision, token.Group, token.ExpiredTime, token.UnlimitedQuota, token.ModelLimitsEnabled, token.ModelLimits, token.AllowIps, user.Group})
	if err != nil {
		return nil, "", err
	}
	return token, fmt.Sprintf("%x", sha256.Sum256(identity)), nil
}

func modelTestTokens(c *gin.Context, catalogue map[string][]string) (map[string][]modelTestTokenMetadata, error) {
	result := make(map[string][]modelTestTokenMetadata)
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		return nil, err
	}
	var tokens []model.Token
	// Project safe columns at the database boundary; no credential is loaded.
	err = model.DB.Select("id", "user_id", "name", "group", "status", "expired_time", "remain_quota", "unlimited_quota", "model_limits_enabled", "model_limits", "allow_ips", "smart_routing", "auto_groups").Where("user_id = ?", user.Id).Order("id DESC").Find(&tokens).Error
	if err != nil {
		return nil, err
	}
	for group := range catalogue {
		result[group] = []modelTestTokenMetadata{}
		for i := range tokens {
			token := &tokens[i]
			if modelTestTokenAllowed(token, user.Id, user.Group, group, "", c.ClientIP()) {
				eligibleModels := make([]string, 0)
				for _, name := range catalogue[group] {
					if modelTestTokenAllowed(token, user.Id, user.Group, group, name, c.ClientIP()) {
						eligibleModels = append(eligibleModels, name)
					}
				}
				result[group] = append(result[group], modelTestTokenMetadata{ID: token.Id, Name: token.Name, Group: group, ModelLimitsEnabled: token.ModelLimitsEnabled, ModelLimits: token.GetModelLimits(), EligibleModels: eligibleModels})
			}
		}
	}
	return result, nil
}
