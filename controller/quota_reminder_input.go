package controller

import (
	"bytes"
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// A value type intentionally distinguishes absent from explicit null. A pointer
// cannot do so: both would silently become nil during JSON binding.
type quotaReminderThresholdInput struct {
	value   float64
	present bool
}

func (threshold *quotaReminderThresholdInput) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("reminder threshold cannot be null")
	}
	var value float64
	if err := common.Unmarshal(data, &value); err != nil {
		return err
	}
	if err := operation_setting.ValidateQuotaReminderProfileThreshold(value); err != nil {
		return err
	}
	threshold.value, threshold.present = value, true
	return nil
}
