package model

import (
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestClientLogDiagnosticsRespectCodeSwitchAndKeepAdminEvidence(t *testing.T) {
	setting := operation_setting.GetLogSetting()
	old := setting.ClientErrorCodesEnabled
	t.Cleanup(func() { setting.ClientErrorCodesEnabled = old })
	for _, enabled := range []bool{true, false} {
		setting.ClientErrorCodesEnabled = enabled
		for _, status := range []int{400, 429, 413} {
			other := NewLogOther()
			other.SetPublic("status_code", status)
			other.SetPublic("error_code", "invalid_request_error")
			original := Log{Type: LogTypeError, Content: "thinking configuration invalid key=secret", Other: other.JSONString()}
			user := original
			formatUserLogs([]*Log{&user}, 0)
			assert.NotContains(t, user.Content, "secret")
			if enabled && status != 413 {
				assert.Contains(t, user.Other, "status_code")
			} else {
				assert.NotContains(t, user.Other, "status_code")
				assert.NotContains(t, user.Other, "error_code")
			}
			admin := original
			FormatAdminLogs([]*Log{&admin})
			assert.Equal(t, original.Content, admin.Content)
			assert.Contains(t, admin.Other, "status_code")
		}
	}
}

func TestErrorLogStructuredContentCannotBypassClientProjection(t *testing.T) {
	for _, kind := range []int{LogTypeError, LogTypeConsume, LogTypeTopup} {
		original := Log{Type: kind, Content: "thinking rejected secret", Other: `{"status_code":400,"content_parts":[{"key":"secret diagnostic"}]}`}
		user := original
		formatUserLogs([]*Log{&user}, 0)
		if kind == LogTypeError {
			assert.NotContains(t, user.Other, "content_parts")
			assert.NotContains(t, user.Content, "secret")
		} else {
			assert.Contains(t, user.Other, "secret diagnostic")
		}
		admin := original
		FormatAdminLogs([]*Log{&admin})
		assert.Contains(t, admin.Other, "secret diagnostic")
	}
}
