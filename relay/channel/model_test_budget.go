package channel

import (
	"bytes"
	"errors"
	"io"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// Only server-created model tests set this flag. Validate the final converted
// HTTP body, not the public model name or pre-conversion request. Unknown or
// raised budgets fail before any upstream request; ordinary relay is unchanged.
func validateModelTestOutputBudget(c *gin.Context, req *http.Request) error {
	if !common.GetContextKeyBool(c, constant.ContextKeyModelTestSingleAttempt) {
		return nil
	}
	limit := common.GetContextKeyInt(c, constant.ContextKeyModelTestOutputLimit)
	fail := errors.New("controlled model test output budget changed or missing")
	if limit <= 0 || limit > 4096 || req == nil || req.Body == nil {
		return fail
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 64<<10+1))
	_ = req.Body.Close()
	if err != nil || len(body) > 64<<10 {
		return fail
	}
	// Restore the exact bytes; no second serialization or pricing mutation.
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil }
	var payload struct {
		MaxTokens           *int `json:"max_tokens"`
		MaxCompletionTokens *int `json:"max_completion_tokens"`
		MaxOutputTokens     *int `json:"max_output_tokens"`
	}
	if common.Unmarshal(body, &payload) != nil {
		return fail
	}
	found := false
	for _, value := range []*int{payload.MaxTokens, payload.MaxCompletionTokens, payload.MaxOutputTokens} {
		if value == nil {
			continue
		}
		if *value != limit {
			return fail
		}
		found = true
	}
	if !found {
		return fail
	}
	return nil
}
