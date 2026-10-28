package service

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const refundFixtureChildEnv = "NEW_API_TEST_REFUND_FIXTURE_CHILD"
const refundFixtureChecked = "refund-fixture-cleanups-complete:"

// Fixed original RUN topology, independently checked against the source and
// the retained 324f full-package report. Never infer completeness from child output.
var refundFixtureTopology = map[string][]string{
	"TestBillingRecoveryMixedLegacyPageProgress": {""},
	"TestBillingRecoveryCursorWriteFailureThenResume": {
		"",
		"history_page=true",
		"history_page=false",
	},
	"TestBillingRecoverySchedulerCreationFailureThenResume": {""},
	"TestBillingUnknownDeliveryObservability": {
		"",
		"wallet",
		"wallet/write_failure",
		"wallet/reservation_probe",
		"wallet/receipt",
		"wallet/commit_rollback",
		"wallet/commit_ack_lost",
		"wallet/success_and_idempotent",
		"wallet/terminal_conflict",
		"subscription",
		"subscription/write_failure",
		"subscription/reservation_probe",
		"subscription/receipt",
		"subscription/commit_rollback",
		"subscription/commit_ack_lost",
		"subscription/success_and_idempotent",
		"subscription/terminal_conflict",
		"unavailable_identity_missing",
		"unavailable_identity_control",
		"unavailable_identity_oversized",
	},
	"TestPolicyPreservingRefundSeparateHeldAmounts": {
		"",
	},
	"TestPolicyPreservingFailedSettlementCanReserveThenRefund": {
		"",
	},
	"TestPolicyPreservingRollbackProbeFailureDoesNotReserve": {
		"",
	},
	"TestPolicyPreservingRefundIndependentViolationFee": {
		"",
		"fee_before_refund",
		"refund_before_fee",
	},
	"TestPolicyPreservingRealtimeIncrementAndTerminal": {
		"",
		"final_settle",
		"refund_initial_hold",
		"failed_final_then_refund",
	},
	"TestPolicyPreservingTerminalCommitBoundaries": {
		"",
		"rollback",
		"rollback/settlement",
		"lost_ack",
		"lost_ack/settlement",
	},
	"TestBillingSettlementDeadlineCommitEvidence": {
		"",
		"wallet",
		"wallet/rolled_back",
		"wallet/committed_lost_ack",
	},
	"TestOfficialRefundBeforeLostACKReadbackKeepsOfficialSequence": {
		"",
	},
	"TestOfficialRequestRefundMarksBeforeSourceAndDoesNotRetry": {
		"",
	},
	"TestOfficialRequestRefundTokenFailureKeepsSourceCredit": {
		"",
	},
	"TestOfficialSubscriptionBaseFailureStillReturnsExtraAndToken": {
		"",
	},
	"TestOfficialRefundPostCommitErrorsDoNotRollBack": {
		"",
		"request_token",
		"task_quota_clear",
	},
	"TestStreamFailureWithoutUsageRefundsReservationOnce": {
		"",
	},
	"TestBillingFinalizationSessionDatabaseMatrix": {
		"",
		"production_finalization",
		"production_finalization/responses_prelude",
		"production_finalization/responses_prelude/client_gone",
		"production_finalization/responses_prelude/timeout",
		"production_finalization/responses_prelude/scanner_error",
		"production_finalization/responses_prelude/eof",
		"production_finalization/responses_prelude/done",
		"production_finalization/responses_prelude/completed_retains_normal_policy",
		"production_finalization/zero_hold_cancel",
		"production_finalization/insufficient_retains_actual_and_hold",
		"production_finalization/unmeasured_cancel_review",
		"production_finalization/trusted_partial_redis",
		"production_finalization/freeze_write_failure",
		"production_finalization/intent_commit_1",
		"production_finalization/intent_commit_2",
		"production_finalization/money_commit_1",
		"production_finalization/money_commit_2",
		"production_finalization/transient_2",
		"production_finalization/transient_5",
		"production_finalization/concurrent_replay_and_refund",
		"recovery_mixed_legacy_progress",
		"recovery_cursor_write_resume",
		"recovery_cursor_write_resume/history_page=true",
		"recovery_cursor_write_resume/history_page=false",
		"recovery_scheduler_create_resume",
		"unknown_delivery_observability",
		"unknown_delivery_observability/wallet",
		"unknown_delivery_observability/wallet/write_failure",
		"unknown_delivery_observability/wallet/reservation_probe",
		"unknown_delivery_observability/wallet/receipt",
		"unknown_delivery_observability/wallet/commit_rollback",
		"unknown_delivery_observability/wallet/commit_ack_lost",
		"unknown_delivery_observability/wallet/success_and_idempotent",
		"unknown_delivery_observability/wallet/terminal_conflict",
		"unknown_delivery_observability/subscription",
		"unknown_delivery_observability/subscription/write_failure",
		"unknown_delivery_observability/subscription/reservation_probe",
		"unknown_delivery_observability/subscription/receipt",
		"unknown_delivery_observability/subscription/commit_rollback",
		"unknown_delivery_observability/subscription/commit_ack_lost",
		"unknown_delivery_observability/subscription/success_and_idempotent",
		"unknown_delivery_observability/subscription/terminal_conflict",
		"unknown_delivery_observability/unavailable_identity_missing",
		"unknown_delivery_observability/unavailable_identity_control",
		"unknown_delivery_observability/unavailable_identity_oversized",
		"separate_held_amounts",
		"official_request_source_failure",
		"official_request_token_failure",
		"official_subscription_three_attempts",
		"official_task_independent_steps",
		"official_task_independent_steps/source",
		"official_task_independent_steps/token",
		"official_task_independent_steps/quota_clear",
		"official_post_commit_errors",
		"official_post_commit_errors/request_token",
		"official_post_commit_errors/task_quota_clear",
		"historical_pending_read_only",
		"historical_pending_read_only/flag",
		"historical_pending_read_only/private_only",
		"historical_pending_read_only/both",
		"inherited_093_allocation_limits",
		"inherited_093_allocation_limits/small_three_account_remainder",
		"inherited_093_allocation_limits/submit_before_task_refund",
		"inherited_093_allocation_limits/task_refund_before_submit",
		"commit_boundaries",
		"commit_boundaries/rollback",
		"commit_boundaries/rollback/settlement",
		"commit_boundaries/lost_ack",
		"commit_boundaries/lost_ack/settlement",
		"settlement_deadline_driver_pool",
		"settlement_deadline_driver_pool/driver",
		"settlement_deadline_driver_pool/connection",
		"settlement_deadline_commit_evidence",
		"settlement_deadline_commit_evidence/wallet",
		"settlement_deadline_commit_evidence/wallet/rolled_back",
		"settlement_deadline_commit_evidence/wallet/committed_lost_ack",
		"official_refund_before_lost_ack_readback",
		"settlement_deadline_redis",
		"settlement_deadline_redis/precommit_fence",
		"settlement_deadline_redis/postcommit_cache",
		"settlement_deadline_redis/zero_delta_cache",
		"settlement_deadline_redis/terminal_replay_cache",
		"settlement_deadline_redis/live_redis_error",
		"settlement_cancelled_client",
		"extension_capability",
		"extension_capability/custom_wallet_atomic_replay",
		"extension_capability/custom_billing_settler_insert",
		"extension_capability/custom_subscription_delta",
		"extension_capability/custom_non_wallet",
		"zero_delta_replacement",
		"zero_delta_replacement/hold_0/key",
		"zero_delta_replacement/hold_0/owner",
		"zero_delta_replacement/hold_100/key",
		"zero_delta_replacement/hold_100/owner",
		"probe_failure",
		"independent_violation_fee",
		"independent_violation_fee/fee_before_refund",
		"independent_violation_fee/refund_before_fee",
		"realtime_increment_terminal",
		"realtime_increment_terminal/final_settle",
		"realtime_increment_terminal/refund_initial_hold",
		"realtime_increment_terminal/failed_final_then_refund",
		"failed_settle_reserve_refund",
		"legacy_final_more_less",
		"legacy_final_more_less/a",
		"legacy_final_more_less/Å",
		"legacy_final_more_less/ĕ",
		"real_session_partial_more",
		"real_session_partial_less",
		"real_session_trusted",
		"real_session_playground",
		"real_session_unlimited",
		"real_session_subscription_zero_delta",
		"real_session_subscription_zero_delta_replaced_token",
	},
}

// Only this fixed refund-consumer group is isolated, not arbitrary package tests.
// Parent t.Run nodes below verify the real child's original assertions; they do
// not claim that the SQL or refund callbacks ran a second time in the parent.
func runRefundFixtureChild(t *testing.T) bool {
	t.Helper()
	scope := t.Name()
	if child := os.Getenv(refundFixtureChildEnv); child != "" {
		require.True(t, scope == child || strings.HasPrefix(scope, child+"/"), "refund child escaped its exact scope")
		if scope == child {
			t.Cleanup(func() {
				waitRefundCallback(t, nil)
				if !t.Failed() && !t.Skipped() {
					fmt.Println(refundFixtureChecked + scope)
				}
			}) // Registered first: runs after the real body's fixture cleanups.
		}
		return false
	}

	originalRun := flag.Lookup("test.run").Value.String()
	originalSkip := flag.Lookup("test.skip").Value.String()
	selector, err := refundFixtureSelector(scope, originalRun)
	require.NoError(t, err)
	expected, err := refundFixtureExpected(scope, originalRun, originalSkip)
	require.NoError(t, err)
	executable, err := os.Executable()
	require.NoError(t, err)
	budget := 4 * time.Minute
	if deadline, ok := t.Deadline(); ok {
		budget = min(budget, time.Until(deadline)-5*time.Second)
	}
	require.Greater(t, budget, 3*time.Second, "insufficient remaining parent test budget")
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable,
		"-test.run="+selector, "-test.skip="+originalSkip, "-test.count=1", "-test.v=true",
		"-test.short="+fmt.Sprint(testing.Short()), "-test.timeout="+(budget-2*time.Second).String())
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, refundFixtureChildEnv+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, refundFixtureChildEnv+"="+scope)
	cmd.WaitDelay = time.Second
	output, runErr := cmd.CombinedOutput() // Always joins, including cancellation.
	t.Logf("real refund assertions ran in owned child %s; parent nodes proxy verified results:\nchild: %s", scope, strings.ReplaceAll(string(output), "\n", "\nchild: "))
	require.NoError(t, validateRefundFixtureChild(scope, expected, output, runErr, ctx.Err()))
	replayed := proxyRefundFixtureChildren(t, scope, expected)
	descendants := 0
	for _, name := range expected {
		if strings.HasPrefix(name, scope+"/") {
			descendants++
		}
	}
	require.Equal(t, descendants, replayed, "parent selector must not lose a verified child node")
	return true
}

func proxyRefundFixtureChildren(t *testing.T, parent string, expected []string) int {
	t.Helper()
	replayed := 0
	for _, name := range expected {
		if !strings.HasPrefix(name, parent+"/") {
			continue
		}
		direct := true
		for _, other := range expected {
			if other != parent && other != name && strings.HasPrefix(other, parent+"/") && strings.HasPrefix(name, other+"/") {
				direct = false
				break
			}
		}
		if direct {
			t.Run(strings.TrimPrefix(name, parent+"/"), func(t *testing.T) {
				require.Equal(t, name, t.Name(), "proxy name must equal the validated real child node")
				replayed++
				replayed += proxyRefundFixtureChildren(t, name, expected)
			})
		}
	}
	return replayed
}

// Go test splits / and | only outside bracket/parenthesis groups. Preserve that
// selector grammar when narrowing a parent run to one owned top/dialect scope.
func refundFixturePatterns(pattern string) [][]string {
	branches := [][]string{}
	parts := []string{}
	start, brackets, parens := 0, 0, 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			brackets++
		case ']':
			brackets = max(0, brackets-1)
		case '(':
			if brackets == 0 {
				parens++
			}
		case ')':
			if brackets == 0 {
				parens--
			}
		case '/', '|':
			if brackets == 0 && parens == 0 {
				parts = append(parts, pattern[start:i])
				start = i + 1
				if pattern[i] == '|' {
					branches = append(branches, parts)
					parts = nil
				}
			}
		}
	}
	return append(branches, append(parts, pattern[start:]))
}

func refundFixtureMatches(name string, branches [][]string, partial bool) bool {
	parts := strings.Split(name, "/")
	for _, branch := range branches {
		matches := true
		for i := 0; matches && i < min(len(parts), len(branch)); i++ {
			matches, _ = regexp.MatchString(branch[i], parts[i]) // Go's parent matcher already validated the regex.
		}
		if matches {
			// testing.alternationMatch stops at its FIRST match, including a
			// partial ancestor match. In skip mode that partial does not skip.
			return partial || len(parts) >= len(branch)
		}
	}
	return false
}

func refundFixtureSelector(scope, original string) (string, error) {
	exact := strings.Split(scope, "/")
	for i := range exact {
		exact[i] = "^" + regexp.QuoteMeta(exact[i]) + "$"
	}
	selections := []string{}
	for _, branch := range refundFixturePatterns(original) {
		if !refundFixtureMatches(scope, [][]string{branch}, true) {
			continue
		}
		selected := append([]string{}, exact...)
		if len(branch) > len(exact) {
			selected = append(selected, branch[len(exact):]...)
		}
		selections = append(selections, strings.Join(selected, "/"))
	}
	if len(selections) == 0 {
		return "", fmt.Errorf("refund scope %q is outside original selector %q", scope, original)
	}
	return strings.Join(selections, "|"), nil
}

func refundFixtureExpected(scope, run, skip string) ([]string, error) {
	parts := strings.Split(scope, "/")
	suffixes, ok := refundFixtureTopology[parts[0]]
	if !ok {
		return nil, fmt.Errorf("unknown refund scope %q", scope)
	}
	if parts[0] == "TestBillingFinalizationSessionDatabaseMatrix" {
		if len(parts) != 2 || (parts[1] != "sqlite" && parts[1] != "mysql" && parts[1] != "postgres") {
			return nil, fmt.Errorf("invalid refund matrix scope %q", scope)
		}
		if parts[1] != "sqlite" {
			native := make([]string, 0, len(suffixes)+1)
			for _, suffix := range suffixes {
				if suffix == "settlement_deadline_commit_evidence" {
					native = append(native, "settlement_deadline_row_lock")
				}
				native = append(native, suffix)
			}
			suffixes = native
		}
	} else if len(parts) != 1 {
		return nil, fmt.Errorf("invalid independent refund scope %q", scope)
	}
	expected := []string{}
	for i := 1; i < len(parts); i++ {
		expected = append(expected, strings.Join(parts[:i], "/"))
	}
	for _, suffix := range suffixes {
		if strings.HasPrefix(suffix, "production_finalization") && (len(parts) != 2 || parts[1] != "postgres" || os.Getenv("TEST_FINALIZATION_SERVICE_POSTGRES_DSN") == "" || os.Getenv("TEST_REDIS_DSN") == "") {
			continue // Explicitly opt-in PG/Redis production fixture, not the legacy dialect matrix.
		}
		name := scope
		if suffix != "" {
			name += "/" + suffix
		}
		if refundFixtureMatches(name, refundFixturePatterns(run), true) && (skip == "" || !refundFixtureMatches(name, refundFixturePatterns(skip), false)) {
			expected = append(expected, name)
		}
	}
	return expected, nil
}

func validateRefundFixtureChild(scope string, expected []string, output []byte, runErr, contextErr error) error {
	if contextErr != nil {
		return fmt.Errorf("refund child context: %w", contextErr)
	}
	if runErr != nil {
		return fmt.Errorf("refund child exit: %w", runErr)
	}
	allowed := map[string]bool{}
	for _, name := range expected {
		if allowed[name] {
			return fmt.Errorf("duplicate expected refund node %q", name)
		}
		allowed[name] = true
	}
	runs, passes := map[string]int{}, map[string]int{}
	checked, terminal := 0, 0
	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(raw)
		if terminal != 0 && (line == "PASS" || strings.HasPrefix(line, "=== ") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, refundFixtureChecked)) {
			return fmt.Errorf("refund protocol event after terminal PASS: %s", line)
		}
		switch {
		case line == refundFixtureChecked+scope:
			checked++
			// Ordinary verbose output buffers descendant PASS reports until
			// the parent reports. RUN is immediate; all exact PASS counts are
			// checked after process exit, not incorrectly before this marker.
			for _, name := range expected {
				if runs[name] != 1 {
					return fmt.Errorf("refund cleanup marker before all real subtree RUN events: %s", name)
				}
			}
		case strings.HasPrefix(line, refundFixtureChecked):
			return fmt.Errorf("wrong refund cleanup marker: %s", line)
		case line == "PASS":
			if checked != 1 {
				return fmt.Errorf("refund terminal PASS before cleanup marker")
			}
			for _, name := range expected {
				if runs[name] != 1 || passes[name] != 1 {
					return fmt.Errorf("refund terminal PASS before complete node: %s", name)
				}
			}
			terminal++
		case line == "FAIL", strings.HasPrefix(line, "--- FAIL:"), strings.HasPrefix(line, "--- SKIP:"), strings.HasPrefix(line, "panic:"), strings.HasPrefix(line, "fatal error:"), strings.Contains(line, "WARNING: DATA RACE"):
			return fmt.Errorf("refund child failure or skip: %s", line)
		case strings.HasPrefix(line, "=== RUN   "):
			name := strings.TrimPrefix(line, "=== RUN   ")
			if !allowed[name] || checked != 0 {
				return fmt.Errorf("unexpected/late refund run %q", name)
			}
			runs[name]++
		case strings.HasPrefix(line, "--- PASS: "):
			name, _, ok := strings.Cut(strings.TrimPrefix(line, "--- PASS: "), " (")
			if !ok || !strings.HasSuffix(line, "s)") || !allowed[name] {
				return fmt.Errorf("unexpected refund pass: %s", line)
			}
			if (name == scope || strings.HasPrefix(scope, name+"/")) && checked != 1 {
				return fmt.Errorf("refund owner passed before cleanup marker: %s", name)
			}
			passes[name]++
		}
	}
	for _, name := range expected {
		if runs[name] != 1 || passes[name] != 1 {
			return fmt.Errorf("incomplete refund node %s: run=%d pass=%d", name, runs[name], passes[name])
		}
	}
	if !allowed[scope] || checked != 1 || terminal != 1 {
		return fmt.Errorf("incomplete refund child: scope=%t checked=%d terminal=%d", allowed[scope], checked, terminal)
	}
	return nil
}

func TestRefundFixtureChildResultProtocol(t *testing.T) {
	const scope = "TestOwnedRefund"
	expected := []string{scope, scope + "/first", scope + "/second"}
	valid := "=== RUN   " + scope + "\n=== RUN   " + scope + "/first\n--- PASS: " + scope + "/first (0.01s)\n=== RUN   " + scope + "/second\n--- PASS: " + scope + "/second (0.01s)\n" + refundFixtureChecked + scope + "\n--- PASS: " + scope + " (0.02s)\nPASS\n"
	for _, tc := range []struct {
		name, output       string
		runErr, contextErr error
		wantError          bool
	}{
		{name: "complete", output: valid},
		{name: "ordinary_verbose_buffered_passes", output: "=== RUN   " + scope + "\n=== RUN   " + scope + "/first\n=== RUN   " + scope + "/second\n" + refundFixtureChecked + scope + "\n--- PASS: " + scope + " (0.02s)\n    --- PASS: " + scope + "/first (0.01s)\n    --- PASS: " + scope + "/second (0.01s)\nPASS\n"},
		{name: "empty", wantError: true},
		{name: "missing_leaf", output: strings.ReplaceAll(strings.ReplaceAll(valid, "=== RUN   "+scope+"/second\n", ""), "--- PASS: "+scope+"/second (0.01s)\n", ""), wantError: true},
		{name: "extra_leaf", output: strings.Replace(valid, refundFixtureChecked, "=== RUN   "+scope+"/extra\n"+refundFixtureChecked, 1), wantError: true},
		{name: "missing_parent", output: strings.Replace(valid, "=== RUN   "+scope+"\n", "", 1), wantError: true},
		{name: "duplicate_run", output: "=== RUN   " + scope + "\n" + valid, wantError: true},
		{name: "duplicate_pass", output: strings.Replace(valid, refundFixtureChecked, "--- PASS: "+scope+"/first (0.01s)\n"+refundFixtureChecked, 1), wantError: true},
		{name: "skipped", output: strings.Replace(valid, "--- PASS: "+scope+"/first", "--- SKIP: "+scope+"/first", 1), wantError: true},
		{name: "missing_cleanup_marker", output: strings.Replace(valid, refundFixtureChecked+scope+"\n", "", 1), wantError: true},
		{name: "early_cleanup_marker", output: refundFixtureChecked + scope + "\n" + strings.Replace(valid, refundFixtureChecked+scope+"\n", "", 1), wantError: true},
		{name: "wrong_scope_marker", output: strings.Replace(valid, refundFixtureChecked+scope, refundFixtureChecked+"TestOther", 1), wantError: true},
		{name: "child_failure", output: valid, runErr: errors.New("synthetic child exit"), wantError: true},
		{name: "deadline", output: valid, contextErr: context.DeadlineExceeded, wantError: true},
		{name: "missing_terminal", output: strings.TrimSuffix(valid, "PASS\n"), wantError: true},
		{name: "duplicate_terminal", output: valid + "PASS\n", wantError: true},
		{name: "early_terminal", output: "PASS\n" + strings.TrimSuffix(valid, "PASS\n"), wantError: true},
		{name: "late_run_after_terminal", output: valid + "=== RUN   " + scope + "/first\n", wantError: true},
		{name: "race", output: valid + "WARNING: DATA RACE\n", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRefundFixtureChild(scope, expected, []byte(tc.output), tc.runErr, tc.contextErr)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRefundFixtureSelectionPreservesParentFilter(t *testing.T) {
	const top = "TestPolicyPreservingRefundIndependentViolationFee"
	for _, tc := range []struct{ name, scope, filter, want string }{
		{"whole_top", top, "", "^" + top + "$"},
		{"selected_child", top, "^" + top + "$/^refund_before_fee$", "^" + top + "$/^refund_before_fee$"},
		{"other_top_alternative", top, "^TestOther$|^" + top + "$/^fee_before_refund$", "^" + top + "$/^fee_before_refund$"},
		{"grouped_top_alternative", top, "^(TestOther|" + top + ")$/^refund_before_fee$", "^" + top + "$/^refund_before_fee$"},
		{"matrix_dialect", "TestBillingFinalizationSessionDatabaseMatrix/sqlite", "^TestBillingFinalizationSessionDatabaseMatrix$/^sqlite$/^official_post_commit_errors$", "^TestBillingFinalizationSessionDatabaseMatrix$/^sqlite$/^official_post_commit_errors$"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selector, err := refundFixtureSelector(tc.scope, tc.filter)
			require.NoError(t, err)
			require.Equal(t, tc.want, selector)
		})
	}
	t.Run("skip_one_real_leaf", func(t *testing.T) {
		expected, err := refundFixtureExpected(top, "^"+top+"$", "^"+top+"$/^fee_before_refund$")
		require.NoError(t, err)
		require.Equal(t, []string{top, top + "/refund_before_fee"}, expected)
	})
	t.Run("skip_first_partial_alternative", func(t *testing.T) {
		skip := "^" + top + "$/^fee_before_refund$|^" + top + "$"
		require.False(t, refundFixtureMatches(top, refundFixturePatterns(skip), false))
		require.True(t, refundFixtureMatches(top+"/refund_before_fee", refundFixturePatterns(skip), false))
		expected, err := refundFixtureExpected(top, "^"+top+"$", skip)
		require.NoError(t, err)
		require.Equal(t, []string{top}, expected)
	})
	t.Run("selected_matrix_has_real_parents", func(t *testing.T) {
		const matrix = "TestBillingFinalizationSessionDatabaseMatrix"
		expected, err := refundFixtureExpected(matrix+"/sqlite", "^"+matrix+"$/^sqlite$/^official_post_commit_errors$/^request_token$", "")
		require.NoError(t, err)
		require.Equal(t, []string{matrix, matrix + "/sqlite", matrix + "/sqlite/official_post_commit_errors", matrix + "/sqlite/official_post_commit_errors/request_token"}, expected)
	})
	t.Run("slash_in_leaf_is_not_an_extra_parent", func(t *testing.T) {
		const matrix = "TestBillingFinalizationSessionDatabaseMatrix"
		expected, err := refundFixtureExpected(matrix+"/sqlite", "^"+matrix+"$/^sqlite$/^zero_delta_replacement$/^hold_0$/^key$", "")
		require.NoError(t, err)
		require.Equal(t, []string{matrix, matrix + "/sqlite", matrix + "/sqlite/zero_delta_replacement", matrix + "/sqlite/zero_delta_replacement/hold_0/key"}, expected)
	})
	t.Run("native_row_lock_is_required", func(t *testing.T) {
		const matrix = "TestBillingFinalizationSessionDatabaseMatrix"
		expected, err := refundFixtureExpected(matrix+"/postgres", "^"+matrix+"$/^postgres$/^settlement_deadline_row_lock$", "")
		require.NoError(t, err)
		require.Equal(t, []string{matrix, matrix + "/postgres", matrix + "/postgres/settlement_deadline_row_lock"}, expected)
	})
}
