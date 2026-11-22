package channel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type preflightFailTaskAdaptor struct {
	stubTaskAdaptor
	failURL, failHeader bool
}

func (a *preflightFailTaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if a.failURL {
		return "", errors.New("local URL failure")
	}
	return a.stubTaskAdaptor.BuildRequestURL(info)
}
func (a *preflightFailTaskAdaptor) BuildRequestHeader(c *gin.Context, r *http.Request, info *relaycommon.RelayInfo) error {
	if a.failHeader {
		return errors.New("local header failure")
	}
	return nil
}

func TestTaskRequestPreflightProvesUnsent(t *testing.T) {
	for _, scenario := range []string{"url", "new_request", "header", "cancel_before_send"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
			if scenario == "cancel_before_send" {
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			}
			common.SetContextKey(c, constant.ContextKeyUpstreamRequestAttempted, true)
			a := &preflightFailTaskAdaptor{stubTaskAdaptor: stubTaskAdaptor{baseURL: "http://127.0.0.1"}, failURL: scenario == "url", failHeader: scenario == "header"}
			if scenario == "new_request" {
				a.baseURL = "://invalid"
			}
			resp, err := DoTaskApiRequest(a, c, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, io.Reader(strings.NewReader("{}")))
			require.Error(t, err)
			assert.Nil(t, resp)
			assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestUnsent))
			assert.False(t, common.GetContextKeyBool(c, constant.ContextKeyUpstreamRequestAttempted))
		})
	}
}
