package moneyport

// boundary_test.go 守卫 moneyport 的叶子包约束：能力接口包必须只依赖标准库。
// 一旦这里 import 了 model / service / relay 等包，依赖方向就会反转，
// BillingSession 的解耦（Modularization Train M2）随即失效。

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

func TestMoneyportImportsStdlibOnly(t *testing.T) {
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
			// 标准库导入路径的首段不含 "."；任何带域名的路径都是外部依赖。
			firstSegment := strings.SplitN(importPath, "/", 2)[0]
			assert.NotContains(t, firstSegment, ".",
				"%s imports %q — moneyport 必须是只依赖标准库的叶子包", name, importPath)
		}
	}
	require.NotZero(t, checked, "no non-test Go files found; boundary guard would silently pass")
}
