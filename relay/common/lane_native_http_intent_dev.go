//go:build lane_billing_integration_dev

package common

import "github.com/gin-gonic/gin"

const laneNativeSyntheticIntentKey = "billing_lane_synthetic_http_intent"

// PinLaneNativeSyntheticHTTPIntent is called by the tagged server-owned quote
// arm. The request remains owned even before a durable bind returns. This is
// not authorization to send, and is separate from the one-use attempt marker.
func PinLaneNativeSyntheticHTTPIntent(c *gin.Context) {
	if c != nil {
		c.Set(laneNativeSyntheticIntentKey, true)
	}
}

func LaneNativeSyntheticHTTPIntentPinned(c *gin.Context) bool {
	return c != nil && c.GetBool(laneNativeSyntheticIntentKey)
}

func laneNativeSyntheticHTTPIntentPinned(c *gin.Context) bool {
	return LaneNativeSyntheticHTTPIntentPinned(c)
}
