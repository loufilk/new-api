package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"strconv"
)

func GetAgentContributions(c *gin.Context)        { getAgentContributions(c, false) }
func GetAgentContributionsFinance(c *gin.Context) { getAgentContributions(c, true) }
func getAgentContributions(c *gin.Context, finance bool) {
	identity, ok := agentPlatformIdentity(c)
	if !ok {
		writeAgentGovernanceError(c, model.ErrAgentProfilePermission)
		return
	}
	owner := identity.UserID
	if finance {
		var err error
		owner, err = strconv.Atoi(c.Query("agent_user_id"))
		if err != nil || owner <= 0 {
			writeAgentGovernanceError(c, model.ErrAgentGovernanceInvalid)
			return
		}
	} else if raw := c.Query("agent_user_id"); raw != "" {
		requested, err := strconv.Atoi(raw)
		if err != nil || requested != owner {
			writeAgentGovernanceError(c, model.ErrAgentProfilePermission)
			return
		}
	}
	page := redemptionPage(c)
	report, err := model.GetAgentContributions(identity, owner, page.Page, page.PageSize, finance)
	if err != nil {
		writeAgentGovernanceError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}
