package setting

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoGroupSnapshotDoesNotExposeMutableRoutingState(t *testing.T) {
	previous := AutoGroups2JsonString()
	t.Cleanup(func() { require.NoError(t, UpdateAutoGroupsByJsonString(previous)) })
	require.NoError(t, UpdateAutoGroupsByJsonString(`["primary","fallback"]`))

	snapshot := GetAutoGroups()
	snapshot[0] = "changed-by-caller"
	assert.True(t, ContainsAutoGroup("primary"))
	assert.False(t, ContainsAutoGroup("changed-by-caller"))
	assert.JSONEq(t, `["primary","fallback"]`, AutoGroups2JsonString())

	require.NoError(t, UpdateAutoGroupsByJsonString(`["replacement"]`))
	assert.Equal(t, []string{"changed-by-caller", "fallback"}, snapshot, "a previous snapshot must remain detached after replacement")
	assert.Equal(t, []string{"replacement"}, GetAutoGroups())
}

func TestAutoGroupInvalidUpdatePreservesPublishedSnapshot(t *testing.T) {
	previous := AutoGroups2JsonString()
	t.Cleanup(func() { require.NoError(t, UpdateAutoGroupsByJsonString(previous)) })
	require.NoError(t, UpdateAutoGroupsByJsonString(`["primary","fallback"]`))
	for _, invalid := range []string{`["partial",123]`, `["unfinished"`, `{"group":"wrong-shape"}`} {
		require.Error(t, UpdateAutoGroupsByJsonString(invalid))
		assert.Equal(t, []string{"primary", "fallback"}, GetAutoGroups())
		assert.JSONEq(t, `["primary","fallback"]`, AutoGroups2JsonString())
	}
	require.NoError(t, UpdateAutoGroupsByJsonString(`[]`))
	assert.Empty(t, GetAutoGroups())
	assert.Equal(t, `[]`, AutoGroups2JsonString())
}

func TestUpdateMaxTokenAutoGroupsAcceptsAnyPositiveInteger(t *testing.T) {
	original := GetMaxTokenAutoGroups()
	t.Cleanup(func() {
		require.NoError(t, UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", original)))
	})

	require.NoError(t, UpdateMaxTokenAutoGroups("123456"))
	assert.Equal(t, 123456, GetMaxTokenAutoGroups())
}

func TestUpdateMaxTokenAutoGroupsRejectsInvalidValuesWithoutChangingState(t *testing.T) {
	original := GetMaxTokenAutoGroups()
	for _, value := range []string{"", "0", "-1", "1.5", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, UpdateMaxTokenAutoGroups(value))
			assert.Equal(t, original, GetMaxTokenAutoGroups())
		})
	}
}
