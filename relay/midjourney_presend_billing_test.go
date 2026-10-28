package relay

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/QuantumNous/new-api/pkg/walletport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestMidjourneyPreSendHoldPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL DSN required")
	}
	admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
	schema := "mj_hold_" + strings.ToLower(common.GetRandomString(12))
	require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error) })
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	config.RuntimeParams["search_path"] = schema
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*config)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Model{}, &model.Vendor{}, &model.Ability{}, &model.Option{}, &model.Midjourney{}, &model.BillingFinalization{}, &model.BalanceLedger{}, &model.BalanceLedgerOperation{}, &model.Log{}))
	oldDB, oldLog := model.DB, model.LOG_DB
	oldMain, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldBatch := common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = false, false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.SetDatabaseTypes(oldMain, oldLogType)
		common.RedisEnabled, common.MemoryCacheEnabled, common.BatchUpdateEnabled = oldRedis, oldMemory, oldBatch
		model.InvalidateModelBonusEligibleCache()
	})
	if _, err := walletport.Default(); errors.Is(err, walletport.ErrNotConfigured) {
		require.NoError(t, walletmodule.RegisterRuntime())
	} else {
		require.NoError(t, err)
	}
	factories := moneyport.NewFactoryRegistry()
	require.NoError(t, service.RegisterHostFundingFactories(factories))
	require.NoError(t, walletmodule.RegisterFundingFactory(factories))
	if err := service.ConfigureFundingFactoryRegistry(factories); err != nil {
		require.Contains(t, err.Error(), "already configured")
	}
	prices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices)) })
	// Preserve the production quota unit and configure an exact 100-unit test price.
	raw, err := common.Marshal(map[string]float64{"swap_face": 100 / common.QuotaPerUnit, "mj_imagine": 100 / common.QuotaPerUnit})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(string(raw)))
	service.InitHttpClient()
	for _, swap := range []bool{true, false} {
		for _, tc := range []struct {
			name               string
			paid, bonus, token int
			reject             bool
			response           string
			expectedState      string
		}{
			{"bonus_ineligible", 0, 500, 500, true, `{"code":1,"result":"accepted"}`, ""},
			{"token_shortage", 500, 0, 50, true, `{"code":1,"result":"accepted"}`, ""},
			{"holds_before_upstream", 500, 0, 500, false, `{"code":1,"result":"accepted"}`, model.BillingTaskOwned},
			{"accepted_existing", 500, 0, 500, false, `{"code":21,"result":"accepted-existing"}`, model.BillingTaskOwned},
			{"accepted_queued", 500, 0, 500, false, `{"code":22,"result":"accepted-queued"}`, model.BillingTaskOwned},
			{"queue_rejected", 500, 0, 500, false, `{"code":23,"description":"queue full"}`, model.BillingRefunded},
			{"unknown_upstream_body", 500, 0, 500, false, `{"code":5,"description":"unknown"}`, model.BillingDeliveryUnknown},
			{"malformed_response", 500, 0, 500, false, `{`, model.BillingDeliveryUnknown},
			{"already_failed", 500, 0, 500, false, `{"code":21,"result":"failed-task","properties":{"status":"FAILURE"}}`, model.BillingRefunded},
			{"unsent_invalid_url", 500, 0, 500, true, ``, model.BillingRefunded},
			{"unsent_unsupported_scheme", 500, 0, 500, true, ``, model.BillingRefunded},
			{"unsent_dial", 500, 0, 500, true, ``, model.BillingRefunded},
		} {
			t.Run(fmt.Sprintf("swap_%t/%s", swap, tc.name), func(t *testing.T) {
				if swap && (tc.name == "accepted_existing" || tc.name == "accepted_queued") {
					tc.expectedState = model.BillingSettled
				}
				modelName := "mj_imagine"
				if swap {
					modelName = "swap_face"
				}
				no := false
				require.NoError(t, db.Where("model_name = ?", modelName).Delete(&model.Model{}).Error)
				require.NoError(t, db.Create(&model.Model{ModelName: modelName, Status: 1, BonusEligible: &no}).Error)
				model.InvalidateModelBonusEligibleCache()
				identity := "mj-" + common.GetRandomString(12)
				user := model.User{Username: identity, AffCode: identity, QuotaPaid: tc.paid, QuotaBonus: tc.bonus, Quota: tc.paid + tc.bonus, Status: common.UserStatusEnabled, Group: "default"}
				require.NoError(t, db.Create(&user).Error)
				token := model.Token{UserId: user.Id, Key: identity, RemainQuota: tc.token, Status: common.TokenStatusEnabled}
				require.NoError(t, db.Create(&token).Error)
				var calls atomic.Int32
				held := make(chan [2]int, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					var u model.User
					var key model.Token
					if db.First(&u, user.Id).Error != nil || db.First(&key, token.Id).Error != nil {
						w.WriteHeader(500)
						return
					}
					held <- [2]int{user.Quota - u.Quota, token.RemainQuota - key.RemainQuota}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.response))
				}))
				t.Cleanup(server.Close)
				channel := model.Channel{Type: constant.ChannelTypeMidjourney, Name: identity, Status: common.ChannelStatusEnabled, Key: "synthetic-key", BaseURL: &server.URL}
				require.NoError(t, db.Create(&channel).Error)
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				body, path := `{"prompt":"synthetic prompt"}`, "/mj/submit/imagine"
				if swap {
					body, path = `{"sourceBase64":"source","targetBase64":"target"}`, "/mj/insight-face/swap"
				}
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				c.Set("channel_id", channel.Id)
				c.Set("channel_type", channel.Type)
				c.Set("channel_key", channel.Key)
				baseURL := server.URL
				if tc.name == "unsent_invalid_url" {
					baseURL = "://bad"
				}
				if tc.name == "unsent_unsupported_scheme" {
					baseURL = "ftp://upstream.invalid"
				}
				if tc.name == "unsent_dial" {
					server.Close()
				}
				c.Set("base_url", baseURL)
				c.Set("token_quota", tc.token)
				info := &relaycommon.RelayInfo{UserId: user.Id, TokenId: token.Id, TokenKey: token.Key, RequestId: identity, OriginModelName: modelName, UserGroup: "default", UsingGroup: "default", RelayMode: relayconstant.RelayModeMidjourneyImagine, StartTime: time.Now()}
				info.UserSetting.QuotaWarningEnabled = &no
				if swap {
					RelaySwapFace(c, info)
				} else {
					RelayMidjourneySubmit(c, info)
				}
				require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
				if tc.reject {
					assert.Zero(t, calls.Load(), "an unfunded request must never reach upstream")
				} else {
					require.EqualValues(t, 1, calls.Load())
					assert.Equal(t, [2]int{100, 100}, <-held, "the upstream must observe committed wallet + token holds")
				}
				var u model.User
				var key model.Token
				require.NoError(t, db.First(&u, user.Id).Error)
				require.NoError(t, db.First(&key, token.Id).Error)
				wantHeld := 100
				if tc.reject || tc.expectedState == model.BillingRefunded || tc.expectedState == model.BillingSettled {
					wantHeld = 0
				}
				assert.Equal(t, user.Quota-wantHeld, u.Quota)
				assert.Equal(t, tc.bonus, u.QuotaBonus)
				assert.Equal(t, tc.token-wantHeld, key.RemainQuota)
				if tc.expectedState == model.BillingTaskOwned {
					assert.Equal(t, 100, u.UsedQuota)
					assert.Equal(t, 1, u.RequestCount)
				} else {
					assert.Zero(t, u.UsedQuota)
					assert.Zero(t, u.RequestCount)
				}
				if tc.expectedState != "" {
					var owner model.BillingFinalization
					require.NoError(t, db.First(&owner, "request_id = ?", identity).Error)
					assert.Equal(t, tc.expectedState, owner.State)
					if tc.expectedState == model.BillingSettled {
						assert.Zero(t, owner.ActualQuota)
					}
				}
			})
		}
	}
}
