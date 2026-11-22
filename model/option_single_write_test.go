package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestSingleOptionFailedWriteDoesNotPublish(t *testing.T) {
	previous := DB
	t.Cleanup(func() { DB = previous })
	tx := DB.Begin()
	require.NoError(t, tx.Error)
	require.NoError(t, tx.Rollback().Error)
	DB = tx
	common.OptionMapRWMutex.RLock()
	before, existed := common.OptionMap["ServerAddress"]
	common.OptionMapRWMutex.RUnlock()
	require.Error(t, UpdateOption("ServerAddress", "https://example.invalid"))
	common.OptionMapRWMutex.RLock()
	after, existsNow := common.OptionMap["ServerAddress"]
	common.OptionMapRWMutex.RUnlock()
	require.Equal(t, existed, existsNow)
	require.Equal(t, before, after)
}
