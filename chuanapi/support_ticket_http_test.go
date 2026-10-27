package chuanapi_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/chuanapi"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/router"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type supportHTTPUser struct {
	id   int
	role int
}

type supportHTTPOptions struct {
	globalLimitNum   int
	criticalLimitNum int
	users            []supportHTTPUser
}

type supportHTTPFixture struct {
	engine    *gin.Engine
	db        *gorm.DB
	tokens    map[int]string
	userIDs   map[int]int
	namespace uint32
}

var supportHTTPFixtureSequence atomic.Uint32

// newSupportHTTPFixture builds a real Gin engine over a fresh in-memory SQLite
// database. Global rate-limit configuration is set before routes are registered
// because the limiter factories snapshot the configured budgets at registration
// time. Each caller uses a unique client IP and unique user IDs so the shared
// package-level in-memory limiter buckets cannot leak between test cases.
func newSupportHTTPFixture(t *testing.T, opts supportHTTPOptions) supportHTTPFixture {
	t.Helper()

	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldType := common.MainDatabaseType()
	oldRedis, oldSecret := common.RedisEnabled, common.SessionSecret
	oldGlobalEnable, oldGlobalNum, oldGlobalDuration := common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration
	oldCriticalEnable, oldCriticalNum, oldCriticalDuration := common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration
	oldSearchEnable, oldSearchNum, oldSearchDuration := common.SearchRateLimitEnable, common.SearchRateLimitNum, common.SearchRateLimitDuration
	oldExpiration := common.RateLimitKeyExpirationDuration

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.AuditLog{},
		&model.SupportTicket{},
		&model.SupportTicketMessage{},
	))

	model.DB, model.LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.SessionSecret = "support-http-isolated-fixture"
	common.GlobalApiRateLimitEnable = true
	common.GlobalApiRateLimitNum = opts.globalLimitNum
	common.GlobalApiRateLimitDuration = 3600
	common.CriticalRateLimitEnable = true
	common.CriticalRateLimitNum = opts.criticalLimitNum
	common.CriticalRateLimitDuration = 3600
	common.SearchRateLimitEnable = false
	common.RateLimitKeyExpirationDuration = time.Hour

	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetMainDatabaseType(oldType)
		common.RedisEnabled, common.SessionSecret = oldRedis, oldSecret
		common.GlobalApiRateLimitEnable, common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration = oldGlobalEnable, oldGlobalNum, oldGlobalDuration
		common.CriticalRateLimitEnable, common.CriticalRateLimitNum, common.CriticalRateLimitDuration = oldCriticalEnable, oldCriticalNum, oldCriticalDuration
		common.SearchRateLimitEnable, common.SearchRateLimitNum, common.SearchRateLimitDuration = oldSearchEnable, oldSearchNum, oldSearchDuration
		common.RateLimitKeyExpirationDuration = oldExpiration
		require.NoError(t, sqlDB.Close())
	})

	namespace := supportHTTPFixtureSequence.Add(1)
	tokens := make(map[int]string)
	userIDs := make(map[int]int)
	for _, item := range opts.users {
		userID := int(namespace)*1000 + item.id
		userIDs[item.id] = userID
		user := &model.User{
			Id:          userID,
			Username:    fmt.Sprintf("support-user-%d", item.id),
			Password:    "fixture-only",
			Role:        item.role,
			Status:      common.UserStatusEnabled,
			Group:       "default",
			AuthVersion: 1,
			AffCode:     fmt.Sprintf("support-aff-%d", item.id),
		}
		require.NoError(t, db.Create(user).Error)

		now := time.Now().Unix()
		session := &model.UserSession{
			SID:             fmt.Sprintf("support-session-%d", userID),
			UserID:          userID,
			Version:         1,
			UserAuthVersion: 1,
			Status:          model.UserSessionStatusActive,
			RefreshHash:     fmt.Sprintf("support-hash-%d", item.id),
			LoginMethod:     "password",
			LastActiveAt:    now,
			ExpiresAt:       now + 3600,
		}
		require.NoError(t, model.CreateUserSession(session))

		token, _, err := service.IssueAccessToken(service.AuthIdentity{
			UserID:          userID,
			SessionID:       session.SID,
			UserAuthVersion: 1,
			SessionVersion:  1,
		})
		require.NoError(t, err)
		tokens[item.id] = token
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.CORS())
	scopes := router.SetApiRouter(engine)
	reg := extension.NewRegistry()
	require.NoError(t, chuanapi.Register(reg))
	require.NoError(t, reg.Freeze())
	require.NoError(t, reg.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	return supportHTTPFixture{engine: engine, db: db, tokens: tokens, userIDs: userIDs, namespace: namespace}
}

func supportHTTPRequest(t *testing.T, fixture supportHTTPFixture, method, path, clientIP string, userID int, body any) *httptest.ResponseRecorder {
	t.Helper()

	var encoded []byte
	if body != nil {
		var err error
		encoded, err = common.Marshal(body)
		require.NoError(t, err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	// Namespace the address without changing whether two requests have the same
	// source IP. Repeated go test -count runs must not reuse a prior limiter window.
	request.RemoteAddr = fmt.Sprintf("[2001:db8:%x::%s]:12345", fixture.namespace, clientIP)
	request.Header.Set("Content-Type", "application/json")
	if userID > 0 {
		request.Header.Set("Authorization", "Bearer "+fixture.tokens[userID])
	}
	response := httptest.NewRecorder()
	fixture.engine.ServeHTTP(response, request)
	return response
}

func TestSupportTicketRoutesHonorConfiguredGlobalAPILimit(t *testing.T) {
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{
		globalLimitNum:   1,
		criticalLimitNum: 1000,
		users:            []supportHTTPUser{{id: 201, role: common.RoleCommonUser}, {id: 299, role: common.RoleAdminUser}},
	})

	control := supportHTTPRequest(t, fixture, http.MethodGet, "/api/notice", "198.51.100.1", 0, nil)
	require.Equal(t, http.StatusOK, control.Code, "the first control request consumes the API budget")
	exhausted := supportHTTPRequest(t, fixture, http.MethodGet, "/api/notice", "198.51.100.1", 0, nil)
	require.Equal(t, http.StatusTooManyRequests, exhausted.Code, "the control endpoint must prove the API budget is exhausted")

	response := supportHTTPRequest(t, fixture, http.MethodPost, "/api/support/tickets", "198.51.100.1", 201, map[string]string{
		"subject": "Global budget",
		"message": "This write must be rejected by the inherited API limiter",
	})
	assert.Equal(t, http.StatusTooManyRequests, response.Code, "ticket creation must obey the configured global API budget")
	assert.Contains(t, response.Header().Get("Cache-Control"), "no-store")
	adminRead := supportHTTPRequest(t, fixture, http.MethodGet, "/api/admin/support/tickets", "198.51.100.1", 299, nil)
	assert.Equal(t, http.StatusTooManyRequests, adminRead.Code, "admin routes must share the API budget")
	adminWrite := supportHTTPRequest(t, fixture, http.MethodPost, "/api/admin/support/tickets/1/replies", "198.51.100.1", 299, map[string]string{"message": "Blocked admin reply"})
	assert.Equal(t, http.StatusTooManyRequests, adminWrite.Code)

	var ticketCount int64
	require.NoError(t, fixture.db.Model(&model.SupportTicket{}).Count(&ticketCount).Error)
	assert.Zero(t, ticketCount, "an exhausted API budget must not persist a ticket")
}

func TestSupportTicketWriteLimitsAreUserScopedAndActionIsolated(t *testing.T) {
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{
		globalLimitNum:   1000,
		criticalLimitNum: 1,
		users: []supportHTTPUser{
			{id: 301, role: common.RoleCommonUser},
			{id: 302, role: common.RoleCommonUser},
		},
	})

	created, err := service.CreateSupportTicket(fixture.userIDs[301], "Reply budget", "Seed conversation")
	require.NoError(t, err)
	replyPath := fmt.Sprintf("/api/support/tickets/%d/replies", created.Ticket.ID)

	first := supportHTTPRequest(t, fixture, http.MethodPost, replyPath, "203.0.113.10", 301, map[string]string{"message": "First reply"})
	require.Equal(t, http.StatusOK, first.Code, "the first reply must consume the user reply budget")
	var firstResult struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(first.Body.Bytes(), &firstResult))
	require.True(t, firstResult.Success)

	rotated := supportHTTPRequest(t, fixture, http.MethodPost, replyPath, "203.0.113.99", 301, map[string]string{"message": "Reply from a rotated IP"})
	assert.Equal(t, http.StatusTooManyRequests, rotated.Code, "rotating the source IP must not bypass the per-user reply limit")
	var messageCount int64
	require.NoError(t, fixture.db.Model(&model.SupportTicketMessage{}).Where("ticket_id = ?", created.Ticket.ID).Count(&messageCount).Error)
	assert.Equal(t, int64(2), messageCount, "a limited reply must not persist a message")

	create := supportHTTPRequest(t, fixture, http.MethodPost, "/api/support/tickets", "203.0.113.99", 301, map[string]string{
		"subject": "Independent create budget",
		"message": "The create scope must stay independent from replies",
	})
	assert.Equal(t, http.StatusOK, create.Code, "create and reply must use independent limiter scopes")
	var createResult struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(create.Body.Bytes(), &createResult))
	require.True(t, createResult.Success)
	blockedCreate := supportHTTPRequest(t, fixture, http.MethodPost, "/api/support/tickets", "203.0.113.100", 301, map[string]string{"subject": "Second create", "message": "Must not be persisted"})
	assert.Equal(t, http.StatusTooManyRequests, blockedCreate.Code, "rotating the source IP must not bypass the create limit")
	var ticketCount int64
	require.NoError(t, fixture.db.Model(&model.SupportTicket{}).Where("user_id = ?", fixture.userIDs[301]).Count(&ticketCount).Error)
	assert.Equal(t, int64(2), ticketCount, "only the seed and first successful create may persist")

	otherUser := supportHTTPRequest(t, fixture, http.MethodPost, "/api/support/tickets", "203.0.113.99", 302, map[string]string{"subject": "Other user", "message": "Independent user budget"})
	require.Equal(t, http.StatusOK, otherUser.Code, "the write limiter must be keyed per user")
	var otherResult struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(otherUser.Body.Bytes(), &otherResult))
	assert.True(t, otherResult.Success, "the independent user must actually create a ticket, not just receive an HTTP 200 business error")
}

func TestSupportTicketRoutesEnforceAuthAndOwnership(t *testing.T) {
	fixture := newSupportHTTPFixture(t, supportHTTPOptions{
		globalLimitNum:   1000,
		criticalLimitNum: 1000,
		users: []supportHTTPUser{
			{id: 401, role: common.RoleCommonUser},
			{id: 402, role: common.RoleCommonUser},
			{id: 499, role: common.RoleAdminUser},
		},
	})

	created, err := service.CreateSupportTicket(fixture.userIDs[401], "Isolated ticket", "Private conversation")
	require.NoError(t, err)
	destPath := fmt.Sprintf("/api/support/tickets/%d", created.Ticket.ID)

	anonymous := supportHTTPRequest(t, fixture, http.MethodGet, destPath, "192.0.2.50", 0, nil)
	assert.Equal(t, http.StatusUnauthorized, anonymous.Code, "anonymous callers must be rejected")

	adminAsUser := supportHTTPRequest(t, fixture, http.MethodGet, "/api/admin/support/tickets", "192.0.2.50", 401, nil)
	assert.Equal(t, http.StatusForbidden, adminAsUser.Code, "a common user must not reach admin ticket routes")

	for _, item := range []struct {
		name, method, path string
		userID             int
		body               any
	}{
		{name: "foreign_read", method: http.MethodGet, path: destPath, userID: 402},
		{name: "foreign_reply", method: http.MethodPost, path: destPath + "/replies", userID: 402, body: map[string]string{"message": "Unexpected reply"}},
		{name: "foreign_mark_read", method: http.MethodPost, path: destPath + "/read", userID: 402},
	} {
		t.Run(item.name, func(t *testing.T) {
			response := supportHTTPRequest(t, fixture, item.method, item.path, "192.0.2.50", item.userID, item.body)
			require.Equal(t, http.StatusOK, response.Code)
			var result struct {
				Success bool `json:"success"`
			}
			require.NoError(t, common.Unmarshal(response.Body.Bytes(), &result))
			assert.False(t, result.Success)
			assert.NotContains(t, response.Body.String(), "Private conversation")
		})
	}

	owner := supportHTTPRequest(t, fixture, http.MethodGet, destPath, "192.0.2.50", 401, nil)
	require.Equal(t, http.StatusOK, owner.Code)
	assert.Contains(t, owner.Body.String(), "Private conversation")

	adminPath := fmt.Sprintf("/api/admin/support/tickets/%d", created.Ticket.ID)
	admin := supportHTTPRequest(t, fixture, http.MethodGet, adminPath, "192.0.2.50", 499, nil)
	require.Equal(t, http.StatusOK, admin.Code)
	var adminResult struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(admin.Body.Bytes(), &adminResult))
	assert.True(t, adminResult.Success)

	require.NoError(t, fixture.db.Model(&model.UserSession{}).
		Where("user_id = ?", fixture.userIDs[401]).
		Update("status", model.UserSessionStatusRevoked).Error)
	revoked := supportHTTPRequest(t, fixture, http.MethodGet, destPath, "192.0.2.50", 401, nil)
	assert.Equal(t, http.StatusUnauthorized, revoked.Code, "a revoked session must fail at the support endpoint")
}
