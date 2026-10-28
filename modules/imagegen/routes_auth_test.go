package imagegen

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/pkg/lifecycle"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageTasksRequireAuthentication(t *testing.T) {
	engine := gin.New()
	reg := extension.NewRegistry()
	require.NoError(t, Register(reg, lifecycle.NewGroup()))
	require.NoError(t, reg.Freeze())
	require.NoError(t, reg.ApplyRoutes(engine, extension.PriorityBeforePlugin))
	for _, tc := range []struct{ method, path string }{
		{"POST", "/v1/images/tasks/generations"},
		{"POST", "/v1/images/tasks/edits"},
		{"GET", "/v1/images/tasks/" + strings.Repeat("a", 48)},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"image","prompt":"test"}`))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, r)
			assert.Equal(t, 401, w.Code)
		})
	}
}
