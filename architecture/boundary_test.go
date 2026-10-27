package architecture

import (
	"go/ast"
	"go/parser"
	goscanner "go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/QuantumNous/new-api"

var frontendConcreteChuanAPIBaseline = map[string]struct{}{}

var coreBrandLiteralBaseline = map[string]struct{}{}

// hostPaymentProviderFiles are the provider protocol/transport bodies the user
// authorized back into host ownership. They must exist exactly once here; the
// extension module contributes only the binding model and a route descriptor.
var hostPaymentProviderFiles = []string{
	"controller/topup_waffo.go",
	"controller/topup_waffo_pancake.go",
	"controller/subscription_payment_waffo_pancake.go",
	"service/waffo_pancake.go",
}

func TestGoCoreDoesNotImportConcreteChuanAPI(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var offenders []string

	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		rel := slashRel(t, root, path)
		if rel == "main.go" || strings.HasPrefix(rel, "chuanapi/") || strings.HasPrefix(rel, "modules/chuanapi/") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if importPath == modulePath+"/chuanapi" || strings.HasPrefix(importPath, modulePath+"/chuanapi/") ||
				importPath == modulePath+"/modules/chuanapi" || strings.HasPrefix(importPath, modulePath+"/modules/chuanapi/") {
				offenders = append(offenders, rel+" -> "+importPath)
			}
		}
		return nil
	}))

	assert.Empty(t, offenders, "core production Go code must not import concrete ChuanAPI modules; main.go is the composition-root exception")
}

func TestServiceProductionDoesNotOwnWalletFunding(t *testing.T) {
	root := repoRoot(t)
	serviceRoot := filepath.Join(root, "service")
	fset := token.NewFileSet()
	var offenders []string

	require.NoError(t, filepath.WalkDir(serviceRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			typeSpec, ok := node.(*ast.TypeSpec)
			if ok && typeSpec.Name != nil && typeSpec.Name.Name == "WalletFunding" {
				offenders = append(offenders, slashRel(t, root, path))
			}
			return true
		})
		return nil
	}))

	assert.Empty(t, offenders, "production service must depend on moneyport funding capabilities, not own WalletFunding")
}

func TestAsyncImageOwnershipStaysInGenericModule(t *testing.T) {
	root := repoRoot(t)
	for _, legacy := range []string{
		"controller/image_async.go",
		"service/image_async.go",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacy)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must not return to a root business package", legacy)
	}

	relayRouter, err := os.ReadFile(filepath.Join(root, "router", "relay-router.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(relayRouter), "/v1/images/tasks",
		"async image task routes must stay owned by modules/imagegen")
}

func TestChuanAPICompositionRootStaysThin(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "chuanapi", "register.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	require.NoError(t, err)

	var offenders []string
	for _, imp := range file.Imports {
		importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
		require.NoError(t, unquoteErr)
		if importPath == "fmt" || importPath == modulePath+"/extension" || importPath == modulePath+"/pkg/moneyport" || strings.HasPrefix(importPath, modulePath+"/modules/chuanapi/") {
			continue
		}
		offenders = append(offenders, importPath)
	}
	assert.Empty(t, offenders, "chuanapi/register.go must remain a thin composition root: no controller/model/service/common business imports")
}

// TestPaymentProviderRoutesAreRegisteredByHostRouter pins the authorized
// post-normalization ownership: provider URLs are declared once in the host
// router's payment route file and handed to the extension registry through
// RouteScopes.PaymentRoutes, so the module never rebuilds middleware policy or
// duplicates the route table.
func TestPaymentProviderRoutesAreRegisteredByHostRouter(t *testing.T) {
	root := repoRoot(t)

	paymentRoutesPath := filepath.Join(root, "router", "payment_routes.go")
	paymentRoutes, err := os.ReadFile(paymentRoutesPath)
	require.NoError(t, err, "host router must own the payment route registrar")
	paymentRoutesSource := string(paymentRoutes)
	for _, handler := range []string{
		"controller.WaffoWebhook",
		"controller.WaffoPancakeWebhook",
		"controller.RequestWaffoAmount",
		"controller.RequestWaffoPay",
		"controller.RequestWaffoPancakeAmount",
		"controller.RequestWaffoPancakePay",
		"controller.SubscriptionRequestWaffoPancakePay",
		"controller.ListWaffoPancakeCatalog",
		"controller.CreateWaffoPancakePair",
		"controller.SaveWaffoPancake",
		"controller.CreateWaffoPancakeSubscriptionProduct",
		"controller.ListWaffoPancakeSubscriptionProductOptions",
	} {
		assert.Contains(t, paymentRoutesSource, handler, "payment route registrar must mount %s", handler)
	}

	apiRouter, err := os.ReadFile(filepath.Join(root, "router", "api-router.go"))
	require.NoError(t, err)
	assert.Contains(t, string(apiRouter), "scopes.PaymentRoutes = RegisterPaymentRoutes",
		"SetApiRouter must expose the host payment route registrar to extension route sets")

	moduleRegister, err := os.ReadFile(filepath.Join(root, "modules", "chuanapi", "payment", "register.go"))
	require.NoError(t, err)
	moduleRegisterSource := string(moduleRegister)
	assert.Contains(t, moduleRegisterSource, "scopes.PaymentRoutes(scopes)",
		"payment route descriptor must delegate to the host registrar")
	for _, forbidden := range []string{`"/waffo`, "middleware."} {
		assert.NotContains(t, moduleRegisterSource, forbidden,
			"module route descriptor must not rebuild provider URLs or middleware policy (%s)", forbidden)
	}
}

// TestPaymentProviderOwnershipReturnsToHost verifies the provider protocol
// bodies exist in the host owner locations exactly once and that the module no
// longer carries a second copy or imports the provider SDKs. The binding schema
// stays module-declared through pkg/paymentcore aliases.
func TestPaymentProviderOwnershipReturnsToHost(t *testing.T) {
	root := repoRoot(t)
	for _, hostFile := range hostPaymentProviderFiles {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(hostFile)))
		assert.NoError(t, err, "%s must exist in its host owner package", hostFile)
	}
	for _, legacyModuleFile := range []string{
		"modules/chuanapi/payment/waffo_handler.go",
		"modules/chuanapi/payment/pancake_handler.go",
		"modules/chuanapi/payment/pancake_service.go",
		"modules/chuanapi/payment/pancake_subscription_handler.go",
		"modules/chuanapi/payment/order_binding.go",
		"modules/chuanapi/payment/catalog.go",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacyModuleFile)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must not keep a duplicate provider implementation", legacyModuleFile)
	}

	moduleRoot := filepath.Join(root, "modules", "chuanapi", "payment")
	fset := token.NewFileSet()
	var offenders []string
	require.NoError(t, filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			switch {
			case importPath == modulePath+"/controller" || strings.HasPrefix(importPath, modulePath+"/controller/"):
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			case importPath == modulePath+"/service" || strings.HasPrefix(importPath, modulePath+"/service/"):
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			case strings.HasPrefix(importPath, "github.com/waffo-com/"):
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "ChuanAPI payment module must not import host controller/service or provider SDKs after the protocol bodies returned to the host")
}

// TestPaymentBindingSchemaStaysRegisteredByModule keeps the migration contract:
// the module model set still contributes the chuanapi_payment_order_bindings
// snapshot (aliased from pkg/paymentcore) in the host migration order.
func TestPaymentBindingSchemaStaysRegisteredByModule(t *testing.T) {
	root := repoRoot(t)
	modelsFile, err := os.ReadFile(filepath.Join(root, "modules", "chuanapi", "payment", "models.go"))
	require.NoError(t, err)
	assert.Contains(t, string(modelsFile), "type PaymentOrderBinding = paymentcore.PaymentOrderBinding",
		"the module must alias the generic binding type instead of duplicating the schema")

	registerFile, err := os.ReadFile(filepath.Join(root, "modules", "chuanapi", "payment", "register.go"))
	require.NoError(t, err)
	assert.Contains(t, string(registerFile), "Models:    []any{&PaymentOrderBinding{}}",
		"the module model set must keep registering the payment binding table")

	_, err = os.Stat(filepath.Join(root, "pkg", "paymentcore", "order_binding.go"))
	assert.NoError(t, err, "pkg/paymentcore must own the generic binding implementation")
}

func TestChuanAPIConsoleDoesNotImportHostControllerOrService(t *testing.T) {
	root := repoRoot(t)
	moduleRoot := filepath.Join(root, "modules", "chuanapi", "console")
	fset := token.NewFileSet()
	var offenders []string
	require.NoError(t, filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			return parseErr
		}
		for _, imp := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(imp.Path.Value)
			if unquoteErr != nil {
				return unquoteErr
			}
			if importPath == modulePath+"/controller" || strings.HasPrefix(importPath, modulePath+"/controller/") ||
				importPath == modulePath+"/service" || strings.HasPrefix(importPath, modulePath+"/service/") {
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "ChuanAPI console must consume shared model/setting projections instead of host controller/service runtime logic")
}

func TestChuanAPIRouteModulesUseHostOwnedAPIScopes(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"modules/chuanapi/payment/register.go",
		"modules/chuanapi/wallet/routes/register.go",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		require.NoError(t, err)
		source := string(data)
		assert.Contains(t, source, "RegisterScoped:", "%s must consume host route scopes", rel)
		for _, forbidden := range []string{
			`Group("/api")`,
			"gzip.Gzip(",
			"AccessTokenAudit()",
			"BodyStorageCleanup()",
			"GlobalAPIRateLimit()",
		} {
			assert.NotContains(t, source, forbidden,
				"%s must not reconstruct host API middleware policy (%s)", rel, forbidden)
		}
	}

	// The payment route set forwards to the host registrar rather than owning
	// the endpoints, which keeps the leaf registry free of provider bodies.
	paymentRegister, err := os.ReadFile(filepath.Join(root, "modules", "chuanapi", "payment", "register.go"))
	require.NoError(t, err)
	assert.Contains(t, string(paymentRegister), "scopes.PaymentRoutes(scopes)",
		"payment route descriptor must delegate to the host registrar")
}

// TestPaymentCatalogueHasSingleHostSource removes the provider catalogue
// registry that only existed to move the Waffo catalogue out of the host. The
// host GetTopUpInfo projection is the single source of pay_methods and
// provider metadata fields; no parallel registry may be reintroduced.
func TestPaymentCatalogueHasSingleHostSource(t *testing.T) {
	root := repoRoot(t)

	_, err := os.Stat(filepath.Join(root, "pkg", "paymentport"))
	assert.ErrorIs(t, err, os.ErrNotExist, "the Waffo-only paymentport catalogue registry must not return")

	topUpController, err := os.ReadFile(filepath.Join(root, "controller", "topup.go"))
	require.NoError(t, err)
	topUpSource := string(topUpController)
	for _, key := range []string{
		"enable_waffo_topup",
		"enable_waffo_pancake_topup",
		"waffo_min_topup",
		"waffo_pancake_min_topup",
		"waffo_pay_methods",
		"model.PaymentMethodWaffoPancake",
		"model.PaymentMethodWaffo",
	} {
		assert.Contains(t, topUpSource, key, "host top-up catalogue must project %s", key)
	}

	moduleRoot := filepath.Join(root, "modules", "chuanapi", "payment")
	var offenders []string
	require.NoError(t, filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, duplicated := range []string{"enable_waffo", "waffo_min_topup", "waffo_pay_methods", "pay_methods"} {
			if strings.Contains(string(data), duplicated) {
				offenders = append(offenders, slashRel(t, root, path)+": "+duplicated)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "the payment module must not duplicate the host catalogue projection")
}

func TestWalletAndAffiliateRuntimeOwnershipStaysInModules(t *testing.T) {
	root := repoRoot(t)
	for _, legacy := range []string{
		"controller/wallet.go",
		"service/affiliate_reward_maintenance.go",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacy)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must remain module-owned", legacy)
	}

	apiRouter, err := os.ReadFile(filepath.Join(root, "router", "api-router.go"))
	require.NoError(t, err)
	for _, path := range []string{
		`"/wallet"`,
		`"/wallet/ledger"`,
		`"/referral/rewards"`,
		`"/aff_transfer"`,
	} {
		assert.NotContains(t, string(apiRouter), path,
			"Wallet/Affiliate user route %s belongs to modules/chuanapi/wallet/routes", path)
	}

	moduleRoot := filepath.Join(root, "modules", "chuanapi", "affiliate")
	fset := token.NewFileSet()
	var offenders []string
	require.NoError(t, filepath.WalkDir(moduleRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if importPath == modulePath+"/controller" || strings.HasPrefix(importPath, modulePath+"/controller/") ||
				importPath == modulePath+"/service" || strings.HasPrefix(importPath, modulePath+"/service/") {
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "Affiliate module must not import root controller/service")
}

func TestWalletCommercialPolicyDoesNotLeakBackIntoCore(t *testing.T) {
	root := repoRoot(t)
	apiRouter, err := os.ReadFile(filepath.Join(root, "router", "api-router.go"))
	require.NoError(t, err)
	assert.NotContains(t, string(apiRouter), "bonus_eligible", "wallet commercial admin routes belong to modules/chuanapi/wallet")

	_, err = os.Stat(filepath.Join(root, "controller", "model_bonus_eligible.go"))
	assert.ErrorIs(t, err, os.ErrNotExist, "wallet commercial admin handler must not return to root controller ownership")

	billingRoot := filepath.Join(root, "pkg", "billingexpr")
	fset := token.NewFileSet()
	var offenders []string
	require.NoError(t, filepath.WalkDir(billingRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			importPath, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if importPath == modulePath+"/chuanapi" || strings.HasPrefix(importPath, modulePath+"/modules/chuanapi/") {
				offenders = append(offenders, slashRel(t, root, path)+" -> "+importPath)
			}
		}
		return nil
	}))
	assert.Empty(t, offenders, "generic billing expression engine must not depend on ChuanAPI commercial policy")
}

func TestFrontendConcreteChuanAPIImportsDoNotGrow(t *testing.T) {
	root := repoRoot(t)
	webRoot := filepath.Join(root, "web", "src")
	actual := map[string]struct{}{}
	var unexpected []string

	require.NoError(t, filepath.WalkDir(webRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if filepath.Base(path) == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) ||
			strings.Contains(filepath.Base(path), ".test.") || strings.Contains(filepath.Base(path), ".spec.") {
			return nil
		}

		rel := slashRel(t, root, path)
		if strings.HasPrefix(rel, "web/src/extensions/chuanapi/") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !frontendConcreteImport.Match(data) {
			return nil
		}
		actual[rel] = struct{}{}
		if _, allowed := frontendConcreteChuanAPIBaseline[rel]; !allowed {
			unexpected = append(unexpected, rel)
		}
		return nil
	}))

	assert.Empty(t, unexpected, "Phase 2 removed frontend concrete ChuanAPI imports from core; new offenders are forbidden")
	assert.Empty(t, actual, "frontend core must consume ChuanAPI UI only through generic extension accessors")
}

func TestFrontendCompositionRootStaysIsolated(t *testing.T) {
	root := repoRoot(t)
	indexPath := filepath.Join(root, "web", "src", "extensions", "index.ts")
	bootstrapPath := filepath.Join(root, "web", "src", "extensions", "bootstrap.ts")

	index, err := os.ReadFile(indexPath)
	require.NoError(t, err)
	assert.NotContains(t, string(index), "./chuanapi",
		"generic extension API must not import concrete ChuanAPI composition")

	bootstrap, err := os.ReadFile(bootstrapPath)
	require.NoError(t, err)
	assert.Contains(t, string(bootstrap), "from './chuanapi'",
		"frontend composition root must explicitly wire the concrete ChuanAPI extension")

	mainEntry, err := os.ReadFile(filepath.Join(root, "web", "src", "main.tsx"))
	require.NoError(t, err)
	assert.Contains(t, string(mainEntry), "./extensions/bootstrap",
		"application entrypoint must initialize frontend composition before rendering")

	var offenders []string
	webRoot := filepath.Join(root, "web", "src")
	require.NoError(t, filepath.WalkDir(webRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !(strings.HasSuffix(path, ".ts") || strings.HasSuffix(path, ".tsx")) {
			return nil
		}
		rel := slashRel(t, root, path)
		if rel == "web/src/extensions/bootstrap.ts" || strings.HasPrefix(rel, "web/src/extensions/chuanapi/") ||
			strings.Contains(filepath.Base(rel), ".test.") || strings.Contains(filepath.Base(rel), ".spec.") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), "from './chuanapi'") || strings.Contains(string(data), "from \"./chuanapi\"") {
			offenders = append(offenders, rel)
		}
		return nil
	}))
	assert.Empty(t, offenders, "only web/src/extensions/bootstrap.ts may wire the concrete frontend ChuanAPI extension")
}

func TestFrontendChuanAPIBootstrapKeepsHeavyPagesLazy(t *testing.T) {
	root := repoRoot(t)
	descriptorPath := filepath.Join(root, "web", "src", "extensions", "chuanapi", "index.ts")
	descriptor, err := os.ReadFile(descriptorPath)
	require.NoError(t, err)
	descriptorSource := string(descriptor)

	for _, forbidden := range []string{
		"from './help'",
		"from \"./help\"",
		"from './wallet'",
		"from \"./wallet\"",
		"from './payment'",
		"from \"./payment\"",
	} {
		assert.NotContains(t, descriptorSource, forbidden,
			"frontend bootstrap descriptor must not statically load heavy ChuanAPI page implementation (%s)", forbidden)
	}
	assert.Contains(t, descriptorSource, "from './lazy-pages'",
		"frontend bootstrap descriptor must delegate heavy page loading to the lazy component boundary")

	lazyPath := filepath.Join(root, "web", "src", "extensions", "chuanapi", "lazy-pages.tsx")
	lazy, err := os.ReadFile(lazyPath)
	require.NoError(t, err)
	lazySource := string(lazy)
	for _, modulePath := range []string{
		"./help/pages/docs-index",
		"./help/pages/guide-page",
		"./help/pages/api-reference-page",
		"./wallet",
		"./payment",
	} {
		pattern := regexp.MustCompile(`import\(["']` + regexp.QuoteMeta(modulePath) + `["']\)`)
		assert.True(t, pattern.MatchString(lazySource),
			"frontend bootstrap descriptor must preserve dynamic import for %s", modulePath)
	}
	assert.Contains(t, lazySource, "<Suspense", "lazy extension pages require an explicit Suspense boundary")
}

func TestWalletFrontendOwnershipStaysInChuanAPIExtension(t *testing.T) {
	root := repoRoot(t)
	legacy := filepath.Join(root, "web", "src", "features", "wallet")
	_, err := os.Stat(legacy)
	assert.ErrorIs(t, err, os.ErrNotExist, "Wallet frontend must remain extension-owned")

	route, err := os.ReadFile(filepath.Join(root, "web", "src", "routes", "_authenticated", "wallet", "index.tsx"))
	require.NoError(t, err)
	assert.NotContains(t, string(route), "@/features/wallet")
	assert.Contains(t, string(route), "extensionWalletPage")
}

func TestPaymentSettingsFrontendOwnershipStaysInChuanAPIExtension(t *testing.T) {
	root := repoRoot(t)
	for _, legacy := range []string{
		"web/src/features/system-settings/integrations/payment-settings-section.tsx",
		"web/src/features/system-settings/integrations/payment-method-dialog.tsx",
		"web/src/features/system-settings/integrations/payment-methods-visual-editor.tsx",
		"web/src/features/system-settings/integrations/waffo-settings-section.tsx",
		"web/src/features/system-settings/integrations/waffo-pancake-settings-section.tsx",
		"web/src/features/system-settings/integrations/waffo-pancake-api.ts",
	} {
		_, err := os.Stat(filepath.Join(root, filepath.FromSlash(legacy)))
		assert.ErrorIs(t, err, os.ErrNotExist, "%s must remain ChuanAPI extension-owned", legacy)
	}

	registry, err := os.ReadFile(filepath.Join(root, "web", "src", "features", "system-settings", "billing", "section-registry.tsx"))
	require.NoError(t, err)
	assert.Contains(t, string(registry), "extensionPaymentSettingsSection")
	assert.NotContains(t, strings.ToLower(string(registry)), "waffo",
		"generic billing section registry must not know concrete Waffo providers")
}

func TestCoreChuanAPIDomainStringLiteralsDoNotGrow(t *testing.T) {
	root := repoRoot(t)
	var offenders []string

	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(root, path) {
				return filepath.SkipDir
			}
			return nil
		}
		rel := slashRel(t, root, path)
		if strings.HasPrefix(rel, "chuanapi/") || strings.HasPrefix(rel, "modules/chuanapi/") ||
			strings.HasPrefix(rel, "web/src/extensions/chuanapi/") || strings.HasPrefix(rel, "docs/") || strings.HasPrefix(rel, "ops/") ||
			strings.HasSuffix(rel, "_test.go") || strings.Contains(filepath.Base(rel), ".test.") || strings.Contains(filepath.Base(rel), ".spec.") {
			return nil
		}

		hasLiteral, scanErr := hasChuanAPIDomainLiteral(path)
		if scanErr != nil {
			return scanErr
		}
		if !hasLiteral {
			return nil
		}
		if _, allowed := coreBrandLiteralBaseline[rel]; !allowed {
			offenders = append(offenders, rel)
		}
		return nil
	}))

	assert.Empty(t, offenders, "Phase 1 removed production core chuanapi.com literals; concrete brand domains must stay in configuration or ChuanAPI modules")
}

func hasChuanAPIDomainLiteral(path string) (bool, error) {
	switch {
	case strings.HasSuffix(path, ".go"):
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		fset := token.NewFileSet()
		file := fset.AddFile(path, fset.Base(), len(data))
		var scanner goscanner.Scanner
		scanner.Init(file, data, nil, 0)
		for {
			_, tok, lit := scanner.Scan()
			if tok == token.EOF {
				return false, nil
			}
			if tok != token.STRING {
				continue
			}
			value, unquoteErr := strconv.Unquote(lit)
			if unquoteErr == nil && strings.Contains(value, "chuanapi.com") {
				return true, nil
			}
		}
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		return quotedDomainLiteral.Match(data), nil
	default:
		return false, nil
	}
}

var frontendConcreteImport = regexp.MustCompile(`(?m)(from\s*["']@/extensions/chuanapi(?:/[^"']*)?["']|import\s*\(\s*["']@/extensions/chuanapi(?:/[^"']*)?["']\s*\))`)

var quotedDomainLiteral = regexp.MustCompile("[\\\"'`]([^\\\"'`]|\\\\.)*chuanapi\\.com([^\\\"'`]|\\\\.)*[\\\"'`]")

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "could not locate repository root from %s", dir)
		dir = parent
	}
}

func skipDir(root, path string) bool {
	if path == root {
		return false
	}
	name := filepath.Base(path)
	return name == ".git" || name == "node_modules" || name == "vendor" || name == "dist" || name == ".codex-worktrees"
}

func slashRel(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	require.NoError(t, err)
	return filepath.ToSlash(rel)
}
