package operation_setting

import (
	"errors"
	"math"
	"strconv"
)

// MaxQuotaReminderThreshold bounds browser/API round trips to the JavaScript
// exact-integer range. This is notification configuration, not a wallet bound.
// Profile thresholds remain float64 for legacy fractional-quota compatibility.
const MaxQuotaReminderThreshold = float64(1<<53 - 1)

func ValidateQuotaReminderProfileThreshold(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > MaxQuotaReminderThreshold {
		return errors.New("reminder threshold must be a finite non-negative number within the supported range")
	}
	return nil
}

// ParseQuotaReminderThreshold validates the site option's integer wire format
// before database/cache/runtime mutation, including when loading legacy rows.
func ParseQuotaReminderThreshold(value string) (int, error) {
	number, err := strconv.ParseInt(value, 10, strconv.IntSize)
	if err != nil || number < 0 || number > (1<<53-1) {
		return 0, errors.New("site reminder threshold must be a non-negative integer within the supported range")
	}
	return int(number), nil
}
