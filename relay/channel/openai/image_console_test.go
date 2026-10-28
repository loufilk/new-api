package openai

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsoleImageJSONRelayPreservesParameters(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode int
		path string
		body string
	}{
		{"legacy image without native controls", relayconstant.RelayModeImagesGenerations, "/v1/images/generations", `{"model":"dall-e-3","prompt":"A lake","n":1,"size":"1024x1024","quality":"standard"}`},
		{"generation", relayconstant.RelayModeImagesGenerations, "/v1/images/generations", `{"model":"Console/grok-imagine-image-2.0","prompt":"A lake","n":2,"aspect_ratio":"16:9","resolution":"1k","quality":"medium","response_format":"url"}`},
		{"reference edit", relayconstant.RelayModeImagesEdits, "/v1/images/edits", `{"model":"Console/grok-imagine-image-2.0","prompt":"Edit this","n":1,"aspect_ratio":"3:2","resolution":"1k","quality":"low","response_format":"url","images":[{"url":"data:image/png;base64,aGVsbG8="}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			request, err := helper.GetAndValidOpenAIImageRequest(c, tc.mode)
			require.NoError(t, err)
			converted, err := (&Adaptor{}).ConvertImageRequest(c, &relaycommon.RelayInfo{RelayMode: tc.mode}, *request)
			require.NoError(t, err)
			body, err := common.Marshal(converted)
			require.NoError(t, err)
			assert.JSONEq(t, tc.body, string(body))
		})
	}
}
