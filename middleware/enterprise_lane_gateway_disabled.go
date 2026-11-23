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
	return []string{"ENTERPRISE_LANE_LAB_ENABLED", "ENTERPRISE_LANE_LAB_STOP"}
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

func ConfigureEnterpriseLaneGateway(context.Context) (bool, error)             { return false, nil }
func authorizeEnterpriseLaneGateway(*gin.Context, *model.Token, []string) bool { return false }
func EnterpriseLaneGatewayEnabled() bool                                       { return false }
