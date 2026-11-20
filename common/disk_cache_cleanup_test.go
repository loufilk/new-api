package common

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiskCacheCleanupKeepsLiveReplayAndAccounting(t *testing.T) {
	previous := GetDiskCacheConfig()
	SetDiskCacheConfig(DiskCacheConfig{Path: t.TempDir()})
	t.Cleanup(func() { SetDiskCacheConfig(previous) })
	before := GetDiskCacheStats()
	const payload = "fixture body"
	live, err := newDiskStorage([]byte(payload), GetDiskCacheDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = live.Close() })

	orphan, err := WriteDiskCacheFile(DiskCacheTypeBody, []byte("old orphan"))
	require.NoError(t, err)
	unowned := filepath.Join(GetDiskCacheDir(), "not-owned.tmp")
	require.NoError(t, os.WriteFile(unowned, []byte("fixture"), 0600))
	old := time.Now().Add(-time.Hour)
	for _, path := range []string{live.filePath, orphan, unowned} {
		require.NoError(t, os.Chtimes(path, old, old))
	}
	require.NoError(t, CleanupOldDiskCacheFiles(10*time.Minute))
	_, err = os.Stat(orphan)
	assert.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(unowned)
	require.NoError(t, err)
	reader, err := live.NewReader()
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, payload, string(data))
	active := GetDiskCacheStats()
	assert.Equal(t, before.ActiveDiskFiles+1, active.ActiveDiskFiles)
	assert.Equal(t, before.CurrentDiskUsageBytes+int64(len(payload)), active.CurrentDiskUsageBytes)
	require.NoError(t, live.Close())
	assert.Equal(t, before.ActiveDiskFiles, GetDiskCacheStats().ActiveDiskFiles)
	assert.Equal(t, before.CurrentDiskUsageBytes, GetDiskCacheStats().CurrentDiskUsageBytes)
}
