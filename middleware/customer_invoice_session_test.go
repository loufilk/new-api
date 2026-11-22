package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCustomerInvoiceHTTPBrowserWriteGuards(t *testing.T) {
	for _, tc := range []struct {
		name, sid, bearer, origin, site string
		pat                             bool
		want                            int
	}{
		{name: "live session", sid: "test-session", bearer: "synthetic", origin: "http://example.com", want: http.StatusNoContent},
		{name: "missing browser identity", bearer: "synthetic", want: http.StatusForbidden},
		{name: "no bearer", sid: "test-session", want: http.StatusForbidden},
		{name: "personal access token", sid: "test-session", bearer: "synthetic", pat: true, want: http.StatusForbidden},
		{name: "token-bound identity", sid: model.AccessTokenSessionID(1), bearer: "synthetic", want: http.StatusForbidden},
		{name: "foreign origin", sid: "test-session", bearer: "synthetic", origin: "https://other.invalid", want: http.StatusForbidden},
		{name: "cross-site", sid: "test-session", bearer: "synthetic", site: "cross-site", want: http.StatusForbidden},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				e := gin.New()
				e.Handle(method, "/", DisableCache(), func(c *gin.Context) {
					// Simulate what the preceding auth middleware populates. The
					// routed integration tests also exercise real sessions.
					c.Set("id", 2)
					c.Set("auth_version", int64(1))
					c.Set("session_version", int64(1))
					c.Set("session_id", tc.sid)
					c.Set("use_access_token", tc.pat)
				}, CustomerInvoiceSessionRequired(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
				r := httptest.NewRequest(method, "http://example.com/", nil)
				if tc.bearer != "" {
					r.Header.Set("Authorization", "Bearer "+tc.bearer)
				}
				if tc.origin != "" {
					r.Header.Set("Origin", tc.origin)
				}
				if tc.site != "" {
					r.Header.Set("Sec-Fetch-Site", tc.site)
				}
				w := httptest.NewRecorder()
				e.ServeHTTP(w, r)
				require.Equal(t, tc.want, w.Code)
				require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			})
		}
	}
}

func TestCustomerInvoiceHTTPAccessLogsOmitCustomerData(t *testing.T) {
	e := gin.New()
	e.Use(snapshotAccessLogPath())
	e.GET("/api/invoice/applications/:id", func(c *gin.Context) {
		require.Equal(t, "/api/invoice/applications/:id", c.GetString(accessLogPathSnapshotKey))
		require.Equal(t, "private-fixture", c.Param("id"))
		require.Equal(t, "private-fixture", c.Query("tax_id"))
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/invoice/applications/private-fixture?tax_id=private-fixture", nil))
	require.Equal(t, http.StatusNoContent, w.Code)
}
