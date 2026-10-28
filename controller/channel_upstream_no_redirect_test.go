package controller

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/require"
)

type modelDiscoveryRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip modelDiscoveryRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestFetchModelsResponseReturnsRedirectWithoutFollowing(t *testing.T) {
	client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
	require.NoError(t, err)
	originalClient := *client
	t.Cleanup(func() { *client = originalClient })
	requestCount := 0
	redirectPolicyCalls := 0
	client.Transport = modelDiscoveryRoundTripper(func(request *http.Request) (*http.Response, error) {
		requestCount++
		if requestCount > 1 {
			return nil, errors.New("fixture rejected unexpected follow-up request")
		}
		require.Equal(t, "fixture-only", request.Header.Get("X-Custom-Authorization"))
		require.Equal(t, "configured.invalid", request.Host)
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
	headers := http.Header{"X-Custom-Authorization": {"fixture-only"}, "Host": {"configured.invalid"}}
	body, err := getFetchModelsResponseBody(http.MethodGet, "https://origin.invalid/models", &model.Channel{}, headers)
	require.Nil(t, body)
	require.EqualError(t, err, "status code: 302")
	require.Equal(t, 1, requestCount)
	require.Zero(t, redirectPolicyCalls)
	require.NoError(t, client.CheckRedirect(nil, nil))
	require.Equal(t, 1, redirectPolicyCalls, "the cached client's policy must remain unchanged")
}
