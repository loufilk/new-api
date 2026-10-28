package service

// task_billing_identity_test.go 锁定异步任务的计费模型身份契约：
//
//	新任务：BillingContext.BillingModelName 持久化价格/倍率/赠金身份
//	旧任务（RC30 历史）：仅 OriginModelName，作为回退
//	重启/轮询：从数据库重新加载后身份保持一致
//
// 资金身份必须与价格身份同源（BillingModelName），路由身份
// （UpstreamModelName）不得影响资金规则。

import (
	"encoding/json"
	"github.com/QuantumNous/new-api/common"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func persistTask(t *testing.T, task *model.Task) *model.Task {
	t.Helper()
	require.NoError(t, model.DB.Create(task).Error)
	var reloaded model.Task
	require.NoError(t, model.DB.Where("task_id = ?", task.TaskID).First(&reloaded).Error)
	return &reloaded
}

func TestTaskBillingContextEffectiveBillingModelName(t *testing.T) {
	cases := []struct {
		name     string
		ctx      *model.TaskBillingContext
		expected string
	}{
		{
			name:     "nil context",
			ctx:      nil,
			expected: "",
		},
		{
			name:     "legacy task only persists origin model",
			ctx:      &model.TaskBillingContext{OriginModelName: "gemini-2.5-flash-thinking-8192"},
			expected: "gemini-2.5-flash-thinking-8192",
		},
		{
			name:     "new task only persists billing model",
			ctx:      &model.TaskBillingContext{BillingModelName: "gemini-2.5-flash"},
			expected: "gemini-2.5-flash",
		},
		{
			name:     "billing model wins when both differ",
			ctx:      &model.TaskBillingContext{OriginModelName: "origin-model", BillingModelName: "billing-model"},
			expected: "billing-model",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.ctx.EffectiveBillingModelName())
		})
	}
}

func TestTaskModelNameResolvesEffectiveBillingIdentity(t *testing.T) {
	cases := []struct {
		name     string
		task     *model.Task
		expected string
	}{
		{
			name: "billing context identity first",
			task: &model.Task{Properties: model.Properties{OriginModelName: "origin-a"},
				PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
					OriginModelName: "origin-b", BillingModelName: "billing-a",
				}}},
			expected: "billing-a",
		},
		{
			name: "legacy billing context falls back to origin",
			task: &model.Task{Properties: model.Properties{OriginModelName: "origin-a"},
				PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
					OriginModelName: "origin-b",
				}}},
			expected: "origin-b",
		},
		{
			name:     "no billing context falls back to task properties",
			task:     &model.Task{Properties: model.Properties{OriginModelName: "origin-a"}},
			expected: "origin-a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, taskModelName(tc.task))
		})
	}
}

// TestTaskBillingIdentitySurvivesRestartAndPolling 验证任务经数据库持久化
// （重启 / 轮询进程重新加载）后，计费身份与赠金资格判定保持一致：
// 新任务以 BillingModelName 结算，旧任务回退 OriginModelName。
func TestTaskBillingIdentitySurvivesRestartAndPolling(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "identity-eligible", boolPtr(true))
	seedBonusEligibleModel(t, "identity-ineligible", boolPtr(false))

	t.Run("new task keeps persisted billing identity", func(t *testing.T) {
		task := persistTask(t, &model.Task{
			TaskID: "task-billing-identity-new", UserId: 921, ChannelId: 1,
			Status: model.TaskStatus(model.TaskStatusInProgress), Group: "default",
			Data:      json.RawMessage(`{}`),
			CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
			Properties: model.Properties{OriginModelName: "identity-ineligible"},
			PrivateData: model.TaskPrivateData{
				BillingSource: BillingSourceWallet,
				BillingContext: &model.TaskBillingContext{
					OriginModelName:  "identity-ineligible",
					BillingModelName: "identity-eligible",
				},
			},
		})

		assert.Equal(t, "identity-eligible", taskModelName(task))
		assert.Equal(t,
			[]model.LedgerAccount{model.AccountBonus, model.AccountReward, model.AccountPaid},
			taskDebitOrder(task),
			"计费身份（而非路由身份）决定赠金资格")
	})

	t.Run("legacy task falls back to origin model", func(t *testing.T) {
		task := persistTask(t, &model.Task{
			TaskID: "task-billing-identity-legacy", UserId: 921, ChannelId: 1,
			Status: model.TaskStatus(model.TaskStatusInProgress), Group: "default",
			Data:      json.RawMessage(`{}`),
			CreatedAt: time.Now().Unix(), UpdatedAt: time.Now().Unix(),
			Properties: model.Properties{OriginModelName: "identity-ineligible"},
			PrivateData: model.TaskPrivateData{
				BillingSource: BillingSourceWallet,
				BillingContext: &model.TaskBillingContext{
					OriginModelName: "identity-ineligible",
				},
			},
		})

		assert.Equal(t, "identity-ineligible", taskModelName(task))
		assert.Equal(t,
			[]model.LedgerAccount{model.AccountReward, model.AccountPaid},
			taskDebitOrder(task))
	})
}

// TestRecalculateTaskQuotaUsesEffectiveBillingIdentity 验证差额结算（轮询触达
// 终态后）按任务记录的计费身份决定扣款账户，与价格口径一致。
func TestRecalculateTaskQuotaUsesEffectiveBillingIdentity(t *testing.T) {
	truncate(t)
	seedBonusEligibleModel(t, "identity-eligible", boolPtr(true))
	seedBonusEligibleModel(t, "identity-ineligible", boolPtr(false))

	t.Run("billing identity drives bonus-first settle", func(t *testing.T) {
		const userID = 922
		seedBonusUser(t, userID, 30, 100)
		task := makeTask(userID, 1, 100, 0, BillingSourceWallet, 0)
		task.TaskID = "task-recalc-identity-new"
		task.PrivateData.WalletDebit = []model.LedgerShare{{Account: model.AccountBonus, Amount: 50}, {Account: model.AccountPaid, Amount: 50}}
		task.Properties.OriginModelName = "identity-ineligible"
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			OriginModelName:  "identity-ineligible",
			BillingModelName: "identity-eligible",
		}
		require.NoError(t, model.DB.Create(task).Error)

		RecalculateTaskQuota(t.Context(), task, 150, common.NewMessage("Token recalculation"))
		// bonus 优先：赠金 30 全额扣减，paid 补 20。
		assertUserBalances(t, userID, 0, 80, 0)
		assert.Equal(t, 150, task.Quota)
	})

	t.Run("legacy identity keeps paid-only settle", func(t *testing.T) {
		const userID = 923
		seedBonusUser(t, userID, 30, 100)
		task := makeTask(userID, 1, 100, 0, BillingSourceWallet, 0)
		task.TaskID = "task-recalc-identity-legacy"
		task.Properties.OriginModelName = "identity-ineligible"
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			OriginModelName: "identity-ineligible",
		}
		require.NoError(t, model.DB.Create(task).Error)

		RecalculateTaskQuota(t.Context(), task, 150, common.NewMessage("Token recalculation"))
		// 旧身份不可用赠金：50 全部由 paid 承担。
		assertUserBalances(t, userID, 30, 50, 0)
		assert.Equal(t, 150, task.Quota)
	})
}
