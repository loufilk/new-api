package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFormatUserLogsStripsQuotaSaturation verifies the admin-only quota
// saturation marker (nested under other.admin_info) is removed for non-admin
// log views, since formatUserLogs strips the whole admin_info object.
func TestFormatUserLogsStripsQuotaSaturation(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"model_price": 0.004,
		"admin_info": map[string]any{
			"quota_saturation": map[string]any{
				"op":      "QuotaFromDecimal",
				"kind":    "overflow",
				"clamped": common.MaxQuota,
			},
		},
	})
	logs := []*Log{{Other: other}}

	formatUserLogs(logs, 0)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	_, hasAdminInfo := parsed["admin_info"]
	require.False(t, hasAdminInfo, "admin_info (and nested quota_saturation) must be stripped for non-admin views")
	// Non-admin billing fields remain visible.
	require.Contains(t, parsed, "model_price")
}

func TestTaskPluginLogVisibilityIsRoleSeparated(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"model_price": 1.25,
		"admin_info": map[string]any{
			"task_plugin": map[string]any{
				"key":     "document-parser",
				"name":    "Document Parser",
				"version": "1.2.3",
			},
		},
		"root_info": map[string]any{
			"upstream_task_id": "upstream-private",
			"task_plugin": map[string]any{
				"generation": 42,
			},
		},
	})

	t.Run("user", func(t *testing.T) {
		logs := []*Log{{Other: other}}
		formatUserLogs(logs, 0)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.NotContains(t, parsed, "admin_info")
		assert.NotContains(t, parsed, "root_info")
		assert.Equal(t, 1.25, parsed["model_price"])
	})

	t.Run("admin", func(t *testing.T) {
		logs := []*Log{{Other: other}}
		FormatAdminLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Contains(t, parsed, "admin_info")
		assert.NotContains(t, parsed, "root_info")
	})

	t.Run("root", func(t *testing.T) {
		logs := []*Log{{Other: other}}
		FormatRootLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Contains(t, parsed, "admin_info")
		assert.Contains(t, parsed, "root_info")
	})
}

func TestLegacyLogOtherVisibilityIsRoleSeparated(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"request_path":  "/v1/chat/completions",
		"channel_id":    202,
		"channel_name":  "legacy-secret-channel",
		"channel_type":  1,
		"reject_reason": "legacy-policy-rejection",
		"admin_info": map[string]any{
			"existing_admin_field": "preserved",
		},
		"root_info": map[string]any{
			"upstream_request_id": "upstream-private",
		},
		"audit_info": map[string]any{
			"method": "POST",
		},
	})

	t.Run("user", func(t *testing.T) {
		logs := []*Log{{
			Id:                99,
			ChannelId:         77,
			ChannelName:       "resolved-secret-channel",
			UpstreamRequestId: "upstream-request-secret",
			Other:             other,
		}}

		formatUserLogs(logs, 10)

		assert.Equal(t, 11, logs[0].Id)
		assert.Zero(t, logs[0].ChannelId, "channel identity is not part of the user projection")
		assert.Empty(t, logs[0].ChannelName)
		assert.Empty(t, logs[0].UpstreamRequestId, "upstream request identity is not part of the user projection")
		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, "/v1/chat/completions", parsed["request_path"])
		for _, key := range []string{
			"channel_id",
			"channel_name",
			"channel_type",
			"reject_reason",
			"admin_info",
			"root_info",
			"audit_info",
		} {
			assert.NotContains(t, parsed, key)
		}
	})

	t.Run("admin", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatAdminLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, "legacy-secret-channel", parsed["channel_name"])
		assert.NotContains(t, parsed, "reject_reason")
		assert.NotContains(t, parsed, "root_info")
		assert.Contains(t, parsed, "audit_info")
		adminInfo, ok := parsed["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "preserved", adminInfo["existing_admin_field"])
		assert.Equal(t, "legacy-policy-rejection", adminInfo["reject_reason"])
	})

	t.Run("root", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatRootLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, "legacy-secret-channel", parsed["channel_name"])
		assert.NotContains(t, parsed, "reject_reason")
		assert.Contains(t, parsed, "root_info")
		assert.Contains(t, parsed, "audit_info")
		adminInfo, ok := parsed["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "preserved", adminInfo["existing_admin_field"])
		assert.Equal(t, "legacy-policy-rejection", adminInfo["reject_reason"])
	})
}

func TestLegacyRejectReasonDoesNotOverrideScopedValue(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"reject_reason": "legacy-value",
		"admin_info": map[string]any{
			"reject_reason": "scoped-value",
		},
	})
	logs := []*Log{{Other: other}}

	FormatRootLogs(logs)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, parsed, "reject_reason")
	adminInfo, ok := parsed["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "scoped-value", adminInfo["reject_reason"])
}

func TestLegacyRejectReasonHandlesNullAdminInfo(t *testing.T) {
	logs := []*Log{{Other: `{"reject_reason":"legacy-value","admin_info":null}`}}

	FormatAdminLogs(logs)

	parsed, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, parsed, "reject_reason")
	adminInfo, ok := parsed["admin_info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "legacy-value", adminInfo["reject_reason"])
}

func TestLogFormattingPreservesLargeIntegerLexemes(t *testing.T) {
	const other = `{"public_id":9007199254740993,"admin_info":{"admin_id":9007199254740995},"root_info":{"generation":18446744073709551615}}`

	t.Run("user", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		formatUserLogs(logs, 0)

		assert.Contains(t, logs[0].Other, `"public_id":9007199254740993`)
		assert.NotContains(t, logs[0].Other, "admin_id")
		assert.NotContains(t, logs[0].Other, "generation")
	})

	t.Run("admin", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatAdminLogs(logs)

		assert.Contains(t, logs[0].Other, `"public_id":9007199254740993`)
		assert.Contains(t, logs[0].Other, `"admin_id":9007199254740995`)
		assert.NotContains(t, logs[0].Other, "generation")
	})

	t.Run("root", func(t *testing.T) {
		logs := []*Log{{Other: other}}

		FormatRootLogs(logs)

		assert.Equal(t, other, logs[0].Other)
	})

	t.Run("unprivileged", func(t *testing.T) {
		const unprivileged = `{"public_id":9007199254740993,"model_price":0.004}`

		userLogs := []*Log{{Other: unprivileged}}
		formatUserLogs(userLogs, 0)
		assert.Equal(t, unprivileged, userLogs[0].Other)

		adminLogs := []*Log{{Other: unprivileged}}
		FormatAdminLogs(adminLogs)
		assert.Equal(t, unprivileged, adminLogs[0].Other)
	})
}

// TestWalletCostVisibleToUserButPrivilegedMetadataIsNot pins the ChuanAPI
// wallet contract against the upstream LogOther permission isolation: wallet
// cost fields are public (log owners may see them) while channel identity,
// reject reasons, plugin provenance and root diagnostics stay privileged.
func TestWalletCostVisibleToUserButPrivilegedMetadataIsNot(t *testing.T) {
	other := NewLogOther()
	require.True(t, other.SetPublic("wallet_quota_deducted", 60))
	require.True(t, other.SetPublic("wallet_cost", map[string]int{"paid": 18, "bonus": 42}))
	require.True(t, other.SetPublic("model_price", 0.02))
	require.True(t, other.SetAdmin("reject_reason", "blocked by channel policy"))
	require.True(t, other.SetAdmin("use_channel", []string{"7"}))
	require.True(t, other.SetAdmin("task_plugin", map[string]interface{}{"key": "hailuo", "version": "1.1.1"}))
	require.True(t, other.SetRoot("upstream_task_id", "upstream-private"))
	require.True(t, other.SetRoot("node_name", "node-a"))

	stored := other.JSONString()

	t.Run("user", func(t *testing.T) {
		logs := []*Log{{Other: stored}}
		formatUserLogs(logs, 0)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, float64(60), parsed["wallet_quota_deducted"])
		assert.Equal(t, map[string]interface{}{"paid": float64(18), "bonus": float64(42)}, parsed["wallet_cost"])
		assert.Equal(t, 0.02, parsed["model_price"])
		for _, key := range []string{"admin_info", "root_info", "audit_info", "reject_reason", "channel_id", "channel_name", "channel_type"} {
			assert.NotContains(t, parsed, key)
		}
	})

	t.Run("admin", func(t *testing.T) {
		logs := []*Log{{Other: stored}}
		FormatAdminLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, float64(60), parsed["wallet_quota_deducted"])
		require.Contains(t, parsed, "admin_info")
		assert.NotContains(t, parsed, "root_info")
		adminInfo, ok := parsed["admin_info"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "blocked by channel policy", adminInfo["reject_reason"])
	})

	t.Run("root", func(t *testing.T) {
		logs := []*Log{{Other: stored}}
		FormatRootLogs(logs)

		parsed, err := common.StrToMap(logs[0].Other)
		require.NoError(t, err)
		assert.Equal(t, float64(60), parsed["wallet_quota_deducted"])
		require.Contains(t, parsed, "admin_info")
		require.Contains(t, parsed, "root_info")
	})
}

// TestFormatUserLogsScrubsHistoricalErrorDiagnostics pins the ordinary-user
// projection for stored error records: raw upstream messages, channel identity,
// upstream request identity and provider-supplied error codes must not reach
// the owner, while administrators keep the untouched stored record.
func TestFormatUserLogsScrubsHistoricalErrorDiagnostics(t *testing.T) {
	other := common.MapToJsonStr(map[string]any{
		"error_type":  "bad_response_status_code",
		"error_code":  "private-provider-code-42",
		"status_code": 502,
	})
	stored := []*Log{{
		Id:                41,
		Type:              LogTypeError,
		Content:           "status_code=502, upstream https://private.example.com/v1?token=secret rejected model private-provider-model",
		ChannelId:         909,
		ChannelName:       "resolved-private-channel",
		UpstreamRequestId: "upstream-request-private",
		Other:             other,
	}}

	formatUserLogs(stored, 0)

	assert.Equal(t, "upstream request failed", stored[0].Content)
	assert.Zero(t, stored[0].ChannelId)
	assert.Empty(t, stored[0].ChannelName)
	assert.Empty(t, stored[0].UpstreamRequestId)
	parsed, err := common.StrToMap(stored[0].Other)
	require.NoError(t, err)
	assert.Equal(t, "bad_response_status_code", parsed["error_type"], "stable classifications remain visible")
	assert.NotContains(t, parsed, "error_code", "provider-supplied codes are dropped from user projections")
	assert.NotContains(t, parsed, "private-provider-code-42")

	adminLogs := []*Log{{
		Id:                41,
		Type:              LogTypeError,
		Content:           "status_code=502, upstream https://private.example.com/v1?token=secret rejected model private-provider-model",
		ChannelId:         909,
		ChannelName:       "resolved-private-channel",
		UpstreamRequestId: "upstream-request-private",
		Other:             other,
	}}
	FormatAdminLogs(adminLogs)
	assert.Contains(t, adminLogs[0].Content, "private-provider-model", "administrators keep stored diagnostics")
	assert.Equal(t, 909, adminLogs[0].ChannelId)
	assert.Equal(t, "resolved-private-channel", adminLogs[0].ChannelName)
	assert.Equal(t, "upstream-request-private", adminLogs[0].UpstreamRequestId)
}

func TestFormatUserLogsKeepsConsumeContent(t *testing.T) {
	stored := []*Log{{
		Type:    LogTypeConsume,
		Content: "模型倍率 1.00，分组倍率 1.00, 钱包扣费 100",
	}}
	formatUserLogs(stored, 0)
	assert.Equal(t, "模型倍率 1.00，分组倍率 1.00, 钱包扣费 100", stored[0].Content)
}
