package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAgentRedemptionControllerTest(t *testing.T) []model.User {
	t.Helper()
	dsn := os.Getenv("TEST_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL 18.6 TEST_AGENT_POSTGRES_DSN")
	}
	db, _ := newAuditTestDatabase(t, "postgres", dsn)
	var version string
	require.NoError(t, db.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, "180006", version)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousPayment := common.RedisEnabled, *operation_setting.GetPaymentSetting()
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled = false
	operation_setting.GetPaymentSetting().ComplianceConfirmed = true
	operation_setting.GetPaymentSetting().ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.RedisEnabled = previousRedis
		*operation_setting.GetPaymentSetting() = previousPayment
	})
	require.NoError(t, db.AutoMigrate(append([]any{&model.User{}, &model.Redemption{}, &model.AgentRedemptionAccount{}, &model.AgentRedemptionLedger{}, &model.AgentRedemptionOperation{}, &model.AuditLog{}, &model.Log{}, &model.UserSession{}, &model.UserAccessToken{}, &model.AuthFlow{}, &model.TwoFA{}, &model.PasskeyCredential{}, &model.TopUp{}}, model.AgentPlatformModels()...)...))
	users := []model.User{
		{Username: "redemption-admin", Role: common.RoleAdminUser},
		{Username: "redemption-agent-a", Role: common.RoleAgentUser},
		{Username: "redemption-agent-b", Role: common.RoleAgentUser},
		{Username: "redemption-common", Role: common.RoleCommonUser},
	}
	for i := range users {
		users[i].Status = common.UserStatusEnabled
		users[i].AffCode = fmt.Sprintf("budget-api-%d", i)
		require.NoError(t, db.Create(&users[i]).Error)
		require.NoError(t, db.Create(&model.UserSession{SID: fmt.Sprintf("budget-controller-session-%d", users[i].Id), UserID: users[i].Id, Version: 1, UserAuthVersion: users[i].AuthVersion, Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64), LoginMethod: "password", ExpiresAt: common.GetTimestamp() + 3600}).Error)
	}

	for _, user := range users {
		require.NoError(t, db.Create(&model.TwoFA{UserId: user.Id, IsEnabled: true, Secret: "synthetic-fixture-only"}).Error)
	}
	for _, user := range users[1:3] {
		require.NoError(t, db.Create(&model.AgentProfile{UserID: user.Id, Status: model.AgentProfileActive, Version: 1, Reason: "fixture"}).Error)
	}
	admin := model.RedemptionScope{UserID: users[0].Id, Role: common.RoleAdminUser}
	for _, user := range users[1:3] {
		_, err := model.AllocateAgentRedemptionQuota(admin, model.AgentQuotaAllocationRequest{AgentUserID: user.Id, Delta: 1000, Reason: "controller fixture", RequestID: fmt.Sprintf("controller-initial-%d", user.Id)})
		require.NoError(t, err)
	}
	return users
}

func performAgentRedemptionRequest(t *testing.T, user model.User, method, target, body string, handler gin.HandlerFunc, id int, proof ...string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if len(proof) > 0 {
		c.Request.Header.Set("X-Security-Proof", proof[0])
	}
	c.Set("id", user.Id)
	c.Set("session_id", fmt.Sprintf("budget-controller-session-%d", user.Id))
	c.Set("auth_version", user.AuthVersion)
	c.Set("session_version", int64(1))
	// Forged or stale role context must never upgrade the persisted identity.
	c.Set("role", common.RoleRootUser)
	if id != 0 {
		c.Params = gin.Params{{Key: "id", Value: fmt.Sprint(id)}}
	}
	handler(c)
	return response
}

func TestAgentRedemptionAPIRejectsOwnerInjectionAndCrossTenantAccess(t *testing.T) {
	users := setupAgentRedemptionControllerTest(t)
	body := fmt.Sprintf(`{"name":"private","quota":100,"count":1,"request_id":"controller-create-own","user_id":%d,"budget_agent_id":%d,"status":3,"key":"injected","used_user_id":%d}`, users[2].Id, users[2].Id, users[2].Id)
	response := performAgentRedemptionRequest(t, users[1], http.MethodPost, "/api/redemption/", body, AddRedemption, 0)
	assert.Contains(t, response.Body.String(), `"success":false`)
	var injected int64
	require.NoError(t, model.DB.Model(&model.Redemption{}).Count(&injected).Error)
	assert.Zero(t, injected)
	body = `{"name":"private","quota":100,"count":1,"request_id":"controller-create-own"}`
	proof := agentRedemptionControllerProof(t, users[1], "redemption.create", "controller-create-own", body)
	response = performAgentRedemptionRequest(t, users[1], http.MethodPost, "/api/redemption/", body, AddRedemption, 0, proof)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	var created struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &created))
	require.True(t, created.Success, response.Body.String())
	require.Len(t, created.Data, 1)
	var code model.Redemption
	require.NoError(t, model.DB.First(&code).Error)
	assert.Equal(t, users[1].Id, code.UserId)
	assert.Equal(t, users[1].Id, code.BudgetAgentID)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
	assert.NotEqual(t, "injected", code.Key)
	assert.Zero(t, code.UsedUserId)
	for _, target := range []string{fmt.Sprintf("/api/redemption/?agent_user_id=%d", users[1].Id), fmt.Sprintf("/api/redemption/search?keyword=%d&agent_user_id=%d", code.Id, users[1].Id)} {
		handler := GetAllRedemptions
		if strings.Contains(target, "search") {
			handler = SearchRedemptions
		}
		response = performAgentRedemptionRequest(t, users[2], http.MethodGet, target, "", handler, 0)
		assert.NotContains(t, response.Body.String(), code.Key)
		assert.Contains(t, response.Body.String(), `"total":0`)
	}
	for _, call := range []struct {
		method, target, body string
		handler              gin.HandlerFunc
		id                   int
	}{
		{http.MethodGet, "/api/redemption/1", "", GetRedemption, code.Id},
		{http.MethodPut, "/api/redemption/?status_only=true", fmt.Sprintf(`{"id":%d,"status":2}`, code.Id), UpdateRedemption, 0},
		{http.MethodDelete, "/api/redemption/1", "", DeleteRedemption, code.Id},
		{http.MethodPost, "/api/redemption/batch", fmt.Sprintf(`{"ids":[%d]}`, code.Id), DeleteRedemptionBatch, 0},
	} {
		response = performAgentRedemptionRequest(t, users[2], call.method, call.target, call.body, call.handler, call.id)
		assert.NotContains(t, response.Body.String(), code.Key)
	}
	require.NoError(t, model.DB.First(&code, code.Id).Error)
	assert.Equal(t, common.RedemptionCodeStatusEnabled, code.Status)
	response = performAgentRedemptionRequest(t, users[2], http.MethodGet, "/api/agent-redemption/accounts/1/ledger", "", GetAgentRedemptionLedger, users[1].Id)
	assert.Equal(t, http.StatusForbidden, response.Code)
	for _, user := range users[1:] {
		response = performAgentRedemptionRequest(t, user, http.MethodPost, "/api/agent-redemption/allocations", fmt.Sprintf(`{"agent_user_id":%d,"delta":100,"reason":"privilege injection","request_id":"forged-admin-allocation","role":100}`, user.Id), AllocateAgentRedemptionQuota, 0)
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
}

func TestAgentRedemptionAPIPrivateLedgerAndLiveRevocation(t *testing.T) {
	users := setupAgentRedemptionControllerTest(t)
	response := performAgentRedemptionRequest(t, users[1], http.MethodGet, fmt.Sprintf("/api/agent-redemption/accounts?agent_user_id=%d", users[2].Id), "", GetAgentRedemptionAccounts, 0)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), users[1].Username)
	assert.NotContains(t, response.Body.String(), users[2].Username)
	response = performAgentRedemptionRequest(t, users[1], http.MethodGet, "/api/agent-redemption/accounts/1/ledger", "", GetAgentRedemptionLedger, users[1].Id)
	require.Equal(t, http.StatusOK, response.Code)
	for _, forbidden := range []string{`"operation_key"`, `"signature"`, `"key"`, `"password"`, `"email"`, `"access_token"`} {
		assert.NotContains(t, response.Body.String(), forbidden)
	}
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", users[1].Id).Update("role", common.RoleCommonUser).Error)
	response = performAgentRedemptionRequest(t, users[1], http.MethodGet, "/api/redemption/", "", GetAllRedemptions, 0)
	assert.Equal(t, http.StatusOK, response.Code, "ordinary role retains an existing active profile")
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", users[1].Id).Update("status", common.UserStatusDisabled).Error)
	for _, handler := range []gin.HandlerFunc{GetAllRedemptions, GetAgentRedemptionAccounts} {
		response = performAgentRedemptionRequest(t, users[1], http.MethodGet, "/api/redemption/", "", handler, 0)
		assert.Equal(t, http.StatusForbidden, response.Code)
	}
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", users[0].Id).Update("status", common.UserStatusDisabled).Error)
	response = performAgentRedemptionRequest(t, users[0], http.MethodPost, "/api/agent-redemption/allocations", fmt.Sprintf(`{"agent_user_id":%d,"delta":100,"reason":"revoked","request_id":"revoked-admin-request"}`, users[2].Id), AllocateAgentRedemptionQuota, 0)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestAgentRedemptionAPIFinancialMutationRejectsRevokedReceipt(t *testing.T) {
	users := setupAgentRedemptionControllerTest(t)
	user := users[1]
	body := `{"name":"session proof","quota":100,"count":1,"request_id":"session-proof-create"}`
	proof := agentRedemptionControllerProof(t, user, "redemption.create", "session-proof-create", body)
	response := performAgentRedemptionRequest(t, user, http.MethodPost, "/api/redemption/", body, AddRedemption, 0, proof)
	require.Contains(t, response.Body.String(), `"success":true`)
	var code model.Redemption
	require.NoError(t, model.DB.First(&code).Error)
	// HTTP handlers are entered with an identity previously accepted by auth;
	// logout commits before their business transaction obtains its receipt lock.
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("user_id = ?", user.Id).Updates(map[string]any{"status": model.UserSessionStatusRevoked, "revoked_at": common.GetTimestamp()}).Error)
	for _, call := range []struct {
		method, target, body string
		handler              gin.HandlerFunc
		id                   int
	}{
		{http.MethodPost, "/api/redemption/", body, AddRedemption, 0}, // exact idempotent replay still requires a live session
		{http.MethodPut, "/api/redemption/?status_only=true", fmt.Sprintf(`{"id":%d,"status":2,"request_id":"revoked-update-request","version":%d}`, code.Id, code.BudgetVersion), UpdateRedemption, 0},
		{http.MethodDelete, "/api/redemption/1?request_id=revoked-delete-request", "", DeleteRedemption, code.Id},
		{http.MethodPost, "/api/redemption/batch", fmt.Sprintf(`{"ids":[%d],"request_id":"revoked-batch-request"}`, code.Id), DeleteRedemptionBatch, 0},
	} {
		response = performAgentRedemptionRequest(t, user, call.method, call.target, call.body, call.handler, call.id)
		assert.Equal(t, http.StatusForbidden, response.Code)
		assert.NotContains(t, response.Body.String(), code.Key)
	}
	var account model.AgentRedemptionAccount
	require.NoError(t, model.DB.First(&account, "agent_user_id = ?", user.Id).Error)
	assert.Equal(t, 900, account.AvailableQuota)
	assert.Equal(t, 100, account.ReservedQuota)
	// Same-role credential invalidation must also stop allocations; a role-only
	// check would accept this stale administrator request.
	admin := users[0]
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", admin.Id).Update("auth_version", admin.AuthVersion+1).Error)
	response = performAgentRedemptionRequest(t, admin, http.MethodPost, "/api/agent-redemption/allocations", fmt.Sprintf(`{"agent_user_id":%d,"delta":100,"reason":"stale receipt","request_id":"stale-receipt-adjust"}`, user.Id), AllocateAgentRedemptionQuota, 0)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestAgentRedemptionAPIPATAndFilteredInvalidCleanup(t *testing.T) {
	users := setupAgentRedemptionControllerTest(t)
	codes := make([]model.Redemption, 2)
	for i, user := range users[1:3] {
		scope := model.RedemptionScope{UserID: user.Id, Role: user.Role}
		keys, err := model.CreateRedemptionsScoped(scope, model.RedemptionCreateRequest{Name: "invalid cleanup", Quota: 100, Count: 1, RequestID: fmt.Sprintf("filtered-cleanup-%d", i)})
		require.NoError(t, err)
		require.NoError(t, model.DB.Where("key = ?", keys[0]).First(&codes[i]).Error)
		_, err = model.UpdateRedemptionScoped(scope, model.RedemptionUpdateRequest{ID: codes[i].Id, Status: common.RedemptionCodeStatusDisabled}, true)
		require.NoError(t, err)
	}
	// Even scoped administrator PATs cannot perform business financial mutations;
	// a browser session and transaction-specific MFA are required.
	token, _ := createScopedAccessToken(t, users[0].Id, 0, "redemption:write")
	router := gin.New()
	router.DELETE("/api/redemption/invalid", middleware.AdminAuth(), DeleteInvalidRedemption)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/redemption/invalid?agent_user_id=%d", users[1].Id), nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusForbidden, response.Code)
	var untouched int64
	require.NoError(t, model.DB.Model(&model.Redemption{}).Count(&untouched).Error)
	require.EqualValues(t, 2, untouched)
	payload := fmt.Sprintf(`{"request_id":"browser-invalid-cleanup","ids":null,"invalid_only":true,"agent_user_id":%d}`, users[1].Id)
	proof := agentRedemptionControllerProof(t, users[0], "redemption.admin.delete", "browser-invalid-cleanup", payload)
	response = performAgentRedemptionRequest(t, users[0], http.MethodDelete, fmt.Sprintf("/api/redemption/invalid?agent_user_id=%d&request_id=browser-invalid-cleanup", users[1].Id), "", DeleteInvalidRedemption, 0, proof)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"success":true`)
	assert.Contains(t, response.Body.String(), `"data":1`)
	var remaining []model.Redemption
	require.NoError(t, model.DB.Find(&remaining).Error)
	require.Len(t, remaining, 1)
	assert.Equal(t, codes[1].Id, remaining[0].Id, "filtered invalid cleanup must not affect another displayed account")
	for _, user := range users[1:3] {
		var account model.AgentRedemptionAccount
		require.NoError(t, model.DB.First(&account, "agent_user_id = ?", user.Id).Error)
		assert.Equal(t, 1000, account.AvailableQuota)
		assert.Zero(t, account.ReservedQuota, "deleting an already-refunded code cannot refund twice")
	}
	// Direct HTTP entry with only a user id/role, but no authenticated receipt,
	// must never become the model's trusted internal compatibility path.
	response = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/redemption/invalid", nil)
	c.Set("id", users[0].Id)
	c.Set("role", users[0].Role)
	DeleteInvalidRedemption(c)
	assert.Equal(t, http.StatusForbidden, response.Code)
}

func TestAgentRedemptionAPIPaginationNormalizesBeforeOffsetMath(t *testing.T) {
	users := setupAgentRedemptionControllerTest(t)
	for i, count := range []int{100, 1} {
		_, err := model.CreateRedemptionsScoped(model.RedemptionScope{UserID: users[1].Id, Role: users[1].Role}, model.RedemptionCreateRequest{Name: "bounded listing", Quota: 1, Count: count, RequestID: fmt.Sprintf("api-pagination-create-%d", i)})
		require.NoError(t, err)
	}
	for _, endpoint := range []struct {
		name, path string
		handler    gin.HandlerFunc
		id, total  int
	}{
		{"list", "/api/redemption/", GetAllRedemptions, 0, 101},
		{"search", "/api/redemption/search", SearchRedemptions, 0, 101},
		{"accounts", "/api/agent-redemption/accounts", GetAgentRedemptionAccounts, 0, 2},
		{"ledger", "/api/agent-redemption/accounts/1/ledger", GetAgentRedemptionLedger, users[1].Id, 3},
	} {
		for _, tc := range []struct {
			name, query string
			page, size  int
		}{
			{"negative", "?p=-5&page_size=-1", 1, 1},
			{"overflow_product", "?p=9223372036854775807&page_size=9223372036854775807", 1_000_000, 100},
			{"large_limit", "?p=1&page_size=9223372036854775807", 1, 100},
			{"beyond_integer_page", "?p=184467440737095516150&page_size=-2147483648", 1_000_000, 1},
			{"negative_ps_alias", "?p=-9223372036854775808&ps=-1", 1, 1},
			{"negative_size_alias", "?p=-1&size=-1", 1, 1},
		} {
			t.Run(endpoint.name+"/"+tc.name, func(t *testing.T) {
				response := performAgentRedemptionRequest(t, users[0], http.MethodGet, endpoint.path+tc.query, "", endpoint.handler, endpoint.id)
				require.Equal(t, http.StatusOK, response.Code)
				var payload struct {
					Success bool `json:"success"`
					Data    struct {
						Page     int   `json:"page"`
						PageSize int   `json:"page_size"`
						Total    int   `json:"total"`
						Items    []any `json:"items"`
					} `json:"data"`
				}
				require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
				require.True(t, payload.Success, response.Body.String())
				assert.Equal(t, tc.page, payload.Data.Page)
				assert.Equal(t, tc.size, payload.Data.PageSize)
				assert.Equal(t, endpoint.total, payload.Data.Total)
				if tc.page > 1 {
					assert.Empty(t, payload.Data.Items)
				} else {
					assert.Len(t, payload.Data.Items, min(endpoint.total, tc.size))
				}
			})
		}
	}
}

func agentRedemptionControllerProof(t *testing.T, user model.User, action, requestID, payload string) string {
	t.Helper()
	identity := model.AuthSessionIdentity{UserID: user.Id, SessionID: fmt.Sprintf("budget-controller-session-%d", user.Id), UserAuthVersion: user.AuthVersion, SessionVersion: 1}
	prepared, err := model.PrepareAgentPurchaseOperation(identity, action, requestID, []byte(payload))
	require.NoError(t, err)
	require.NotNil(t, prepared)
	hash, err := model.AgentTransactionContextHash(prepared.VerificationContext)
	require.NoError(t, err)
	proof, _, err := model.IssueAgentTransactionProof(identity, hash, "2fa")
	require.NoError(t, err)
	return proof
}

func TestAgentRedemptionAPIExactReplayNeedsNoNewProofButRejectsChangedInputs(t *testing.T) {
	users := setupAgentRedemptionControllerTest(t)
	body := fmt.Sprintf(`{"agent_user_id":%d,"delta":25,"reason":"explicit fixture grant","request_id":"http-allocate-exact-replay"}`, users[1].Id)
	proof := agentRedemptionControllerProof(t, users[0], "redemption.allocate", "http-allocate-exact-replay", body)
	first := performAgentRedemptionRequest(t, users[0], http.MethodPost, "/api/agent-redemption/allocations", body, AllocateAgentRedemptionQuota, 0, proof)
	require.Contains(t, first.Body.String(), `"success":true`)
	replay := performAgentRedemptionRequest(t, users[0], http.MethodPost, "/api/agent-redemption/allocations", body, AllocateAgentRedemptionQuota, 0)
	require.Contains(t, replay.Body.String(), `"success":true`)
	assert.Contains(t, replay.Body.String(), `"replayed":true`)
	changed := strings.Replace(body, `"delta":25`, `"delta":26`, 1)
	conflict := performAgentRedemptionRequest(t, users[0], http.MethodPost, "/api/agent-redemption/allocations", changed, AllocateAgentRedemptionQuota, 0)
	assert.Equal(t, http.StatusConflict, conflict.Code)
	var account model.AgentRedemptionAccount
	require.NoError(t, model.DB.First(&account, "agent_user_id = ?", users[1].Id).Error)
	assert.Equal(t, 1025, account.AvailableQuota)
}

func TestAgentPurchaseFinancialReadsRejectDurablyRevokedCachedIdentity(t *testing.T) {
	users := setupAgentRedemptionControllerTest(t)
	owner := users[1]
	require.NoError(t, model.DB.Model(&model.UserSession{}).Where("user_id = ?", owner.Id).Update("status", model.UserSessionStatusRevoked).Error)
	for _, handler := range []gin.HandlerFunc{GetAgentPurchaseOrders, GetAgentPurchaseRefunds, GetAgentFundBatches, GetAgentFundLedger, GetAgentPurchaseAudit, GetAgentDeliveries, GetReceivedAgentDeliveries, GetAllRedemptions, GetAgentRedemptionAccounts} {
		response := performAgentRedemptionRequest(t, owner, http.MethodGet, "/api/agent-platform/read", "", handler, 0)
		assert.Equal(t, http.StatusForbidden, response.Code, "stale accepted session context cannot read finance data")
		assert.Contains(t, response.Body.String(), `"success":false`)
	}
}
