package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const notificationWindowChildEnv = "NEW_API_TEST_NOTIFICATION_WINDOW_CHILD"

// checkMemoryLimit starts the real, process-lifetime cleanup worker. Run each
// original memory-window case in this same test binary, not in the parent's
// default gopool, whose finite refund callbacks other serial fixtures join.
// There are exactly two children (one per original case), with no retry.
func runNotificationWindowChild(name string) ([]byte, error, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err, nil
	}
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] != "TestCheckNotificationLimitWindowSpansClockHours" {
		return nil, fmt.Errorf("invalid notification child name: %q", name), nil
	}
	selector := "^" + regexp.QuoteMeta(parts[0]) + "$/^" + regexp.QuoteMeta(parts[1]) + "$"
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "-test.run="+selector, "-test.count=1", "-test.v=true", "-test.timeout=30s")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, notificationWindowChildEnv+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, notificationWindowChildEnv+"="+name)
	cmd.WaitDelay = 2 * time.Second
	output, runErr := cmd.CombinedOutput() // Includes Wait, including the cancelled-child path.
	return output, runErr, ctx.Err()
}

func validateNotificationWindowChild(name string, output []byte, runErr, contextErr error) error {
	if contextErr != nil {
		return fmt.Errorf("notification child context: %w", contextErr)
	}
	if runErr != nil {
		return fmt.Errorf("notification child exit: %w", runErr)
	}
	top := "TestCheckNotificationLimitWindowSpansClockHours"
	runs, passes := map[string]int{}, map[string]int{}
	terminal, checked := 0, 0
	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "PASS":
			terminal++
		case line == "notification-window-checked:"+name:
			checked++
		case line == "FAIL", strings.HasPrefix(line, "--- FAIL:"), strings.HasPrefix(line, "--- SKIP:"), strings.HasPrefix(line, "panic:"), strings.HasPrefix(line, "fatal error:"), strings.Contains(line, "WARNING: DATA RACE"):
			return fmt.Errorf("notification child reported failure or skip: %s", line)
		case strings.HasPrefix(line, "=== RUN   "):
			node := strings.TrimPrefix(line, "=== RUN   ")
			if node != top && node != name {
				return fmt.Errorf("unexpected notification child node: %s", node)
			}
			runs[node]++
		case strings.HasPrefix(line, "--- PASS: "):
			node, _, found := strings.Cut(strings.TrimPrefix(line, "--- PASS: "), " (")
			if !found || !strings.HasSuffix(line, "s)") || (node != top && node != name) {
				return fmt.Errorf("unexpected notification child pass: %s", line)
			}
			passes[node]++
		}
	}
	if runs[top] != 1 || runs[name] != 1 || passes[top] != 1 || passes[name] != 1 || terminal != 1 || checked != 1 {
		return fmt.Errorf("notification child incomplete: run=%v pass=%v terminal=%d checked=%d", runs, passes, terminal, checked)
	}
	return nil
}

// 限流窗口必须由 NOTIFICATION_LIMIT_DURATION_MINUTE 决定，不能在整点被重置
func TestCheckNotificationLimitWindowSpansClockHours(t *testing.T) {
	originalRedis := common.RedisEnabled
	originalCount := constant.NotifyLimitCount
	originalMinute := constant.NotificationLimitDurationMinute
	t.Cleanup(func() {
		common.RedisEnabled = originalRedis
		constant.NotifyLimitCount = originalCount
		constant.NotificationLimitDurationMinute = originalMinute
	})
	common.RedisEnabled = false
	constant.NotifyLimitCount = 2
	constant.NotificationLimitDurationMinute = 720

	tests := []struct {
		name        string
		windowStart time.Duration
		expected    bool
	}{
		{name: "limit reached two hours ago still blocks", windowStart: 2 * time.Hour, expected: false},
		{name: "limit reached after window expired allows", windowStart: 721 * time.Minute, expected: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if child := os.Getenv(notificationWindowChildEnv); child == "" {
				output, runErr, contextErr := runNotificationWindowChild(t.Name())
				t.Logf("owned notification child output:\nchild: %s", strings.ReplaceAll(string(output), "\n", "\nchild: "))
				require.NoError(t, validateNotificationWindowChild(t.Name(), output, runErr, contextErr))
				return
			} else {
				require.Equal(t, t.Name(), child, "a child must run only its exact selected original case")
			}
			userId := 900001 + i
			key := fmt.Sprintf("%d:%s", userId, dto.NotifyTypeQuotaExceed)
			notifyLimitStore.Store(key, limitCount{Count: 2, Timestamp: time.Now().Add(-tt.windowStart)})
			t.Cleanup(func() { notifyLimitStore.Delete(key) })

			canSend, err := CheckNotificationLimit(userId, dto.NotifyTypeQuotaExceed)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, canSend)
			fmt.Println("notification-window-checked:" + t.Name())
		})
	}
}

// These are protocol negative controls, not stand-ins for the two real child
// executions above. Empty/no-match/skip/failed/timed-out children cannot pass
// merely because their output happens to contain the word PASS.
func TestNotificationWindowChildResultProtocol(t *testing.T) {
	const top = "TestCheckNotificationLimitWindowSpansClockHours"
	const child = top + "/limit_reached_two_hours_ago_still_blocks"
	valid := fmt.Sprintf("=== RUN   %s\n=== RUN   %s\nnotification-window-checked:%s\n--- PASS: %s (0.01s)\n    --- PASS: %s (0.00s)\nPASS\n", top, child, child, top, child)
	for _, tc := range []struct {
		name       string
		output     string
		runErr     error
		contextErr error
		wantError  bool
	}{
		{name: "complete", output: valid},
		{name: "empty", wantError: true},
		{name: "no_match", output: "testing: warning: no tests to run\nPASS\n", wantError: true},
		{name: "wrong_selector", output: strings.ReplaceAll(valid, child, top+"/wrong_case"), wantError: true},
		{name: "missing_assertion_marker", output: strings.ReplaceAll(valid, "notification-window-checked:"+child+"\n", ""), wantError: true},
		{name: "skipped", output: strings.ReplaceAll(valid, "--- PASS: "+child, "--- SKIP: "+child), wantError: true},
		{name: "duplicate_node", output: valid + "=== RUN   " + child + "\n", wantError: true},
		{name: "nonzero_exit", output: valid, runErr: errors.New("synthetic child exit"), wantError: true},
		{name: "deadline", output: valid, contextErr: context.DeadlineExceeded, wantError: true},
		{name: "missing_terminal", output: strings.TrimSuffix(valid, "PASS\n"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNotificationWindowChild(child, []byte(tc.output), tc.runErr, tc.contextErr)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckNotificationLimitRedisWindowSpansClockHours(t *testing.T) {
	originalRedis := common.RedisEnabled
	originalRDB := common.RDB
	originalCount := constant.NotifyLimitCount
	originalMinute := constant.NotificationLimitDurationMinute
	server := miniredis.RunT(t)
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB = originalRDB
		common.RedisEnabled = originalRedis
		constant.NotifyLimitCount = originalCount
		constant.NotificationLimitDurationMinute = originalMinute
	})
	common.RedisEnabled = true
	constant.NotifyLimitCount = 2
	constant.NotificationLimitDurationMinute = 720

	// 窗口在 2 小时前开始并已发满，剩余 10 小时
	userId := 900010
	key := fmt.Sprintf("notify_limit:%d:%s", userId, dto.NotifyTypeQuotaExceed)
	require.NoError(t, server.Set(key, "2"))
	server.SetTTL(key, 10*time.Hour)

	steps := []struct {
		name     string
		elapse   time.Duration
		expected bool
	}{
		{name: "limit reached two hours ago still blocks", expected: false},
		{name: "new window opens after the old one ends", elapse: 10*time.Hour + time.Minute, expected: true},
		{name: "second notification in the new window", expected: true},
		{name: "third notification in the new window is blocked", expected: false},
		{name: "new window ends after the configured duration", elapse: 720 * time.Minute, expected: true},
	}
	for _, step := range steps {
		server.FastForward(step.elapse)
		canSend, err := CheckNotificationLimit(userId, dto.NotifyTypeQuotaExceed)
		require.NoError(t, err, step.name)
		assert.Equal(t, step.expected, canSend, step.name)
	}
}
