package helper

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOfficialImageRequestContracts(t *testing.T) {
	for _, tc := range []struct {
		body    string
		invalid bool
	}{
		{`{"model":"gpt-image-2","n":10,"size":"2048x2048","quality":"high"}`, false},
		{`{"model":"gpt-image-2-2026-04-21","size":"3840x2160"}`, false},
		{`{"model":"gpt-image-2","size":"2160x3840"}`, false},
		{`{"model":"gpt-image-2","size":"2880x2880"}`, false},
		{`{"model":"gpt-image-2","n":0}`, true},
		{`{"model":"gpt-image-2","n":11}`, true},
		{`{"model":"gpt-image-2","size":"4096x2160"}`, true},
		{`{"model":"gpt-image-2","size":"2047x2048"}`, true},
		{`{"model":"gpt-image-2","size":"512x512"}`, true},
		{`{"model":"gpt-image-2","size":"3840x3840"}`, true},
		{`{"model":"gpt-image-2","size":"3840x1024"}`, true},
		{`{"model":"gpt-image-2","size":"999999999999999x999999999999999"}`, true},
		{`{"model":"gpt-image-2","quality":"standard"}`, true},
		{`{"model":"gpt-image-2","response_format":"url"}`, true},
		{`{"model":"gpt-image-2","input_fidelity":"high"}`, true},
		{`{"model":"gpt-image-2-4k","size":"4096x4096","response_format":"url"}`, false},
		{`{"model":"grok-imagine-image","n":4,"resolution":"2k","aspect_ratio":"16:9"}`, false},
		{`{"model":"grok-imagine-image","n":11}`, true},
		{`{"model":"grok-imagine-image","resolution":"4k"}`, true},
		{`{"model":"grok-imagine-image","resolution":"1.5k"}`, true},
		{`{"model":"grok-imagine-image","quality":"low"}`, true},
		{`{"model":"grok-imagine-image-pro","quality":"low"}`, true},
		{`{"model":"grok-imagine-image","aspect_ratio":"21:9"}`, true},
		{`{"model":"grok-imagine-image-2.0","n":4,"resolution":"1.5k","quality":"medium","aspect_ratio":"21:9"}`, false},
		{`{"model":"grok-imagine-image-2.0","quality":"high"}`, true},
		{`{"model":"grok-imagine-image-2.0","deferred":true}`, true},
		{`{"model":"grok-imagine-image-2.0","images":[{},{},{},{},{},{}]}`, true},
		{`{"model":"grok-imagine-image-2.0","image":{"url":"one"},"images":[{"url":"two"}]}`, true},
	} {
		t.Run(tc.body, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(tc.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			t.Cleanup(func() { common.CleanupBodyStorage(ctx) })
			_, err := GetAndValidOpenAIImageRequest(ctx, relayconstant.RelayModeImagesGenerations)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestImageBillingSnapshotUsesSafeEffectiveScalars(t *testing.T) {
	var request dto.ImageRequest
	require.NoError(t, common.Unmarshal([]byte(`{"model":"grok-imagine-image-2.0","prompt":"private prompt","n":4,"resolution":"2k","quality":"auto","aspect_ratio":"16:9","images":[{"url":"https://private.invalid/a"},{"url":"data:image/png;base64,private"}]}`), &request))
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	input, err := ResolveImageBillingInputForRequest(ctx, &request, billingexpr.RequestInput{Params: map[string]any{"resolution": "1k", "quality": "low"}})
	require.NoError(t, err)
	assert.Equal(t, "2k", gjson.GetBytes(input.Body, "resolution").String())
	assert.Equal(t, "medium", gjson.GetBytes(input.Body, "quality").String())
	assert.Equal(t, "16:9", gjson.GetBytes(input.Body, "aspect_ratio").String())
	assert.Equal(t, int64(2), gjson.GetBytes(input.Body, "reference_count").Int())
	assert.Equal(t, 4, *input.ImageCount)
	assert.NotContains(t, string(input.Body), "private")
	assert.NotContains(t, input.Params, "resolution")
	assert.NotContains(t, input.Params, "quality")
}

func TestGPTImageMultipartContractCannotHideDuplicateOrExcessInputs(t *testing.T) {
	for _, tc := range []struct {
		name       string
		fields     map[string][]string
		references int
		invalid    bool
	}{
		{name: "sixteen references", references: 16},
		{name: "seventeen references", references: 17, invalid: true},
		{name: "duplicate count", references: 1, fields: map[string][]string{"n": {"1", "11"}}, invalid: true},
		{name: "duplicate size", references: 1, fields: map[string][]string{"size": {"2048x2048", "4096x4096"}}, invalid: true},
		{name: "unsupported input fidelity", references: 1, fields: map[string][]string{"input_fidelity": {"high"}}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("model", "gpt-image-2"))
			for name, values := range tc.fields {
				for _, value := range values {
					require.NoError(t, writer.WriteField(name, value))
				}
			}
			for range tc.references {
				file, err := writer.CreateFormFile("image[]", "reference.png")
				require.NoError(t, err)
				_, err = file.Write([]byte("synthetic request fixture, not a generated image"))
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
			c.Request.Header.Set("Content-Type", writer.FormDataContentType())
			t.Cleanup(func() {
				common.CleanupBodyStorage(c)
				if c.Request.MultipartForm != nil {
					require.NoError(t, c.Request.MultipartForm.RemoveAll())
				}
			})
			_, err := GetAndValidOpenAIImageRequest(c, relayconstant.RelayModeImagesEdits)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
