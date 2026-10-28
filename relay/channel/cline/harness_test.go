package cline_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/cline"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type capturedUpstreamRequest struct {
	path   string
	method string
	header http.Header
	body   []byte
}

func newClineUpstream(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, body []byte)) (*httptest.Server, <-chan capturedUpstreamRequest) {
	t.Helper()
	received := make(chan capturedUpstreamRequest, 4)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- capturedUpstreamRequest{path: r.URL.Path, method: r.Method, header: r.Header.Clone(), body: body}
		respond(w, r, body)
	}))
	t.Cleanup(upstream.Close)
	return upstream, received
}

type clineTestEnv struct {
	recorder *httptest.ResponseRecorder
	context  *gin.Context
	info     *relaycommon.RelayInfo
	adaptor  channel.Adaptor
}

func newClineTestEnv(t *testing.T, format types.RelayFormat, mode int, stream bool, requestedModel, upstreamBaseURL string) *clineTestEnv {
	t.Helper()
	previousTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = previousTimeout })
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set("role", common.RoleCommonUser)
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeCline)
	common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstreamBaseURL)
	common.SetContextKey(c, constant.ContextKeyChannelKey, "test-cline-key")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, requestedModel)
	info := &relaycommon.RelayInfo{
		RelayFormat:     format,
		RelayMode:       mode,
		IsStream:        stream,
		OriginModelName: requestedModel,
		StartTime:       time.Now(),
		DisablePing:     true,
		ChannelMeta:     &relaycommon.ChannelMeta{},
	}
	info.InitChannelMeta(c)
	adaptor := resolveClineAdaptor(t)
	adaptor.Init(info)
	return &clineTestEnv{recorder: recorder, context: c, info: info, adaptor: adaptor}
}

func resolveClineAdaptor(t *testing.T) channel.Adaptor {
	t.Helper()
	apiType, ok := common.ChannelType2APIType(constant.ChannelTypeCline)
	require.True(t, ok)
	require.Equal(t, constant.APITypeCline, apiType)
	adaptor := relay.GetAdaptor(apiType)
	require.NotNil(t, adaptor)
	require.IsType(t, &cline.Adaptor{}, adaptor)
	return adaptor
}

func (e *clineTestEnv) mapModel(t *testing.T, request dto.Request, upstreamModel string) {
	t.Helper()
	e.context.Set("model_mapping", `{"`+e.info.OriginModelName+`":"`+upstreamModel+`"}`)
	require.NoError(t, helper.ModelMappedHelper(e.context, e.info, request))
	require.Equal(t, upstreamModel, e.info.UpstreamModelName)
}

func (e *clineTestEnv) send(t *testing.T, converted any) *http.Response {
	t.Helper()
	body, err := common.Marshal(converted)
	require.NoError(t, err)
	resp, err := e.adaptor.DoRequest(e.context, e.info, strings.NewReader(string(body)))
	require.NoError(t, err)
	httpResp, ok := resp.(*http.Response)
	require.True(t, ok)
	return httpResp
}

func (e *clineTestEnv) receive(t *testing.T, resp *http.Response) (any, *types.NewAPIError) {
	t.Helper()
	return e.adaptor.DoResponse(e.context, resp, e.info)
}

func streamEventTypes(body string) []string {
	events := make([]string, 0)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if event, ok := strings.CutPrefix(trimmed, "event:"); ok {
			events = append(events, strings.TrimSpace(event))
		}
	}
	return events
}

func dataPayloads(body string) []string {
	payloads := make([]string, 0)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if data, ok := strings.CutPrefix(trimmed, "data:"); ok {
			payloads = append(payloads, strings.TrimSpace(data))
		}
	}
	return payloads
}
