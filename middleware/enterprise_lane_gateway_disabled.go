//go:build !enterprise_lane_lab

package middleware

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// An ordinary binary cannot interpret lab escrow. Reject explicit lab launch
// intent rather than silently starting it as an ordinary wallet process.
func PrepareEnterpriseLaneGateway(bool) error {
	if enterpriseLaneLaunchRequested() {
		return errors.New("enterprise lane lab launch requires the tagged lab binary")
	}
	return nil
}

func ConfigureEnterpriseLaneGateway(ctx context.Context) (bool, error) {
	return false, model.CheckEnterpriseLaneGatewayDisabled(ctx, model.DB)
}
func authorizeEnterpriseLaneGateway(*gin.Context, *model.Token, []string) bool { return false }
func EnterpriseLaneGatewayEnabled() bool                                       { return false }

func EnterpriseLaneBuildProfile() string { return "ordinary" }
