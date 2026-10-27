package setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPublicModelOwnerNormalizationAndFallback(t *testing.T) {
	original := GetPublicModelOwnedBy()
	t.Cleanup(func() { LoadPublicModelOwnedBy(original) })
	for _, tc := range []struct {
		name, input, want string
		invalid           bool
	}{
		{"empty", "", "chuanapi", false},
		{"blank", " \t\r\n", "chuanapi", false},
		{"trim", "  My site  ", "My site", false},
		{"unicode", "  川 API  ", "川 API", false},
		{"control", "site\nprovider", "chuanapi", true},
		{"bidi", "site\u202e", "chuanapi", true},
		{"zero width", "site\u200b", "chuanapi", true},
		{"long", strings.Repeat("x", 129), "chuanapi", true},
		{"invalid utf8", string([]byte{0xff}), "chuanapi", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized, err := NormalizePublicModelOwnedBy(tc.input)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.want, normalized)
			}
			LoadPublicModelOwnedBy(tc.input)
			assert.Equal(t, tc.want, GetPublicModelOwnedBy())
		})
	}
}

func TestSensitiveExemptionNormalizationAndFailClosedReload(t *testing.T) {
	original := SensitiveCheckExemptGroupsToString()
	t.Cleanup(func() { LoadSensitiveCheckExemptGroups(original) })
	normalized, err := NormalizeSensitiveCheckExemptGroups(" 破甲 \r\n\nVIP\n破甲\n VIP \n")
	require.NoError(t, err)
	assert.Equal(t, "破甲\nVIP", normalized)
	LoadSensitiveCheckExemptGroups(normalized)
	assert.True(t, IsSensitiveCheckExemptGroup("破甲"))
	assert.True(t, IsSensitiveCheckExemptGroup("VIP"))
	assert.False(t, IsSensitiveCheckExemptGroup("vip"), "group names are exact")
	assert.False(t, IsSensitiveCheckExemptGroup(""))
	assert.False(t, IsSensitiveCheckExemptGroup("auto"))
	for _, value := range []string{"auto", "*", "破甲\nauto", "bad\x00group", "bad\u200bgroup", "bad\u202egroup", string([]byte{0xff}), strings.Repeat("x", 129)} {
		_, err := NormalizeSensitiveCheckExemptGroups(value)
		require.Error(t, err, value)
		LoadSensitiveCheckExemptGroups(value)
		assert.Empty(t, SensitiveCheckExemptGroupsToString())
		assert.False(t, IsSensitiveCheckExemptGroup("破甲"), "invalid reload must revoke old exemptions")
	}
}
