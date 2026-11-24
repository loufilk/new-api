//go:build lane_billing_integration_dev

package middleware

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestLaneKeyAvailabilityDevHTTPPostgresRedis(t *testing.T) {
	dsn, redisDSN := os.Getenv("TEST_LANE_AVAILABILITY_POSTGRES_DSN"), os.Getenv("TEST_LANE_AVAILABILITY_REDIS_DSN")
	if dsn == "" || redisDSN == "" {
		t.Skip("requires disposable loopback PostgreSQL and native Redis")
	}
	// Ordinary middleware tests share dialect, option and group-cache globals.
	// Isolate the rehearsal, just as the tagged full-router fixture does.
	const child = "LANE_KEY_AVAILABILITY_TEST_CHILD"
	if os.Getenv(child) != "1" {
		binary, err := os.Executable()
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestLaneKeyAvailabilityDevHTTPPostgresRedis$", "-test.v", "-test.count=1", "-test.timeout=50s")
		cmd.Env = append(os.Environ(), child+"=1")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		require.NotContains(t, string(out), "--- SKIP:")
		require.Contains(t, string(out), "--- PASS: TestLaneKeyAvailabilityDevHTTPPostgresRedis")
		t.Log(string(out))
		return
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.Equal(t, "postgres", u.Scheme)
	require.True(t, net.ParseIP(u.Hostname()).IsLoopback())
	require.Equal(t, "/lane_availability_test", u.Path)
	require.Equal(t, url.Values{"sslmode": {"disable"}}, u.Query())
	bootstrap, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	pool, err := bootstrap.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	var ver int
	require.NoError(t, bootstrap.Raw("SHOW server_version_num").Scan(&ver).Error)
	require.Equal(t, 180006, ver)
	schema := "lane_billing_dev_" + common.GetUUID()[:8]
	require.Regexp(t, `^lane_billing_dev_[a-f0-9]{8}$`, schema)
	require.NoError(t, bootstrap.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	t.Cleanup(func() { require.NoError(t, bootstrap.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error) })
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	previousDB, previousLog := model.DB, model.LOG_DB
	previousMain, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
	previousRedis, previousRDB, previousSecret, previousMemory := common.RedisEnabled, common.RDB, common.SessionSecret, common.MemoryCacheEnabled
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLog
		common.SetDatabaseTypes(previousMain, previousLogType)
		common.RedisEnabled, common.RDB, common.SessionSecret, common.MemoryCacheEnabled = previousRedis, previousRDB, previousSecret, previousMemory
	})
	t.Setenv("SQL_DSN", u.String())
	opened, err := model.OpenPrimaryForMaintenance(false)
	require.NoError(t, err)
	scopedPool, err := opened.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, scopedPool.Close()) })
	db := opened.Session(&gorm.Session{Logger: logger.Discard})
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	common.RedisEnabled, common.MemoryCacheEnabled, common.SessionSecret = false, false, "synthetic-lane-availability-test"
	require.NoError(t, i18n.Init())
	models := []any{&model.User{}, &model.Token{}, &model.Option{}, &model.BillingFinalization{}, &model.BalanceLedger{},
		&model.BalanceLedgerOperation{}, &model.SubscriptionPlan{}, &model.UserCallInterception{}, &model.UserCallInterceptionHit{}, &model.UserCallInterceptionAudit{}}
	models = append(models, model.SharedWalletTreasuryModels()...)
	models = append(models, model.BonusSourceModels()...)
	require.NoError(t, db.AutoMigrate(models...))
	require.NoError(t, model.InstallLaneBillingSyntheticDevSchema(t.Context(), db))
	user := model.User{Id: 661, Username: "synthetic-lane-availability", AffCode: "lane-avail", Group: "default",
		Status: common.UserStatusEnabled, AuthVersion: 1, Role: common.RoleCommonUser, Quota: 100, QuotaPaid: 100}
	require.NoError(t, db.Create(&user).Error)
	owned := model.Token{Id: 662, UserId: user.Id, Key: "syntheticlaneavailability", Group: "default",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 100}
	require.NoError(t, db.Create(&owned).Error)
	ordinary := model.Token{Id: 663, UserId: user.Id, Key: "syntheticlaneordinary", Group: "default",
		Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 10}
	require.NoError(t, db.Create(&ordinary).Error)
	id := model.LaneBillingFiniteKeyIdentity{UserID: user.Id, TokenID: owned.Id, TokenFingerprint: model.BillingTokenFingerprint(owned.Key), Epoch: 3}
	require.NoError(t, model.EnrollLaneBillingFiniteKeyDev(t.Context(), db, id))
	availability, err := model.NewLaneKeyAvailabilityDev(t.Context(), db, id)
	require.NoError(t, err)
	_, _, err = model.ApplySharedWalletTransfer(t.Context(), db, model.SharedWalletTransfer{
		UserID: user.Id, Epoch: 5, Slot: 4, Direction: "grant", Amount: 100, OperationID: "auth-availability-wallet-grant",
	})
	require.NoError(t, err)
	redisOptions, err := redis.ParseURL(redisDSN)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(redisOptions.Addr)
	require.NoError(t, err)
	require.True(t, net.ParseIP(host).IsLoopback())
	redisOptions.ContextTimeoutEnabled = true
	redisOptions.ReadTimeout, redisOptions.WriteTimeout, redisOptions.DialTimeout = time.Second, time.Second, time.Second
	redisOptions.MaxRetries = -1
	rdb := redis.NewClient(redisOptions)
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })
	info, err := rdb.InfoMap(t.Context(), "server").Result()
	require.NoError(t, err)
	require.Equal(t, "8.8.0", info["Server"]["redis_version"])
	size, err := rdb.DBSize(t.Context()).Result()
	require.NoError(t, err)
	require.Zero(t, size, "Redis fixture must be empty")
	common.RDB = rdb
	_, _, err = model.ApplyLaneBillingFiniteKeyDevTransfer(t.Context(), db, model.LaneBillingFiniteKeyTransfer{
		Identity: id, Slot: 2, Direction: "grant", Amount: 100, OperationID: "auth-availability-key-grant",
	})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.GET("/ordinary", TokenAuth(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	router.GET("/internal-test", tokenAuthWithLaneKeyAvailability(availability), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)
	get := func(path string, token model.Token, want int) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, gateway.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer sk-"+token.Key)
		req.Header.Set("X-Lane-Force", "true")
		res, err := gateway.Client().Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, 4097))
		require.NoError(t, err)
		require.Equal(t, want, res.StatusCode, string(body))
		require.NotContains(t, string(body), token.Key)
	}
	var stored model.Token
	require.NoError(t, db.First(&stored, owned.Id).Error)
	require.Zero(t, stored.RemainQuota)
	get("/internal-test", owned, http.StatusNoContent)
	require.NoError(t, db.First(&stored, owned.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, stored.Status)
	// The configured handler must be indistinguishable from ordinary auth for
	// an unrelated Key, including the number of repository query callbacks.
	var queries atomic.Int64
	const countAuthQueries = "count_lane_key_availability_test_queries"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(countAuthQueries, func(*gorm.DB) {
		queries.Add(1)
	}))
	get("/ordinary", ordinary, http.StatusNoContent)
	ordinaryQueries := queries.Swap(0)
	require.Positive(t, ordinaryQueries)
	get("/internal-test", ordinary, http.StatusNoContent)
	require.Equal(t, ordinaryQueries, queries.Load())
	require.NoError(t, db.Callback().Query().Remove(countAuthQueries))
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", ordinary.Id).UpdateColumn("remain_quota", 0).Error)
	get("/ordinary", ordinary, http.StatusUnauthorized)
	var ordinaryStored model.Token
	require.NoError(t, db.First(&ordinaryStored, ordinary.Id).Error)
	require.Equal(t, common.TokenStatusExhausted, ordinaryStored.Status, "legacy path still owns ordinary exhaustion")

	// A correctly MAC'd cached permission object with an old revision must
	// not skip a real database check, even with plenty of idle quota.
	common.RedisEnabled = true
	warm, err := model.GetTokenByKey(owned.Key, false)
	require.NoError(t, err)
	require.Zero(t, warm.RemainQuota)
	// A late async counter update may leave a stale quota with a perfectly
	// current, valid permission MAC. Inject that actual Redis counter only;
	// the preceding grant also advanced the permission revision.
	cacheKey := "token:" + common.GenerateHMAC(owned.Key)
	require.NoError(t, rdb.HSet(t.Context(), cacheKey, "RemainQuota", 100).Err())
	cached, err := model.GetTokenByKey(owned.Key, false)
	require.NoError(t, err)
	require.Equal(t, 100, cached.RemainQuota, "must be cache hit, not DB raw quota")
	require.Equal(t, warm.CacheRevision, cached.CacheRevision)
	get("/internal-test", owned, http.StatusNoContent)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", owned.Id).
		UpdateColumn("cache_revision", gorm.Expr("cache_revision + 1")).Error)
	get("/internal-test", owned, http.StatusUnauthorized)
	// Restore only this synthetic failure injection. Later tests still use
	// the authentic permissions and stale quota counter.
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", owned.Id).
		UpdateColumn("cache_revision", cached.CacheRevision).Error)
	common.RedisEnabled = false

	// Consume ALL idle into one real canonical hold. Redis still holds a
	// valid permission MAC with a stale positive counter, but the scoped request must fail.
	binding := model.LaneBillingChildDevBinding{
		Owner: model.LaneBillingOwnerIdentity{RequestID: "auth-available-all-held", LaneID: "auth-available-lane", Epoch: 1, UserID: user.Id,
			TokenID: owned.Id, TokenFingerprint: id.TokenFingerprint},
		WalletEpoch: 5, WalletSlot: 4, KeyEpoch: id.Epoch, KeySlot: 2, ChannelID: 800,
		FundingPolicySHA256: strings.Repeat("a", 64), FrozenPriceSHA256: strings.Repeat("b", 64), RouteSHA256: strings.Repeat("c", 64), FinalWireSHA256: strings.Repeat("d", 64),
	}
	_, err = model.ReserveLaneBillingChildDev(t.Context(), db, binding, 100)
	require.NoError(t, err)
	common.RedisEnabled = true
	stale, err := model.GetTokenByKey(owned.Key, false)
	require.NoError(t, err)
	require.Equal(t, 100, stale.RemainQuota, "exercise stale positive Redis, not a DB cache miss")
	get("/internal-test", owned, http.StatusUnauthorized)
	require.NoError(t, db.First(&stored, owned.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, stored.Status)
	require.Zero(t, stored.RemainQuota)
	common.RedisEnabled = false
	get("/internal-test", owned, http.StatusUnauthorized)
	require.NoError(t, db.First(&stored, owned.Id).Error)
	require.Equal(t, common.TokenStatusEnabled, stored.Status, "scoped denial cannot call raw exhaustion writer")

	// A proof failure cannot fall back even when raw quota is again positive.
	require.NoError(t, db.Exec("DELETE FROM lane_billing_key_transfers_v1 WHERE token_id = ?", owned.Id).Error)
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", owned.Id).UpdateColumn("remain_quota", 7).Error)
	get("/internal-test", owned, http.StatusUnauthorized)
	require.NoError(t, db.First(&stored, owned.Id).Error)
	require.Equal(t, 7, stored.RemainQuota)
	require.Equal(t, common.TokenStatusEnabled, stored.Status)
}
