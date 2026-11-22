package gemini

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

type geminiModelsRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip geminiModelsRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestFetchGeminiModelsReturnsRedirectWithoutFollowing(t *testing.T) {
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	originalClient := *client
	t.Cleanup(func() { *client = originalClient })
	requestCount := 0
	redirectPolicyCalls := 0
	client.Transport = geminiModelsRoundTripper(func(request *http.Request) (*http.Response, error) {
		requestCount++
		if requestCount > 1 {
			return nil, errors.New("fixture rejected unexpected follow-up request")
		}
		require.Equal(t, "fixture-only", request.Header.Get("x-goog-api-key"))
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": {"https://redirect.invalid/models"}},
			Body:       io.NopCloser(strings.NewReader("fixture redirect body")),
			Request:    request,
		}, nil
	})
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		redirectPolicyCalls++
		return nil
	}
	models, err := FetchGeminiModels("https://origin.invalid", "fixture-only", dto.ChannelSettings{})
	require.Nil(t, models)
	require.ErrorContains(t, err, "server returned error 302: fixture redirect body")
	require.Equal(t, 1, requestCount)
	require.Zero(t, redirectPolicyCalls)
	require.NoError(t, client.CheckRedirect(nil, nil))
	require.Equal(t, 1, redirectPolicyCalls, "the cached client's policy must remain unchanged")
}
