package extension

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
)

// RouteScopes are host-owned mount points whose middleware policy has already
// been applied. Extensions may attach endpoints to these groups without
// reconstructing the host's API middleware chain.
//
// Fields are intentionally concrete gin handles instead of host DTOs/packages,
// preserving extension/'s leaf-package dependency rule (stdlib + gin only).
type RouteScopes struct {
	Engine                    *gin.Engine
	API                       *gin.RouterGroup
	UserSelf                  *gin.RouterGroup
	SubscriptionUser          *gin.RouterGroup
	RootOptions               *gin.RouterGroup
	AdminModels               *gin.RouterGroup
	PublicAPI                 *gin.RouterGroup
	BrowserSession            *gin.RouterGroup
	AnonymousRequestBodyLimit gin.HandlerFunc

	// PaymentRoutes is the host-owned registrar for payment provider endpoints.
	// The host router supplies it so provider handler bodies can live in host
	// controller/service packages while extension route descriptors keep their
	// historical identity, order and URL/middleware contract. Route sets that
	// only forward host payment routes must delegate here and fail closed when
	// the registrar is unavailable.
	PaymentRoutes func(RouteScopes) error
}

// RoutePriority is a closed enum naming the points in host router wiring where
// an extension route set may be attached. The split exists because the host
// snapshots the engine's static routes when it builds the plugin dispatcher:
// routes registered before that snapshot are visible to it, routes registered
// after are not.
type RoutePriority int

const (
	// PriorityBeforePlugin attaches routes while the host is still building its
	// static route table, so the plugin dispatcher's snapshot includes them.
	PriorityBeforePlugin RoutePriority = iota + 1
	// PriorityAfterPlugin attaches routes after the plugin dispatcher snapshot
	// has been taken. This matches how the host registers /llms.txt today.
	PriorityAfterPlugin
)

// routePriorityOrder is the canonical execution order of the priorities.
var routePriorityOrder = [...]RoutePriority{PriorityBeforePlugin, PriorityAfterPlugin}

// RoutePriorities returns the priorities in their fixed execution order.
func RoutePriorities() []RoutePriority {
	return append([]RoutePriority(nil), routePriorityOrder[:]...)
}

func (p RoutePriority) valid() bool {
	return p == PriorityBeforePlugin || p == PriorityAfterPlugin
}

func (p RoutePriority) String() string {
	switch p {
	case PriorityBeforePlugin:
		return "before_plugin"
	case PriorityAfterPlugin:
		return "after_plugin"
	default:
		return fmt.Sprintf("RoutePriority(%d)", int(p))
	}
}

// RouteSet is a build-time descriptor for the routes one extension attaches to
// the host engine. Register is called by the host during router wiring; it must
// only attach handlers and must not depend on host router or controller
// packages, keeping the dependency edge one-directional.
type RouteSet struct {
	Extension      string
	Name           string
	Priority       RoutePriority
	Register       func(*gin.Engine) error
	RegisterScoped func(RouteScopes) error
}

func (s RouteSet) id() descriptorID {
	return descriptorID{extension: s.Extension, name: s.Name}
}

func (s RouteSet) validate() error {
	id := s.id()
	if s.Extension == "" {
		return fmt.Errorf("extension: route set %s has an empty extension", id)
	}
	if s.Name == "" {
		return fmt.Errorf("extension: route set %s has an empty name", id)
	}
	if !s.Priority.valid() {
		return fmt.Errorf("extension: route set %s has invalid priority %s", id, s.Priority)
	}
	if (s.Register == nil) == (s.RegisterScoped == nil) {
		return fmt.Errorf("extension: route set %s must define exactly one route registrar", id)
	}
	return nil
}

// RegisterRouteSet adds a route set. It rejects invalid descriptors and an
// identity already used by another route set, and panics if the registry is
// already frozen.
func (r *Registry) RegisterRouteSet(set RouteSet) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requireMutableLocked("route set", set.id())
	if err := set.validate(); err != nil {
		return err
	}
	for _, existing := range r.routeSets {
		if existing.id() == set.id() {
			return fmt.Errorf("extension: duplicate route set %s", set.id())
		}
	}
	r.routeSets = append(r.routeSets, set)
	return nil
}

// RouteSets returns every registered route set ordered by priority, then by
// registration order within a priority.
func (r *Registry) RouteSets() []RouteSet {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sets := make([]RouteSet, 0, len(r.routeSets))
	for _, priority := range routePriorityOrder {
		for _, set := range r.routeSets {
			if set.Priority == priority {
				sets = append(sets, set)
			}
		}
	}
	return sets
}

// RouteSetsFor returns the route sets registered for one priority, in
// registration order. An unknown priority yields no route sets.
func (r *Registry) RouteSetsFor(priority RoutePriority) []RouteSet {
	if !priority.valid() {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var sets []RouteSet
	for _, set := range r.routeSets {
		if set.Priority == priority {
			sets = append(sets, set)
		}
	}
	return sets
}

// ApplyRoutes attaches the route sets registered for one priority, in
// registration order. It fails closed: the first failing route set aborts the
// priority and its error is returned wrapped with the route set identity.
func (r *Registry) ApplyRoutes(engine *gin.Engine, priority RoutePriority) error {
	return r.ApplyRoutesWithScopes(RouteScopes{Engine: engine}, priority)
}

// ApplyRoutesWithScopes attaches route contributions with host-owned mount
// points. Legacy engine-only contributions still receive scopes.Engine.
func (r *Registry) ApplyRoutesWithScopes(scopes RouteScopes, priority RoutePriority) error {
	if !priority.valid() {
		return fmt.Errorf("extension: cannot apply routes for invalid priority %s", priority)
	}
	sets := r.RouteSetsFor(priority)
	if len(sets) == 0 {
		return nil
	}
	if scopes.Engine == nil {
		return errors.New("extension: cannot apply routes with a nil engine")
	}
	for _, set := range sets {
		var err error
		if set.RegisterScoped != nil {
			err = set.RegisterScoped(scopes)
		} else {
			err = set.Register(scopes.Engine)
		}
		if err != nil {
			return fmt.Errorf("route set %s failed: %w", set.id(), err)
		}
	}
	return nil
}
