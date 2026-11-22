package service

import (
	"github.com/QuantumNous/new-api/setting"
	"strings"
)

// CheckSensitiveText preserves the host keyword policy used by the
// account/group-aware request guard alongside request plugins.
func CheckSensitiveText(text string) (bool, []string) {
	return SensitiveWordContains(text)
}

func SensitiveWordContains(text string) (bool, []string) {
	if len(setting.SensitiveWords) == 0 {
		return false, nil
	}
	if len(text) == 0 {
		return false, nil
	}
	checkText := strings.ToLower(text)
	return AcSearch(checkText, setting.SensitiveWords, true)
}
