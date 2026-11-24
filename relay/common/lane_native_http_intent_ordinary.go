//go:build !lane_billing_integration_dev

package common

import "github.com/gin-gonic/gin"

func laneNativeSyntheticHTTPIntentPinned(*gin.Context) bool { return false }
