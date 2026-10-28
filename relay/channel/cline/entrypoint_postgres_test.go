package cline_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/relay"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TEST_CLINE_POSTGRES_DSN must point at an isolated, disposable PostgreSQL.
func TestClineResponsesFailureAccountingPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_CLINE_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_CLINE_POSTGRES_DSN is not configured")
	}
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	var version string
	require.NoError(t, db.Raw("SHOW server_version").Scan(&version).Error)
	t.Logf("PostgreSQL %s", version)
	schema := "cline_response_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	t.Cleanup(func() { require.NoError(t, db.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error) })
	require.NoError(t, db.Exec(fmt.Sprintf("SET search_path TO %q", schema)).Error)
	oldDB, oldLogs := model.DB, model.LOG_DB
	oldType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldBatch, oldConsume := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogs
		common.SetDatabaseTypes(oldType, oldLogType)
		common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldBatch, oldConsume
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Ability{}, &model.Model{}, &model.Log{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}))
	require.NoError(t, walletmodule.RegisterRuntime())
	factories := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(factories))
	require.NoError(t, walletmodule.RegisterFundingFactory(factories))
	require.NoError(t, service.ConfigureFundingFactoryRegistry(factories))

	for _, tc := range []struct {
		name, first string
		want        int
	}{
		{"role_only_error", `{"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`, 0},
		{"partial_output_error", `{"choices":[{"index":0,"delta":{"content":"partial"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`, 12},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const start = 50000
			user := model.User{Username: tc.name, AffCode: tc.name, Quota: start, QuotaPaid: start, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			token := model.Token{UserId: user.Id, Key: "test-only-" + tc.name, RemainQuota: start, Status: common.TokenStatusEnabled}
			require.NoError(t, db.Create(&token).Error)
			ch := model.Channel{Name: "test-cline", Type: constant.ChannelTypeCline, Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&ch).Error)
			upstream, received := newClineUpstream(t, func(w http.ResponseWriter, r *http.Request, body []byte) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: "+tc.first+"\n\ndata: "+`{"error":{"type":"invalid_request_error","message":"user message must have content"}}`+"\n\n")
			})
			env := newClineTestEnv(t, types.RelayFormatOpenAIResponses, relayconstant.RelayModeResponses, true, "test-model", upstream.URL)
			common.SetContextKey(env.context, constant.ContextKeyChannelId, ch.Id)
			info := env.info
			info.UserId, info.TokenId, info.TokenKey = user.Id, token.Id, token.Key
			info.RequestId = tc.name
			info.Request = newResponsesStreamRequest("test-model")
			info.UserSetting = dto.UserSetting{BillingPreference: "wallet_only"}
			info.UsingGroup, info.UserGroup, info.ForcePreConsume = "default", "default", true
			info.PriceData = hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}}
			info.SetEstimatePromptTokens(30308)
			require.Nil(t, service.PreConsumeBilling(env.context, 1000, info))
			require.NoError(t, db.First(&user, user.Id).Error)
			assert.Equal(t, start-1000, user.QuotaPaid)
			apiErr := relay.ResponsesHelper(env.context, info)
			require.NotNil(t, apiErr)
			assert.False(t, service.ShouldRetryRelayError(env.context, apiErr, 2))
			assert.Equal(t, tc.want == 0, info.Billing.NeedsRefund())
			refunded := make(chan struct{}, 1)
			const callback = "cline_refund_observed"
			require.NoError(t, db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "tokens" && tx.Error == nil {
					select {
					case refunded <- struct{}{}:
					default:
					}
				}
			}))
			t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
			// Concurrent cleanup and a later replay must refund the same reserve once.
			var wg sync.WaitGroup
			wg.Go(func() { info.Billing.Refund(env.context) })
			wg.Go(func() { info.Billing.Refund(env.context) })
			wg.Wait()
			if tc.want == 0 {
				select {
				case <-refunded:
				case <-time.After(5 * time.Second):
					t.Fatal("refund did not finish")
				}
			}
			info.Billing.Refund(env.context)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			assert.Equal(t, start-tc.want, user.QuotaPaid)
			assert.Equal(t, start-tc.want, user.Quota)
			assert.Zero(t, user.QuotaBonus)
			assert.Equal(t, start-tc.want, token.RemainQuota)
			assert.Equal(t, tc.want, token.UsedQuota)
			var ledger []model.BalanceLedger
			require.NoError(t, db.Where("user_id = ?", user.Id).Order("id").Find(&ledger).Error)
			require.Len(t, ledger, 2)
			assert.Equal(t, -1000, ledger[0].Delta)
			assert.Equal(t, 1000-tc.want, ledger[1].Delta)
			assert.Equal(t, "paid", ledger[0].Account)
			assert.Equal(t, "paid", ledger[1].Account)
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeConsume).Find(&logs).Error)
			if tc.want == 0 {
				assert.Empty(t, logs)
			} else {
				require.Len(t, logs, 1)
				assert.Equal(t, tc.want, logs[0].Quota)
			}
			assert.Len(t, received, 1)
		})
	}
}
