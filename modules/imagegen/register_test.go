package imagegen

import (
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegisterOwnsAsyncImageTaskRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	reg := extension.NewRegistry()
	require.Error(t, Register(reg, nil), "routes must never create untracked background work")
	require.NoError(t, Register(reg, lifecycle.NewGroup()))
	require.NoError(t, reg.Freeze())
	engine := gin.New()
	require.NoError(t, reg.ApplyRoutes(engine, extension.PriorityBeforePlugin))

	want := map[string]bool{
		"POST /v1/images/tasks/generations": false,
		"POST /v1/images/tasks/edits":       false,
		"GET /v1/images/tasks/:id":          false,
	}
	for _, route := range engine.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		assert.True(t, found, "missing async image route %s", route)
	}
}
