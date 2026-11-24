//go:build !enterprise_lane_lab

package middleware

import (
	"context"
	"errors"
	"os"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func EnterpriseLaneGatewayLaunchKeys() []string {
	return []string{"ENTERPRISE_LANE_LAB_ENABLED", "ENTERPRISE_LANE_LAB_STOP", "ENTERPRISE_LANE_LAB_ADMISSION_GLOBAL", "ENTERPRISE_LANE_LAB_ADMISSION_PER_LANE", "ENTERPRISE_LANE_LAB_ADMISSION_WAIT_MS", "ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_GLOBAL", "ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_PER_LANE"}
}

// An ordinary binary cannot interpret lab escrow. Reject explicit lab launch
// intent rather than silently starting it as an ordinary wallet process.
func PrepareEnterpriseLaneGateway(bool) error {
	for _, key := range EnterpriseLaneGatewayLaunchKeys() {
		if value := os.Getenv(key); value != "" && value != "false" {
			return errors.New("enterprise lane lab launch requires the tagged lab binary")
		}
	}
	return nil
}

func ConfigureEnterpriseLaneGateway(ctx context.Context) (bool, error) {
	return false, model.CheckEnterpriseLaneGatewayDisabled(ctx, model.DB)
}
func authorizeEnterpriseLaneGateway(*gin.Context, *model.Token, []string) bool { return false }
func EnterpriseLaneGatewayEnabled() bool                                       { return false }
