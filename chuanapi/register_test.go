package chuanapi

import (
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	agentmodule "github.com/QuantumNous/new-api/modules/chuanapi/agents"
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
	&model.SupportTicketMessage{}, &model.SupportTicketAttachment{}, &model.SupportTicketSubmission{},
	&model.SupportEmailSettings{}, &model.SupportEmailPreference{}, &model.SupportEmailOutbox{}, &model.SupportEmailRate{},
}

func TestRegisterContributesWalletModelsInMigrationOrder(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	expected := append([]any{}, expectedWalletModelOrder...)
	expected = append(expected, expectedAffiliateModelOrder...)
	expected = append(expected, agentmodule.Models()...)
	expected = append(expected, expectedPaymentModelOrder...)
	expected = append(expected, expectedSupportModelOrder...)
	models := reg.ModelsFor(extension.PhaseAfterCoreModels)
	require.Len(t, models, len(expected))
	for i, want := range expected {
		assert.IsType(t, want, models[i], "registered model at index %d changed", i)
	}
	sets := reg.ModelSets()
	names := []string{WalletModelsName, AffiliateModelsName, agentmodule.ModelsName, PaymentModelsName, SupportModelsName}
	require.Len(t, sets, len(names))
	for i, name := range names {
		assert.Equal(t, ExtensionName, sets[i].Extension)
		assert.Equal(t, name, sets[i].Name)
		assert.Equal(t, extension.PhaseAfterCoreModels, sets[i].Phase)
	}
}

// The backfill mutates balances, so it must run only after the wallet schema
// and host core-data initialization are complete.
func TestRegisterContributesBackfillStepAfterCoreData(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	steps := reg.StepsFor(extension.PhaseAfterCoreData)
	require.Len(t, steps, 2)
	assert.Equal(t, ExtensionName, steps[0].Extension)
	assert.Equal(t, LegacyQuotaBackfillName, steps[0].Name)
	require.NotNil(t, steps[0].Run)
	assert.Equal(t, agentmodule.MigrationName, steps[1].Name)
	require.NotNil(t, steps[1].Run)

	assert.Empty(t, reg.ModelsFor(extension.PhaseAfterCoreData))
	assert.Empty(t, reg.StepsFor(extension.PhaseAfterCoreModels))
}

func TestRegisterContributesModuleRoutesBeforePluginSnapshot(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	sets := reg.RouteSetsFor(extension.PriorityBeforePlugin)
	names := []string{WalletPolicyRoutesName, agentmodule.RoutesName, PaymentRoutesName, LlmsRoutesName, ConsolePublicRoutesName, SupportRoutesName}
	require.Len(t, sets, len(names))
	for i, name := range names {
		assert.Equal(t, ExtensionName, sets[i].Extension)
		assert.Equal(t, name, sets[i].Name)
		assert.True(t, sets[i].Register != nil || sets[i].RegisterScoped != nil)
	}
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

	assert.Len(t, reg.ModelsFor(extension.PhaseAfterCoreModels), len(expectedWalletModelOrder)+len(expectedAffiliateModelOrder)+len(agentmodule.Models())+len(expectedPaymentModelOrder)+len(expectedSupportModelOrder))
	assert.Len(t, reg.StepsFor(extension.PhaseAfterCoreData), 2)
	assert.Len(t, reg.RouteSetsFor(extension.PriorityBeforePlugin), 6)
}

func TestSupportRouteSetRegistersUserAndAdminTicketAPIs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))
	sets := reg.RouteSetsFor(extension.PriorityBeforePlugin)
	require.Len(t, sets, 6)

	engine := gin.New()
	require.NoError(t, sets[5].Register(engine))
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
