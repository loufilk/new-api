package extension

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLifecyclePhaseAndRegistrationContract(t *testing.T) {
	assert.Equal(t, []LifecyclePhase{PhaseBeforeBackgroundWorkers}, LifecyclePhases())
	assert.Equal(t, "before_background_workers", PhaseBeforeBackgroundWorkers.String())
	assert.False(t, LifecyclePhase(0).valid())

	reg := NewRegistry()
	var order []string
	require.NoError(t, reg.RegisterLifecycleSet(LifecycleSet{
		Extension: "one", Name: "first", Phase: PhaseBeforeBackgroundWorkers,
		Run: func() error { order = append(order, "first"); return nil },
	}))
	require.NoError(t, reg.RegisterLifecycleSet(LifecycleSet{
		Extension: "two", Name: "second", Phase: PhaseBeforeBackgroundWorkers,
		Run: func() error { order = append(order, "second"); return nil },
	}))
	require.NoError(t, reg.Freeze())
	require.NoError(t, reg.ApplyLifecycle(PhaseBeforeBackgroundWorkers))
	assert.Equal(t, []string{"first", "second"}, order)
}

func TestLifecycleFailsClosedAndWrapsOwner(t *testing.T) {
	errBoom := errors.New("boom")
	reg := NewRegistry()
	var ranThird bool
	require.NoError(t, reg.RegisterLifecycleSet(LifecycleSet{
		Extension: "one", Name: "first", Phase: PhaseBeforeBackgroundWorkers,
		Run: func() error { return nil },
	}))
	require.NoError(t, reg.RegisterLifecycleSet(LifecycleSet{
		Extension: "two", Name: "second", Phase: PhaseBeforeBackgroundWorkers,
		Run: func() error { return errBoom },
	}))
	require.NoError(t, reg.RegisterLifecycleSet(LifecycleSet{
		Extension: "three", Name: "third", Phase: PhaseBeforeBackgroundWorkers,
		Run: func() error { ranThird = true; return nil },
	}))
	require.NoError(t, reg.Freeze())

	err := reg.ApplyLifecycle(PhaseBeforeBackgroundWorkers)
	require.Error(t, err)
	assert.ErrorIs(t, err, errBoom)
	assert.Contains(t, err.Error(), "two/second")
	assert.False(t, ranThird)
}

func TestLifecycleRegistrationRejectsInvalidAndDuplicateDescriptors(t *testing.T) {
	reg := NewRegistry()
	assert.Error(t, reg.RegisterLifecycleSet(LifecycleSet{Name: "x", Phase: PhaseBeforeBackgroundWorkers, Run: func() error { return nil }}))
	assert.Error(t, reg.RegisterLifecycleSet(LifecycleSet{Extension: "x", Phase: PhaseBeforeBackgroundWorkers, Run: func() error { return nil }}))
	assert.Error(t, reg.RegisterLifecycleSet(LifecycleSet{Extension: "x", Name: "x", Run: func() error { return nil }}))
	assert.Error(t, reg.RegisterLifecycleSet(LifecycleSet{Extension: "x", Name: "x", Phase: PhaseBeforeBackgroundWorkers}))

	require.NoError(t, reg.RegisterLifecycleSet(LifecycleSet{Extension: "x", Name: "x", Phase: PhaseBeforeBackgroundWorkers, Run: func() error { return nil }}))
	assert.Error(t, reg.RegisterLifecycleSet(LifecycleSet{Extension: "x", Name: "x", Phase: PhaseBeforeBackgroundWorkers, Run: func() error { return nil }}))
}

func TestLifecycleRegistrationAfterFreezePanics(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.Freeze())
	assert.PanicsWithValue(t,
		"extension: cannot register lifecycle set chuanapi/late: registry is frozen",
		func() {
			_ = reg.RegisterLifecycleSet(LifecycleSet{Extension: "chuanapi", Name: "late", Phase: PhaseBeforeBackgroundWorkers, Run: func() error { return nil }})
		},
	)
}
