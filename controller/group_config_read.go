package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func groupConfigReadFailure(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": i18n.T(c, i18n.MsgGroupConfigChanged), "error": gin.H{"message": i18n.T(c, i18n.MsgGroupConfigChanged), "type": "api_error", "code": "group_config_changed"}})
}

func beginControllerGroupConfigRead(c *gin.Context) (*model.GroupConfigRead, bool) {
	guard, err := model.BeginRequestGroupConfigRead(c)
	if err != nil {
		groupConfigReadFailure(c)
		return nil, false
	}
	return guard, true
}

func finishControllerGroupConfigRead(c *gin.Context, guard *model.GroupConfigRead) bool {
	if err := guard.Finish(); err != nil {
		groupConfigReadFailure(c)
		return false
	}
	return true
}
