package service

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/tokenkit"
	"github.com/gin-gonic/gin"
)

type StreamingEstimateByModel = tokenkit.StreamingEstimate

func NewStreamingEstimateByModel(model string) *StreamingEstimateByModel {
	return tokenkit.NewStreamingEstimate(model)
}

// StreamingEstimate2Usage builds fallback usage from an incremental estimator.
// It sets the local-count marker exactly like ResponseText2Usage.
func StreamingEstimate2Usage(c *gin.Context, estimator *StreamingEstimateByModel, model string, promptTokens int) *dto.Usage {
	common.SetContextKey(c, constant.ContextKeyLocalCountTokens, true)
	usage := &dto.Usage{}
	usage.PromptTokens = promptTokens
	if estimator != nil {
		usage.CompletionTokens = estimator.Tokens()
	} else {
		usage.CompletionTokens = tokenkit.Estimate(model, "")
	}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}
