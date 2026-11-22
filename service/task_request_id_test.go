package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

func TestTaskWalletCostRefsUsesEffectiveRequestID(t *testing.T) {
	tests := []struct {
		name string
		task *model.Task
		want [][2]string
	}{
		{name: "nil task"},
		{
			name: "execution only",
			task: &model.Task{
				TaskID: "task_exec_refs_001",
				PrivateData: model.TaskPrivateData{
					Execution: &model.TaskExecutionSnapshot{RequestID: "req-exec-refs-001"},
				},
			},
			want: [][2]string{{"relay", "req-exec-refs-001"}, {"task", "task_exec_refs_001"}},
		},
		{
			name: "legacy only",
			task: &model.Task{
				TaskID:      "task_legacy_refs_002",
				PrivateData: model.TaskPrivateData{RequestId: "req-legacy-refs-002"},
			},
			want: [][2]string{{"relay", "req-legacy-refs-002"}, {"task", "task_legacy_refs_002"}},
		},
		{
			name: "execution wins conflict",
			task: &model.Task{
				TaskID: "task_conflict_refs_003",
				PrivateData: model.TaskPrivateData{
					Execution: &model.TaskExecutionSnapshot{RequestID: "req-exec-refs-003"},
					RequestId: "req-legacy-conflict-refs-003",
				},
			},
			want: [][2]string{{"relay", "req-exec-refs-003"}, {"task", "task_conflict_refs_003"}},
		},
		{
			name: "task reference only",
			task: &model.Task{TaskID: "task_no_req_004"},
			want: [][2]string{{"task", "task_no_req_004"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, TaskWalletCostRefs(test.task))
		})
	}
}
