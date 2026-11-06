package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func modelTestDatabaseMatrix(t *testing.T, test func(*testing.T, string, string)) {
	t.Helper()
	dialects := []string{"sqlite", "mysql", "postgres"}
	selected := os.Getenv("TEST_FINGERPRINT_DIALECT")
	if selected != "" {
		require.Contains(t, dialects, selected, "unsupported TEST_FINGERPRINT_DIALECT")
		dialects = []string{selected}
	}
	for _, dialect := range dialects {
		t.Run(dialect, func(t *testing.T) {
			dsn := os.Getenv("TEST_" + strings.ToUpper(dialect) + "_DSN")
			if dialect != "sqlite" && dsn == "" {
				if selected != "" {
					t.Fatalf("selected %s verification requires TEST_%s_DSN", dialect, strings.ToUpper(dialect))
				}
				t.Skip("TEST_" + strings.ToUpper(dialect) + "_DSN is not configured")
			}
			test(t, dialect, dsn)
		})
	}
}

func TestModelTestTargetCatalogueDatabaseMatrix(t *testing.T) {
	modelTestDatabaseMatrix(t, func(t *testing.T, dialect, dsn string) {
		db, userID := setupModelTestsDatabase(t, dialect, dsn)
		groups, usable := ratio_setting.GroupRatio2JSONString(), setting.UserUsableGroups2JSONString()
		special, prices := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.MarshalJSONString(), ratio_setting.ModelRatio2JSONString()
		t.Cleanup(func() {
			require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groups))
			require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(usable))
			require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(special)))
			require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(prices))
			model.InvalidatePricingCache()
		})
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"forbidden":1}`))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default"}`))
		require.NoError(t, ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.UnmarshalJSON([]byte(`{}`)))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-mini":0.15,"gpt-disabled":1,"gpt-unconfigured":1}`))
		var channel model.Channel
		require.NoError(t, db.First(&channel).Error)
		require.NoError(t, db.Model(&channel).Updates(map[string]any{"group": "default,forbidden", "models": "gpt-4o-mini,gpt-disabled,gpt-unconfigured"}).Error)
		require.NoError(t, db.Create(&[]model.Ability{
			{ChannelId: channel.Id, Group: "default", Model: "gpt-disabled", Enabled: true},
			{ChannelId: channel.Id, Group: "default", Model: "gpt-unconfigured", Enabled: true},
			{ChannelId: channel.Id, Group: "forbidden", Model: "gpt-4o-mini", Enabled: true},
		}).Error)
		config, err := model.GetModelFingerprintSiteConfig(t.Context())
		require.NoError(t, err)
		config.Targets = []model.ModelFingerprintSiteTarget{
			{Group: "default", Model: "gpt-4o-mini", Enabled: true},
			{Group: "default", Model: "gpt-disabled", Enabled: false},
			{Group: "default", Model: "gpt-no-route", Enabled: true},
			{Group: "forbidden", Model: "gpt-4o-mini", Enabled: true},
		}
		config, err = model.UpdateModelFingerprintSiteConfig(t.Context(), config.Version, userID, *config)
		require.NoError(t, err)
		model.InvalidatePricingCache()
		catalogue, err := modelTestCatalogue(userID)
		require.NoError(t, err)
		assert.Equal(t, map[string][]string{"default": {"gpt-4o-mini"}}, catalogue)
		var token model.Token
		require.NoError(t, db.Where("user_id = ?", userID).First(&token).Error)
		for _, tc := range []struct {
			name, group, model string
			status             int
		}{
			{"enabled", "default", "gpt-4o-mini", http.StatusOK},
			{"disabled", "default", "gpt-disabled", http.StatusBadRequest},
			{"unconfigured", "default", "gpt-unconfigured", http.StatusBadRequest},
			{"unavailable", "default", "gpt-no-route", http.StatusBadRequest},
			{"unauthorized-group", "forbidden", "gpt-4o-mini", http.StatusBadRequest},
		} {
			selection := modelTestSelection{TokenID: token.Id, Model: tc.model, Group: tc.group}
			body, err := common.Marshal(selection)
			require.NoError(t, err)
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Set("id", userID)
			c.Request = httptest.NewRequest(http.MethodPost, "/estimate", strings.NewReader(string(body)))
			EstimateModelTest(c)
			assert.Equal(t, tc.status, response.Code, tc.name+": "+response.Body.String())
			if tc.status == http.StatusOK {
				continue
			}
			// A correctly signed ticket cannot replace current server-side scope
			// validation, even when the browser posts a hidden model directly.
			ticket := modelTestConfirmation{OutputUnbounded: true, modelTestSelection: selection, UserID: userID, Version: modelTestVersion, ExpiresAt: time.Now().Add(time.Minute).Unix()}
			body, err = common.Marshal(modelTestRequest{modelTestSelection: selection, ConfirmationToken: signModelTestConfirmation(ticket), Confirmed: true, RequestID: uuid.NewString()})
			require.NoError(t, err)
			response = httptest.NewRecorder()
			c, _ = gin.CreateTestContext(response)
			c.Set("id", userID)
			c.Request = httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(string(body)))
			PrepareModelTest(c)
			assert.Equal(t, http.StatusBadRequest, response.Code, tc.name+": "+response.Body.String())
			assert.Contains(t, response.Body.String(), "model_or_group_unavailable")
		}
		config.Enabled = false
		_, err = model.UpdateModelFingerprintSiteConfig(t.Context(), config.Version, userID, *config)
		require.NoError(t, err)
		catalogue, err = modelTestCatalogue(userID)
		require.NoError(t, err)
		assert.Empty(t, catalogue)
		// Current admission and shared historical observations have separate
		// contracts. Turning the feature off does not erase a safe public report.
		require.NoError(t, db.AutoMigrate(&model.ModelFingerprintSiteRun{}))
		require.NoError(t, db.Create(&model.ModelTestRun{ID: uuid.NewString(), Source: "user", UserID: userID, TokenID: token.Id, Model: "gpt-disabled", Group: "default", BenchmarkID: modelTestVersion, Status: "completed", Assessment: "scored", FingerprintResult: model.JSONValue(`{"status":"scored"}`), CreatedAt: time.Now().Unix()}).Error)
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodGet, "/site", nil)
		GetSiteModelFingerprints(c)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var payload struct {
			Data struct {
				Enabled   bool                       `json:"enabled"`
				Targets   []map[string]any           `json:"targets"`
				UserItems []model.ModelTestPublicRun `json:"user_items"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
		assert.False(t, payload.Data.Enabled)
		assert.Empty(t, payload.Data.Targets)
		require.Len(t, payload.Data.UserItems, 1)
		assert.Equal(t, "gpt-disabled", payload.Data.UserItems[0].Model)
		assert.NotContains(t, response.Body.String(), "token_id")
	})
}

func TestModelFingerprintUnavailableTargetSettingsDatabaseMatrix(t *testing.T) {
	modelTestDatabaseMatrix(t, func(t *testing.T, dialect, dsn string) {
		db, userID := setupModelTestsDatabase(t, dialect, dsn)
		require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("status", common.ChannelStatusManuallyDisabled).Error)
		for _, tc := range []struct {
			name                   string
			enabled, targetEnabled bool
			invalid, restoreRoute  bool
			status                 int
		}{
			{name: "cannot-enable-unavailable", enabled: true, targetEnabled: true, status: http.StatusBadRequest},
			{name: "disable-unavailable-target", enabled: true, targetEnabled: false, status: http.StatusOK},
			{name: "cannot-reactivate-unavailable", enabled: true, targetEnabled: true, status: http.StatusBadRequest},
			{name: "disable-global-retains-selection", enabled: false, targetEnabled: true, status: http.StatusOK},
			{name: "cannot-reenable-global-unavailable", enabled: true, targetEnabled: true, status: http.StatusBadRequest},
			{name: "still-validates-disabled-shape", enabled: false, targetEnabled: false, invalid: true, status: http.StatusBadRequest},
			{name: "reactivate-restored-route", enabled: true, targetEnabled: true, restoreRoute: true, status: http.StatusOK},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if tc.restoreRoute {
					require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Update("status", common.ChannelStatusEnabled).Error)
				}
				config, err := model.GetModelFingerprintSiteConfig(t.Context())
				require.NoError(t, err)
				config.Targets[0].Enabled = tc.targetEnabled
				if tc.invalid {
					config.Targets[0].Group = ""
				}
				body, err := common.Marshal(gin.H{"enabled": tc.enabled, "automatic_enabled": false, "expected_version": config.Version, "default_period_secs": 3600, "targets": config.Targets})
				require.NoError(t, err)
				response := securityEnrollmentRequest(http.MethodPut, "/settings", string(body), "", service.AuthIdentity{UserID: userID, SessionID: "settings-fixture", UserAuthVersion: 1, SessionVersion: 1}, UpdateModelFingerprintSettings)
				require.Equal(t, tc.status, response.Code, response.Body.String())
				after, err := model.GetModelFingerprintSiteConfig(t.Context())
				require.NoError(t, err)
				if tc.status == http.StatusOK {
					assert.Equal(t, config.Version+1, after.Version)
					assert.Equal(t, tc.enabled, after.Enabled)
					assert.Equal(t, tc.targetEnabled, after.Targets[0].Enabled)
					assert.Equal(t, config.Targets[0].ID, after.Targets[0].ID)
				} else {
					assert.Equal(t, config.Version, after.Version, "a rejected edit must not persist")
				}
			})
		}
	})
}

func TestModelTestTargetRevocationDatabaseMatrix(t *testing.T) {
	modelTestDatabaseMatrix(t, func(t *testing.T, dialect, dsn string) {
		for _, boundary := range []string{"after-prepare", "before-reservation", "after-reservation", "after-final-admission", "after-first-send"} {
			t.Run(boundary, func(t *testing.T) {
				db, userID := setupModelTestsDatabase(t, dialect, dsn)
				setupModelTestSwitchAccounting(t, db, userID)
				require.NoError(t, db.Model(&model.Channel{}).Where("models = ?", "gpt-4o-mini").Updates(map[string]any{"base_url": "https://fingerprint-fixture.invalid", "model_mapping": `{"gpt-4o-mini":"gpt-mapped-fixture"}`}).Error)
				model.InvalidatePricingCache()
				config, err := model.GetModelFingerprintSiteConfig(t.Context())
				require.NoError(t, err)
				var revoked atomic.Bool
				type revocationContextKey struct{}
				revoke := func() error {
					config.Targets[0].Enabled = false
					ctx := context.WithValue(t.Context(), revocationContextKey{}, true)
					_, err := model.UpdateModelFingerprintSiteConfig(ctx, config.Version, userID, *config)
					return err
				}
				var beforeExecution gin.HandlerFunc
				var reservedBalance int
				var cutoffMu sync.Mutex
				if boundary == "after-prepare" {
					beforeExecution = func(c *gin.Context) {
						require.NoError(t, revoke())
						revoked.Store(true)
					}
				} else if boundary == "before-reservation" {
					callback := "test:model-test-target-after-sample-intent"
					require.NoError(t, model.DB.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
						updates, ok := tx.Statement.Dest.(map[string]any)
						if !ok || updates["samples_json"] == nil {
							return
						}
						if tx.Statement.Table == "model_test_runs" && tx.Error == nil && tx.RowsAffected == 1 && revoked.CompareAndSwap(false, true) {
							assert.NoError(t, revoke())
						}
					}))
					t.Cleanup(func() { require.NoError(t, model.DB.Callback().Update().Remove(callback)) })
				} else if boundary == "after-reservation" {
					cut := func(tx *gorm.DB) {
						if tx.Statement.Table != "options" || tx.Error != nil || tx.Statement.Context.Value(revocationContextKey{}) != nil {
							return
						}
						// Other probes must wait for the target update to commit,
						// not treat an early cutoff flag as a completed revocation.
						cutoffMu.Lock()
						defer cutoffMu.Unlock()
						if revoked.Load() {
							return
						}
						var held int64
						if !assert.NoError(t, db.Model(&model.BillingFinalization{}).Where("user_id = ? AND state = ?", userID, model.BillingHeld).Count(&held).Error) || held == 0 {
							return
						}
						var user model.User
						assert.NoError(t, db.First(&user, userID).Error)
						reservedBalance = user.QuotaPaid
						revoked.Store(assert.NoError(t, revoke()))
					}
					callback := "test:model-test-target-final-admission"
					require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(callback, cut))
					t.Cleanup(func() { require.NoError(t, model.DB.Callback().Query().Remove(callback)) })
				}
				client, err := service.GetHttpClientWithProxySettings("", dto.ChannelSettings{})
				require.NoError(t, err)
				oldClient := *client
				t.Cleanup(func() { *client = oldClient })
				var calls atomic.Int32
				var admitted atomic.Int32
				allAdmitted := make(chan struct{})
				var revokeOnce sync.Once
				var revokeErr error
				client.Transport = modelDiscoveryRoundTripper(func(req *http.Request) (*http.Response, error) {
					if req.URL.Host != "fingerprint-fixture.invalid" {
						return nil, fmt.Errorf("unexpected fixture destination")
					}
					var request map[string]any
					if !assert.NoError(t, common.DecodeJson(req.Body, &request)) {
						return nil, fmt.Errorf("invalid fixture request")
					}
					assert.Equal(t, "gpt-mapped-fixture", request["model"], "the allowlist binds the original model, before channel mapping")
					firstSent := false
					if boundary == "after-final-admission" || boundary == "after-first-send" {
						// RoundTrip is reached only after this probe's real final
						// permission read. Hold all three there before revoking;
						// another probe's held wallet row cannot prove admission.
						if admitted.Add(1) == 3 {
							close(allAdmitted)
						}
						select {
						case <-allAdmitted:
						case <-req.Context().Done():
							return nil, req.Context().Err()
						case <-time.After(10 * time.Second):
							return nil, fmt.Errorf("three probes did not reach final admission concurrently")
						}
						revokeOnce.Do(func() {
							if boundary == "after-first-send" {
								calls.Add(1)
								firstSent = true
							}
							revokeErr = revoke()
							revoked.Store(revokeErr == nil)
						})
						if revokeErr != nil {
							return nil, revokeErr
						}
					}
					if !firstSent {
						calls.Add(1)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"fixture-completion","object":"chat.completion","model":"gpt-mapped-fixture","choices":[{"index":0,"message":{"role":"assistant","content":"0.1, 0.2, 0.3"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":100,"total_tokens":220}}`)), Request: req}, nil
				})
				engine := modelTestSwitchEngine(userID, beforeExecution)
				id := uuid.NewString()
				body := modelTestRequestBody(t, userID, id)
				newBody := modelTestRequestBody(t, userID, uuid.NewString())
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
				require.True(t, revoked.Load(), "the intended boundary must have been reached")
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				if boundary == "after-final-admission" || boundary == "after-first-send" {
					assert.Contains(t, response.Body.String(), `"status":"completed"`, "revocation preserves work already admitted for all three samples")
				} else {
					assert.Contains(t, response.Body.String(), `"status":"failed"`)
					assert.Contains(t, response.Body.String(), "model_or_group_unavailable")
				}
				denied := httptest.NewRecorder()
				engine.ServeHTTP(denied, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(newBody)))
				require.Equal(t, http.StatusBadRequest, denied.Code, denied.Body.String())
				assert.Contains(t, denied.Body.String(), "model_or_group_unavailable", "a quoted request still needs current target authorization")
				waitTaskSubmissionFixtureQuiescence(t)
				run, err := model.GetModelTestRun(userID, id)
				require.NoError(t, err)
				var user model.User
				require.NoError(t, db.First(&user, userID).Error)
				var owners []model.BillingFinalization
				require.NoError(t, db.Where("user_id = ?", userID).Find(&owners).Error)
				var ledger []model.BalanceLedger
				require.NoError(t, db.Where("user_id = ?", userID).Order("id").Find(&ledger).Error)
				switch boundary {
				case "after-prepare", "before-reservation":
					assert.Zero(t, calls.Load())
					assert.Equal(t, 1_000_000, user.QuotaPaid)
					assert.Empty(t, owners)
					assert.Empty(t, ledger, "rejection before reservation cannot debit the wallet")
				case "after-reservation":
					assert.Zero(t, calls.Load())
					assert.Less(t, reservedBalance, 1_000_000, "a real reservation preceded the cutoff")
					assert.GreaterOrEqual(t, reservedBalance, 998500)
					assert.Zero(t, (1_000_000-reservedBalance)%500)
					assert.Equal(t, 1_000_000, user.QuotaPaid)
					require.NotEmpty(t, owners)
					require.LessOrEqual(t, len(owners), 3)
					var audits []modelFingerprintSampleAudit
					require.NoError(t, common.UnmarshalJsonStr(run.SamplesJSON, &audits))
					require.Len(t, audits, 3)
					auditIDs := make(map[string]bool, len(audits))
					for _, audit := range audits {
						require.NotEmpty(t, audit.RequestID)
						assert.False(t, auditIDs[audit.RequestID])
						auditIDs[audit.RequestID] = true
					}
					assert.Contains(t, auditIDs, run.RequestID)
					require.Len(t, ledger, 2*len(owners))
					for _, owner := range owners {
						assert.Contains(t, auditIDs, owner.RequestID)
						assert.Equal(t, model.BillingRefunded, owner.State)
						assert.Equal(t, 500, owner.SourceHeld)
						assert.Equal(t, 500, owner.TokenHeld)
						var reasons []string
						var deltas []int
						for _, entry := range ledger {
							if entry.RequestId == owner.RequestID {
								reasons = append(reasons, entry.Reason)
								deltas = append(deltas, entry.Delta)
							}
						}
						assert.Equal(t, []string{model.LedgerReasonBillingPreConsume, model.LedgerReasonBillingRefund}, reasons)
						assert.Equal(t, []int{-500, 500}, deltas, "each undispatched reservation is refunded exactly once")
					}
				default:
					assert.EqualValues(t, 3, admitted.Load())
					assert.EqualValues(t, 3, calls.Load(), "each of the three admitted challenges finishes without replay")
					assert.Equal(t, 998500, user.QuotaPaid)
					require.Len(t, owners, 3)
					require.Len(t, ledger, 3)
					for _, owner := range owners {
						assert.Equal(t, model.BillingSettled, owner.State)
						assert.Equal(t, 500, owner.ActualQuota)
					}
					for _, entry := range ledger {
						assert.NotEqual(t, model.LedgerReasonBillingRefund, entry.Reason, "revocation does not refund already admitted work")
						assert.Equal(t, model.LedgerReasonBillingPreConsume, entry.Reason)
						assert.Equal(t, -500, entry.Delta)
					}
				}
				assert.Equal(t, user.QuotaPaid, user.Quota)
				var token model.Token
				require.NoError(t, db.Where("user_id = ?", userID).First(&token).Error)
				assert.Equal(t, user.QuotaPaid, token.RemainQuota, "token and wallet settlement remain aligned")
				var channel model.Channel
				require.NoError(t, db.Where("models = ?", "gpt-4o-mini").Take(&channel).Error)
				assert.Equal(t, common.ChannelStatusEnabled, channel.Status, "local permission denial is not an upstream outage")
				var errorLogs int64
				require.NoError(t, db.Model(&model.Log{}).Where("type = ?", model.LogTypeError).Count(&errorLogs).Error)
				assert.Zero(t, errorLogs)
				beforeCalls := calls.Load()
				replay := httptest.NewRecorder()
				engine.ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/runs", strings.NewReader(body)))
				require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
				assert.Equal(t, beforeCalls, calls.Load())
				var ledgerAfter int64
				require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ?", userID).Count(&ledgerAfter).Error)
				assert.EqualValues(t, len(ledger), ledgerAfter, "replay cannot debit or refund again")
			})
		}
	})
}
