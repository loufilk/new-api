package extension

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type modelA struct{ ID int }
type modelB struct{ ID int }

func noopStep(*gorm.DB) error { return nil }

func noopRoutes(*gin.Engine) error { return nil }

// withIsolatedDefault swaps the process-wide default registry for the duration
// of one test. The production API keeps SetDefault write-once; this test-only
// hook is what lets each test observe that contract from a clean slate without
// leaking state into sibling tests. Tests using it must not call t.Parallel.
func withIsolatedDefault(t *testing.T) {
	t.Helper()
	defaultMu.Lock()
	previous := defaultRegistry
	defaultRegistry = nil
	defaultMu.Unlock()
	t.Cleanup(func() {
		defaultMu.Lock()
		defaultRegistry = previous
		defaultMu.Unlock()
	})
}

func TestRegisterFreezeThenRead(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "api", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))

	assert.False(t, reg.Frozen())
	require.NoError(t, reg.Freeze())
	assert.True(t, reg.Frozen())
	require.NoError(t, reg.Freeze(), "freeze is idempotent")

	require.Len(t, reg.ModelsFor(PhaseAfterCoreModels), 1)
	require.Len(t, reg.StepsFor(PhaseAfterCoreData), 1)
	require.Len(t, reg.RouteSetsFor(PriorityAfterPlugin), 1)
}

func TestRegisterAfterFreezePanicsWithIdentity(t *testing.T) {
	cases := []struct {
		name     string
		register func(*Registry) error
		identity string
	}{
		{
			name: "model set",
			register: func(r *Registry) error {
				return r.RegisterModelSet(ModelSet{
					Extension: "chuanapi", Name: "late-models", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
				})
			},
			identity: "chuanapi/late-models",
		},
		{
			name: "data step",
			register: func(r *Registry) error {
				return r.RegisterDataStep(DataStep{
					Extension: "chuanapi", Name: "late-step", Phase: PhaseAfterCoreData, Run: noopStep,
				})
			},
			identity: "chuanapi/late-step",
		},
		{
			name: "route set",
			register: func(r *Registry) error {
				return r.RegisterRouteSet(RouteSet{
					Extension: "chuanapi", Name: "late-routes", Priority: PriorityAfterPlugin, Register: noopRoutes,
				})
			},
			identity: "chuanapi/late-routes",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			require.NoError(t, reg.Freeze())
			assert.PanicsWithValue(
				t,
				"extension: cannot register "+tc.name+" "+tc.identity+": registry is frozen",
				func() { _ = tc.register(reg) },
			)
		})
	}
}

func TestInvalidRegisterAfterFreezeStillPanics(t *testing.T) {
	cases := []struct {
		name      string
		register  func(*Registry) error
		wantPanic string
	}{
		{
			name: "invalid model set",
			register: func(r *Registry) error {
				return r.RegisterModelSet(ModelSet{Extension: "chuanapi", Name: "invalid-models"})
			},
			wantPanic: "extension: cannot register model set chuanapi/invalid-models: registry is frozen",
		},
		{
			name: "invalid data step",
			register: func(r *Registry) error {
				return r.RegisterDataStep(DataStep{Extension: "chuanapi", Name: "invalid-step"})
			},
			wantPanic: "extension: cannot register data step chuanapi/invalid-step: registry is frozen",
		},
		{
			name: "invalid route set",
			register: func(r *Registry) error {
				return r.RegisterRouteSet(RouteSet{Extension: "chuanapi", Name: "invalid-routes"})
			},
			wantPanic: "extension: cannot register route set chuanapi/invalid-routes: registry is frozen",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			require.NoError(t, reg.Freeze())
			assert.PanicsWithValue(t, tc.wantPanic, func() { _ = tc.register(reg) })
		})
	}
}

func TestFrozenRegistryReadsAreConcurrencySafe(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}, &modelB{}},
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "api", Priority: PriorityBeforePlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.Freeze())

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.Len(t, reg.ModelsFor(PhaseAfterCoreModels), 2)
			assert.Len(t, reg.ModelSets(), 1)
			assert.Len(t, reg.Steps(), 1)
			assert.Len(t, reg.StepsFor(PhaseAfterCoreData), 1)
			assert.Len(t, reg.RouteSets(), 1)
			assert.Len(t, reg.RouteSetsFor(PriorityBeforePlugin), 1)
			assert.True(t, reg.Frozen())
		}()
	}
	wg.Wait()
}

// TestCallbacksMayReadTheRegistry pins down that RunStepsFor and ApplyRoutes
// snapshot their descriptors and release the read lock before invoking a
// callback. Extension callbacks run inside the host process and may legitimately
// consult the registry; holding the lock across them would make that hang.
func TestCallbacksMayReadTheRegistry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := NewRegistry()
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
	}))

	var stepSawModels int
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "reads-registry", Phase: PhaseAfterCoreData,
		Run: func(*gorm.DB) error {
			stepSawModels = len(reg.ModelsFor(PhaseAfterCoreModels))
			return nil
		},
	}))

	var routeSawSteps int
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "reads-registry", Priority: PriorityAfterPlugin,
		Register: func(*gin.Engine) error {
			routeSawSteps = len(reg.Steps())
			return nil
		},
	}))
	require.NoError(t, reg.Freeze())

	require.NoError(t, reg.RunStepsFor(&gorm.DB{}, PhaseAfterCoreData))
	assert.Equal(t, 1, stepSawModels, "a step observes the frozen snapshot")

	require.NoError(t, reg.ApplyRoutes(gin.New(), PriorityAfterPlugin))
	assert.Equal(t, 1, routeSawSteps, "a route set observes the frozen snapshot")
}

func TestSetDefaultInstallsFrozenSnapshot(t *testing.T) {
	withIsolatedDefault(t)

	reg := NewRegistry()
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
	}))
	require.NoError(t, SetDefault(reg))

	assert.True(t, reg.Frozen(), "SetDefault freezes the registry")
	assert.Same(t, reg, Default())
	assert.Len(t, Default().ModelsFor(PhaseAfterCoreModels), 1)
}

func TestSetDefaultRejectsNilAndRepeatCalls(t *testing.T) {
	withIsolatedDefault(t)

	require.ErrorIs(t, SetDefault(nil), ErrNilRegistry)

	first := NewRegistry()
	require.NoError(t, SetDefault(first))

	second := NewRegistry()
	require.ErrorIs(t, SetDefault(second), ErrDefaultAlreadySet)
	assert.Same(t, first, Default(), "the installed default is not replaced")
	assert.False(t, second.Frozen(), "a rejected registry is left untouched")
}

func TestSetDefaultConcurrentCallersHaveOneWinnerAndUntouchedLosers(t *testing.T) {
	withIsolatedDefault(t)

	const callers = 32
	registries := make([]*Registry, callers)
	errs := make([]error, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range registries {
		registries[i] = NewRegistry()
		require.NoError(t, registries[i].RegisterDataStep(DataStep{
			Extension: "candidate", Name: fmt.Sprintf("step-%d", i), Phase: PhaseAfterCoreData, Run: noopStep,
		}))
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = SetDefault(registries[index])
		}(i)
	}
	close(start)
	wg.Wait()

	winner := -1
	for i, err := range errs {
		if err == nil {
			require.Equal(t, -1, winner, "only one caller may install the default")
			winner = i
			continue
		}
		require.ErrorIs(t, err, ErrDefaultAlreadySet)
	}
	require.NotEqual(t, -1, winner)
	assert.Same(t, registries[winner], Default())
	for i, reg := range registries {
		if i == winner {
			assert.True(t, reg.Frozen(), "the installed registry is frozen")
		} else {
			assert.False(t, reg.Frozen(), "rejected registry %d must remain mutable", i)
		}
	}
}

func TestSetDefaultRejectsInvalidRegistry(t *testing.T) {
	withIsolatedDefault(t)

	reg := NewRegistry()
	// Bypass Register* validation to prove SetDefault validates the snapshot
	// itself rather than trusting how descriptors were added.
	reg.dataSteps = append(reg.dataSteps, DataStep{
		Extension: "chuanapi", Name: "broken", Phase: PhaseAfterCoreData,
	})

	err := SetDefault(reg)
	require.Error(t, err)
	assert.EqualError(t, err, "extension: data step chuanapi/broken has a nil Run function")
	assert.False(t, reg.Frozen(), "a failed freeze leaves the registry mutable")
	assert.Empty(t, Default().Steps(), "no default was installed")
}

func TestDefaultWithoutSetDefaultIsEmptyAndFrozen(t *testing.T) {
	withIsolatedDefault(t)

	reg := Default()
	require.NotNil(t, reg)
	assert.True(t, reg.Frozen())
	assert.Empty(t, reg.ModelSets())
	assert.Empty(t, reg.Steps())
	assert.Empty(t, reg.RouteSets())
	assert.Same(t, reg, Default(), "the empty default is shared")

	assert.PanicsWithValue(
		t,
		"extension: cannot register data step chuanapi/x: registry is frozen",
		func() {
			_ = reg.RegisterDataStep(DataStep{
				Extension: "chuanapi", Name: "x", Phase: PhaseAfterCoreData, Run: noopStep,
			})
		},
	)
}

func TestDuplicateIdentityAcrossKindsIsAllowed(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "core", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.Freeze(), "identity is scoped per descriptor kind")
}

func TestFreezeRejectsDuplicateIdentityInSnapshot(t *testing.T) {
	step := DataStep{Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreData, Run: noopStep}
	reg := NewRegistry()
	reg.dataSteps = append(reg.dataSteps, step, step)

	err := reg.Freeze()
	require.Error(t, err)
	assert.EqualError(t, err, "extension: duplicate data step chuanapi/backfill")
	assert.False(t, reg.Frozen())
}

func TestDescriptorIdentityRendersEmptyFields(t *testing.T) {
	reg := NewRegistry()
	err := reg.RegisterDataStep(DataStep{Phase: PhaseAfterCoreData, Run: noopStep})
	require.Error(t, err)
	assert.EqualError(t, err, "extension: data step <empty>/<empty> has an empty extension")

	err = reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, "extension: model set chuanapi/<empty> has an empty name")
}

var errBoom = errors.New("boom")
