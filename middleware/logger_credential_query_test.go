package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccessLogRedactsCredentialQueryWithoutChangingRequest(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		query         string
		want          string
		mutateRequest bool
	}{
		{name: "encoded question mark in path", path: "/v1beta/models/part%3Fsection", query: "key=fixture-secret&page=2", want: "key=[REDACTED]&page=2"},
		{name: "downstream URL mutation does not alter log snapshot", query: "key=fixture-secret&page=2", want: "key=[REDACTED]&page=2", mutateRequest: true},
		{name: "supported Gemini key", query: "key=fixture-secret&page=2", want: "key=[REDACTED]&page=2"},
		{name: "duplicates and encoded name", query: "key=fixture-secret&%6b%65%79=fixture-secret&KEY=fixture-secret", want: "key=[REDACTED]&%6b%65%79=[REDACTED]&KEY=[REDACTED]"},
		{name: "credential aliases", query: "api_key=fixture-secret&x-goog-api-key=fixture-secret&mj-api-secret=fixture-secret&access_token=fixture-secret&client_secret=fixture-secret", want: "api_key=[REDACTED]&x-goog-api-key=[REDACTED]&mj-api-secret=[REDACTED]&access_token=[REDACTED]&client_secret=[REDACTED]"},
		{name: "session identity and AWS aliases", query: "session_token=fixture-secret&id_token=fixture-secret&AWSAccessKeyId=fixture-secret&api-version=2024-02-01", want: "session_token=[REDACTED]&id_token=[REDACTED]&AWSAccessKeyId=[REDACTED]&api-version=2024-02-01"},
		{name: "mixed encoded duplicate aliases", query: "%20Session_Token%20=fixture-secret&SESSION-TOKEN=fixture-secret&%61wsaccesskeyid=fixture-secret&id_token=fixture-secret&id_token=fixture-secret", want: "%20Session_Token%20=[REDACTED]&SESSION-TOKEN=[REDACTED]&%61wsaccesskeyid=[REDACTED]&id_token=[REDACTED]&id_token=[REDACTED]"},
		{name: "provider credential patterns", query: "vendor_token=fixture-secret&upstream_secret=fixture-secret&request_signature=fixture-secret&apikey_hint=visible", want: "vendor_token=[REDACTED]&upstream_secret=[REDACTED]&request_signature=[REDACTED]&apikey_hint=visible"},
		{name: "encoded path delimiters with credentials", path: "/v1/models%3Fpart%23section", query: "id_token=fixture-secret&alt=sse", want: "id_token=[REDACTED]&alt=sse"},
		{name: "OAuth query fully suppressed", path: "/%61pi/oauth/test", query: "code=fixture-secret&state=fixture-secret&custom=fixture-secret"},
		{name: "OAuth encoded separator query fully suppressed", path: "/oauth%2Fcallback", query: "code=fixture-secret&custom=fixture-secret"},
		{name: "Stripe query fully suppressed", path: "/api/stripe/webhook", query: "event=fixture-secret&custom=fixture-secret"},
		{name: "Creem query fully suppressed", path: "/api/creem/webhook", query: "event=fixture-secret&custom=fixture-secret"},
		{name: "Waffo query fully suppressed", path: "/api/waffo/webhook", query: "event=fixture-secret&custom=fixture-secret"},
		{name: "Waffo pancake query fully suppressed", path: "/api/waffo-pancake/webhook/test", query: "event=fixture-secret&custom=fixture-secret"},
		{name: "Epay notify query fully suppressed", path: "/api/user/epay/notify", query: "order=fixture-secret&custom=fixture-secret"},
		{name: "subscription notify query fully suppressed", path: "/api/subscription/epay/notify", query: "order=fixture-secret&custom=fixture-secret"},
		{name: "subscription return query fully suppressed", path: "/api/subscription/epay/return", query: "order=fixture-secret&custom=fixture-secret"},
		{name: "encoded credential value", query: "key=fixture%2Dsecret&model=demo%2Fmodel", want: "key=[REDACTED]&model=demo%2Fmodel"},
		{name: "empty credential", query: "key&key=&page=1", want: "key=[REDACTED]&key=[REDACTED]&page=1"},
		{name: "malformed query fails closed", query: "key=fixture-secret&bad=%ZZ", want: "[REDACTED]"},
		{name: "semicolon query fails closed", query: "page=1;key=fixture-secret", want: "[REDACTED]"},
		{name: "harmless diagnostics unchanged", query: "page=2&model=demo%2Fmodel&apikey_hint=visible", want: "page=2&model=demo%2Fmodel&apikey_hint=visible"},
		{name: "no query", query: "", want: ""},
	}

	originalWriter := gin.DefaultWriter
	originalMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		gin.DefaultWriter = originalWriter
		gin.SetMode(originalMode)
	})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			gin.DefaultWriter = &logs
			server := gin.New()
			SetUpLogger(server)
			path := test.path
			if path == "" {
				path = "/v1beta/models/demo"
			}
			target := path
			if test.query != "" {
				target += "?" + test.query
			}
			server.NoRoute(func(c *gin.Context) {
				require.Equal(t, test.query, c.Request.URL.RawQuery)
				require.Equal(t, target, c.Request.RequestURI)
				if test.mutateRequest {
					c.Request.URL.Path = "/rewritten"
					c.Request.URL.RawQuery = "key=fixture-after-handler"
				}
				c.Status(http.StatusNoContent)
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, target, nil)
			originalURL := *request.URL
			server.ServeHTTP(response, request)
			require.Equal(t, http.StatusNoContent, response.Code)
			require.Equal(t, target, request.RequestURI)
			if !test.mutateRequest {
				require.Equal(t, originalURL, *request.URL, "logging must not rewrite the signed request")
			}
			wantPath := path
			if test.want != "" {
				wantPath += "?" + test.want
			}
			require.Contains(t, logs.String(), "GET "+wantPath+"\n")
			require.NotContains(t, logs.String(), "fixture-secret")
			require.NotContains(t, logs.String(), "fixture%2Dsecret")
			require.NotContains(t, logs.String(), "fixture-after-handler")
		})
	}
}
