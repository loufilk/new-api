package router

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	walletroutes "github.com/QuantumNous/new-api/modules/chuanapi/wallet/routes"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestTokenMixP1RoutesRegistered 验证钱包/推荐赠金路由，并确保旧提现入口未注册。
func TestTokenMixP1RoutesRegistered(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	db, err := gorm.Open(sqlite.Open("file:wallet-routes?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	// 路由注册本身不 panic 即代表无冲突（重复注册相同路径会 panic）。
	var scopes extension.RouteScopes
	require.NotPanics(t, func() { scopes = SetApiRouter(engine) })
	registry := extension.NewRegistry()
	require.NoError(t, walletroutes.Register(registry))
	require.NoError(t, registry.Freeze())
	require.NoError(t, registry.ApplyRoutesWithScopes(scopes, extension.PriorityBeforePlugin))

	routes := engine.Routes()
	paths := make(map[string]bool)
	for _, route := range routes {
		paths[route.Method+" "+route.Path] = true
	}
	expected := []string{
		http.MethodGet + " /api/user/wallet",
		http.MethodGet + " /api/user/wallet/ledger",
		http.MethodGet + " /api/user/referral/rewards",
		http.MethodPost + " /api/user/aff_transfer",
		http.MethodPost + " /api/token/:id/key",
		http.MethodPost + " /api/token/batch/keys",
	}
	for _, path := range expected {
		require.True(t, paths[path], "route not registered: %s", path)
	}
	removed := []string{
		http.MethodGet + " /api/user/referral/overview",
		http.MethodGet + " /api/user/referral/invitees",
		http.MethodPost + " /api/user/referral/mature",
		http.MethodGet + " /api/user/referral/withdrawals",
		http.MethodPost + " /api/user/referral/withdrawals",
		http.MethodGet + " /api/user/referral/admin/withdrawals",
		http.MethodPost + " /api/user/referral/admin/withdrawals/:id/approve",
		http.MethodPost + " /api/user/referral/admin/withdrawals/:id/reject",
		http.MethodPost + " /api/user/referral/admin/withdrawals/:id/pay",
	}
	for _, path := range removed {
		require.False(t, paths[path], "removed route still registered: %s", path)
	}
}

func TestSetApiRouterExposesHostOwnedExtensionScopes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	scopes := SetApiRouter(engine)

	require.Same(t, engine, scopes.Engine)
	require.NotNil(t, scopes.API)
	require.NotNil(t, scopes.UserSelf)
	require.NotNil(t, scopes.SubscriptionUser)
	require.NotNil(t, scopes.RootOptions)
	require.NotNil(t, scopes.AdminModels)
	require.NotNil(t, scopes.PublicAPI)
	require.NotNil(t, scopes.BrowserSession)
	require.NotNil(t, scopes.AnonymousRequestBodyLimit)
	require.NotNil(t, scopes.PaymentRoutes, "host payment provider registrar must be exposed to extension route sets")
}
