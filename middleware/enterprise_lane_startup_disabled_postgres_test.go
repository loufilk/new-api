//go:build !enterprise_lane_lab

package middleware

import (
	"net"
	"net/url"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestEnterpriseLaneOrdinaryStartupFencePostgres(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated loopback TEST_POSTGRES_DSN")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(u.Hostname()).IsLoopback())
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	defer pool.Close()
	old := model.DB
	t.Cleanup(func() { model.DB = old })
	// Session-bound temporary tables are intentionally not considered current
	// schema evidence; use a private persistent test schema for startup.
	require.NoError(t, db.Exec("CREATE SCHEMA ordinary_lane_startup_fence").Error)
	defer db.Exec("DROP SCHEMA ordinary_lane_startup_fence CASCADE")
	q := u.Query()
	q.Set("search_path", "ordinary_lane_startup_fence")
	u.RawQuery = q.Encode()
	scoped, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	scopedPool, err := scoped.DB()
	require.NoError(t, err)
	defer scopedPool.Close()
	model.DB = scoped
	stop, err := ConfigureEnterpriseLaneGateway(t.Context())
	require.NoError(t, err)
	require.False(t, stop)
	require.False(t, EnterpriseLaneGatewayEnabled())
	require.NoError(t, scoped.Exec("CREATE TABLE enterprise_lanes (state text)").Error)
	stop, err = ConfigureEnterpriseLaneGateway(t.Context())
	require.ErrorIs(t, err, model.ErrEnterpriseLaneUnavailable, "ordinary build must inspect persisted lab state without an opt-in flag")
	require.False(t, stop)
	require.False(t, EnterpriseLaneGatewayEnabled())
}
