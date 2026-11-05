package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type blockedTransport struct{ calls atomic.Int32 }

func (transport *blockedTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("network is forbidden")
}

func TestLoadAndScoringNeverUseNetwork(t *testing.T) {
	transport := &blockedTransport{}
	previous := http.DefaultTransport
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	engine := referenceEngine(t)
	_, err := GenerateChallenges()
	require.NoError(t, err)
	result, err := engine.Analyze(referenceSamples(t), true)
	require.NoError(t, err)
	assert.Equal(t, "scored", result.Status)
	for _, path := range []string{"", "https://example.invalid/data/", "http://127.0.0.1:1", t.TempDir()} {
		_, err := LoadEngine(context.Background(), path)
		require.Error(t, err)
	}
	assert.Zero(t, transport.calls.Load(), "neither local loading nor errors may fall back to SDK downloads")
	entries, err := os.ReadDir(os.Getenv("XDG_CACHE_HOME"))
	require.NoError(t, err)
	assert.Empty(t, entries, "offline local loading must not use an SDK cache")
}

func TestPinnedDataTamperFailsClosed(t *testing.T) {
	for _, target := range []string{"unified_bank.json", "shared_detector.bin"} {
		t.Run(target, func(t *testing.T) {
			directory := t.TempDir()
			for _, name := range []string{"unified_bank.json", "shared_detector.bin"} {
				data, err := os.ReadFile(filepath.Join("data", name))
				require.NoError(t, err)
				if name == target {
					data[len(data)-1] ^= 1
				}
				require.NoError(t, os.WriteFile(filepath.Join(directory, name), data, 0o600))
			}
			engine, err := LoadEngine(context.Background(), directory)
			require.ErrorContains(t, err, "checksum mismatch")
			assert.Nil(t, engine)
			// Startup exits before creating any listener with bad reference data.
			socket := filepath.Join(directory, "service.sock")
			require.Error(t, run(context.Background(), socket, directory))
			_, err = os.Lstat(socket)
			assert.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestReferenceRejectsSpecialFiles(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "directory", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "unified_bank.json")
			switch kind {
			case "symlink":
				target, err := filepath.Abs("data/unified_bank.json")
				require.NoError(t, err)
				require.NoError(t, os.Symlink(target, path))
			case "fifo":
				require.NoError(t, syscall.Mkfifo(path, 0o600))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0o700))
			case "oversized":
				file, err := os.Create(path)
				require.NoError(t, err)
				require.NoError(t, file.Truncate(2<<20))
				require.NoError(t, file.Close())
			}
			_, err := LoadEngine(context.Background(), directory)
			require.Error(t, err)
		})
	}
}
