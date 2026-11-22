package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDoRequestWithoutRedirectsPreservesResponseAndClientSettings(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			jar, err := cookiejar.New(nil)
			require.NoError(t, err)
			origin, err := url.Parse("https://origin.invalid/request")
			require.NoError(t, err)
			jar.SetCookies(origin, []*http.Cookie{{Name: "fixture-cookie", Value: "fixture-only"}})
			requestCount := 0
			redirectPolicyCalls := 0
			transport := authenticatedRequestRoundTripper(func(request *http.Request) (*http.Response, error) {
				requestCount++
				if requestCount > 1 {
					return nil, errors.New("fixture rejected unexpected follow-up request")
				}
				require.Equal(t, "fixture-only", request.Header.Get("X-Fixture-Credential"))
				cookie, err := request.Cookie("fixture-cookie")
				require.NoError(t, err)
				require.Equal(t, "fixture-only", cookie.Value)
				_, hasDeadline := request.Context().Deadline()
				require.True(t, hasDeadline, "the configured client timeout must still apply")
				return &http.Response{
					StatusCode: status,
					Header:     http.Header{"Location": {"https://redirect.invalid/next"}},
					Body:       io.NopCloser(strings.NewReader("fixture response body")),
					Request:    request,
				}, nil
			})
			client := &http.Client{
				Transport: transport,
				Timeout:   time.Minute,
				Jar:       jar,
				CheckRedirect: func(*http.Request, []*http.Request) error {
					redirectPolicyCalls++
					return nil
				},
			}
			request, err := http.NewRequest(http.MethodGet, origin.String(), nil)
			require.NoError(t, err)
			request.Header.Set("X-Fixture-Credential", "fixture-only")
			response, err := DoRequestWithoutRedirects(client, request)
			require.NoError(t, err)
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			require.NoError(t, err)
			require.Equal(t, status, response.StatusCode)
			require.Equal(t, "fixture response body", string(body))
			require.Equal(t, "https://redirect.invalid/next", response.Header.Get("Location"))
			require.Equal(t, 1, requestCount)
			require.Zero(t, redirectPolicyCalls)
			require.Same(t, jar, client.Jar)
			require.Equal(t, time.Minute, client.Timeout)
			require.NoError(t, client.CheckRedirect(nil, nil))
			require.Equal(t, 1, redirectPolicyCalls, "the configured shared redirect policy must not be replaced")
		})
	}
}

func TestDoRequestWithoutRedirectsRejectsUninitializedClient(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "https://origin.invalid/request", nil)
	require.NoError(t, err)
	response, err := DoRequestWithoutRedirects(nil, request)
	require.Nil(t, response)
	require.EqualError(t, err, "HTTP client is not initialized")
}
