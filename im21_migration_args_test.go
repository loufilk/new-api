package main

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestIM21MigratorRejectsNoOpCommands(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"--help"}, {"plugin"}, {"--"}, {"--log-dir"}, {"--log-dir", "--version"}, {"--log-dir=/tmp/qa", "--help"}} {
		if err := validateIM21MigratorArgs(args); err == nil {
			t.Errorf("accepted non-migration args %v", args)
		}
	}
	for _, args := range [][]string{nil, {"--log-dir", "/tmp/qa"}, {"--log-dir=/tmp/qa"}} {
		if err := validateIM21MigratorArgs(args); err != nil {
			t.Errorf("rejected valid args %v: %v", args, err)
		}
	}
}

func TestIM21ManagedModeIgnoresDotenv(t *testing.T) {
	for _, key := range []string{"CORE_IM21_ROLLOUT_MODE", "CORE_IM21_RELEASE_ID", "CORE_IM21_MIGRATION_MODE", "IM21_TEST_FILE_ONLY"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	path := filepath.Join(t.TempDir(), ".env")
	require.NoError(t, os.WriteFile(path, []byte("CORE_IM21_MIGRATION_MODE=migrate\nIM21_TEST_FILE_ONLY=untrusted\n"), 0600))
	t.Setenv("CORE_IM21_ROLLOUT_MODE", "locked")
	require.NoError(t, loadStartupDotenv(path))
	require.Empty(t, os.Getenv("IM21_TEST_FILE_ONLY"))
	require.NoError(t, os.Unsetenv("CORE_IM21_ROLLOUT_MODE"))
	require.Error(t, loadStartupDotenv(path))
}
