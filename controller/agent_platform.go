package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func agentPlatformIdentity(c *gin.Context) (model.AuthSessionIdentity, bool) {
	c.Header("Cache-Control", "no-store")
	identity, ok := middleware.GetSessionAuthIdentity(c)
	if !ok || identity.UserID != c.GetInt("id") || c.GetBool("use_access_token") {
		return model.AuthSessionIdentity{}, false
	}
	if _, pat := model.ParseAccessTokenSessionID(identity.SessionID); pat {
		return model.AuthSessionIdentity{}, false
	}
	return identity, true
}

func GetAgentPlatformSelf(c *gin.Context) {
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	profile, err := model.GetAgentProfile(user.Id)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	capabilities, err := model.GetAgentSimpleCapabilities(user, profile)
	if err != nil {
		writeAgentSimpleError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"profile": profile, "capabilities": capabilities})
}
