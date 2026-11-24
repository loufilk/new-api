package service

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

type notifyErrorTransport func(*http.Request) (*http.Response, error)

func (f notifyErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestGotifyErrorsDoNotExposeNotificationCredentials(t *testing.T) {
	originalClient, originalWorker := httpClient, system_setting.WorkerUrl
	fetch := system_setting.GetFetchSetting()
	originalProtection := fetch.EnableSSRFProtection
	t.Cleanup(func() {
		httpClient, system_setting.WorkerUrl = originalClient, originalWorker
		fetch.EnableSSRFProtection = originalProtection
	})
	// The in-memory RoundTripper below handles every request. No actual
	// connections are made, regardless of the synthetic destination.
	fetch.EnableSSRFProtection = false
	const target = "https://notify-fixture.example.invalid"
	const token = "fixture-token-never-log"
	const details = "provider text never log"
	data := dto.NewNotify(dto.NotifyTypeQuotaExceed, "notice", "contents", nil)

	for _, tc := range []struct {
		name, worker, expected string
		status                 int
	}{
		{name: "direct error", expected: "gotify_transport_failed"},
		{name: "worker error", worker: "https://worker-fixture.example.invalid", expected: "gotify_worker_failed"},
		{name: "provider rejection", expected: "gotify_http_503", status: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			system_setting.WorkerUrl = tc.worker
			calls := 0
			httpClient = &http.Client{Transport: notifyErrorTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if tc.worker == "" {
					require.Equal(t, token, request.URL.Query().Get("token"))
				}
				if tc.status == 0 {
					return nil, errors.New(details)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(details))}, nil
			})}
			err := sendGotifyNotify(target, token, 5, data)
			require.Equal(t, 1, calls)
			require.EqualError(t, err, tc.expected)
			for _, secret := range []string{target, token, details} {
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
}
