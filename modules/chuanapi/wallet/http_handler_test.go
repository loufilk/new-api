package wallet

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupWalletTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	var db *gorm.DB
	var err error
	if configured := os.Getenv("TEST_AGENT_POSTGRES_DSN"); configured != "" {
		parsed, parseErr := url.Parse(configured)
		require.NoError(t, parseErr)
		require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback(), "wallet fixture must be disposable loopback PostgreSQL")
		admin, openErr := gorm.Open(postgres.Open(configured), &gorm.Config{})
		require.NoError(t, openErr)
		schema := fmt.Sprintf("wallet_http_%d", time.Now().UnixNano())
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		db, err = gorm.Open(postgres.Open(parsed.String()), &gorm.Config{})
		require.NoError(t, err)
		common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			sqlDB, e := admin.DB()
			if e == nil {
				_ = sqlDB.Close()
			}
		})
	} else {
		common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
		dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
		db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
	}

	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.BalanceLedgerOperation{},
		&model.BalanceLedger{},
		&model.ReferralCommission{},
		&model.Log{},
		&model.AuthFlow{}, &model.UserSession{}, &model.UserAccessToken{}, &model.TwoFA{}, &model.PasskeyCredential{},
	))
	require.NoError(t, db.AutoMigrate(model.AgentPlatformModels()...))

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func confirmPaymentComplianceForTest(t *testing.T) {
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

func performWalletRequest(t *testing.T, method, path, body string, userId, role int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", userId)
	c.Set("role", role)
	var user model.User
	if model.DB.First(&user, userId).Error == nil {
		sessionID := fmt.Sprintf("wallet-http-session-%d", userId)
		var count int64
		require.NoError(t, model.DB.Model(&model.UserSession{}).Where("sid = ?", sessionID).Count(&count).Error)
		if count == 0 {
			require.NoError(t, model.DB.Create(&model.UserSession{SID: sessionID, UserID: userId, Version: 1, UserAuthVersion: user.AuthVersion, Status: model.UserSessionStatusActive, ExpiresAt: common.GetTimestamp() + 3600, RefreshHash: "synthetic-wallet", LoginMethod: "password"}).Error)
			require.NoError(t, model.DB.Create(&model.TwoFA{UserId: userId, IsEnabled: true, Secret: "synthetic-wallet-not-real-secret"}).Error)
		}
		c.Set("session_id", sessionID)
		c.Set("auth_version", user.AuthVersion)
		c.Set("session_version", int64(1))
	}
	return c, recorder
}

func TestGetWalletSummaryAndLedgerExposesRewardAccount(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "wallet-summary-user", Password: "password", AffCode: "ws-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
		Quota: 1700, QuotaPaid: 1000, QuotaBonus: 300, QuotaReward: 400,
	}
	require.NoError(t, db.Create(&user).Error)
	other := model.User{
		Username: "wallet-summary-other", Password: "password", AffCode: "ws-aff-2", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", Quota: 9000, QuotaPaid: 9000,
	}
	require.NoError(t, db.Create(&other).Error)

	now := common.GetTimestamp()
	require.NoError(t, db.Create(&model.BalanceLedgerOperation{
		IdempotencyKey: "wallet-test-op-1", UserId: user.Id, Operation: "credit",
		OperationSignature: "s1", TotalBefore: 500, TotalAfter: 1000, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: 1, UserId: user.Id, Account: "paid", Delta: 500,
		BalanceBefore: 500, BalanceAfter: 1000, TotalBefore: 500, TotalAfter: 1000,
		Reason: "topup", ReferenceType: "topup", ReferenceId: "TP-1",
		IdempotencyKey: "wallet-test-op-1", CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedgerOperation{
		IdempotencyKey: "wallet-test-op-reward", UserId: user.Id, Operation: "credit",
		OperationSignature: "reward", TotalBefore: 1000, TotalAfter: 1400, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: 2, UserId: user.Id, Account: "reward", Delta: 400,
		BalanceBefore: 0, BalanceAfter: 400, TotalBefore: 1000, TotalAfter: 1400,
		Reason: "referral_transfer", ReferenceType: "aff_transfer", ReferenceId: "key-1",
		IdempotencyKey: "wallet-test-op-reward", CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedgerOperation{
		IdempotencyKey: "wallet-test-op-legacy-affiliate", UserId: user.Id, Operation: "credit",
		OperationSignature: "legacy", TotalBefore: 1400, TotalAfter: 1600, CreatedAt: now,
	}).Error)
	require.NoError(t, db.Create(&model.BalanceLedger{
		OperationId: 3, UserId: user.Id, Account: "affiliate", Delta: 200,
		BalanceBefore: 0, BalanceAfter: 200, TotalBefore: 1400, TotalAfter: 1600,
		Reason: "legacy_referral", ReferenceType: "legacy", ReferenceId: "AFF-OLD",
		IdempotencyKey: "wallet-test-op-legacy-affiliate", CreatedAt: now,
	}).Error)

	c, recorder := performWalletRequest(t, http.MethodGet, "/api/user/wallet", "", user.Id, common.RoleCommonUser)
	GetWalletSummary(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"total":1700`)
	assert.Contains(t, body, `"paid":1000`)
	assert.Contains(t, body, `"bonus":300`)
	assert.Contains(t, body, `"reward":400`)
	assert.NotContains(t, body, `"affiliate"`)
	assert.Contains(t, body, `"referral_bonus_rate":0.05`)
	assert.NotContains(t, body, "freeze_days")

	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/wallet/ledger?p=1&page_size=10", "", user.Id, common.RoleCommonUser)
	GetWalletLedger(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body = recorder.Body.String()
	assert.Contains(t, body, `"total":2`)
	assert.Contains(t, body, `"reference":"TP-1"`)
	assert.Contains(t, body, `"reference":"key-1"`)
	assert.NotContains(t, body, "AFF-OLD")
	assert.NotContains(t, body, "TP-2")

	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/wallet/ledger?account=affiliate", "", user.Id, common.RoleCommonUser)
	GetWalletLedger(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "account")
}

func TestGetReferralRewardsMasksSourceAndScopesToCurrentUser(t *testing.T) {
	db := setupWalletTestDB(t)
	inviter := model.User{
		Username: "referral-inviter", Password: "password", AffCode: "rr-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&inviter).Error)
	sourceA := model.User{
		Username: "alice", Password: "password", AffCode: "rr-aff-2", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", InviterId: inviter.Id,
	}
	require.NoError(t, db.Create(&sourceA).Error)
	sourceB := model.User{
		Username: "bob", Password: "password", AffCode: "rr-aff-3", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", InviterId: inviter.Id,
	}
	require.NoError(t, db.Create(&sourceB).Error)
	other := model.User{
		Username: "other-inviter", Password: "password", AffCode: "rr-aff-4", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default",
	}
	require.NoError(t, db.Create(&other).Error)

	now := common.GetTimestamp()
	rows := []model.ReferralCommission{
		{UserId: inviter.Id, SourceUserId: sourceA.Id, ReferenceType: "topup", ReferenceId: "REF-A", PaidQuota: 1000000, RewardQuota: 50000, Rate: 0.05, CreatedAt: now},
		{UserId: inviter.Id, SourceUserId: sourceB.Id, ReferenceType: "topup", ReferenceId: "REF-B", PaidQuota: 2000000, RewardQuota: 100000, Rate: 0.05, CreatedAt: now + 1},
		{UserId: other.Id, SourceUserId: sourceB.Id, ReferenceType: "topup", ReferenceId: "REF-C", PaidQuota: 3000000, RewardQuota: 150000, Rate: 0.05, CreatedAt: now + 2},
	}
	require.NoError(t, db.Create(&rows).Error)

	c, recorder := performWalletRequest(t, http.MethodGet, "/api/user/referral/rewards?p=1&page_size=1", "", inviter.Id, common.RoleCommonUser)
	GetReferralRewards(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()
	assert.Contains(t, body, `"total":2`)
	assert.Contains(t, body, `"source_name":"b***"`)
	assert.Contains(t, body, `"reward_quota":100000`)
	assert.NotContains(t, body, `alice`)
	assert.NotContains(t, body, `REF-C`)

	c, recorder = performWalletRequest(t, http.MethodGet, "/api/user/referral/rewards?p=2&page_size=1", "", inviter.Id, common.RoleCommonUser)
	GetReferralRewards(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	body = recorder.Body.String()
	assert.Contains(t, body, `"source_name":"a***"`)
	assert.NotContains(t, body, "bob")
	assert.NotContains(t, body, "alice")
}

func TestTransferAffQuotaRequiresComplianceAndIdempotencyKey(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "transfer-guard-user", Password: "password", AffCode: "tg-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffQuota: 1000000, AffHistoryQuota: 1000000,
	}
	require.NoError(t, db.Create(&user).Error)

	// 未确认合规：直接拒绝，余额不动。
	c, recorder := performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, user.Id, common.RoleCommonUser)
	TransferAffQuota(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 1000000, user.AffQuota)
	assert.Zero(t, user.QuotaReward)

	confirmPaymentComplianceForTest(t)

	// 缺少 Idempotency-Key：拒绝。
	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, user.Id, common.RoleCommonUser)
	TransferAffQuota(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "Idempotency-Key")
}

func TestTransferAffQuotaValidatesMinimumAndUnknownFields(t *testing.T) {
	db := setupWalletTestDB(t)
	confirmPaymentComplianceForTest(t)
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })

	user := model.User{
		Username: "transfer-min-user", Password: "password", AffCode: "tm-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffQuota: 1000000, AffHistoryQuota: 1000000,
	}
	require.NoError(t, db.Create(&user).Error)

	c, recorder := performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":499999}`, user.Id, common.RoleCommonUser)
	c.Request.Header.Set("Idempotency-Key", "min-key")
	TransferAffQuota(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "transfer_quota_minimum")

	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000,"extra":1}`, user.Id, common.RoleCommonUser)
	c.Request.Header.Set("Idempotency-Key", "strict-key")
	TransferAffQuota(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 1000000, user.AffQuota)
	assert.Zero(t, user.QuotaReward)
}

func TestTransferAffQuotaMovesRewardOnceAndReplaysByKey(t *testing.T) {
	db := setupWalletTestDB(t)
	confirmPaymentComplianceForTest(t)
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })

	user := model.User{
		Username: "transfer-user", Password: "password", AffCode: "tr-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffQuota: 1000000, AffHistoryQuota: 1000000,
	}
	require.NoError(t, db.Create(&user).Error)

	c, recorder := performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, user.Id, common.RoleCommonUser)
	c.Request.Header.Set("Idempotency-Key", "transfer-key-1")
	authorizeWalletTransfer(t, c)
	TransferAffQuota(c)
	require.Contains(t, recorder.Body.String(), `"success":true`)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 500000, user.AffQuota, "划转后待使用收益减少")
	assert.Equal(t, 500000, user.QuotaReward, "划转后 reward 增加")
	assert.Equal(t, 1000000, user.AffHistoryQuota, "累计收益不因划转减少")
	assert.Equal(t, 500000, user.Quota, "quota 镜像 = paid + bonus + reward")

	// 同键同金额重放：返回原结果，不重复扣加。
	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, user.Id, common.RoleCommonUser)
	c.Request.Header.Set("Idempotency-Key", "transfer-key-1")
	authorizeWalletTransfer(t, c)
	TransferAffQuota(c)
	require.Contains(t, recorder.Body.String(), `"success":true`)
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 500000, user.AffQuota)
	assert.Equal(t, 500000, user.QuotaReward)

	var ledgerRows int64
	require.NoError(t, db.Model(&model.BalanceLedger{}).Where("user_id = ? AND reason = ?", user.Id, model.LedgerReasonReferralTransfer).Count(&ledgerRows).Error)
	assert.EqualValues(t, 1, ledgerRows)

	// 同键不同金额：明确冲突失败，不再扣加。
	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":600000}`, user.Id, common.RoleCommonUser)
	c.Request.Header.Set("Idempotency-Key", "transfer-key-1")
	authorizeWalletTransfer(t, c)
	TransferAffQuota(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "不一致")
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 500000, user.AffQuota)
	assert.Equal(t, 500000, user.QuotaReward)

	// 超出待使用收益：拒绝且余额不动。
	c, recorder = performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":600000}`, user.Id, common.RoleCommonUser)
	c.Request.Header.Set("Idempotency-Key", "transfer-key-2")
	authorizeWalletTransfer(t, c)
	TransferAffQuota(c)
	assert.Contains(t, recorder.Body.String(), `"success":false`)
	assert.Contains(t, recorder.Body.String(), "不足")
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 500000, user.AffQuota)
	assert.Equal(t, 500000, user.QuotaReward)
}

func TestTransferAffQuotaScopesIdempotencyKeyToUser(t *testing.T) {
	db := setupWalletTestDB(t)
	confirmPaymentComplianceForTest(t)
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })

	first := model.User{
		Username: "transfer-scope-a", Password: "password", AffCode: "ts-aff-1", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffQuota: 500000, AffHistoryQuota: 500000,
	}
	require.NoError(t, db.Create(&first).Error)
	second := model.User{
		Username: "transfer-scope-b", Password: "password", AffCode: "ts-aff-2", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffQuota: 500000, AffHistoryQuota: 500000,
	}
	require.NoError(t, db.Create(&second).Error)

	for _, user := range []model.User{first, second} {
		c, recorder := performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, user.Id, common.RoleCommonUser)
		c.Request.Header.Set("Idempotency-Key", "shared-key")
		authorizeWalletTransfer(t, c)
		TransferAffQuota(c)
		require.Contains(t, recorder.Body.String(), `"success":true`)
	}

	require.NoError(t, db.First(&first, first.Id).Error)
	require.NoError(t, db.First(&second, second.Id).Error)
	assert.Equal(t, 0, first.AffQuota)
	assert.Equal(t, 500000, first.QuotaReward)
	assert.Equal(t, 0, second.AffQuota)
	assert.Equal(t, 500000, second.QuotaReward)
}

func authorizeWalletTransfer(t *testing.T, c *gin.Context) {
	t.Helper()
	data, err := io.ReadAll(c.Request.Body)
	require.NoError(t, err)
	c.Request.Body = io.NopCloser(bytes.NewReader(data))
	var input struct {
		Quota int `json:"quota"`
	}
	require.NoError(t, common.Unmarshal(data, &input))
	identity := model.AuthSessionIdentity{UserID: c.GetInt("id"), SessionID: c.GetString("session_id"), UserAuthVersion: c.GetInt64("auth_version"), SessionVersion: c.GetInt64("session_version")}
	request := model.AgentLegacyCommissionRequest{RequestID: c.GetHeader("Idempotency-Key"), Quota: input.Quota}
	payload, err := common.Marshal(request)
	require.NoError(t, err)
	prepared, err := model.PrepareAgentCommissionOperation(identity, "commission.convert.legacy", request.RequestID, payload)
	require.NoError(t, err)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	c.Request.Header.Set("X-Security-Proof", proof)
}

func TestTransferAffQuotaBrowserProofRequiredPostgres(t *testing.T) {
	if os.Getenv("TEST_AGENT_POSTGRES_DSN") == "" {
		t.Skip("opt-in PostgreSQL fixture; acceptance runner must configure TEST_AGENT_POSTGRES_DSN and require zero skips")
	}
	db := setupWalletTestDB(t)
	confirmPaymentComplianceForTest(t)
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	user := model.User{Username: "legacy-financial-browser", AffCode: "legacy-financial-browser", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffQuota: 1000000, AuthVersion: 1}
	require.NoError(t, db.Create(&user).Error)
	for _, kind := range []string{"no_proof", "pat", "wrong_context_type"} {
		c, response := performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, user.Id, user.Role)
		c.Request.Header.Set("Idempotency-Key", "legacy-proof-"+kind)
		if kind == "pat" {
			c.Set("session_id", model.AccessTokenSessionID(99))
			c.Set("use_access_token", true)
		}
		if kind == "wrong_context_type" {
			c.Set("session_version", "1")
		}
		TransferAffQuota(c)
		assert.Equal(t, http.StatusForbidden, response.Code, kind+": "+response.Body.String())
	}
	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 1000000, user.AffQuota)
	assert.Zero(t, user.QuotaReward)
	c, response := performWalletRequest(t, http.MethodPost, "/api/user/aff_transfer", `{"quota":500000}`, user.Id, user.Role)
	c.Request.Header.Set("Idempotency-Key", "x.1")
	authorizeWalletTransfer(t, c)
	TransferAffQuota(c)
	require.Contains(t, response.Body.String(), `"success":true`)
}
