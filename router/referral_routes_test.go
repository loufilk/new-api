package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	walletroutes "github.com/QuantumNous/new-api/modules/chuanapi/wallet/routes"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 本文件通过真实路由与中间件（SetApiRouter + 扩展路由注册）验证划转/返佣
// 接口的认证、越权、输入与幂等契约，而不是手工注入 c.Set("id")。

func setupReferralRouteTest(t *testing.T) (*gin.Engine, *gorm.DB) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousCriticalRateLimit := common.CriticalRateLimitEnable
	previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()

	common.RedisEnabled = false
	common.CriticalRateLimitEnable = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)

	name := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", name)), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{},
		&model.ReferralCommission{},
		&model.Log{},
		&model.AuditLog{},
		&model.UserAccessToken{},
	))

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.CriticalRateLimitEnable = previousCriticalRateLimit
		common.SetDatabaseTypes(previousMainType, previousLogType)
		_ = sqlDB.Close()
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	var scopes extension.RouteScopes
	require.NotPanics(t, func() { scopes = SetApiRouter(engine) })
	registry := extension.NewRegistry()
	require.NoError(t, walletroutes.Register(registry))
	require.NoError(t, registry.Freeze())
	require.NoError(t, registry.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))
	return engine, db
}

func confirmReferralRouteCompliance(t *testing.T) {
	t.Helper()
	paymentSetting := operation_setting.GetPaymentSetting()
	previousConfirmed, previousVersion := paymentSetting.ComplianceConfirmed, paymentSetting.ComplianceTermsVersion
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = previousConfirmed
		paymentSetting.ComplianceTermsVersion = previousVersion
	})
}

func seedReferralRouteUser(t *testing.T, db *gorm.DB, id int, token string, affQuota int, status int) model.User {
	t.Helper()
	user := model.User{
		Id: id, Username: fmt.Sprintf("route-user-%d", id), Password: "password",
		AffCode: fmt.Sprintf("route-aff-%d", id), Role: common.RoleCommonUser, Status: status,
		Group: "default", AffQuota: affQuota, AffHistoryQuota: affQuota,
	}
	require.NoError(t, db.Create(&user).Error)
	if token != "" {
		seedRouteScopedToken(t, db, user.Id, token, "wallet:read", "wallet:write")
	}
	return user
}

type routeHeader struct {
	key   string
	value string
}

func performReferralRouteRequest(engine *gin.Engine, method, path, body, token string, headers ...routeHeader) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for _, header := range headers {
		request.Header.Set(header.key, header.value)
	}
	engine.ServeHTTP(recorder, request)
	return recorder
}

func countReferralRouteLedgerRows(t *testing.T, db *gorm.DB, userId int) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&model.BalanceLedger{}).
		Where("user_id = ? AND reason = ?", userId, model.LedgerReasonReferralTransfer).
		Count(&count).Error)
	return count
}

// 未登录、无效与已撤销凭据都必须被真实 UserAuth 中间件拒绝。
func TestReferralRoutesRequireLiveAuthentication(t *testing.T) {
	engine, db := setupReferralRouteTest(t)
	confirmReferralRouteCompliance(t)
	activeToken := model.AccessTokenPrefix + strings.Repeat("a", 32)
	disabledToken := model.AccessTokenPrefix + strings.Repeat("b", 32)
	seedReferralRouteUser(t, db, 701, activeToken, 1_000_000, common.UserStatusEnabled)
	seedReferralRouteUser(t, db, 702, disabledToken, 1_000_000, common.UserStatusDisabled)

	keyHeader := routeHeader{key: "Idempotency-Key", value: "route-auth-1"}

	// 未登录
	response := performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, "", keyHeader)
	assert.Equal(t, http.StatusUnauthorized, response.Code)
	response = performReferralRouteRequest(engine, http.MethodGet, "/api/user/referral/rewards", "", "")
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	// 无效凭据
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`,
		strings.Repeat("f", 32), keyHeader)
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	// Positive control proves the credential was usable before revocation.
	response = performReferralRouteRequest(engine, http.MethodGet, "/api/user/referral/rewards", "", activeToken)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	// Deleting a scoped PAT immediately revokes the previously valid credential.
	require.NoError(t, db.Where("token_hash = ?", model.AccessTokenFingerprint(activeToken)).Delete(&model.UserAccessToken{}).Error)
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, activeToken, keyHeader)
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	// 禁用用户即使持有有效凭据也不得访问
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, disabledToken, keyHeader)
	assert.Equal(t, http.StatusUnauthorized, response.Code)

	assert.Zero(t, countReferralRouteLedgerRows(t, db, 701), "认证失败不得产生任何资金变动")
}

// 返佣列表必须只看认证用户本人：伪造 user_id 查询参数不得越权读取他人记录。
func TestReferralRewardsRouteScopesToAuthenticatedUser(t *testing.T) {
	engine, db := setupReferralRouteTest(t)
	tokenA := model.AccessTokenPrefix + strings.Repeat("c", 32)
	tokenB := model.AccessTokenPrefix + strings.Repeat("d", 32)
	userA := seedReferralRouteUser(t, db, 703, tokenA, 0, common.UserStatusEnabled)
	userB := seedReferralRouteUser(t, db, 704, tokenB, 0, common.UserStatusEnabled)
	seedReferralRouteUser(t, db, 705, "", 0, common.UserStatusEnabled)

	now := common.GetTimestamp()
	require.NoError(t, db.Create(&model.ReferralCommission{
		UserId: userA.Id, SourceUserId: 705, ReferenceType: "topup", ReferenceId: "ROUTE-A",
		PaidQuota: 1_000_000, RewardQuota: 50_000, Rate: 0.05, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.ReferralCommission{
		UserId: userB.Id, SourceUserId: 705, ReferenceType: "topup", ReferenceId: "ROUTE-B",
		PaidQuota: 2_000_000, RewardQuota: 100_000, Rate: 0.05, CreatedAt: now + 1,
	}).Error)

	// A 的凭据 + 伪造 user_id=B：只能看到 A 的记录。
	response := performReferralRouteRequest(engine, http.MethodGet,
		fmt.Sprintf("/api/user/referral/rewards?user_id=%d&p=1&page_size=10", userB.Id), "", tokenA)
	require.Equal(t, http.StatusOK, response.Code)
	body := response.Body.String()
	assert.Contains(t, body, `"total":1`)
	assert.Contains(t, body, `"reward_quota":50000`)
	assert.NotContains(t, body, `"reward_quota":100000`)

	// 超大页码明确失败，而不是回绕成负偏移。
	response = performReferralRouteRequest(engine, http.MethodGet, "/api/user/referral/rewards?p=99999999", "", tokenA)
	assert.Contains(t, response.Body.String(), `"success":false`)
}

// 真实路由下的输入校验：缺少/非法幂等键、严格 JSON、有界请求体与越额。
func TestReferralTransferRouteValidatesInput(t *testing.T) {
	engine, db := setupReferralRouteTest(t)
	confirmReferralRouteCompliance(t)
	token := model.AccessTokenPrefix + strings.Repeat("e", 32)
	seedReferralRouteUser(t, db, 706, token, 1_000_000, common.UserStatusEnabled)

	// 缺少 Idempotency-Key
	response := performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, token)
	assert.Contains(t, response.Body.String(), `"success":false`)
	assert.Contains(t, response.Body.String(), "Idempotency-Key")

	// 非法字符幂等键
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, token,
		routeHeader{key: "Idempotency-Key", value: "bad key with spaces"})
	assert.Contains(t, response.Body.String(), `"success":false`)

	// 未知字段：严格 JSON 拒绝
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000,"user_id":707}`, token,
		routeHeader{key: "Idempotency-Key", value: "route-strict"})
	assert.Contains(t, response.Body.String(), `"success":false`)

	// 有界读取：超大请求体被拒绝
	oversizedBody := `{"quota":500000}` + strings.Repeat(" ", 8*1024)
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", oversizedBody, token,
		routeHeader{key: "Idempotency-Key", value: "route-oversized"})
	assert.Contains(t, response.Body.String(), `"success":false`)

	// 超过钱包上限
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer",
		fmt.Sprintf(`{"quota":%d}`, common.MaxWalletQuota+1), token,
		routeHeader{key: "Idempotency-Key", value: "route-over-max"})
	assert.Contains(t, response.Body.String(), "上限")

	// 超过待使用收益
	response = performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":1500000}`, token,
		routeHeader{key: "Idempotency-Key", value: "route-over-balance"})
	assert.Contains(t, response.Body.String(), "不足")

	assert.Zero(t, countReferralRouteLedgerRows(t, db, 706), "校验失败不得产生账本")
}

// 真实路由下的重放/冲突：同键同额只入账一次，同键异额明确冲突。
func TestReferralTransferRouteReplaysSameKeyOnce(t *testing.T) {
	engine, db := setupReferralRouteTest(t)
	confirmReferralRouteCompliance(t)
	token := model.AccessTokenPrefix + strings.Repeat("g", 32)
	seedReferralRouteUser(t, db, 708, token, 1_000_000, common.UserStatusEnabled)

	keyHeader := routeHeader{key: "Idempotency-Key", value: "route-replay-key"}

	first := performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, token, keyHeader)
	require.Contains(t, first.Body.String(), `"success":true`)

	replay := performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, token, keyHeader)
	require.Contains(t, replay.Body.String(), `"success":true`)
	require.NoError(t, db.First(&model.User{}, 708).Error)
	var user model.User
	require.NoError(t, db.Where("id = ?", 708).First(&user).Error)
	assert.Equal(t, 500_000, user.AffQuota, "重放不得重复扣减")
	assert.Equal(t, 500_000, user.QuotaReward, "重放不得重复入账")
	assert.EqualValues(t, 1, countReferralRouteLedgerRows(t, db, 708))

	conflict := performReferralRouteRequest(engine, http.MethodPost, "/api/user/aff_transfer", `{"quota":600000}`, token, keyHeader)
	require.Contains(t, conflict.Body.String(), `"success":false`)
	assert.Contains(t, conflict.Body.String(), "不一致")
	require.NoError(t, db.Where("id = ?", 708).First(&user).Error)
	assert.Equal(t, 500_000, user.AffQuota)
	assert.EqualValues(t, 1, countReferralRouteLedgerRows(t, db, 708))
}

// CORS 必须允许浏览器发送 Idempotency-Key 预检头。
func TestReferralCORSAllowsIdempotencyKeyHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.CORS())
	engine.POST("/api/user/aff_transfer", func(c *gin.Context) { c.Status(http.StatusOK) })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/user/aff_transfer", nil)
	request.Header.Set("Origin", "http://localhost:3000")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "Idempotency-Key")
	engine.ServeHTTP(recorder, request)

	assert.Contains(t, recorder.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key")
}
