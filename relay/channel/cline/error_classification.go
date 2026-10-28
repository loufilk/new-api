package cline

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/tidwall/gjson"
)

var clineContentParamPattern = regexp.MustCompile(`^(messages(\.[0-9]+|\[[0-9]+\])|\*\*\*\.\*\*\*)\.content([.\[][A-Za-z0-9_.*\[\]]+)?$`)

// classifyClineContentError recognizes a structured content rejection, not any
// message that happens to mention 400. Keep the outer gateway failure distinct
// from a locally rejected client request; only its retry policy changes.
func classifyClineContentError(detail clineErrorDetail) (clineErrorDetail, bool) {
	isContent := func(value clineErrorDetail) bool {
		return (value.Type == "invalid_request_error" || value.Code == "invalid_request_error") &&
			len(value.Param) <= 256 && clineContentParamPattern.MatchString(value.Param)
	}
	if isContent(detail) && (detail.StatusCode == 0 || detail.StatusCode == http.StatusBadRequest) {
		detail.StatusCode = http.StatusBadRequest
		return detail, true
	}
	_, tail, found := strings.Cut(detail.Message, "request failed with status 400: ")
	if !found || !gjson.Valid(tail) {
		return detail, false
	}
	nested := gjson.Get(tail, "error")
	if !nested.IsObject() {
		return detail, false
	}
	upstream := clineErrorDetailFromValue(nested)
	if !isContent(upstream) {
		return detail, false
	}
	detail.Param = upstream.Param
	detail.StatusCode = http.StatusBadRequest
	detail.Type = upstream.Type
	detail.Code = upstream.Code
	return detail, true
}

func withClineContentDiagnostics(apiErr *types.NewAPIError, detail clineErrorDetail) {
	metadata, err := common.Marshal(map[string]any{
		"upstream_status_code": detail.StatusCode,
		"upstream_error_param": detail.Param,
		"retryable":            false,
	})
	if err == nil {
		apiErr.Metadata = metadata
	}
}

// classifyClineHTTPError also covers non-200 replies, which bypass DoResponse.
// Inspect a bounded prefix and restore unclassified bodies byte-for-byte.
func classifyClineHTTPError(value any) (any, error) {
	resp, ok := value.(*http.Response)
	if !ok || resp == nil || resp.Body == nil || resp.StatusCode < http.StatusBadRequest {
		return value, nil
	}
	const limit = 64 << 10
	original := resp.Body
	prefix, err := io.ReadAll(io.LimitReader(original, limit+1))
	if err != nil {
		_ = original.Close()
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusBadGateway)
	}
	if len(prefix) <= limit && gjson.ValidBytes(prefix) {
		detail := clineErrorDetailFromBody(prefix)
		detail.StatusCode = resp.StatusCode
		if normalized, contentError := classifyClineContentError(detail); contentError {
			_ = original.Close()
			return nil, newClineEnvelopeError(normalized)
		}
	}
	resp.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(prefix), original), Closer: original}
	return resp, nil
}
