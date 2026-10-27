package cline

import (
	"bufio"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readAllStream(t *testing.T, reader io.Reader) string {
	t.Helper()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(data)
}

func TestClineStreamReaderUnwrapsAndNormalizes(t *testing.T) {
	upstream := strings.Join([]string{
		": ping",
		"event: message",
		`data: {"success":true,"data":{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"cline-free/deepseek-v4.1-flash","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"}}]}}`,
		"",
		`data: {"success":true,"data":{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"reasoning":"think","reasoning_details":[{"type":"reasoning.text","text":"think "},{"type":"reasoning.text","text":"hard"}],"provider_metadata":{"secret":"x"}}}]}}`,
		"",
		`data: {"success":true,"data":{"id":"chatcmpl-1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.StreamStatus = relaycommon.NewStreamStatus()
	output := readAllStream(t, newClineStreamReader(strings.NewReader(upstream), info))

	assert.NotContains(t, output, `"success"`)
	assert.NotContains(t, output, "reasoning_details")
	assert.NotContains(t, output, "provider_metadata")
	assert.Contains(t, output, `"content":"hi"`)
	assert.Contains(t, output, `"reasoning_content":"think hard"`)
	assert.Contains(t, output, `"total_tokens":7`)
	assert.Equal(t, 1, strings.Count(output, "[DONE]"))
	assert.False(t, info.StreamStatus.ResponseFailed())
}

func TestClineStreamReaderReportsErrorAfterOutputOnce(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}`,
		"",
		`data: {"success":false,"errors":[{"message":"quota exceeded for free tier"}]}`,
		"",
		`data: {"choices":[{"index":0,"delta":{"content":"should not appear"}}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	t.Run("responses bridge emits a failed frame", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{}}
		info.StreamStatus = relaycommon.NewStreamStatus()
		output := readAllStream(t, newClineStreamReader(strings.NewReader(upstream), info))

		assert.Contains(t, output, `"partial"`)
		assert.NotContains(t, output, "should not appear")
		assert.NotContains(t, output, "[DONE]")
		assert.Equal(t, 1, strings.Count(output, `"error"`))
		assert.Contains(t, output, "quota exceeded for free tier")
		assert.True(t, info.StreamStatus.ResponseFailed())
		assert.Equal(t, 1, info.StreamStatus.TotalErrorCount())
	})

	t.Run("chat path stops without fabricating an empty chunk", func(t *testing.T) {
		info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI, ChannelMeta: &relaycommon.ChannelMeta{}}
		info.StreamStatus = relaycommon.NewStreamStatus()
		output, err := io.ReadAll(newClineStreamReader(strings.NewReader(upstream), info))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "quota exceeded for free tier")

		assert.Contains(t, string(output), `"partial"`)
		assert.NotContains(t, string(output), "should not appear")
		assert.NotContains(t, string(output), `"error"`)
		assert.NotContains(t, string(output), "[DONE]")
		assert.True(t, info.StreamStatus.ResponseFailed())
	})
}

func TestClineStreamReaderReportsEventError(t *testing.T) {
	upstream := "event: error\ndata: {\"data\":{\"error\":{\"message\":\"upstream refused\"}}}\n\n"
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, ChannelMeta: &relaycommon.ChannelMeta{}}
	info.StreamStatus = relaycommon.NewStreamStatus()
	output := readAllStream(t, newClineStreamReader(strings.NewReader(upstream), info))

	assert.Contains(t, output, "upstream refused")
	assert.True(t, info.StreamStatus.ResponseFailed())
}

func TestPeekClineStreamEventsDetectsLeadingError(t *testing.T) {
	stream := "event: message\ndata: {\"success\":false,\"error\":\"free quota exhausted\"}\n\n"
	reader := bufio.NewReader(strings.NewReader(stream))
	prefix, apiErr, err := peekClineStreamEvents(reader)
	require.NoError(t, err)
	require.NotNil(t, apiErr)
	assert.Contains(t, apiErr.Error(), "free quota exhausted")
	assert.Contains(t, string(prefix), "free quota exhausted")

	replayed := readAllStream(t, io.MultiReader(strings.NewReader(string(prefix)), reader))
	assert.Equal(t, stream, replayed)
}

func TestPeekClineStreamEventsReplaysSuccessfulPrefix(t *testing.T) {
	stream := "data: {\"success\":true,\"data\":{\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}}\n\ndata: [DONE]\n\n"
	reader := bufio.NewReader(strings.NewReader(stream))
	prefix, apiErr, err := peekClineStreamEvents(reader)
	require.NoError(t, err)
	require.Nil(t, apiErr)
	assert.Contains(t, string(prefix), `"success":true`)

	replayed := readAllStream(t, io.MultiReader(strings.NewReader(string(prefix)), reader))
	assert.Equal(t, stream, replayed)
}

func TestPeekClineStreamEventsEnforcesHardByteLimit(t *testing.T) {
	for _, tc := range []struct {
		name      string
		payload   string
		wantError bool
	}{
		{name: "exact limit", payload: strings.Repeat("x", maxClineStreamPeekBytes)},
		{name: "one byte over", payload: strings.Repeat("x", maxClineStreamPeekBytes+1), wantError: true},
		{name: "huge no newline", payload: strings.Repeat("x", maxClineStreamPeekBytes*8), wantError: true},
		{name: "multi line cumulative over", payload: strings.Repeat(strings.Repeat("x", maxClineStreamPeekBytes/maxClineStreamPeekLines)+"\n", maxClineStreamPeekLines), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := bufio.NewReaderSize(strings.NewReader(tc.payload), 1024)
			prefix, apiErr, err := peekClineStreamEvents(reader)
			require.Nil(t, apiErr)
			if tc.wantError {
				require.ErrorIs(t, err, errClineStreamPeekTooLarge)
				assert.LessOrEqual(t, len(prefix), maxClineStreamPeekBytes)
				return
			}
			require.NoError(t, err)
			assert.Len(t, prefix, maxClineStreamPeekBytes)
		})
	}
}

func TestReadClinePeekLinePreservesFragmentsAndEOF(t *testing.T) {
	input := strings.Repeat("a", 8192) + "\n"
	reader := bufio.NewReaderSize(strings.NewReader(input), 128)
	line, err := readClinePeekLine(reader, len(input))
	require.NoError(t, err)
	assert.Equal(t, input, string(line))

	reader = bufio.NewReaderSize(strings.NewReader("tail"), 2)
	line, err = readClinePeekLine(reader, 4)
	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, "tail", string(line))
}

func TestClineStreamBodyCloseIsIdempotentAndPropagates(t *testing.T) {
	closed := 0
	closer := closerFunc(func() error {
		closed++
		return nil
	})
	body := &clineStreamBody{reader: strings.NewReader(""), closer: closer}
	require.NoError(t, body.Close())
	require.NoError(t, body.Close())
	assert.Equal(t, 1, closed)
}

func TestPrepareClineStreamResponseCancellationClosesUpstream(t *testing.T) {
	upstreamClosed := false
	body := &trackedReadCloser{
		Reader: strings.NewReader("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n\n"),
		closed: &upstreamClosed,
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.StreamStatus = relaycommon.NewStreamStatus()
	require.Nil(t, prepareClineStreamResponse(resp, info))
	require.NoError(t, resp.Body.Close())
	assert.True(t, upstreamClosed)
}

func TestClineStreamReaderPropagatesScannerError(t *testing.T) {
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
	info.StreamStatus = relaycommon.NewStreamStatus()
	reader := newClineStreamReader(errorReader{}, info)
	_, err := io.ReadAll(reader)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read failure")
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

type trackedReadCloser struct {
	io.Reader
	closed *bool
}

func (r *trackedReadCloser) Close() error {
	*r.closed = true
	return nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failure")
}
