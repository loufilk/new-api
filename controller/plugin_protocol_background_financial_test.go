package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type pluginBackgroundFinancialSnapshot struct {
	User       model.User
	Token      model.Token
	Channel    model.Channel
	Owner      model.BillingFinalization
	Ledger     []model.BalanceLedger
	Operations []model.BalanceLedgerOperation
	Logs       []model.Log
}

func readPluginBackgroundFinancialSnapshot(t *testing.T, f *taskSubmissionFinancialFixture) pluginBackgroundFinancialSnapshot {
	t.Helper()
	var snapshot pluginBackgroundFinancialSnapshot
	require.NoError(t, f.db.First(&snapshot.User, f.user.Id).Error)
	require.NoError(t, f.db.First(&snapshot.Token, f.token.Id).Error)
	require.NoError(t, f.db.First(&snapshot.Channel, f.channel.Id).Error)
	require.NoError(t, f.db.Where("request_id = ?", f.info.RequestId).First(&snapshot.Owner).Error)
	require.NoError(t, f.db.Where("user_id = ?", f.user.Id).Order("id").Find(&snapshot.Ledger).Error)
	require.NoError(t, f.db.Where("user_id = ?", f.user.Id).Order("id").Find(&snapshot.Operations).Error)
	require.NoError(t, f.db.Where("user_id = ?", f.user.Id).Order("id").Find(&snapshot.Logs).Error)
	return snapshot
}

func TestServeTaskPluginProtocolBackgroundPreservesConcurrentFinancialState(t *testing.T) {
	for _, stage := range []string{"pending", "terminal_intent", "settled", "refunded", "review"} {
		t.Run(stage, func(t *testing.T) {
			f := newTaskSubmissionFinancialFixtureForModel(t, "background-"+stage, 1000, 100, "video-model")
			pinned := compilePluginProtocolTestEndpoint(t, "background-financial", `
				export const protocols = {openai_responses: {
					renderEvents: function() { throw new Error("background create must not render"); },
					renderFinal: function() { throw new Error("background create must not render"); }
				}};
			`)
			f.c.Set(pluginruntime.ContextKeyPinnedPlugin, pluginruntime.PinnedPlugin{Generation: pinned.Generation, Plugin: pinned.Plugin})
			outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
				return &relay.TaskSubmitResult{
					UpstreamTaskID: "provider-background-job", Platform: constant.TaskPlatform(pinned.Plugin.Meta.Key), Quota: 100,
					TaskData: []byte(`{"provider":"submitted"}`), PluginState: []byte(`{"round":0}`),
				}, nil
			})
			require.Nil(t, taskErr)
			require.NotNil(t, outcome)
			f.assertFinancial(t, model.BillingTaskOwned, 100, 100, 1, 1)
			require.Nil(t, outcome.Task.PrivateData.FinancialIntent)
			require.NotNil(t, outcome.Task.PrivateData.Execution)
			originalStatus := outcome.Task.Status

			c, recorder := newPluginProtocolTestContext(false, false)
			common.SetContextKey(c, constant.ContextKeyUserId, f.user.Id)
			common.SetContextKey(c, constant.ContextKeyTokenId, f.token.Id)
			setProtocolRequestBackground(c, true)
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseBridge := func() { releaseOnce.Do(func() { close(release) }) }
			deps := pluginProtocolTestDeps()
			deps.submit = func(*gin.Context, *relaycommon.RelayInfo) (*taskSubmissionOutcome, *dto.TaskError) {
				// This outcome crossed the real atomic task_owned barrier above.
				// Keep that submit snapshot while the poller commits newer state.
				close(entered)
				<-release
				return outcome, nil
			}
			deps.loadTask = func(context.Context, int, constant.TaskPlatform, string) (*model.Task, bool, error) {
				return nil, false, errors.New("background create must not observe")
			}
			go func() {
				defer close(done)
				serveTaskPluginProtocol(c, pinned, deps)
			}()
			defer func() { releaseBridge(); <-done }()
			select {
			case <-entered:
			case <-done:
				t.Fatal("bridge returned before the durable submit barrier")
			}

			var current model.Task
			require.NoError(t, f.db.First(&current, outcome.Task.ID).Error)
			expectedCharge, expectedLedger := 100, 1
			if stage == "review" {
				require.NoError(t, model.MarkTaskPollingReview(t.Context(), &current, "provider identity requires review"))
				require.NotEmpty(t, current.PrivateData.FinancialReviewReason)
			} else if stage != "pending" {
				current.Status, current.Progress = model.TaskStatusFailure, "100%"
				current.StartTime, current.FinishTime, current.FailReason = 123, 456, "provider rejected job"
				current.Data = []byte(`{"provider":"terminal","job":"provider-background-job"}`)
				current.PrivateData.PluginState = []byte(`{"round":2,"complete":true}`)
				current.PrivateData.ResultURL = "https://provider.example/result"
				current.PrivateData.PollFailures = 2
				kind, actual := "refund", 0
				if stage == "settled" {
					current.Status, current.FailReason = model.TaskStatusSuccess, ""
					kind, actual = "settle", 150
				}
				intent := &model.TaskFinancialIntent{Kind: kind, PreviousQuota: 100, ActualQuota: actual, OperationID: fmt.Sprintf("background:%d:%s", current.ID, stage), Reason: common.NewMessage("provider terminal decision")}
				won, err := model.CommitTaskTerminalFinancial(t.Context(), &current, originalStatus, intent)
				require.NoError(t, err)
				require.True(t, won)
				require.True(t, current.SettlementPending)
				require.Equal(t, intent, current.PrivateData.FinancialIntent)
				if stage == "settled" || stage == "refunded" {
					applied, err := service.ApplyTaskQuota(t.Context(), &current, actual, kind == "refund")
					require.NoError(t, err)
					require.True(t, applied)
					expectedCharge, expectedLedger = actual, 2
					require.False(t, current.SettlementPending)
					require.Nil(t, current.PrivateData.FinancialIntent)
					require.Equal(t, intent.OperationID, current.PrivateData.FinancialOperationID)
					require.Equal(t, actual, current.PrivateData.FinancialActualQuota)
					require.NoError(t, model.ValidateTaskWalletReceipt(current.PrivateData.WalletDebit, actual))
				}
			}
			require.NoError(t, f.db.First(&current, outcome.Task.ID).Error)
			before := readPluginBackgroundFinancialSnapshot(t, f)
			require.Equal(t, model.BillingTaskOwned, before.Owner.State)
			require.Equal(t, 1000-expectedCharge, before.User.QuotaPaid)
			require.Equal(t, expectedCharge, before.User.UsedQuota)
			require.Equal(t, 1, before.User.RequestCount)
			require.Equal(t, 1000-expectedCharge, before.Token.RemainQuota)
			require.Equal(t, expectedCharge, before.Token.UsedQuota)
			require.EqualValues(t, expectedCharge, before.Channel.UsedQuota)
			require.Len(t, before.Ledger, expectedLedger)
			require.Len(t, before.Operations, expectedLedger)
			current.PrivateData.ResponsesBackground = true

			releaseBridge()
			<-done

			assert.Equal(t, http.StatusOK, recorder.Code)
			var response map[string]any
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, "resp_"+strings.TrimPrefix(outcome.Task.TaskID, "task_"), response["id"])
			assert.Equal(t, "response", response["object"])
			assert.Equal(t, "video-model", response["model"])
			assert.Equal(t, true, response["background"])
			assert.Equal(t, "queued", response["status"], "create preserves the original pending response, not the concurrent terminal snapshot")
			assert.Nil(t, response["error"])
			assert.Nil(t, response["usage"])
			assert.Empty(t, response["output"])
			metadata, ok := response["metadata"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, outcome.Task.TaskID, metadata["task_id"])
			assert.Equal(t, "/v1/responses/"+response["id"].(string), metadata["retrieval_path"])
			assert.Equal(t, originalStatus, outcome.Task.Status)
			assert.True(t, outcome.Task.PrivateData.ResponsesBackground)
			assert.Nil(t, outcome.Task.PrivateData.FinancialIntent, "do not replace the caller's response snapshot")

			var stored model.Task
			require.NoError(t, f.db.First(&stored, outcome.Task.ID).Error)
			current.UpdatedAt = stored.UpdatedAt
			assert.Equal(t, current, stored, "only the background flag and normal update timestamp may change")
			assert.Equal(t, before, readPluginBackgroundFinancialSnapshot(t, f), "background metadata must not touch money, original token, counters, ownership, ledger or logs")
			if stage == "pending" {
				// The opposite ordering must also preserve the one-way marker:
				// an observation captured before background was saved arrives later.
				observation := *outcome.Task
				observation.PrivateData.ResponsesBackground = false
				observation.Status, observation.Progress = model.TaskStatusInProgress, "50%"
				won, err := observation.UpdateWithStatus(originalStatus)
				require.NoError(t, err)
				require.True(t, won)
				require.NoError(t, f.db.First(&stored, outcome.Task.ID).Error)
				assert.True(t, stored.PrivateData.ResponsesBackground)
				assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), stored.Status)
				assert.Equal(t, before, readPluginBackgroundFinancialSnapshot(t, f))
			}
		})
	}
}

func TestTaskResponsesBackgroundMetadataRejectsInvalidWrites(t *testing.T) {
	f := newTaskSubmissionFinancialFixture(t, "background-identity", 1000, 100)
	outcome, taskErr := executeTaskSubmissionWith(f.c, f.info, func(*gin.Context, *relaycommon.RelayInfo) (*relay.TaskSubmitResult, *dto.TaskError) {
		return &relay.TaskSubmitResult{UpstreamTaskID: "provider-job", Platform: "background-identity", Quota: 100}, nil
	})
	require.Nil(t, taskErr)
	require.NotNil(t, outcome)
	var original model.Task
	require.NoError(t, f.db.First(&original, outcome.Task.ID).Error)
	// Historical retention metadata must survive even if the submit snapshot
	// lacks it; background marking must not make a discarded result retrievable.
	original.PrivateData.ResultDiscarded = true
	original.PrivateData.PluginState = []byte(`{"historical":"retain"}`)
	require.NoError(t, f.db.Model(&original).Update("private_data", original.PrivateData).Error)
	require.NoError(t, f.db.First(&original, outcome.Task.ID).Error)
	before := readPluginBackgroundFinancialSnapshot(t, f)
	for _, field := range []string{"id", "user", "zero_id", "zero_user", "public_id", "platform", "empty_public_id", "empty_platform", "nil_context", "cancelled_context", "write_failure"} {
		t.Run(field, func(t *testing.T) {
			stale := original
			ctx := t.Context()
			switch field {
			case "id":
				stale.ID++
			case "user":
				stale.UserId++
			case "zero_id":
				stale.ID = 0
			case "zero_user":
				stale.UserId = 0
			case "public_id":
				stale.TaskID += "-wrong"
			case "platform":
				stale.Platform = "wrong-plugin"
			case "empty_public_id":
				stale.TaskID = ""
			case "empty_platform":
				stale.Platform = ""
			case "nil_context":
				ctx = nil
			case "cancelled_context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "write_failure":
				name := "test:background_metadata_failure"
				require.NoError(t, f.db.Callback().Update().After("gorm:update").Register(name, func(tx *gorm.DB) {
					if tx.Statement.Table == f.db.NamingStrategy.TableName("tasks") {
						tx.AddError(errors.New("synthetic background metadata failure"))
					}
				}))
				t.Cleanup(func() { require.NoError(t, f.db.Callback().Update().Remove(name)) })
			}
			require.Error(t, stale.MarkResponsesBackground(ctx))
			var stored model.Task
			require.NoError(t, f.db.First(&stored, original.ID).Error)
			assert.Equal(t, original, stored)
			assert.Equal(t, before, readPluginBackgroundFinancialSnapshot(t, f))
		})
	}
	// The same true write must succeed even on MySQL's changed-row semantics.
	// The stale caller's other private fields cannot replace persisted values.
	stale := original
	stale.PrivateData = model.TaskPrivateData{}
	require.NoError(t, stale.MarkResponsesBackground(t.Context()))
	require.NoError(t, stale.MarkResponsesBackground(t.Context()))
	var stored model.Task
	require.NoError(t, f.db.First(&stored, original.ID).Error)
	original.PrivateData.ResponsesBackground = true
	original.UpdatedAt = stored.UpdatedAt
	assert.Equal(t, original, stored)
	assert.Equal(t, before, readPluginBackgroundFinancialSnapshot(t, f))
	f.assertTaskCount(t, 1)
}
