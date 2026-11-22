package extension

// boundary_test.go 守卫 extension 的依赖方向：注册表是通用机制包，只能依赖标准库
// 与框架（gorm/gin），不得依赖任何宿主业务包。一旦这里 import 了 model / service /
// router / chuanapi，依赖方向就会反转 —— 而 chuanapi 之所以作为独立的 composition
// adapter 存在，就是为了承载那条 extension + model 的边。

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

// forbiddenHostPackages are the host packages that must never appear in an
// extension import. They are matched as full import paths, so a future package
// whose name merely contains one of these words is not falsely rejected.
var forbiddenHostPackages = []string{
	"github.com/QuantumNous/new-api/model",
	"github.com/QuantumNous/new-api/service",
	"github.com/QuantumNous/new-api/router",
	"github.com/QuantumNous/new-api/controller",
	"github.com/QuantumNous/new-api/chuanapi",
}

// allowedNonStdlibImports are the framework packages the descriptor types are
// defined against: DataStep.Run takes a *gorm.DB and RouteSet.Register takes a
// *gin.Engine. Everything else outside the standard library is a new dependency
// that must be reviewed rather than added silently.
var allowedNonStdlibImports = map[string]bool{
	"gorm.io/gorm":             true,
	"github.com/gin-gonic/gin": true,
}

func TestExtensionDoesNotDependOnHostPackages(t *testing.T) {
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
			for _, forbidden := range forbiddenHostPackages {
				assert.False(t,
					importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/"),
					"%s imports %q — extension 必须保持通用，宿主业务依赖只能出现在 chuanapi/ 适配层",
					name, importPath)
			}
			// 标准库导入路径的首段不含 "."；任何带域名的路径都是外部依赖。
			if !strings.Contains(strings.SplitN(importPath, "/", 2)[0], ".") {
				continue
			}
			assert.True(t, allowedNonStdlibImports[importPath],
				"%s imports %q — extension 只允许 gorm/gin 两个框架依赖", name, importPath)
		}
	}
	require.NotZero(t, checked, "no non-test Go files found; boundary guard would silently pass")
}
