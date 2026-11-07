package router

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestNewUserBenefitRoutes(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an owned PostgreSQL TEST_POSTGRES_DSN fixture")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	schema := "benefit_routes_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: parsed.String(), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldMemory, oldLimit := common.RedisEnabled, common.MemoryCacheEnabled, common.CriticalRateLimitEnable
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.MemoryCacheEnabled, common.CriticalRateLimitEnable = false, false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.RedisEnabled, common.MemoryCacheEnabled, common.CriticalRateLimitEnable = oldRedis, oldMemory, oldLimit
		require.NoError(t, db.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserAccessToken{}, &model.UserSession{}, &model.Option{},
		&model.Token{}, &model.Channel{}, &model.Ability{}, &model.SubscriptionPlan{}, &model.AuditLog{},
		&model.NewUserBenefitConfig{}, &model.NewUserBenefitConfigAudit{}, &model.NewUserBenefitOrder{}))
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	user := seedReferralRouteUser(t, db, 7101, "", 0, common.UserStatusEnabled)
	root := seedReferralRouteUser(t, db, 7102, "", 0, common.UserStatusEnabled)
	require.NoError(t, db.Model(&root).Update("role", common.RoleRootUser).Error)
	reader := model.AccessTokenPrefix + "benefit-user-reader"
	wrongScope := model.AccessTokenPrefix + "benefit-profile-reader"
	rootReader := model.AccessTokenPrefix + "benefit-root-reader"
	rootWriter := model.AccessTokenPrefix + "benefit-root-writer"
	userOptions := model.AccessTokenPrefix + "benefit-common-options"
	seedRouteScopedToken(t, db, user.Id, reader, "wallet:read")
	seedRouteScopedToken(t, db, user.Id, wrongScope, "profile:read")
	seedRouteScopedToken(t, db, user.Id, userOptions, "option:write")
	seedRouteScopedToken(t, db, root.Id, rootReader, "option:read")
	seedRouteScopedToken(t, db, root.Id, rootWriter, "option:read", "option:write")
	body := `{"enabled":false,"pay_amount_cny":"0.10","credit_amount_cny":"1.00","version":0}`
	for _, test := range []struct {
		method, path, token, body string
		status                    int
	}{
		{http.MethodGet, "/api/user/new-user-benefit", "", "", http.StatusUnauthorized},
		{http.MethodPost, "/api/user/new-user-benefit/pay", "", "{}", http.StatusUnauthorized},
		{http.MethodGet, "/api/option/new-user-benefit", "", "", http.StatusUnauthorized},
		{http.MethodPut, "/api/option/new-user-benefit", "", body, http.StatusUnauthorized},
		{http.MethodGet, "/api/user/new-user-benefit", wrongScope, "", http.StatusForbidden},
		{http.MethodPost, "/api/user/new-user-benefit/pay", reader, "{}", http.StatusForbidden},
		{http.MethodPut, "/api/option/new-user-benefit", userOptions, body, http.StatusForbidden},
		{http.MethodPut, "/api/option/new-user-benefit", rootReader, body, http.StatusForbidden},
		{http.MethodGet, "/api/user/new-user-benefit", reader, "", http.StatusOK},
		{http.MethodGet, "/api/option/new-user-benefit", rootReader, "", http.StatusOK},
	} {
		response := performReferralRouteRequest(engine, test.method, test.path, test.body, test.token)
		assert.Equal(t, test.status, response.Code, "%s %s: %s", test.method, test.path, response.Body.String())
	}
	var count int64
	require.NoError(t, db.Model(&model.NewUserBenefitOrder{}).Count(&count).Error)
	assert.Zero(t, count, "denied requests and reads must never reserve payment orders")
	require.NoError(t, db.Model(&model.NewUserBenefitConfigAudit{}).Count(&count).Error)
	assert.Zero(t, count, "denied writes must never change the offer")
	saved := performReferralRouteRequest(engine, http.MethodPut, "/api/option/new-user-benefit", body, rootWriter)
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	assert.Contains(t, saved.Body.String(), `"version":1`)
	var audit model.NewUserBenefitConfigAudit
	require.NoError(t, db.First(&audit).Error)
	assert.Equal(t, root.Id, audit.ActorID, "the authenticated operator owns the price change")
}
