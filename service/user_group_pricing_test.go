package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskTokenSettlementUsesAdmittedPersonalGroupAndModelQuote(t *testing.T) {
	quote := &types.GroupRatioInfo{GroupRatio: .5, InheritedGroupRatio: .8, RateSource: "personal", UserPricingRevision: 3}
	task := &model.Task{Group: "historical-group", PrivateData: model.TaskPrivateData{
		BillingContext: &model.TaskBillingContext{
			BillingModelName: "unconfigured-synthetic-model",
			GroupRatioInfo:   quote,
			ModelRatio:       2,
		},
	}}
	quota, _, _, ok := taskQuotaByTokens(task, 100)
	require.True(t, ok)
	assert.Equal(t, 100, quota)
	quote.GroupRatio = 0
	quota, _, _, ok = taskQuotaByTokens(task, 100)
	require.True(t, ok)
	assert.Zero(t, quota)
}
