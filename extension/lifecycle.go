package extension

import "fmt"

// LifecyclePhase names startup points where extension-owned wiring may run.
// Lifecycle callbacks are build-time contributions: they must only register
// capabilities/startup hooks and must not start background goroutines directly.
type LifecyclePhase int

const (
	// PhaseBeforeBackgroundWorkers runs after core resources are initialized but
	// before host scheduled tasks and background workers are started.
	PhaseBeforeBackgroundWorkers LifecyclePhase = iota + 1
)

var lifecyclePhaseOrder = [...]LifecyclePhase{PhaseBeforeBackgroundWorkers}

func LifecyclePhases() []LifecyclePhase {
	return append([]LifecyclePhase(nil), lifecyclePhaseOrder[:]...)
}

func (p LifecyclePhase) valid() bool { return p == PhaseBeforeBackgroundWorkers }

func (p LifecyclePhase) String() string {
	switch p {
	case PhaseBeforeBackgroundWorkers:
		return "before_background_workers"
	default:
		return fmt.Sprintf("LifecyclePhase(%d)", int(p))
	}
}

// LifecycleSet is a startup contribution owned by one extension module.
type LifecycleSet struct {
	Extension string
	Name      string
	Phase     LifecyclePhase
	Run       func() error
}

func (s LifecycleSet) id() descriptorID {
	return descriptorID{extension: s.Extension, name: s.Name}
}

func (s LifecycleSet) validate() error {
	id := s.id()
	if s.Extension == "" {
		return fmt.Errorf("extension: lifecycle set %s has an empty extension", id)
	}
	if s.Name == "" {
		return fmt.Errorf("extension: lifecycle set %s has an empty name", id)
	}
	if !s.Phase.valid() {
		return fmt.Errorf("extension: lifecycle set %s has invalid phase %s", id, s.Phase)
	}
	if s.Run == nil {
		return fmt.Errorf("extension: lifecycle set %s has a nil Run function", id)
	}
	return nil
}

func (r *Registry) RegisterLifecycleSet(set LifecycleSet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requireMutableLocked("lifecycle set", set.id())
	if err := set.validate(); err != nil {
		return err
	}
	for _, existing := range r.lifecycleSets {
		if existing.id() == set.id() {
			return fmt.Errorf("extension: duplicate lifecycle set %s", set.id())
		}
	}
	r.lifecycleSets = append(r.lifecycleSets, set)
	return nil
}

func (r *Registry) LifecycleSetsFor(phase LifecyclePhase) []LifecycleSet {
	if !phase.valid() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sets []LifecycleSet
	for _, set := range r.lifecycleSets {
		if set.Phase == phase {
			sets = append(sets, set)
		}
	}
	return sets
}

// ApplyLifecycle executes the frozen phase snapshot in registration order and
// fails closed on the first error. A phase with no contributions is a no-op.
func (r *Registry) ApplyLifecycle(phase LifecyclePhase) error {
	if !phase.valid() {
		return fmt.Errorf("extension: cannot apply lifecycle for invalid phase %s", phase)
	}
	for _, set := range r.LifecycleSetsFor(phase) {
		if err := set.Run(); err != nil {
			return fmt.Errorf("lifecycle set %s failed: %w", set.id(), err)
		}
	}
	return nil
}
