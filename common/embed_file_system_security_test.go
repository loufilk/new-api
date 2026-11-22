package common

import (
	"embed"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/frontend-asset-fixture
var frontendContractFiles embed.FS

func TestEmbeddedFrontendOnlyExposesRegularAssets(t *testing.T) {
	fs := EmbedFolder(frontendContractFiles, "testdata/frontend-asset-fixture")
	for _, path := range []string{"/", "/static", "/static/", "/static/js", "/static/js/", "/static/js/missing.js", "/static/js/../fixture.deadbeef.js"} {
		t.Run(path, func(t *testing.T) {
			assert.False(t, fs.Exists("/", path), "directory/traversal/missing paths must reach the guarded router fallback")
		})
	}
	for _, path := range []string{"/index.html", "/static/js/fixture.deadbeef.js"} {
		t.Run(path, func(t *testing.T) {
			assert.True(t, fs.Exists("/", path))
			file, err := fs.Open(path)
			require.NoError(t, err)
			defer file.Close()
			data, err := io.ReadAll(file)
			require.NoError(t, err)
			assert.NotEmpty(t, data)
		})
	}
}
