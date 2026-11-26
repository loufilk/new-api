package setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltinChatPresetsDefaultIsEmptyArray(t *testing.T) {
	require.Empty(t, Chats)
	require.Equal(t, "[]", Chats2JsonString(), "an unset option is an array, not null")
}

func TestChatPresetsInvalidHistoricalOptionHandlingUnchanged(t *testing.T) {
	old := Chats
	t.Cleanup(func() { Chats = old })
	require.NoError(t, UpdateChatsByJsonString(`[{"fixture":"https://example.invalid"}]`))
	require.Error(t, UpdateChatsByJsonString(`{}`))
	require.Equal(t, "[]", Chats2JsonString())
	// Preserve the preexisting explicit-null behavior. The empty default is
	// not a migration or change in how a previously supplied value is read.
	require.NoError(t, UpdateChatsByJsonString("null"))
	require.Equal(t, "null", Chats2JsonString())
}
