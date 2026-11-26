package middleware

import "os"

const (
	laneEnabledEnv             = "ENTERPRISE_LANE_LAB_ENABLED"
	laneStopEnv                = "ENTERPRISE_LANE_LAB_STOP"
	laneTokenIDEnv             = "ENTERPRISE_LANE_LAB_TOKEN_ID"
	laneCountEnv               = "ENTERPRISE_LANE_LAB_COUNT"
	lanePaidBudgetEnv          = "ENTERPRISE_LANE_LAB_PAID_PER_LANE"
	laneTokenBudgetEnv         = "ENTERPRISE_LANE_LAB_TOKEN_PER_LANE"
	laneChannelEnv             = "ENTERPRISE_LANE_LAB_CHANNEL_ID"
	laneModelEnv               = "ENTERPRISE_LANE_LAB_MODEL"
	laneAdmissionGlobalEnv     = "ENTERPRISE_LANE_LAB_ADMISSION_GLOBAL"
	laneAdmissionPerLaneEnv    = "ENTERPRISE_LANE_LAB_ADMISSION_PER_LANE"
	laneAdmissionWaitMSEnv     = "ENTERPRISE_LANE_LAB_ADMISSION_WAIT_MS"
	laneAdmissionQueueTotalEnv = "ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_GLOBAL"
	laneAdmissionQueueLaneEnv  = "ENTERPRISE_LANE_LAB_ADMISSION_QUEUE_PER_LANE"
)

func EnterpriseLaneGatewayLaunchKeys() []string {
	return []string{laneEnabledEnv, laneStopEnv, laneTokenIDEnv, laneCountEnv, lanePaidBudgetEnv, laneTokenBudgetEnv, laneChannelEnv, laneModelEnv, laneAdmissionGlobalEnv, laneAdmissionPerLaneEnv, laneAdmissionWaitMSEnv, laneAdmissionQueueTotalEnv, laneAdmissionQueueLaneEnv}
}

// The binary variants must see the same environment and must not mistake a
// partly applied lab manifest for a safe ordinary launch. The only permitted
// values without an active manifest are unset options and explicit false flags.
func enterpriseLaneLaunchRequested() bool {
	for _, key := range EnterpriseLaneGatewayLaunchKeys() {
		value := os.Getenv(key)
		if value != "" && !((key == laneEnabledEnv || key == laneStopEnv) && value == "false") {
			return true
		}
	}
	return false
}
