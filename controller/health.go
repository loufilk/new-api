package controller

import (
	"context"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// GetHealthz is a process liveness probe. It intentionally avoids database
// and cache dependencies so an orchestrator can distinguish a live process
// from one that is ready to receive traffic.
func GetHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetReadyz checks the dependencies required to serve authenticated and relay
// traffic. The response deliberately omits connection details and component
// error text because this endpoint is reachable through the public API router.
func GetReadyz(c *gin.Context) {
	if model.DB == nil || model.LOG_DB == nil {
		writeReadinessFailure(c)
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()

	sqlDB, err := model.DB.DB()
	if err != nil {
		writeReadinessFailure(c)
		return
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		writeReadinessFailure(c)
		return
	}
	if model.LOG_DB != model.DB {
		logDB, err := model.LOG_DB.DB()
		if err != nil || logDB.PingContext(ctx) != nil {
			writeReadinessFailure(c)
			return
		}
	}

	if common.RedisEnabled {
		if common.RDB == nil || common.RDB.Ping(ctx).Err() != nil {
			writeReadinessFailure(c)
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func writeReadinessFailure(c *gin.Context) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"success": false})
}
