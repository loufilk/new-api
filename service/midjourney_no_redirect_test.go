package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type authenticatedRequestRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip authenticatedRequestRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestMidjourneyAuxiliaryRequestReturnsRedirectWithoutFollowing(t *testing.T) {
	originalClient := httpClient
	t.Cleanup(func() { httpClient = originalClient })
	requestCount := 0
	redirectPolicyCalls := 0
	const responseBody = `{"code":1,"description":"fixture redirect","result":"fixture-result"}`
	httpClient = &http.Client{
		Transport: authenticatedRequestRoundTripper(func(request *http.Request) (*http.Response, error) {
			requestCount++
			if requestCount > 1 {
				return nil, errors.New("fixture rejected unexpected follow-up request")
			}
			require.Equal(t, "fixture-only", request.Header.Get("mj-api-secret"))
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {"https://redirect.invalid/next"}},
				Body:       io.NopCloser(strings.NewReader(responseBody)),
				Request:    request,
			}, nil
		}),
		CheckRedirect: func(*http.Request, []*http.Request) error {
			redirectPolicyCalls++
			return nil
		},
		Timeout: time.Minute,
	}
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/mj/submit/imagine", strings.NewReader(`{}`))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Set(string(constant.ContextKeyChannelKey), "Bearer fixture-only")
	response, body, err := DoMidjourneyHttpRequest(context, time.Minute, "https://origin.invalid/mj/submit/imagine")
	require.NoError(t, err)
	require.Equal(t, http.StatusFound, response.StatusCode)
	require.Equal(t, []byte(responseBody), body)
	require.Equal(t, "fixture-result", response.Response.Result)
	require.Equal(t, 1, requestCount)
	require.Zero(t, redirectPolicyCalls)
	require.NoError(t, httpClient.CheckRedirect(nil, nil))
	require.Equal(t, 1, redirectPolicyCalls, "the cached client's policy must remain unchanged")
}
