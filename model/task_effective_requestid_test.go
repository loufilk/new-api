package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskPrivateData_EffectiveRequestID(t *testing.T) {
	tests := []struct {
		name     string
		data     *TaskPrivateData
		expected string
	}{
		{
			name:     "nil receiver",
			data:     nil,
			expected: "",
		},
		{
			name:     "both empty",
			data:     &TaskPrivateData{},
			expected: "",
		},
		{
			name: "execution only",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "req-exec-001",
				},
			},
			expected: "req-exec-001",
		},
		{
			name: "legacy only",
			data: &TaskPrivateData{
				RequestId: "req-legacy-002",
			},
			expected: "req-legacy-002",
		},
		{
			name: "both equal",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "req-same-003",
				},
				RequestId: "req-same-003",
			},
			expected: "req-same-003",
		},
		{
			name: "both conflict - execution wins",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "req-exec-004",
				},
				RequestId: "req-legacy-conflict",
			},
			expected: "req-exec-004",
		},
		{
			name: "execution present but empty string - fallback to legacy",
			data: &TaskPrivateData{
				Execution: &TaskExecutionSnapshot{
					RequestID: "",
				},
				RequestId: "req-legacy-005",
			},
			expected: "req-legacy-005",
		},
		{
			name: "execution nil but legacy present",
			data: &TaskPrivateData{
				Execution: nil,
				RequestId: "req-legacy-006",
			},
			expected: "req-legacy-006",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.data.EffectiveRequestID()
			assert.Equal(t, tt.expected, result)
		})
	}
}
