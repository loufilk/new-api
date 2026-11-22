// Package extension holds the build-time registry that optional extension
// modules use to contribute schema migrations and HTTP routes to the host.
//
// The registry is deliberately build-time only. Extensions do not self-register
// from init(); the composition root constructs a Registry, calls the extension
// packages' explicit registration functions, and then freezes the result with
// SetDefault. After freezing, the registry is an immutable snapshot: reads are
// safe from any goroutine and every accessor returns a defensive copy, so a
// caller can never mutate registry state.
//
// Descriptors are identified by an (Extension, Name) pair that must be unique
// within a descriptor kind. Identity appears in every validation error, panic,
// and execution failure so operators can trace a problem to its owner.
package extension

import (
	"errors"
	"fmt"
	"sync"
)

// descriptorID is the (Extension, Name) identity shared by every descriptor
// kind. It is a struct rather than a joined string so that a name containing a
// separator cannot collide with a different extension.
type descriptorID struct {
	extension string
	name      string
}

func (id descriptorID) String() string {
	extension := id.extension
	if extension == "" {
		extension = "<empty>"
	}
	name := id.name
	if name == "" {
		name = "<empty>"
	}
	return extension + "/" + name
}

// Registry collects extension descriptors during startup wiring. A Registry is
// mutable until Freeze (or SetDefault) succeeds and immutable afterwards.
type Registry struct {
	mu            sync.RWMutex
	frozen        bool
	modelSets     []ModelSet
	dataSteps     []DataStep
	routeSets     []RouteSet
	lifecycleSets []LifecycleSet
}

// NewRegistry returns an empty, mutable Registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// requireMutableLocked panics when a registration is attempted after the
// registry was frozen. Callers must hold the write lock; the deferred unlock
// still runs while the panic unwinds, so a recovered panic does not strand the
// mutex.
//
// Freezing is checked before descriptor validation on purpose: a late
// registration is a wiring bug in the host binary regardless of whether the
// descriptor is well-formed, and it fails loudly rather than returning an error
// a caller might ignore.
func (r *Registry) requireMutableLocked(kind string, id descriptorID) {
	if r.frozen {
		panic(fmt.Sprintf("extension: cannot register %s %s: registry is frozen", kind, id))
	}
}

// Freeze validates every registered descriptor and makes the registry
// immutable. It is idempotent: freezing an already frozen registry is a no-op.
// When validation fails the registry stays mutable so the caller can report the
// wiring error without leaving a half-frozen registry behind.
func (r *Registry) Freeze() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return nil
	}
	if err := r.validateLocked(); err != nil {
		return err
	}
	r.frozen = true
	return nil
}

// Frozen reports whether the registry has been frozen.
func (r *Registry) Frozen() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

// validateLocked re-checks every descriptor and enforces identity uniqueness
// within each descriptor kind. Register* already rejects bad input at the call
// site; this pass guarantees the invariant holds for the snapshot itself.
func (r *Registry) validateLocked() error {
	seenModelSets := make(map[descriptorID]struct{}, len(r.modelSets))
	for _, set := range r.modelSets {
		if err := set.validate(); err != nil {
			return err
		}
		if _, ok := seenModelSets[set.id()]; ok {
			return fmt.Errorf("extension: duplicate model set %s", set.id())
		}
		seenModelSets[set.id()] = struct{}{}
	}
	seenDataSteps := make(map[descriptorID]struct{}, len(r.dataSteps))
	for _, step := range r.dataSteps {
		if err := step.validate(); err != nil {
			return err
		}
		if _, ok := seenDataSteps[step.id()]; ok {
			return fmt.Errorf("extension: duplicate data step %s", step.id())
		}
		seenDataSteps[step.id()] = struct{}{}
	}
	seenRouteSets := make(map[descriptorID]struct{}, len(r.routeSets))
	for _, set := range r.routeSets {
		if err := set.validate(); err != nil {
			return err
		}
		if _, ok := seenRouteSets[set.id()]; ok {
			return fmt.Errorf("extension: duplicate route set %s", set.id())
		}
		seenRouteSets[set.id()] = struct{}{}
	}
	seenLifecycleSets := make(map[descriptorID]struct{}, len(r.lifecycleSets))
	for _, set := range r.lifecycleSets {
		if err := set.validate(); err != nil {
			return err
		}
		if _, ok := seenLifecycleSets[set.id()]; ok {
			return fmt.Errorf("extension: duplicate lifecycle set %s", set.id())
		}
		seenLifecycleSets[set.id()] = struct{}{}
	}
	return nil
}

// ErrDefaultAlreadySet is returned when SetDefault is called more than once.
// The process-wide default is write-once so that a frozen snapshot cannot be
// swapped out from under code that already read it.
var ErrDefaultAlreadySet = errors.New("extension: default registry is already set")

// ErrNilRegistry is returned when SetDefault is given a nil registry.
var ErrNilRegistry = errors.New("extension: registry must not be nil")

var (
	defaultMu       sync.RWMutex
	defaultRegistry *Registry
)

// emptyDefault is the frozen snapshot Default serves before any registry is
// installed. It is immutable, so one shared value is enough.
var emptyDefault = &Registry{frozen: true}

// SetDefault validates and freezes reg, then installs it as the process-wide
// default. It may be called at most once, rejects nil, and refuses a registry
// that fails validation, so Default always returns a valid frozen snapshot.
//
// The already-set check, the freeze, and the install happen under one lock, so
// concurrent callers cannot both observe an empty default and both freeze their
// argument. Exactly one caller wins; every other call returns
// ErrDefaultAlreadySet with its own registry left untouched.
func SetDefault(reg *Registry) error {
	if reg == nil {
		return ErrNilRegistry
	}
	defaultMu.Lock()
	defer defaultMu.Unlock()
	if defaultRegistry != nil {
		return ErrDefaultAlreadySet
	}
	if err := reg.Freeze(); err != nil {
		return err
	}
	defaultRegistry = reg
	return nil
}

// Default returns the frozen default registry. When no default was installed it
// returns a shared empty frozen registry, so callers never have to nil-check.
func Default() *Registry {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	if defaultRegistry != nil {
		return defaultRegistry
	}
	return emptyDefault
}
