package common

import "os"

const IM21RolloutModeEnv = "CORE_IM21_ROLLOUT_MODE"

var ErrIM21RolloutLocked = NewMessage("This change is locked until the im.21 rollout is complete.")

// IM21RolloutActive is intentionally strict: a missing or misspelled value must
// never enable writes that a previous gateway cannot enforce or understand.
func IM21RolloutActive() bool {
	return os.Getenv(IM21RolloutModeEnv) == "active"
}
