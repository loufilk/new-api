package service

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	relaytypes "github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func userGroupPricingServiceFixture(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_SERVICE_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 TEST_SERVICE_POSTGRES_DSN")
	}
	truncate(t)
	saved := config.GlobalConfig.ExportAllConfigs()
	savedPrices, savedRatios := ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString()
	savedUsable := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedRatios))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(savedUsable))
	})
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_ratio":       `{"free-a":0.8,"paid-b":1.8,"private":0.1}`,
		"group_ratio_setting.group_group_ratio": `{"member":{"free-a":0.7,"paid-b":0.7}}`,
		"billing_setting.billing_mode":          `{"personal-expr":"tiered_expr"}`,
		"billing_setting.billing_expr":          `{"personal-expr":"tier(\"base\", p * 4)"}`,
	}))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"personal-legacy":2}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"personal-fixed":0.01}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"free-a":"Free A","paid-b":"Paid B"}`))
	operation_setting.GetQuotaSetting().EnableFreeModelPreConsume = false
	operation_setting.GetQuotaSetting().PreConsumeMultiplier = 1
}

func TestUserGroupPricingPostgresPriceRoutingUsesPersonalSnapshot(t *testing.T) {
	userGroupPricingServiceFixture(t)
	seedUser(t, 99401, 500000)
	require.NoError(t, model.DB.Create(&model.User{Id: 99402, Username: "pricing-other-user", AffCode: "pricing-other", Quota: 500000, QuotaPaid: 500000, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, model.DB.Create(&model.UserGroupPricing{UserID: 99401, RatiosJSON: `{"paid-b":0.3,"private":0}`, Revision: 1}).Error)
	candidates := []model.RoutingCandidate{{Group: "free-a", Channel: &model.Channel{Id: 1}}, {Group: "paid-b", Channel: &model.Channel{Id: 2}}}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("id", 99401)
	common.SetContextKey(ctx, constant.ContextKeyUserGroup, "member")
	common.SetContextKey(ctx, constant.ContextKeyTokenRoutingStrategy, "price")
	selected, err := rankTokenRoutes(ctx, candidates, nil)
	require.NoError(t, err)
	assert.Equal(t, "paid-b", selected.Group)
	assert.False(t, IsUserSelectableGroup("member", "private"), "a private zero rate never grants calling permission")
	require.NoError(t, model.DB.Model(&model.UserGroupPricing{}).Where("user_id = ?", 99401).Updates(map[string]any{"ratios_json": `{"paid-b":1.5}`, "revision": 2}).Error)
	selected, err = rankTokenRoutes(ctx, candidates, nil)
	require.NoError(t, err)
	assert.Equal(t, "paid-b", selected.Group, "retries retain the admitted price map")
	for _, userID := range []int{99401, 99402} {
		next, _ := gin.CreateTestContext(httptest.NewRecorder())
		next.Set("id", userID)
		common.SetContextKey(next, constant.ContextKeyUserGroup, "member")
		common.SetContextKey(next, constant.ContextKeyTokenRoutingStrategy, "price")
		selected, err = rankTokenRoutes(next, candidates, nil)
		require.NoError(t, err)
		assert.Equal(t, "free-a", selected.Group, "new requests and another same-class user use their own policy")
	}
}

func TestUserGroupPricingPostgresFreeToPaidRetryReservesAndSettles(t *testing.T) {
	for _, tc := range []struct {
		name             string
		reserve, settled int
	}{
		{"personal-legacy", 1200, 600}, {"personal-fixed", 3000, 3000}, {"personal-expr", 1200, 600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			userGroupPricingServiceFixture(t)
			const userID = 99411
			seedUser(t, userID, 500000)
			seedBonusEligibleModel(t, tc.name, boolPtr(false))
			require.NoError(t, model.DB.Create(&model.UserGroupPricing{UserID: userID, RatiosJSON: `{"free-a":0,"paid-b":0.6}`, Revision: 1}).Error)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
			ctx.Set("id", userID)
			ctx.Set(common.RequestIdKey, tc.name+"-pricing-retry")
			info := &relaycommon.RelayInfo{UserId: userID, UserGroup: "member", UsingGroup: "free-a", RequestId: tc.name + "-pricing-retry", OriginModelName: tc.name, IsPlayground: true, ForcePreConsume: true, StartTime: time.Now(), UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}, BillingRequestInput: &billingexpr.RequestInput{}}
			policy, err := model.GetRequestUserGroupPricing(ctx, userID)
			require.NoError(t, err)
			price := types.PriceData{FreeModel: true, ModelRatio: 2, CompletionRatio: 1, QuotaBeforeGroup: 2000, HasQuotaBeforeGroup: true, GroupRatioInfo: policy.ResolveGroupRatio("member", "free-a")}
			if tc.name == "personal-fixed" {
				price.UsePrice, price.ModelPrice, price.QuotaBeforeGroup = true, 0.01, 5000
			}
			if tc.name == "personal-expr" {
				info.TieredBillingSnapshot = makeRelayInfo(`tier("base", p * 4)`, 0, 1000, 0).TieredBillingSnapshot
			}
			info.PriceData = price
			assert.True(t, price.FreeModel)
			assert.Zero(t, price.QuotaToPreConsume)
			assert.Nil(t, info.Billing)
			// Saving a new policy cannot reprice this request's later attempt.
			require.NoError(t, model.DB.Model(&model.UserGroupPricing{}).Where("user_id = ?", userID).Updates(map[string]any{"ratios_json": `{"free-a":0,"paid-b":0.9}`, "revision": 2}).Error)
			ctx.Set("auto_group", "paid-b")
			policy, err = model.GetRequestUserGroupPricing(ctx, userID)
			require.NoError(t, err)
			info.UsingGroup = "paid-b"
			info.PriceData.GroupRatioInfo = policy.ResolveGroupRatio("member", "paid-b")
			assert.Equal(t, 0.6, info.PriceData.GroupRatioInfo.GroupRatio)
			require.Nil(t, PrepareBillingForSelectedGroup(ctx, info))
			require.NotNil(t, info.Billing)
			assert.False(t, info.PriceData.FreeModel)
			assert.Equal(t, tc.reserve, info.FinalPreConsumedQuota)
			quota, err := model.GetUserQuota(userID, false)
			require.NoError(t, err)
			assert.Equal(t, 500000-tc.reserve, quota)
			summary := calculateTextQuotaSummary(ctx, info, &dto.Usage{PromptTokens: 500, TotalTokens: 500})
			actual := summary.Quota
			if ok, quota, _ := TryTieredSettle(info, billingexpr.TokenParams{P: 500, Len: 500}); ok {
				actual = quota
			}
			assert.Equal(t, tc.settled, actual)
			require.NoError(t, SettleBilling(ctx, info, actual))
			quota, err = model.GetUserQuota(userID, false)
			require.NoError(t, err)
			assert.Equal(t, 500000-tc.settled, quota)
			info.Billing.Refund(ctx)
			afterRefund, err := model.GetUserQuota(userID, false)
			require.NoError(t, err)
			assert.Equal(t, quota, afterRefund, "settled work cannot be refunded twice")
		})
	}
}

func TestUserGroupPricingPostgresPaidRetryRejectsBeforeSubmitAndRefunds(t *testing.T) {
	userGroupPricingServiceFixture(t)
	const userID = 99421
	seedUser(t, userID, 1000)
	seedBonusEligibleModel(t, "personal-legacy", boolPtr(false))
	require.NoError(t, model.DB.Create(&model.UserGroupPricing{UserID: userID, RatiosJSON: `{"free-a":0.2,"paid-b":0.8}`, Revision: 1}).Error)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx.Set(common.RequestIdKey, "personal-retry-insufficient")
	info := &relaycommon.RelayInfo{UserId: userID, UserGroup: "member", UsingGroup: "free-a", RequestId: "personal-retry-insufficient", OriginModelName: "personal-legacy", IsPlayground: true, ForcePreConsume: true, UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
	policy, err := model.GetRequestUserGroupPricing(ctx, userID)
	require.NoError(t, err)
	info.PriceData = types.PriceData{ModelRatio: 2, CompletionRatio: 1, QuotaBeforeGroup: 2000, HasQuotaBeforeGroup: true, GroupRatioInfo: policy.ResolveGroupRatio("member", "free-a")}
	require.Nil(t, PrepareBillingForSelectedGroup(ctx, info))
	assert.Equal(t, 400, info.FinalPreConsumedQuota)
	ctx.Set("auto_group", "paid-b")
	policy, err = model.GetRequestUserGroupPricing(ctx, userID)
	require.NoError(t, err)
	info.PriceData.GroupRatioInfo = policy.ResolveGroupRatio("member", "paid-b")
	require.NotNil(t, PrepareBillingForSelectedGroup(ctx, info), "1600 quota must be reserved before another upstream attempt")
	info.Billing.Refund(ctx)
	waitRefundCallback(t, nil)
	quota, err := model.GetUserQuota(userID, false)
	require.NoError(t, err)
	assert.Equal(t, 1000, quota)
}

func TestUserGroupPricingTaskCompletionUsesSubmittedRate(t *testing.T) {
	userGroupPricingServiceFixture(t)
	policy := &model.UserGroupPricing{Revision: 9, Ratios: map[string]float64{"paid-b": 0.3}}
	price := policy.ResolveGroupRatio("member", "paid-b")
	task := &model.Task{Group: "paid-b", PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{GroupRatio: price.GroupRatio, GroupRatioInfo: &price, OriginModelName: "personal-legacy"}}}
	policy.Ratios["paid-b"] = 0.9
	quota, _, clamp, ok := taskQuotaByTokens(task, 1000)
	require.True(t, ok)
	assert.Nil(t, clamp)
	assert.Equal(t, 600, quota)
	encoded, err := common.Marshal(taskBillingOther(task))
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"user_group_price_override":0.3`)
	assert.Contains(t, string(encoded), `"user_group_pricing_revision":9`)
}

func trustedUserGroupPricingRequest(t *testing.T, expression bool, balance int) (*gin.Context, *relaycommon.RelayInfo) {
	t.Helper()
	userGroupPricingServiceFixture(t)
	const userID = 99431
	seedUser(t, userID, balance)
	seedBonusEligibleModel(t, "personal-legacy", boolPtr(false))
	operation_setting.GetQuotaSetting().TrustQuotaUSD = 0.001
	require.NoError(t, model.DB.Create(&model.UserGroupPricing{UserID: userID, RatiosJSON: `{"free-a":0.2,"paid-b":0.8}`, Revision: 1}).Error)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	requestID := strings.TrimPrefix(t.Name(), "TestUserGroupPricing")
	ctx.Set(common.RequestIdKey, requestID)
	policy, err := model.GetRequestUserGroupPricing(ctx, userID)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{UserId: userID, UserGroup: "member", UsingGroup: "free-a", RequestId: requestID, OriginModelName: "personal-legacy", IsPlayground: true, TokenUnlimited: true, StartTime: time.Now(), UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningEnabled: boolPtr(false)}}
	info.PriceData = types.PriceData{ModelRatio: 2, CompletionRatio: 1, QuotaBeforeGroup: 2000, HasQuotaBeforeGroup: true, QuotaToPreConsume: 400, GroupRatioInfo: policy.ResolveGroupRatio("member", "free-a")}
	if expression {
		info.TieredBillingSnapshot = makeRelayInfo(`tier("base", p * 4)`, 0.2, 1000, 0).TieredBillingSnapshot
	}
	require.Nil(t, PreConsumeBilling(ctx, 400, info))
	require.Zero(t, info.Billing.GetPreConsumedQuota(), "initial estimate passed affordability without a hold")
	return ctx, info
}

func TestUserGroupPricingTrustedInitialQuoteKeepsZeroHold(t *testing.T) {
	for _, expression := range []bool{false, true} {
		name := "legacy"
		if expression {
			name = "expression"
		}
		t.Run(name, func(t *testing.T) {
			ctx, info := trustedUserGroupPricingRequest(t, expression, 1000)
			require.Nil(t, PrepareBillingForSelectedGroup(ctx, info))
			assert.Zero(t, info.Billing.GetPreConsumedQuota())
			info.Billing.Refund(ctx)
			waitRefundCallback(t, nil)
			quota, err := model.GetUserQuota(info.UserId, false)
			require.NoError(t, err)
			assert.Equal(t, 1000, quota)
		})
	}
}

func TestUserGroupPricingTrustedHigherFallbackRejectsBeforeSubmit(t *testing.T) {
	for _, expression := range []bool{false, true} {
		name := "legacy"
		if expression {
			name = "expression"
		}
		t.Run(name, func(t *testing.T) {
			ctx, info := trustedUserGroupPricingRequest(t, expression, 1000)
			policy, err := model.GetRequestUserGroupPricing(ctx, info.UserId)
			require.NoError(t, err)
			info.UsingGroup, info.PriceData.GroupRatioInfo = "paid-b", policy.ResolveGroupRatio("member", "paid-b")
			apiErr := PrepareBillingForSelectedGroup(ctx, info)
			require.NotNil(t, apiErr)
			assert.Equal(t, 403, apiErr.StatusCode)
			assert.Equal(t, relaytypes.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode())
			assert.True(t, relaytypes.IsSkipRetryError(apiErr))
			info.Billing.Refund(ctx)
			waitRefundCallback(t, nil)
			quota, err := model.GetUserQuota(info.UserId, false)
			require.NoError(t, err)
			assert.Equal(t, 1000, quota)
		})
	}
}

func TestUserGroupPricingTrustedHigherFallbackFinalizesOnce(t *testing.T) {
	for _, expression := range []bool{false, true} {
		name := "legacy"
		if expression {
			name = "expression"
		}
		for _, terminal := range []string{"settle", "refund"} {
			t.Run(name+"/"+terminal, func(t *testing.T) {
				ctx, info := trustedUserGroupPricingRequest(t, expression, 3000)
				policy, err := model.GetRequestUserGroupPricing(ctx, info.UserId)
				require.NoError(t, err)
				info.UsingGroup, info.PriceData.GroupRatioInfo = "paid-b", policy.ResolveGroupRatio("member", "paid-b")
				require.Nil(t, PrepareBillingForSelectedGroup(ctx, info))
				assert.Equal(t, 1600, info.Billing.GetPreConsumedQuota())
				quota, err := model.GetUserQuota(info.UserId, false)
				require.NoError(t, err)
				assert.Equal(t, 1400, quota)
				want := 3000
				if terminal == "settle" {
					actual := calculateTextQuotaSummary(ctx, info, &dto.Usage{PromptTokens: 500, TotalTokens: 500}).Quota
					if ok, result, _ := TryTieredSettle(info, billingexpr.TokenParams{P: 500, Len: 500}); ok {
						actual = result
					}
					assert.Equal(t, 800, actual)
					require.NoError(t, SettleBilling(ctx, info, actual))
					require.NoError(t, SettleBilling(ctx, info, actual))
					want = 2200
				}
				info.Billing.Refund(ctx)
				waitRefundCallback(t, nil)
				info.Billing.Refund(ctx)
				quota, err = model.GetUserQuota(info.UserId, false)
				require.NoError(t, err)
				assert.Equal(t, want, quota)
			})
		}
	}
}
