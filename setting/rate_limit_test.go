package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimitGroupInvalidUpdatePreservesPublishedPolicy(t *testing.T) {
	previous := ModelRequestRateLimitGroup2JSONString()
	t.Cleanup(func() { require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(previous)) })
	require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(`{"stable":[10,5]}`))
	for _, invalid := range []string{`{"stable":[1,"bad"]}`, `{"partial":[2,3]`, `[1,2]`} {
		require.Error(t, UpdateModelRequestRateLimitGroupByJSONString(invalid))
		total, success, found := GetGroupRateLimit("stable")
		assert.True(t, found)
		assert.Equal(t, 10, total)
		assert.Equal(t, 5, success)
		assert.JSONEq(t, `{"stable":[10,5]}`, ModelRequestRateLimitGroup2JSONString())
	}
	require.NoError(t, UpdateModelRequestRateLimitGroupByJSONString(`{}`))
	_, _, found := GetGroupRateLimit("stable")
	assert.False(t, found)
}
