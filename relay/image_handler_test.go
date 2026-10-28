package relay

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type imageRelayCapture struct {
	body          []byte
	contentType   string
	path          string
	contentLength int64
}

// Exercise validation -> ImageHelper -> real adaptor -> HTTP transport. The
// upstream deliberately ends with an error after checking the model, so these
// request-contract tests never enter billing or require a database.
func newImageRelayFixture(t *testing.T, path, contentType string, body []byte, upstreamModel string) (*gin.Context, *relaycommon.RelayInfo, <-chan imageRelayCapture) {
	t.Helper()
	captured := make(chan imageRelayCapture, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		captured <- imageRelayCapture{data, r.Header.Get("Content-Type"), r.URL.Path, r.ContentLength}
		var model string
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			r.Body = io.NopCloser(bytes.NewReader(data))
			if !assert.NoError(t, r.ParseMultipartForm(1<<20)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer r.MultipartForm.RemoveAll()
			model = r.FormValue("model")
		} else {
			var payload struct{ Model string }
			if !assert.NoError(t, common.Unmarshal(data, &payload)) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			model = payload.Model
		}
		w.Header().Set("Content-Type", "application/json")
		if model != upstreamModel {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error","message":"model not found"}}`)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"error":{"type":"fixture_request_captured","message":"request captured; no billing"}}`)
	}))
	t.Cleanup(server.Close)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", contentType)
	t.Cleanup(func() {
		common.CleanupBodyStorage(c)
		if c.Request.MultipartForm != nil {
			require.NoError(t, c.Request.MultipartForm.RemoveAll())
		}
	})
	mode := relayconstant.RelayModeImagesGenerations
	if path == "/v1/images/edits" {
		mode = relayconstant.RelayModeImagesEdits
	}
	request, err := helper.GetAndValidOpenAIImageRequest(c, mode)
	require.NoError(t, err)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelId, 20)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
	common.SetContextKey(c, constant.ContextKeyOriginalModel, request.Model)
	info := &relaycommon.RelayInfo{
		Request: request, OriginModelName: request.Model, RequestURLPath: path,
		RelayMode: mode, RelayFormat: types.RelayFormatOpenAIImage,
	}
	return c, info, captured
}

func TestImageHelperJSONModelMapping(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	original := settings.PassThroughRequestEnabled
	t.Cleanup(func() { settings.PassThroughRequestEnabled = original })
	for _, tc := range []struct {
		name, path, body, mapping, wantModel string
		global, channel                      bool
		channelType                          int
	}{
		{
			name: "global passthrough generation", path: "/v1/images/generations", global: true,
			body:    `{"model":"grok-imagine-image-2.0-console","prompt":"A lake","aspect_ratio":"16:9","resolution":"1k","quality":"medium","response_format":"url","stream":false,"seed":9007199254740993,"vendor":{"model":"nested","zero":0,"off":false,"nullable":null},"empty":[]}`,
			mapping: `{"grok-imagine-image-2.0-console":"Console/grok-imagine-image-2.0"}`, wantModel: "Console/grok-imagine-image-2.0",
		},
		{
			name: "channel passthrough multi image edit", path: "/v1/images/edits", channel: true,
			body:    `{"model":"image-alias","prompt":"Edit this","n":2,"images":[{"url":"data:image/png;base64,aGVsbG8="}],"aspect_ratio":"3:2","resolution":"1k","quality":"low","custom":1e100}`,
			mapping: `{"image-alias":"intermediate","intermediate":"Console/grok-imagine-image-2.0"}`, wantModel: "Console/grok-imagine-image-2.0",
		},
		{
			name: "xai passthrough single image edit", path: "/v1/images/edits", global: true, channelType: constant.ChannelTypeXai,
			body:    `{"model":"image-alias","prompt":"Edit this","image":{"url":"data:image/png;base64,aGVsbG8=","type":"image_url"},"resolution":"1k","custom":false}`,
			mapping: `{"image-alias":"grok-imagine-image-2.0"}`, wantModel: "grok-imagine-image-2.0",
		},
		{
			name: "no mapping remains byte identical", path: "/v1/images/generations", global: true,
			body: "{ \"model\":\"dall-e-3\", \"prompt\":\"A lake\", \"custom\":null }", wantModel: "dall-e-3",
		},
		{
			name: "unrelated mapping remains byte identical", path: "/v1/images/edits", channel: true,
			body: `{"model":"image-alias", "prompt":"Edit this", "unknown":0}`, mapping: `{"other":"other-upstream"}`, wantModel: "image-alias",
		},
		{
			name: "self mapping remains byte identical", path: "/v1/images/generations", global: true,
			body: `{"model":"image-alias", "prompt":"A lake", "unknown":false}`, mapping: `{"image-alias":"image-alias"}`, wantModel: "image-alias",
		},
		{
			name: "non passthrough retains DTO conversion", path: "/v1/images/generations",
			body:    `{"model":"image-alias","prompt":"A lake","aspect_ratio":"16:9","resolution":"1k","quality":"medium","unknown":false}`,
			mapping: `{"image-alias":"Console/grok-imagine-image-2.0"}`, wantModel: "Console/grok-imagine-image-2.0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settings.PassThroughRequestEnabled = tc.global
			c, info, captured := newImageRelayFixture(t, tc.path, "application/json; charset=utf-8", []byte(tc.body), tc.wantModel)
			c.Set("model_mapping", tc.mapping)
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: tc.channel})
			if tc.channelType != 0 {
				common.SetContextKey(c, constant.ContextKeyChannelType, tc.channelType)
			}
			// Passthrough must still ignore parameter overrides except model mapping.
			if tc.global || tc.channel {
				common.SetContextKey(c, constant.ContextKeyChannelParamOverride, map[string]any{"prompt": "do not apply"})
			}
			apiErr := ImageHelper(c, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode, "%v", apiErr)
			require.Len(t, captured, 1)
			got := <-captured
			assert.Equal(t, tc.path, got.path)
			assert.Equal(t, "application/json; charset=utf-8", got.contentType)
			assert.Equal(t, int64(len(got.body)), got.contentLength)
			var want, actual map[string]json.RawMessage
			require.NoError(t, common.Unmarshal([]byte(tc.body), &want))
			require.NoError(t, common.Unmarshal(got.body, &actual))
			want["model"], _ = common.Marshal(tc.wantModel)
			if !tc.global && !tc.channel {
				delete(want, "unknown")
				want["n"] = json.RawMessage(`1`)
			}
			assert.Equal(t, want, actual)
			if !info.IsModelMapped && (tc.global || tc.channel) {
				assert.Equal(t, tc.body, string(got.body))
			}
			storage, err := common.GetBodyStorage(c)
			require.NoError(t, err)
			originalBody, err := storage.Bytes()
			require.NoError(t, err)
			assert.Equal(t, tc.body, string(originalBody), "retry source must be unchanged")
			assert.Equal(t, info.OriginModelName, info.Request.(*dto.ImageRequest).Model)
		})
	}
}

type imageMultipartPart struct {
	header textproto.MIMEHeader
	body   []byte
}

func readImageMultipart(t *testing.T, body []byte, contentType string) []imageMultipartPart {
	t.Helper()
	_, params, err := mime.ParseMediaType(contentType)
	require.NoError(t, err)
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var parts []imageMultipartPart
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			return parts
		}
		require.NoError(t, err)
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		parts = append(parts, imageMultipartPart{part.Header, data})
	}
}

func TestImageHelperMultipartModelMapping(t *testing.T) {
	settings := model_setting.GetGlobalSettings()
	original := settings.PassThroughRequestEnabled
	t.Cleanup(func() { settings.PassThroughRequestEnabled = original })
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits"} {
		t.Run(path, func(t *testing.T) {
			// Generation reaches the generic multipart decoder; edits has a
			// dedicated decoder. Use fields accepted by both actual entry paths.
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("model", "image-alias"))
			require.NoError(t, writer.WriteField("prompt", "A lake"))
			require.NoError(t, writer.WriteField("aspect_ratio", "16:9"))
			require.NoError(t, writer.WriteField("resolution", "1k"))
			require.NoError(t, writer.WriteField("vendor_option", "first"))
			require.NoError(t, writer.WriteField("vendor_option", "second"))
			if path == "/v1/images/edits" {
				require.NoError(t, writer.WriteField("n", "2"))
				require.NoError(t, writer.WriteField("stream", "false"))
			}
			for _, name := range []string{"image[0]", "image[]", "mask", "vendor_file", "model"} {
				part, err := writer.CreatePart(textproto.MIMEHeader{
					"Content-Disposition": {`form-data; name="` + name + `"; filename="sample.bin"`},
					"Content-Type":        {"application/octet-stream"}, "X-Vendor": {"one", "two"},
					"Content-Transfer-Encoding": {"quoted-printable"},
				})
				require.NoError(t, err)
				_, err = part.Write([]byte("=00=FFimage-alias=3D\r\n"))
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			contentType := writer.FormDataContentType()
			settings.PassThroughRequestEnabled = path == "/v1/images/generations"
			c, info, captured := newImageRelayFixture(t, path, contentType, body.Bytes(), "mapped-image")
			common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: !settings.PassThroughRequestEnabled})
			want := readImageMultipart(t, body.Bytes(), contentType)
			want[0].body = []byte("mapped-image")
			for _, mapping := range []string{`{"image-alias":"mapped-image"}`, `{}`} {
				c.Set("model_mapping", mapping)
				apiErr := ImageHelper(c, info)
				require.NotNil(t, apiErr)
				if mapping != `{}` {
					assert.Equal(t, http.StatusUnprocessableEntity, apiErr.StatusCode, "%v", apiErr)
				} else {
					// A retry without mapping must send the original alias.
					assert.Equal(t, http.StatusNotFound, apiErr.StatusCode)
					want[0].body = []byte("image-alias")
				}
				require.Len(t, captured, 1)
				got := <-captured
				assert.Equal(t, path, got.path)
				assert.Equal(t, int64(len(got.body)), got.contentLength)
				assert.Equal(t, want, readImageMultipart(t, got.body, got.contentType))
				assert.Equal(t, contentType, c.Request.Header.Get("Content-Type"), "retry boundary must remain usable")
				if mapping == `{}` {
					assert.Equal(t, body.Bytes(), got.body)
				}
			}
		})
	}
}
