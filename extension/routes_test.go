package extension

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoutePrioritiesAreClosedAndOrdered(t *testing.T) {
	assert.Equal(t, []RoutePriority{PriorityBeforePlugin, PriorityAfterPlugin}, RoutePriorities())
	assert.Equal(t, "before_plugin", PriorityBeforePlugin.String())
	assert.Equal(t, "after_plugin", PriorityAfterPlugin.String())
	assert.Equal(t, "RoutePriority(0)", RoutePriority(0).String())

	assert.True(t, PriorityBeforePlugin.valid())
	assert.True(t, PriorityAfterPlugin.valid())
	for _, priority := range []RoutePriority{0, -1, 3, 42} {
		assert.False(t, priority.valid(), "priority %d must be rejected", int(priority))
	}

	priorities := RoutePriorities()
	priorities[0] = PriorityAfterPlugin
	assert.Equal(t, PriorityBeforePlugin, RoutePriorities()[0])
}

func TestRegisterRouteSetRejectsInvalidDescriptors(t *testing.T) {
	cases := []struct {
		name    string
		set     RouteSet
		wantErr string
	}{
		{
			name:    "empty extension",
			set:     RouteSet{Name: "api", Priority: PriorityAfterPlugin, Register: noopRoutes},
			wantErr: "extension: route set <empty>/api has an empty extension",
		},
		{
			name:    "empty name",
			set:     RouteSet{Extension: "chuanapi", Priority: PriorityAfterPlugin, Register: noopRoutes},
			wantErr: "extension: route set chuanapi/<empty> has an empty name",
		},
		{
			name:    "invalid priority",
			set:     RouteSet{Extension: "chuanapi", Name: "api", Register: noopRoutes},
			wantErr: "extension: route set chuanapi/api has invalid priority RoutePriority(0)",
		},
		{
			name:    "nil register",
			set:     RouteSet{Extension: "chuanapi", Name: "api", Priority: PriorityAfterPlugin},
			wantErr: "extension: route set chuanapi/api must define exactly one route registrar",
		},
		{
			name: "both registrars",
			set: RouteSet{
				Extension: "chuanapi", Name: "api", Priority: PriorityAfterPlugin,
				Register: noopRoutes, RegisterScoped: func(RouteScopes) error { return nil },
			},
			wantErr: "extension: route set chuanapi/api must define exactly one route registrar",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			err := reg.RegisterRouteSet(tc.set)
			require.Error(t, err)
			assert.EqualError(t, err, tc.wantErr)
			assert.Empty(t, reg.RouteSets(), "a rejected descriptor is not stored")
		})
	}
}

func TestDuplicateRouteSetIdentityIsRejectedWithoutOverwriting(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "api", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))
	err := reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "api", Priority: PriorityBeforePlugin, Register: noopRoutes,
	})
	require.Error(t, err)
	assert.EqualError(t, err, "extension: duplicate route set chuanapi/api")

	sets := reg.RouteSets()
	require.Len(t, sets, 1)
	assert.Equal(t, PriorityAfterPlugin, sets[0].Priority, "the original priority survived")

	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "admin", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))
}

func TestRouteSetOrderIsStableAcrossAndWithinPriorities(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "after-1", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "before-1", Priority: PriorityBeforePlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "after-2", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "before-2", Priority: PriorityBeforePlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.Freeze())

	var all []string
	for _, set := range reg.RouteSets() {
		all = append(all, set.Name)
	}
	assert.Equal(t, []string{"before-1", "before-2", "after-1", "after-2"}, all)

	var after []string
	for _, set := range reg.RouteSetsFor(PriorityAfterPlugin) {
		after = append(after, set.Name)
	}
	assert.Equal(t, []string{"after-1", "after-2"}, after)
	assert.Nil(t, reg.RouteSetsFor(RoutePriority(0)), "unknown priority yields no route sets")
}

func TestRouteSetReadsReturnDefensiveCopies(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "api", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.Freeze())

	all := reg.RouteSets()
	all[0].Name = "mutated"
	all[0].Priority = PriorityBeforePlugin
	require.Len(t, reg.RouteSets(), 1)
	assert.Equal(t, "api", reg.RouteSets()[0].Name)
	assert.Equal(t, PriorityAfterPlugin, reg.RouteSets()[0].Priority)
	assert.Empty(t, reg.RouteSetsFor(PriorityBeforePlugin))

	byPriority := reg.RouteSetsFor(PriorityAfterPlugin)
	byPriority[0].Name = "mutated"
	assert.Equal(t, "api", reg.RouteSetsFor(PriorityAfterPlugin)[0].Name)
}

// TestApplyRoutesAttachesHandlersLikeLlmsRoutes mirrors how the host attaches
// /llms.txt after the plugin dispatcher snapshot: the descriptor only receives
// the engine and binds handlers to it.
func TestApplyRoutesAttachesHandlersLikeLlmsRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := NewRegistry()
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "llms", Priority: PriorityAfterPlugin,
		Register: func(engine *gin.Engine) error {
			engine.GET("/extension-llms.txt", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
			engine.HEAD("/extension-llms.txt", func(c *gin.Context) { c.Status(http.StatusOK) })
			return nil
		},
	}))
	require.NoError(t, reg.Freeze())

	engine := gin.New()
	require.NoError(t, reg.ApplyRoutes(engine, PriorityAfterPlugin))
	require.NoError(t, reg.ApplyRoutes(engine, PriorityBeforePlugin), "a priority with no sets is a no-op")

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/extension-llms.txt", nil))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "ok", recorder.Body.String())

	var methods []string
	for _, route := range engine.Routes() {
		if route.Path == "/extension-llms.txt" {
			methods = append(methods, route.Method)
		}
	}
	assert.ElementsMatch(t, []string{http.MethodGet, http.MethodHead}, methods)
}

func TestApplyRoutesWithScopesPassesHostMountPoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := NewRegistry()
	engine := gin.New()
	api := engine.Group("/api")
	user := api.Group("/user")
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "scoped", Priority: PriorityBeforePlugin,
		RegisterScoped: func(scopes RouteScopes) error {
			require.Same(t, engine, scopes.Engine)
			require.Same(t, api, scopes.API)
			require.Same(t, user, scopes.UserSelf)
			scopes.UserSelf.GET("/scoped", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			return nil
		},
	}))
	require.NoError(t, reg.Freeze())
	require.NoError(t, reg.ApplyRoutesWithScopes(RouteScopes{Engine: engine, API: api, UserSelf: user}, PriorityBeforePlugin))

	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/user/scoped", nil))
	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestApplyRoutesExecutesInOrderAndWrapsFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var executed []string
	reg := NewRegistry()
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "first", Priority: PriorityBeforePlugin,
		Register: func(*gin.Engine) error { executed = append(executed, "first"); return nil },
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "second", Priority: PriorityBeforePlugin,
		Register: func(*gin.Engine) error { executed = append(executed, "second"); return errBoom },
	}))
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "third", Priority: PriorityBeforePlugin,
		Register: func(*gin.Engine) error { executed = append(executed, "third"); return nil },
	}))
	require.NoError(t, reg.Freeze())

	err := reg.ApplyRoutes(gin.New(), PriorityBeforePlugin)
	require.Error(t, err)
	assert.EqualError(t, err, "route set chuanapi/second failed: boom")
	assert.ErrorIs(t, err, errBoom, "the cause stays inspectable")
	assert.Equal(t, []string{"first", "second"}, executed, "fail-closed: later sets do not run")
}

func TestApplyRoutesRejectsInvalidPriorityAndNilEngine(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterRouteSet(RouteSet{
		Extension: "chuanapi", Name: "api", Priority: PriorityAfterPlugin, Register: noopRoutes,
	}))
	require.NoError(t, reg.Freeze())

	err := reg.ApplyRoutes(gin.New(), RoutePriority(0))
	require.Error(t, err)
	assert.EqualError(t, err, "extension: cannot apply routes for invalid priority RoutePriority(0)")

	err = reg.ApplyRoutes(nil, PriorityAfterPlugin)
	require.Error(t, err)
	assert.EqualError(t, err, "extension: cannot apply routes with a nil engine")

	require.NoError(t, reg.ApplyRoutes(nil, PriorityBeforePlugin), "no sets means nothing to attach")
}
