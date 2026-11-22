package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogOtherScopesAndMerges(t *testing.T) {
	var other LogOther

	assert.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	other.MergePublic(map[string]any{
		"zero": 0,
	})
	assert.True(t, other.SetAdmin("use_channel", []string{"channel-a"}))
	other.MergeAdmin(map[string]any{
		"rejected": false,
	})
	assert.True(t, other.SetRoot("upstream_request_id", "upstream-private"))
	other.MergeRoot(map[string]any{
		"generation": 0,
	})
	assert.True(t, other.SetAudit("method", "POST"))
	other.MergeAudit(map[string]any{
		"success": false,
	})

	require.JSONEq(t, `{
		"request_path": "/v1/chat/completions",
		"zero": 0,
		"admin_info": {
			"use_channel": ["channel-a"],
			"rejected": false
		},
		"root_info": {
			"upstream_request_id": "upstream-private",
			"generation": 0
		},
		"audit_info": {
			"method": "POST",
			"success": false
		}
	}`, other.JSONString())
}

func TestLogOtherRejectsSensitivePublicFields(t *testing.T) {
	other := NewLogOther()

	for _, key := range []string{
		"admin_info",
		"root_info",
		"audit_info",
		"channel_id",
		"channel_name",
		"channel_type",
		"reject_reason",
		"upstream_model_name",
		"po",
		"request_conversion",
		"response_model",
		"billing_model",
		"conversion_diagnostics",
		"conversion_diagnostics_truncated",
		"request_policy",
		"stream_diagnostics",
		"stream_status",
	} {
		assert.False(t, other.SetPublic(key, "must-not-leak"), key)
	}
	other.MergePublic(map[string]any{
		"request_path": "/v1/responses",
		"channel_name": "still-must-not-leak",
		"admin_info":   map[string]any{"secret": true},
	})

	require.JSONEq(t, `{"request_path":"/v1/responses"}`, other.JSONString())
	require.JSONEq(t, `{}`, NewLogOther().JSONString())
}

func TestLogOtherJSONStringDoesNotMutateReceiver(t *testing.T) {
	other := NewLogOther()
	require.True(t, other.SetPublic("request_path", "/v1/chat/completions"))
	require.True(t, other.SetAdmin("rejected", false))

	before := other.Snapshot()
	first := other.JSONString()
	after := other.Snapshot()
	second := other.JSONString()

	require.Equal(t, before, after)
	require.Equal(t, first, second)
}

func TestLegacyUpstreamDiagnosticsAreOnlyVisibleToAdministrators(t *testing.T) {
	legacy := `{"upstream_model_name":"private-model","po":[{"value":"private-provider"}],"request_conversion":"private-route","response_model":{"returned_model":"private-model"},"conversion_diagnostics":["private-detail"],"request_policy":["private-channel"],"stream_diagnostics":{"error":"private-failure"},"stream_status":{"error":"private-failure"},"request_path":"/v1/responses","cache_tokens":9007199254740993,"admin_info":{"upstream_model_name":"scoped-model"},"root_info":{"secret":"root-only"}}`

	assert.JSONEq(t, `{"request_path":"/v1/responses","cache_tokens":9007199254740993}`, formatLogOtherJSON(legacy, logOtherVisibilityUser))
	admin := formatLogOtherJSON(legacy, logOtherVisibilityAdmin)
	assert.JSONEq(t, `{"request_path":"/v1/responses","cache_tokens":9007199254740993,"admin_info":{"upstream_model_name":"scoped-model","po":[{"value":"private-provider"}],"request_conversion":"private-route","response_model":{"returned_model":"private-model"},"conversion_diagnostics":["private-detail"],"request_policy":["private-channel"],"stream_diagnostics":{"error":"private-failure"},"stream_status":{"error":"private-failure"}}}`, admin)
	assert.Equal(t, admin, formatLogOtherJSON(admin, logOtherVisibilityAdmin))
	root := formatLogOtherJSON(legacy, logOtherVisibilityRoot)
	assert.Contains(t, root, `"root_info":{"secret":"root-only"}`)
	assert.JSONEq(t, `{"request_path":"/v1/responses","cache_tokens":9007199254740993}`, formatLogOtherJSON(root, logOtherVisibilityUser))
}
