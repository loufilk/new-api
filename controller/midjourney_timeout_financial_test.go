package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMidjourneyTimeoutFinancialPostgres(t *testing.T) {
	if os.Getenv("TEST_TASK_DB_DIALECT") != "postgres" {
		t.Skip("isolated PostgreSQL fixture required")
	}
	db, dialect := openTaskDialectDatabase(t, &model.User{}, &model.Token{}, &model.Midjourney{}, &model.Channel{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.Log{}, &model.Model{}, &model.Ability{}, &model.Vendor{}, &model.Option{})
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(oldMain, oldLogType)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
	})
	service.InitHttpClient()
	for _, status := range []string{"IN_PROGRESS", "SUCCESS", "FAILURE", "NONTERMINAL_REASON"} {
		t.Run(status, func(t *testing.T) {
			old := time.Now().Add(-2 * time.Hour).UnixMilli()
			if status == "NONTERMINAL_REASON" {
				old = time.Now().UnixMilli()
			}
			identity := "mj-timeout-" + common.GetRandomString(12)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				progress, reason := "50%", ""
				upstreamStatus := status
				if status == "NONTERMINAL_REASON" {
					upstreamStatus = "IN_PROGRESS"
					reason = "temporary diagnostic"
				}
				if status != "IN_PROGRESS" && status != "NONTERMINAL_REASON" && status != "FAILURE" {
					progress = "100%"
				}
				if status == "FAILURE" {
					reason = "explicit upstream failure"
				}
				body, err := common.Marshal([]dto.MidjourneyDto{{MjId: identity, Status: upstreamStatus, Progress: progress, SubmitTime: old, FailReason: reason}})
				if err != nil {
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			t.Cleanup(server.Close)
			user := model.User{Username: identity, AffCode: identity, Quota: 1000, QuotaPaid: 1000, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: identity, RemainQuota: 1000, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			channel := model.Channel{Name: identity, Key: "synthetic", BaseURL: &server.URL, Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			task := &model.Midjourney{UserId: user.Id, MjId: identity, ChannelId: channel.Id, Status: "IN_PROGRESS", Progress: "20%", SubmitTime: old}
			info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: identity, OriginModelName: "mj_imagine"}
			info.UserSetting.QuotaWarningEnabled = common.GetPointer(false)
			prepared, err := service.PrepareMidjourneyTaskBilling(info, task, 100, true)
			require.NoError(t, err)
			require.True(t, prepared)
			require.NoError(t, task.Insert())
			applied, err := service.SettleMidjourneyTaskBilling(info, task, prepared)
			require.NoError(t, err)
			require.True(t, applied)
			runMidjourneyTaskUpdateOnce(context.Background(), nil)
			require.NoError(t, db.First(task, task.Id).Error)
			wantStatus, wantQuota, wantBalance := status, 100, 900
			if status == "NONTERMINAL_REASON" {
				wantStatus = "IN_PROGRESS"
			}
			if status == "IN_PROGRESS" {
				wantStatus = "UNKNOWN"
				assert.True(t, task.RequiresFinancialReview())
				assert.Equal(t, model.MidjourneyTimeoutReviewReason, task.FailReason)
				assert.False(t, service.RefundMidjourneyQuota(context.Background(), task, "timeout is not refund authority"))
			}
			if status == "FAILURE" {
				wantQuota, wantBalance = 0, 1000
			}
			assert.Equal(t, wantStatus, task.Status)
			assert.Equal(t, wantQuota, task.Quota)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, wantBalance, user.QuotaPaid)
			assert.Equal(t, wantBalance, token.RemainQuota)
			assert.Equal(t, wantQuota, user.UsedQuota)
			assert.Equal(t, 1, user.RequestCount)
			require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
		})
	}
}
