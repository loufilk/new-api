package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
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

func TestIM21TaskTokenSettlementFollowsWrittenContractAcrossCutover(t *testing.T) {
	original := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(original)) })
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"rollout-test":2}`))
	info := &relaycommon.RelayInfo{OriginModelName: "rollout-test", PriceData: types.PriceData{
		ModelRatio: 2, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 0.25},
	}}
	for _, tc := range []struct {
		name, admitMode, pollMode string
		wantQuota                 int
		wantSnapshot              bool
	}{
		{"mixed admissions use baseline model lookup after activation", "locked", "active", 125, false},
		{"active admissions retain their model quote after relock", "active", "locked", 50, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(common.IM21RolloutModeEnv, tc.admitMode)
			data, err := common.Marshal(model.TaskPrivateData{BillingContext: model.NewTaskBillingContext(info)})
			require.NoError(t, err)
			if tc.wantSnapshot {
				require.Contains(t, string(data), "group_rate_snapshot")
			} else {
				require.NotContains(t, string(data), "group_rate_snapshot")
			}
			task := &model.Task{Group: "historic"}
			require.NoError(t, common.Unmarshal(data, &task.PrivateData))
			t.Setenv(common.IM21RolloutModeEnv, tc.pollMode)
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"rollout-test":5}`))
			quota, _, _, ok := taskQuotaByTokens(task, 100)
			require.True(t, ok)
			require.Equal(t, tc.wantQuota, quota)
		})
	}
}
