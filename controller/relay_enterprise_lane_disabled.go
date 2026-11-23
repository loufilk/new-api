//go:build !enterprise_lane_lab

package controller

import (
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

func maybeRelayEnterpriseLane(*gin.Context, types.RelayFormat) bool { return false }
