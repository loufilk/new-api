package service

import (
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
)

func TestImageUsageEvidenceHonorsPricingContract(t *testing.T) {
	for _, tc := range []struct {
		name, expr string
		usage      *dto.Usage
		fixed      bool
		review     bool
	}{
		{name: "fixed image price does not need tokens", fixed: true},
		{name: "flat expression does not need tokens", expr: `tier("image", fixed(0.02))`},
		{name: "token expression missing all usage", expr: `tier("tokens", p * 5 + c * 30)`, review: true},
		{name: "token expression missing output usage", expr: `tier("tokens", p * 5 + c * 30)`, usage: &dto.Usage{PromptTokens: 20}, review: true},
		{name: "token expression missing input usage", expr: `tier("tokens", p * 5 + c * 30)`, usage: &dto.Usage{CompletionTokens: 40}, review: true},
		{name: "token expression complete usage", expr: `tier("tokens", p * 5 + c * 30)`, usage: &dto.Usage{PromptTokens: 20, CompletionTokens: 40}},
		{name: "fixed estimate cannot exempt a mixed token expression", expr: `c > 0 ? tier("tokens", c * 30) : tier("fixed", fixed(0.01))`, review: true},
		{name: "explicit output-only rate", expr: `tier("tokens", c * 30)`, usage: &dto.Usage{CompletionTokens: 40}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{OriginModelName: "gpt-image-2"}
			info.PriceData.UsePrice = tc.fixed
			if tc.expr != "" {
				unit := billingexpr.BillingUnitToken
				if tc.name == "flat expression does not need tokens" || tc.name == "fixed estimate cannot exempt a mixed token expression" {
					unit = billingexpr.BillingUnitRequest
				}
				info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{ExprString: tc.expr, ExprHash: billingexpr.ExprHashString(tc.expr), EstimatedBillingUnit: unit}
			}
			assert.Equal(t, tc.review, ImageUsageRequiresReview(info, tc.usage))
		})
	}
}
