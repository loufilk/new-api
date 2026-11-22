package common

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

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
