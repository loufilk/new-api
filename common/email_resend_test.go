package common

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resendTestTransport func(*http.Request) (*http.Response, error)

func (transport resendTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestResendFailuresKeepCredentialsAndEmailContentsOutOfLogs(t *testing.T) {
	for _, transportFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "provider rejection", true: "transport failure"}[transportFailure], func(t *testing.T) {
			var logs bytes.Buffer
			LogWriterMu.Lock()
			originalWriter := gin.DefaultErrorWriter
			gin.DefaultErrorWriter = &logs
			LogWriterMu.Unlock()
			originalClient := resendEmailClient
			t.Cleanup(func() {
				resendEmailClient = originalClient
				LogWriterMu.Lock()
				gin.DefaultErrorWriter = originalWriter
				LogWriterMu.Unlock()
			})
			resendEmailClient = &http.Client{Transport: resendTestTransport(func(*http.Request) (*http.Response, error) {
				if transportFailure {
					return nil, errors.New("private-key verification-654321 password-reset-token")
				}
				return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"name":"private-key","message":"verification-654321 password-reset-token"}`))}, nil
			})}
			err := sendResendEmail("private-key", "sender@example.com", "Verification", "recipient@example.com", "verification-654321", "<p>password-reset-token</p>")
			require.Error(t, err)
			require.NotEmpty(t, logs.String(), "delivery failures must remain observable without recording secrets")
			for _, secret := range []string{"private-key", "verification-654321", "password-reset-token"} {
				assert.NotContains(t, err.Error(), secret)
				assert.NotContains(t, logs.String(), secret)
			}
		})
	}
}

func TestQueuedResendRequiresOneRecipientAndHonorsContext(t *testing.T) {
	previous := resendEmailClient
	t.Cleanup(func() { resendEmailClient = previous })
	calls := 0
	resendEmailClient = &http.Client{Transport: resendTestTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		<-req.Context().Done()
		return nil, errors.New("sensitive upstream text")
	})}
	for _, recipient := range []string{"list@example.invalid;another@example.invalid", "User <user@example.invalid>"} {
		require.Error(t, SendQueuedResend(t.Context(), "test", "from@example.invalid", "safe", recipient, "plain", "html", "event"))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, SendQueuedResend(ctx, "test", "from@example.invalid", "safe", "to@example.invalid", "plain", "html", "event"), context.Canceled)
	assert.Zero(t, calls)
	ctx, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer stop()
	require.ErrorIs(t, SendQueuedResend(ctx, "test", "from@example.invalid", "safe", "to@example.invalid", "plain", "html", "event"), context.DeadlineExceeded)
	assert.Equal(t, 1, calls)
}
