package helper

import (
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestPersonalGroupPricingPostgresQuoteAndRetrySnapshot(t *testing.T) {
	dsn := os.Getenv("TEST_PRICING_HELPER_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18.6 TEST_PRICING_HELPER_POSTGRES_DSN")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	require.NoError(t, db.AutoMigrate(&model.UserGroupPricing{}))
	previousDB := model.DB
	previous := config.GlobalConfig.ExportAllConfigs()
	prices, ratios := ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString()
	model.DB = db
	t.Cleanup(func() {
		model.DB = previousDB
		require.NoError(t, config.GlobalConfig.LoadFromDB(previous))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(ratios))
		connection, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, connection.Close())
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_ratio":       `{"quote-free":0.8,"quote-paid":2}`,
		"group_ratio_setting.group_group_ratio": `{"member":{"quote-free":0.7,"quote-paid":1.5}}`,
		"billing_setting.billing_mode":          `{"personal-quote-expr":"tiered_expr"}`,
		"billing_setting.billing_expr":          `{"personal-quote-expr":"tier(\"base\", p * 4)"}`,
	}))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"personal-quote-token":2}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"personal-quote-fixed":0.01}`))
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	operation_setting.GetQuotaSetting().PreConsumeMultiplier = 2
	for i, name := range []string{"personal-quote-token", "personal-quote-fixed", "personal-quote-expr"} {
		t.Run(name, func(t *testing.T) {
			userID := 99500 + i
			require.NoError(t, db.Create(&model.UserGroupPricing{UserID: userID, Revision: 1, RatiosJSON: `{"quote-free":0,"quote-paid":0.6}`}).Error)
			t.Cleanup(func() { require.NoError(t, db.Where("user_id = ?", userID).Delete(&model.UserGroupPricing{}).Error) })
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			info := &relaycommon.RelayInfo{UserId: userID, UserGroup: "member", UsingGroup: "quote-free", OriginModelName: name, BillingRequestInput: &billingexpr.RequestInput{}}
			price, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
			require.NoError(t, err)
			assert.True(t, price.FreeModel)
			assert.Zero(t, price.QuotaToPreConsume)
			if name == "personal-quote-token" {
				assert.Equal(t, 4000.0, price.QuotaBeforeGroup)
			}
			if name == "personal-quote-fixed" {
				assert.Equal(t, 5000.0, price.QuotaBeforeGroup)
			}
			if name == "personal-quote-expr" {
				assert.Equal(t, 4000.0, info.TieredBillingSnapshot.EstimatedQuotaBeforeGroup)
			}
			require.NoError(t, db.Model(&model.UserGroupPricing{}).Where("user_id = ?", userID).Updates(map[string]any{"revision": 2, "ratios_json": `{"quote-paid":0.9}`}).Error)
			ctx.Set("auto_group", "quote-paid")
			rate, err := HandleGroupRatio(ctx, info)
			require.NoError(t, err)
			assert.Equal(t, "quote-paid", info.UsingGroup)
			assert.Equal(t, 0.6, rate.GroupRatio)
			assert.Equal(t, "user", rate.Source)
			assert.EqualValues(t, 1, rate.UserPricingRevision)
		})
	}
}
