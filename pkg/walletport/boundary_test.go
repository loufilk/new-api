package walletport

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalletportImportsStdlibOnly(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		require.NoError(t, err)
		checked++
		for _, imp := range file.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			firstSegment := strings.SplitN(importPath, "/", 2)[0]
			assert.NotContains(t, firstSegment, ".", "%s imports %q; walletport must remain a stdlib-only leaf", name, importPath)
		}
	}
	require.NotZero(t, checked)
}
