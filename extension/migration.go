package extension

import (
	"errors"
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

// MigrationPhase is a closed enum naming the points in host startup where
// extension migrations may run. There is deliberately no "before core" phase:
// extension schema and data always follow the core schema, so an extension can
// depend on core tables while core code never depends on an extension.
type MigrationPhase int

const (
	// PhaseAfterCoreModels runs directly after the host AutoMigrates its own
	// models. Extension model sets are created here.
	PhaseAfterCoreModels MigrationPhase = iota + 1
	// PhaseAfterCoreData runs after the host finishes its own data migrations,
	// so a step here sees both core and extension tables fully migrated.
	PhaseAfterCoreData
)

// migrationPhaseOrder is the canonical execution order of the phases.
var migrationPhaseOrder = [...]MigrationPhase{PhaseAfterCoreModels, PhaseAfterCoreData}

// MigrationPhases returns the phases in their fixed execution order.
func MigrationPhases() []MigrationPhase {
	return append([]MigrationPhase(nil), migrationPhaseOrder[:]...)
}

func (p MigrationPhase) valid() bool {
	return p == PhaseAfterCoreModels || p == PhaseAfterCoreData
}

func (p MigrationPhase) String() string {
	switch p {
	case PhaseAfterCoreModels:
		return "after_core_models"
	case PhaseAfterCoreData:
		return "after_core_data"
	default:
		return fmt.Sprintf("MigrationPhase(%d)", int(p))
	}
}

// ModelSet is a group of GORM models one extension contributes to AutoMigrate.
// Models are kept separate from DataStep so that schema creation stays a
// declarative list the host can hand straight to GORM, rather than an opaque
// callback.
type ModelSet struct {
	Extension string
	Name      string
	Phase     MigrationPhase
	// Models holds pointers to GORM model structs, in the order they should be
	// migrated.
	Models []any
}

func (s ModelSet) id() descriptorID {
	return descriptorID{extension: s.Extension, name: s.Name}
}

// Host-executed phases. The production startup path (model.migrateDB /
// mainAutoMigrateModels) executes exactly two (kind, phase) pairs:
//
//	ModelSet  -> ModelsFor(PhaseAfterCoreModels)   (appended to core AutoMigrate)
//	DataStep  -> RunStepsFor(PhaseAfterCoreData)   (fixed point in migrateDB)
//
// validate() restricts each descriptor kind to its executed phase so the
// registry can never accept a descriptor the host would silently never run.
func (s ModelSet) executedPhase() MigrationPhase { return PhaseAfterCoreModels }

func (s DataStep) executedPhase() MigrationPhase { return PhaseAfterCoreData }

func (s ModelSet) validate() error {
	id := s.id()
	if s.Extension == "" {
		return fmt.Errorf("extension: model set %s has an empty extension", id)
	}
	if s.Name == "" {
		return fmt.Errorf("extension: model set %s has an empty name", id)
	}
	if !s.Phase.valid() {
		return fmt.Errorf("extension: model set %s has invalid phase %s", id, s.Phase)
	}
	if s.Phase != s.executedPhase() {
		return fmt.Errorf("extension: model set %s has phase %s: model sets may only register for %s, the only phase the host executes for AutoMigrate", id, s.Phase, s.executedPhase())
	}
	if len(s.Models) == 0 {
		return fmt.Errorf("extension: model set %s has no models", id)
	}
	for i, model := range s.Models {
		if isNilModel(model) {
			return fmt.Errorf("extension: model set %s has a nil model at index %d", id, i)
		}
	}
	return nil
}

// isNilModel catches both a nil interface and typed nil values stored in an
// interface (for example (*User)(nil)). Passing either form to GORM would defer
// a composition error until migration execution, so registry validation rejects
// it while the owning descriptor identity is still available.
func isNilModel(model any) bool {
	if model == nil {
		return true
	}
	value := reflect.ValueOf(model)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// DataStep is a single idempotent data migration an extension runs in a given
// phase. Steps must tolerate running against an already migrated database.
type DataStep struct {
	Extension string
	Name      string
	Phase     MigrationPhase
	Run       func(*gorm.DB) error
}

func (s DataStep) id() descriptorID {
	return descriptorID{extension: s.Extension, name: s.Name}
}

func (s DataStep) validate() error {
	id := s.id()
	if s.Extension == "" {
		return fmt.Errorf("extension: data step %s has an empty extension", id)
	}
	if s.Name == "" {
		return fmt.Errorf("extension: data step %s has an empty name", id)
	}
	if !s.Phase.valid() {
		return fmt.Errorf("extension: data step %s has invalid phase %s", id, s.Phase)
	}
	if s.Phase != s.executedPhase() {
		return fmt.Errorf("extension: data step %s has phase %s: data steps may only register for %s, the only phase the host executes for RunStepsFor", id, s.Phase, s.executedPhase())
	}
	if s.Run == nil {
		return fmt.Errorf("extension: data step %s has a nil Run function", id)
	}
	return nil
}

// RegisterModelSet adds a model set. It rejects invalid descriptors and an
// identity already used by another model set, and panics if the registry is
// already frozen.
func (r *Registry) RegisterModelSet(set ModelSet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requireMutableLocked("model set", set.id())
	if err := set.validate(); err != nil {
		return err
	}
	for _, existing := range r.modelSets {
		if existing.id() == set.id() {
			return fmt.Errorf("extension: duplicate model set %s", set.id())
		}
	}
	set.Models = append([]any(nil), set.Models...)
	r.modelSets = append(r.modelSets, set)
	return nil
}

// RegisterDataStep adds a data migration step. It rejects invalid descriptors
// and an identity already used by another data step, and panics if the registry
// is already frozen.
func (r *Registry) RegisterDataStep(step DataStep) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requireMutableLocked("data step", step.id())
	if err := step.validate(); err != nil {
		return err
	}
	for _, existing := range r.dataSteps {
		if existing.id() == step.id() {
			return fmt.Errorf("extension: duplicate data step %s", step.id())
		}
	}
	r.dataSteps = append(r.dataSteps, step)
	return nil
}

// ModelSets returns every registered model set ordered by phase, then by
// registration order within a phase.
func (r *Registry) ModelSets() []ModelSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sets := make([]ModelSet, 0, len(r.modelSets))
	for _, phase := range migrationPhaseOrder {
		for _, set := range r.modelSets {
			if set.Phase == phase {
				set.Models = append([]any(nil), set.Models...)
				sets = append(sets, set)
			}
		}
	}
	return sets
}

// ModelsFor returns the models registered for one phase, flattened in
// registration order and ready to pass to gorm's AutoMigrate. An unknown phase
// yields no models.
func (r *Registry) ModelsFor(phase MigrationPhase) []any {
	if !phase.valid() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var models []any
	for _, set := range r.modelSets {
		if set.Phase == phase {
			models = append(models, set.Models...)
		}
	}
	return models
}

// Steps returns every registered data step ordered by phase, then by
// registration order within a phase.
func (r *Registry) Steps() []DataStep {
	r.mu.RLock()
	defer r.mu.RUnlock()
	steps := make([]DataStep, 0, len(r.dataSteps))
	for _, phase := range migrationPhaseOrder {
		for _, step := range r.dataSteps {
			if step.Phase == phase {
				steps = append(steps, step)
			}
		}
	}
	return steps
}

// StepsFor returns the data steps registered for one phase, in registration
// order. An unknown phase yields no steps.
func (r *Registry) StepsFor(phase MigrationPhase) []DataStep {
	if !phase.valid() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var steps []DataStep
	for _, step := range r.dataSteps {
		if step.Phase == phase {
			steps = append(steps, step)
		}
	}
	return steps
}

// RunStepsFor executes the data steps for one phase in registration order. It
// fails closed: the first failing step aborts the phase and its error is
// returned wrapped with the step identity.
func (r *Registry) RunStepsFor(db *gorm.DB, phase MigrationPhase) error {
	if !phase.valid() {
		return fmt.Errorf("extension: cannot run migration steps for invalid phase %s", phase)
	}
	steps := r.StepsFor(phase)
	if len(steps) == 0 {
		return nil
	}
	if db == nil {
		return errors.New("extension: cannot run migration steps with a nil database")
	}
	for _, step := range steps {
		if err := step.Run(db); err != nil {
			return fmt.Errorf("migration step %s failed: %w", step.id(), err)
		}
	}
	return nil
}
