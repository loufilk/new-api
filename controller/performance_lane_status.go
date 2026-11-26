package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type enterpriseLaneInstanceStatus struct {
	BuildProfile     string `json:"build_profile"`
	GatewayEnabled   bool   `json:"gateway_enabled"`
	RuntimeCapable   bool   `json:"runtime_capable"`
	ObservationScope string `json:"observation_scope"`
}

// GetEnterpriseLaneStatus reports only this process's actual build and lab
// state. A working fixture gateway cannot establish production wallet authority,
// fleet status, or permission to perform any write.
func GetEnterpriseLaneStatus(c *gin.Context) {
	common.ApiSuccess(c, enterpriseLaneInstanceStatus{
		BuildProfile:     middleware.EnterpriseLaneBuildProfile(),
		GatewayEnabled:   middleware.EnterpriseLaneGatewayEnabled(),
		RuntimeCapable:   model.LaneStoreRuntimeCapable,
		ObservationScope: "current_instance",
	})
}
