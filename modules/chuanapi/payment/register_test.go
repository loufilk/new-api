package payment

import (
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterContributesBindingModelAndDelegatesRoutesToHost(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg))

	models := reg.ModelsFor(extension.PhaseAfterCoreModels)
	require.Len(t, models, 1)
	assert.IsType(t, &PaymentOrderBinding{}, models[0])

	sets := reg.RouteSetsFor(extension.PriorityBeforePlugin)
	require.Len(t, sets, 1)
	assert.Equal(t, ExtensionName, sets[0].Extension)
	assert.Equal(t, RoutesName, sets[0].Name)
	require.NotNil(t, sets[0].RegisterScoped)
	assert.Nil(t, sets[0].Register, "payment routes must consume host scopes, not a raw engine")

	// The module must forward to the host registrar instead of reconstructing
	// provider URLs or middleware policy itself.
	var received extension.RouteScopes
	called := false
	hostRegistrar := func(scopes extension.RouteScopes) error {
		called = true
		received = scopes
		return nil
	}
	expected := extension.RouteScopes{PaymentRoutes: hostRegistrar}
	require.NoError(t, sets[0].RegisterScoped(expected))
	assert.True(t, called, "host payment route registrar must be invoked")
	assert.NotNil(t, received.PaymentRoutes)

	require.Error(t, sets[0].RegisterScoped(extension.RouteScopes{}), "a missing host registrar must fail closed")
}

func TestRegisterRejectsNilRegistry(t *testing.T) {
	assert.Error(t, Register(nil))
}
