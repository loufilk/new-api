package chuanapi

import (
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	paymentmodule "github.com/QuantumNous/new-api/modules/chuanapi/payment"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectedWalletModelOrder is the migration order the host relied on before the
// registry took over: the ledger operation header precedes the ledger rows that
// reference it. The affiliate model set contributes the referral commission
// voucher schema after the wallet ledger. Asserting the exact type sequence is
// what makes an accidental reorder or a dropped model fail here instead of at a
// customer's AutoMigrate.
var expectedWalletModelOrder = []any{
	&model.BalanceLedgerOperation{},
	&model.BalanceLedger{},
	&model.ModelBonusEligibleAudit{},
}

var expectedAffiliateModelOrder = []any{
	&model.ReferralCommission{},
}

var expectedPaymentModelOrder = []any{
	&paymentmodule.PaymentOrderBinding{},
}

var expectedSupportModelOrder = []any{
	&model.SupportTicket{},
	&model.SupportTicketMessage{},
}

func TestRegisterContributesWalletModelsInMigrationOrder(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	models := reg.ModelsFor(extension.PhaseAfterCoreModels)
	require.Len(t, models, len(expectedWalletModelOrder)+len(expectedAffiliateModelOrder)+len(expectedPaymentModelOrder)+len(expectedSupportModelOrder))
	for i, expected := range expectedWalletModelOrder {
		assert.IsType(t, expected, models[i], "wallet model at index %d changed", i)
	}
	for i, expected := range expectedAffiliateModelOrder {
		assert.IsType(t, expected, models[len(expectedWalletModelOrder)+i], "affiliate model at index %d changed", i)
	}
	for i, expected := range expectedPaymentModelOrder {
		assert.IsType(t, expected, models[len(expectedWalletModelOrder)+len(expectedAffiliateModelOrder)+i], "payment model at index %d changed", i)
	}

	for i, expected := range expectedSupportModelOrder {
		assert.IsType(t, expected, models[len(expectedWalletModelOrder)+len(expectedAffiliateModelOrder)+len(expectedPaymentModelOrder)+i], "support model at index %d changed", i)
	}

	sets := reg.ModelSets()
	require.Len(t, sets, 4)
	assert.Equal(t, ExtensionName, sets[0].Extension)
	assert.Equal(t, WalletModelsName, sets[0].Name)
	assert.Equal(t, extension.PhaseAfterCoreModels, sets[0].Phase)
	assert.Equal(t, ExtensionName, sets[1].Extension)
	assert.Equal(t, AffiliateModelsName, sets[1].Name)
	assert.Equal(t, extension.PhaseAfterCoreModels, sets[1].Phase)
	assert.Equal(t, ExtensionName, sets[2].Extension)
	assert.Equal(t, PaymentModelsName, sets[2].Name)
	assert.Equal(t, extension.PhaseAfterCoreModels, sets[2].Phase)
	assert.Equal(t, ExtensionName, sets[3].Extension)
	assert.Equal(t, SupportModelsName, sets[3].Name)
	assert.Equal(t, extension.PhaseAfterCoreModels, sets[3].Phase)
}

// The backfill mutates balances, so it must run only after the wallet schema
// and host core-data initialization are complete.
func TestRegisterContributesBackfillStepAfterCoreData(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	steps := reg.StepsFor(extension.PhaseAfterCoreData)
	require.Len(t, steps, 1)
	assert.Equal(t, ExtensionName, steps[0].Extension)
	assert.Equal(t, LegacyQuotaBackfillName, steps[0].Name)
	require.NotNil(t, steps[0].Run)

	assert.Empty(t, reg.ModelsFor(extension.PhaseAfterCoreData))
	assert.Empty(t, reg.StepsFor(extension.PhaseAfterCoreModels))
}

func TestRegisterContributesModuleRoutesBeforePluginSnapshot(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	sets := reg.RouteSetsFor(extension.PriorityBeforePlugin)
	require.Len(t, sets, 5)
	assert.Equal(t, ExtensionName, sets[0].Extension)
	assert.Equal(t, WalletPolicyRoutesName, sets[0].Name)
	require.NotNil(t, sets[0].RegisterScoped)
	assert.Equal(t, ExtensionName, sets[1].Extension)
	assert.Equal(t, PaymentRoutesName, sets[1].Name)
	require.NotNil(t, sets[1].RegisterScoped)
	assert.Equal(t, ExtensionName, sets[2].Extension)
	assert.Equal(t, LlmsRoutesName, sets[2].Name)
	require.NotNil(t, sets[2].Register)
	assert.Equal(t, ExtensionName, sets[3].Extension)
	assert.Equal(t, ConsolePublicRoutesName, sets[3].Name)
	require.NotNil(t, sets[3].RegisterScoped)
	assert.Equal(t, ExtensionName, sets[4].Extension)
	assert.Equal(t, SupportRoutesName, sets[4].Name)
	require.NotNil(t, sets[4].Register)
	assert.Empty(t, reg.RouteSetsFor(extension.PriorityAfterPlugin))
}

// WalletModels is handed straight to the registry, so a shared backing array
// would let one caller reorder what a later caller registers.
func TestWalletModelsReturnsAFreshSlicePerCall(t *testing.T) {
	first := WalletModels()
	second := WalletModels()
	require.Len(t, first, len(expectedWalletModelOrder))
	require.Len(t, second, len(expectedWalletModelOrder))

	first[0] = nil
	assert.IsType(t, expectedWalletModelOrder[0], second[0],
		"mutating a returned slice must not affect a later call")
}

func TestRegisterRejectsNilRegistry(t *testing.T) {
	assert.Error(t, Register(nil))
}

// A frozen registry means the composition root wired registration too late.
// That is a build-time bug in the host binary, so it must fail loudly rather
// than return an error a caller could ignore and start with no wallet schema.
func TestRegisterOnFrozenRegistryPanics(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, reg.Freeze())
	assert.Panics(t, func() { _ = Register(reg) })
}

// Registering twice into one registry is a duplicate identity, not a silent
// second copy of the schema in the AutoMigrate list.
func TestRegisterTwiceIsRejectedWithoutDuplicatingModels(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	require.Error(t, Register(reg))

	assert.Len(t, reg.ModelsFor(extension.PhaseAfterCoreModels), len(expectedWalletModelOrder)+len(expectedAffiliateModelOrder)+len(expectedPaymentModelOrder)+len(expectedSupportModelOrder))
	assert.Len(t, reg.StepsFor(extension.PhaseAfterCoreData), 1)
	assert.Len(t, reg.RouteSetsFor(extension.PriorityBeforePlugin), 5)
}

func TestSupportRouteSetRegistersUserAndAdminTicketAPIs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	sets := reg.RouteSetsFor(extension.PriorityBeforePlugin)
	require.Len(t, sets, 5)

	engine := gin.New()
	require.NoError(t, sets[4].Register(engine))
	routes := make(map[string]struct{})
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		"GET /api/support/tickets",
		"POST /api/support/tickets",
		"GET /api/support/tickets/:id",
		"POST /api/support/tickets/:id/replies",
		"POST /api/support/tickets/:id/read",
		"GET /api/admin/support/tickets",
		"GET /api/admin/support/tickets/:id",
		"POST /api/admin/support/tickets/:id/replies",
		"POST /api/admin/support/tickets/:id/read",
		"PUT /api/admin/support/tickets/:id/status",
	} {
		_, ok := routes[expected]
		assert.True(t, ok, "missing route %s", expected)
	}
}

func TestSupportModelsReturnsAFreshSlicePerCall(t *testing.T) {
	first := SupportModels()
	second := SupportModels()
	require.Len(t, first, len(expectedSupportModelOrder))
	require.Len(t, second, len(expectedSupportModelOrder))

	first[0] = nil
	assert.IsType(t, expectedSupportModelOrder[0], second[0])
}
