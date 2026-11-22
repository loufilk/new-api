package cline

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/types"
)

const (
	maxClineStreamPeekBytes = 256 << 10
	maxClineStreamPeekLines = 64
)

var errClineStreamPeekTooLarge = fmt.Errorf("cline upstream SSE prefix exceeds %d bytes", maxClineStreamPeekBytes)

func prepareClineStreamResponse(resp *http.Response, info *relaycommon.RelayInfo) *types.NewAPIError {
	original := resp.Body
	reader := bufio.NewReader(original)
	prefix, apiErr, readErr := peekClineStreamEvents(reader)
	if apiErr != nil {
		_ = original.Close()
		return apiErr
	}
	if readErr != nil {
		_ = original.Close()
		return types.NewOpenAIError(readErr, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	transformed := newClineStreamReader(io.MultiReader(bytes.NewReader(prefix), reader), info)
	resp.Body = &clineStreamBody{reader: transformed, closer: original}
	return nil
}

// peekClineStreamEvents inspects the first data event before any output is
// written so a leading error envelope is reported as a normal API error instead
// of a truncated stream. The consumed bytes are replayed to the stream
// transformer, and an error after the first event is handled in-band by the
// transformer so partial output is never discarded or duplicated.
func peekClineStreamEvents(reader *bufio.Reader) ([]byte, *types.NewAPIError, error) {
	var prefix bytes.Buffer
	var eventType string
	for lines := 0; lines < maxClineStreamPeekLines; lines++ {
		remaining := maxClineStreamPeekBytes - prefix.Len()
		if remaining <= 0 {
			if _, err := reader.Peek(1); err == nil {
				return prefix.Bytes(), nil, errClineStreamPeekTooLarge
			} else if !errors.Is(err, io.EOF) {
				return prefix.Bytes(), nil, err
			}
			return prefix.Bytes(), nil, nil
		}

		line, err := readClinePeekLine(reader, remaining)
		if len(line) > 0 {
			prefix.Write(line)
			trimmed := strings.TrimSpace(string(line))
			switch {
			case strings.HasPrefix(trimmed, "event:"):
				eventType = strings.TrimSpace(strings.TrimPrefix(trimmed, "event:"))
			case strings.HasPrefix(trimmed, "data:"):
				payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
				if payload != "" {
					if payload == "[DONE]" {
						return prefix.Bytes(), nil, nil
					}
					if _, apiErr := normalizeClineStreamPayload(eventType, payload); apiErr != nil {
						return prefix.Bytes(), apiErr, nil
					}
					return prefix.Bytes(), nil, nil
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return prefix.Bytes(), nil, nil
			}
			return prefix.Bytes(), nil, err
		}
	}
	return prefix.Bytes(), nil, nil
}

func readClinePeekLine(reader *bufio.Reader, limit int) ([]byte, error) {
	if limit <= 0 {
		return nil, errClineStreamPeekTooLarge
	}
	line := make([]byte, 0, min(limit, reader.Size()))
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if len(line)+len(fragment) > limit {
				return nil, errClineStreamPeekTooLarge
			}
			line = append(line, fragment...)
		}
		switch {
		case err == nil:
			return line, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, io.EOF
		default:
			return line, err
		}
	}
}

type clineStreamBody struct {
	reader io.Reader
	closer io.Closer
	once   sync.Once
}

func (b *clineStreamBody) Read(p []byte) (int, error) {
	return b.reader.Read(p)
}

func (b *clineStreamBody) Close() error {
	var err error
	b.once.Do(func() {
		err = b.closer.Close()
	})
	return err
}

// clineStreamReader rewrites Cline SSE events into plain OpenAI chunks:
// success/data envelopes are unwrapped, reasoning fields are normalized, and
// error events become a single client-safe error frame before the stream stops.
type clineStreamReader struct {
	scanner   *bufio.Scanner
	output    bytes.Buffer
	info      *relaycommon.RelayInfo
	eventType string
	err       error
}

func newClineStreamReader(reader io.Reader, info *relaycommon.RelayInfo) *clineStreamReader {
	return &clineStreamReader{scanner: helper.NewStreamScanner(reader), info: info}
}

func (r *clineStreamReader) Read(p []byte) (int, error) {
	for r.output.Len() == 0 {
		if r.err != nil {
			return 0, r.err
		}
		if !r.scanner.Scan() {
			if err := r.scanner.Err(); err != nil {
				r.err = err
			} else {
				r.err = io.EOF
			}
			continue
		}
		r.processLine(r.scanner.Text())
	}
	return r.output.Read(p)
}

func (r *clineStreamReader) processLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return
	}
	if event, ok := strings.CutPrefix(trimmed, "event:"); ok {
		r.eventType = strings.TrimSpace(event)
		return
	}
	payload, ok := strings.CutPrefix(trimmed, "data:")
	if !ok {
		return
	}
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return
	}
	if payload == "[DONE]" {
		r.emit("data: [DONE]\n")
		return
	}
	eventType := r.eventType
	r.eventType = ""
	normalized, apiErr := normalizeClineStreamPayload(eventType, payload)
	if apiErr != nil {
		r.recordFailure(apiErr)
		// Only the Responses bridge understands an in-band chat error frame;
		// the chat/Claude/Gemini paths re-marshal typed chunks and would drop
		// it into an empty chunk, so there the failed stream simply stops and
		// the failure stays recorded in StreamStatus.
		if r.info != nil && r.info.RelayFormat == types.RelayFormatOpenAIResponses {
			r.emit(clineErrorFrame(apiErr))
			r.err = io.EOF
			return
		}
		// Chat/Claude/Gemini need the typed semantic failure to reach the
		// adaptor. Returning EOF here would make the shared stream handler emit
		// a normal terminal and classify the request as successful.
		r.err = apiErr
		return
	}
	if len(normalized) == 0 {
		return
	}
	r.emit("data: " + string(normalized) + "\n")
}

func (r *clineStreamReader) emit(frame string) {
	if frame == "" {
		return
	}
	r.output.WriteString(frame)
}

func (r *clineStreamReader) recordFailure(apiErr *types.NewAPIError) {
	if r.info == nil || r.info.StreamStatus == nil || apiErr == nil {
		return
	}
	status := r.info.StreamStatus
	status.RecordError(apiErr.Error())
	status.SetAPIError(apiErr)
	openAIError := apiErr.ToOpenAIError()
	status.MarkFailed(string(apiErr.GetErrorCode()), openAIError.Type, apiErr.StatusCode)
}
