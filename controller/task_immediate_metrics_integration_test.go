package controller

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A successful provider response is not a health sample until the custom atomic
// task/billing handoff commits. These cases exercise that controller boundary,
// not just RecordImmediateTaskResult's local status filter.
func TestImmediateTaskMetricsRequireAtomicFinancialHandoff(t *testing.T) {
	previousEnabled := perf_metrics_setting.GetSetting().Enabled
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"perf_metrics_setting.enabled": "true"}))
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"perf_metrics_setting.enabled": strconv.FormatBool(previousEnabled)}))
	})
	for _, scenario := range []string{
		"success", "terminal_failure", "asynchronous", "cancel_after_acceptance", "post_insert_failure", "final_reserve_shortage",
	} {
		t.Run(scenario, func(t *testing.T) {
			// Metrics are process-local across fixture databases. A fresh model
			// isolates repeated -count runs without draining other producers.
			modelName := "immediate-metrics-" + common.GetUUID()
			initial := 1000
			if scenario == "final_reserve_shortage" {
				initial = 100
			}
			f := newTaskSubmissionFinancialFixtureForModel(t, "immediate-metrics-"+scenario, initial, 100, modelName)
			// PerfMetric has a fixed table name, so apply the shared fixture's
			// refuse-existing-table rule before migrating or cleaning it up.
			require.False(t, f.db.Migrator().HasTable(&model.PerfMetric{}))
			require.NoError(t, f.db.AutoMigrate(&model.PerfMetric{}))
			t.Cleanup(func() { require.NoError(t, f.db.Migrator().DropTable(&model.PerfMetric{})) })
			f.info.StartTime = time.Now()

			before, err := perfmetrics.Query(perfmetrics.QueryParams{Model: modelName, Hours: 1})
			require.NoError(t, err)
			require.Nil(t, before.Summary)

			result := &relay.TaskSubmitResult{
				UpstreamTaskID: "provider-immediate-metric", Platform: "task_plugin", Quota: 100,
				Immediate: &relaycommon.TaskInfo{Status: model.TaskStatusSuccess, TotalTokens: 37},
			}
			requestContext, cancel := context.WithCancel(f.c.Request.Context())
			t.Cleanup(cancel)
			f.c.Request = f.c.Request.WithContext(requestContext)
			switch scenario {
			case "terminal_failure":
				result.Quota = 0
				result.Immediate = relaycommon.FailTaskInfo("provider rejected the request")
			case "asynchronous":
				result.Immediate = nil
			case "post_insert_failure":
				hits := installTaskPostInsertFailure(t, f.db, f.user.Id, f.info.TaskRelayInfo.PublicTaskID)
				t.Cleanup(func() { assert.EqualValues(t, 1, hits.Load(), "failure must follow a real task INSERT") })
			case "final_reserve_shortage":
				result.Quota = 600
			}
			outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				if scenario == "cancel_after_acceptance" {
					cancel()
				}
				return result, nil
			})

			switch scenario {
			case "cancel_after_acceptance", "post_insert_failure", "final_reserve_shortage":
				require.Nil(t, outcome)
				require.NotNil(t, taskErr)
				wantCode := map[string]string{
					"cancel_after_acceptance": "request_cancelled", "post_insert_failure": "task_insert_failed", "final_reserve_shortage": "insufficient_user_quota",
				}[scenario]
				assert.Equal(t, wantCode, taskErr.Code)
				f.assertFinancial(t, model.BillingDeliveryUnknown, 100, 0, 0, 1)
				f.assertTaskCount(t, 0)
				f.assertUnknownCannotRefund(t)
			default:
				require.Nil(t, taskErr)
				require.NotNil(t, outcome)
				ledgerCount := 1
				if scenario == "terminal_failure" {
					ledgerCount = 2
				}
				f.assertFinancial(t, model.BillingTaskOwned, result.Quota, result.Quota, 1, ledgerCount)
				f.assertTaskCount(t, 1)
			}

			after, err := perfmetrics.Query(perfmetrics.QueryParams{Model: modelName, Hours: 1})
			require.NoError(t, err)
			if scenario != "success" {
				assert.Nil(t, after.Summary, "uncommitted or non-successful submissions must not become health samples")
				assert.Empty(t, after.Series)
				return
			}
			require.NotNil(t, after.Summary)
			assert.Equal(t, 100.0, after.Summary.SuccessRate)
			require.Len(t, after.Groups, 1)
			assert.Equal(t, "default", after.Groups[0].Group)
			summary, err := perfmetrics.QuerySummaryAll(1, nil)
			require.NoError(t, err)
			var count int64
			for _, item := range summary.Models {
				if item.ModelName == modelName {
					count += item.RequestCount
				}
			}
			assert.EqualValues(t, 1, count, "one successful atomic handoff must produce exactly one sample")
		})
	}
}
