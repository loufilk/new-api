package controller

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// This response is intentionally distinct from validation and from an expired
// admin session. Do not encourage callers to immediately retry a locked write.
func im21RolloutLocked(c *gin.Context) {
	common.ApiErrorStatus(c, http.StatusConflict, common.ErrIM21RolloutLocked, gin.H{"code": "IM21_ROLLOUT_LOCKED"})
}
