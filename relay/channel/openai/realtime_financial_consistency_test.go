package openai

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	walletmodule "github.com/QuantumNous/new-api/modules/chuanapi/wallet"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var realtimeFinancialRuntimeOnce sync.Once
var realtimeFinancialRuntimeErr error
var realtimeFinancialActorID atomic.Int64
var realtimeFinancialMatrixDB *gorm.DB
var realtimeFinancialMatrixDialect common.DatabaseType

const realtimeFinancialModel = "review-realtime-financial"

// The peers are real network connections. Returning from the HTTP upgrader is
// intentional: only the relay, not this helper, owns the accepted connection.
func realtimeFinancialSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	t.Cleanup(srv.Close)
	peer, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	var relay *websocket.Conn
	select {
	case relay = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("websocket upgrade did not reach the relay endpoint")
	}
	t.Cleanup(func() { _ = relay.Close(); _ = peer.Close() })
	return relay, peer
}

func realtimeFinancialFixture(t *testing.T) (*gin.Context, context.CancelFunc, *relaycommon.RelayInfo, *websocket.Conn, *websocket.Conn) {
	t.Helper()
	realtimeFinancialRuntimeOnce.Do(func() {
		if realtimeFinancialRuntimeErr = i18n.Init(); realtimeFinancialRuntimeErr != nil {
			return
		}
		if realtimeFinancialRuntimeErr = walletmodule.RegisterRuntime(); realtimeFinancialRuntimeErr != nil {
			return
		}
		registry := moneyport.NewFactoryRegistry()
		if realtimeFinancialRuntimeErr = service.RegisterHostFundingFactories(registry); realtimeFinancialRuntimeErr != nil {
			return
		}
		if realtimeFinancialRuntimeErr = walletmodule.RegisterFundingFactory(registry); realtimeFinancialRuntimeErr != nil {
			return
		}
		realtimeFinancialRuntimeErr = service.ConfigureFundingFactoryRegistry(registry)
	})
	require.NoError(t, realtimeFinancialRuntimeErr)
	db := realtimeFinancialMatrixDB
	dialect := realtimeFinancialMatrixDialect
	if db == nil {
		var err error
		db, err = gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
		dialect = common.DatabaseTypeSQLite
	}
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	oldRedis, oldBatch, oldConsume := common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled
	oldMaster := common.IsMasterNode
	t.Setenv("LOG_SQL_DSN", "")
	model.DB, model.LOG_DB = db, db
	common.SetDatabaseTypes(dialect, dialect)
	common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = false, false, true
	common.IsMasterNode = false
	require.NoError(t, model.InitLogDB()) // Initialize dialect-specific quoted token/group columns, without production migrations.
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.IsMasterNode = false
		require.NoError(t, model.InitLogDB())
		model.LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMain, oldLog)
		common.IsMasterNode = oldMaster
		common.RedisEnabled, common.BatchUpdateEnabled, common.LogConsumeEnabled = oldRedis, oldBatch, oldConsume
		model.InvalidateModelBonusEligibleCache()
	})
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}, &model.Channel{}, &model.Log{}, &model.Ability{}, &model.BillingFinalization{}, &model.BalanceLedgerOperation{}, &model.BalanceLedger{}, &model.Model{}, &model.Vendor{}, &model.ModelBonusEligibleAudit{}, &model.ReferralCommission{}, &model.SubscriptionPlan{}, &model.UserSubscription{}, &model.SubscriptionPreConsumeRecord{}))
	for _, ratio := range []struct {
		get func() string
		set func(string) error
	}{
		{ratio_setting.ModelRatio2JSONString, ratio_setting.UpdateModelRatioByJSONString},
		{ratio_setting.CompletionRatio2JSONString, ratio_setting.UpdateCompletionRatioByJSONString},
		{ratio_setting.AudioRatio2JSONString, ratio_setting.UpdateAudioRatioByJSONString},
		{ratio_setting.AudioCompletionRatio2JSONString, ratio_setting.UpdateAudioCompletionRatioByJSONString},
	} {
		old, setter := ratio.get(), ratio.set
		t.Cleanup(func() { require.NoError(t, setter(old)) })
		require.NoError(t, setter(`{"`+realtimeFinancialModel+`":1}`))
	}
	id := int(realtimeFinancialActorID.Add(1)) + 87000
	key := fmt.Sprintf("synthetic-realtime-%d", id)
	require.NoError(t, db.Create(&model.User{Id: id, Username: key, AffCode: key, Quota: 1000, QuotaPaid: 1000, Group: "default"}).Error)
	require.NoError(t, db.Create(&model.Token{Id: id, UserId: id, Key: key, RemainQuota: 1000, Status: common.TokenStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: id, Name: key}).Error)
	c, engine := gin.CreateTestContext(httptest.NewRecorder())
	engine.ContextWithFallback = true
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/realtime", nil).WithContext(ctx)
	client, clientPeer := realtimeFinancialSocketPair(t)
	target, targetPeer := realtimeFinancialSocketPair(t)
	info := &relaycommon.RelayInfo{
		UserId: id, TokenId: id, TokenKey: key,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: id, UpstreamModelName: realtimeFinancialModel},
		RequestId:   key, OriginModelName: realtimeFinancialModel, BillingModelName: realtimeFinancialModel,
		UserGroup: "default", UsingGroup: "default", ForcePreConsume: true,
		UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1},
		PriceData:   hosttypes.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1}},
		StartTime:   time.Now(), ClientWs: client, TargetWs: target,
	}
	require.Nil(t, service.PreConsumeBilling(c, 100, info))
	require.Equal(t, 100, info.Billing.GetPreConsumedQuota())
	return c, cancel, info, clientPeer, targetPeer
}

type realtimeFinancialResult struct {
	err   *types.NewAPIError
	usage *dto.RealtimeUsage
}

func realtimeFinancialStart(t *testing.T, c *gin.Context, info *relaycommon.RelayInfo) <-chan realtimeFinancialResult {
	t.Helper()
	done := make(chan realtimeFinancialResult, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		err, usage := OpenaiRealtimeHandler(c, info)
		done <- realtimeFinancialResult{err, usage}
	}()
	t.Cleanup(func() {
		_ = info.ClientWs.Close()
		_ = info.TargetWs.Close()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Error("realtime fixture could not join the handler before restoring its SQL globals")
		}
	})
	return done
}

func realtimeFinancialWait(t *testing.T, done <-chan realtimeFinancialResult) realtimeFinancialResult {
	t.Helper()
	select {
	case result := <-done:
		require.Nil(t, result.err, "WssHelper only reaches PostWssConsumeQuota when the adaptor returns no API error")
		require.NotNil(t, result.usage)
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("realtime handler did not stop its readers")
		return realtimeFinancialResult{}
	}
}

func realtimeFinancialTextUsage(n int) *dto.RealtimeUsage {
	return &dto.RealtimeUsage{TotalTokens: n, InputTokens: n, InputTokenDetails: dto.InputTokenDetails{TextTokens: n}}
}

func realtimeFinancialSend(t *testing.T, peer *websocket.Conn, usage *dto.RealtimeUsage) {
	t.Helper()
	require.NoError(t, peer.WriteJSON(dto.RealtimeEvent{Type: dto.RealtimeEventTypeResponseDone, Response: &dto.RealtimeResponse{Usage: usage}}))
}

func realtimeFinancialReadForwarded(t *testing.T, peer *websocket.Conn) {
	t.Helper()
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, message, err := peer.ReadMessage()
	require.NoError(t, err)
	require.Contains(t, string(message), "response.done")
}

func realtimeFinancialAssertPaid(t *testing.T, info *relaycommon.RelayInfo, actual int) {
	t.Helper()
	var user model.User
	var token model.Token
	var channel model.Channel
	require.NoError(t, model.DB.First(&user, info.UserId).Error)
	require.NoError(t, model.DB.First(&token, info.TokenId).Error)
	require.NoError(t, model.DB.First(&channel, info.ChannelId).Error)
	require.Equal(t, 1000-actual, user.QuotaPaid, "initial hold + segment reserves must net to actual once")
	require.Equal(t, 1000-actual, user.Quota)
	require.Equal(t, 1000-actual, token.RemainQuota)
	require.Equal(t, actual, token.UsedQuota)
	require.Equal(t, actual, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.EqualValues(t, actual, channel.UsedQuota)
	require.False(t, info.Billing.NeedsRefund())
	var ledgerNet int64
	require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("user_id = ?", info.UserId).Select("COALESCE(SUM(delta), 0)").Scan(&ledgerNet).Error)
	require.EqualValues(t, -actual, ledgerNet)
}

func TestRealtimeFinancialConsistency(t *testing.T) {
	t.Run("multiple_response_done", func(t *testing.T) {
		c, _, info, client, upstream := realtimeFinancialFixture(t)
		done := realtimeFinancialStart(t, c, info)
		for _, n := range []int{20, 30} {
			realtimeFinancialSend(t, upstream, realtimeFinancialTextUsage(n))
			realtimeFinancialReadForwarded(t, client)
		}
		require.NoError(t, upstream.Close())
		result := realtimeFinancialWait(t, done)
		require.Equal(t, 50, result.usage.TotalTokens)
		require.Equal(t, 50, result.usage.InputTokenDetails.TextTokens)
		service.PostWssConsumeQuota(c, info, realtimeFinancialModel, result.usage, nil)
		realtimeFinancialAssertPaid(t, info, 50)
	})
	t.Run("segment_sql_failure_observed_once", func(t *testing.T) {
		c, _, info, _, upstream := realtimeFinancialFixture(t)
		var hits atomic.Int64
		const callback = "review:realtime-segment-token-failure"
		require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "tokens" {
				hits.Add(1)
				tx.AddError(errors.New("injected realtime segment failure before token SQL"))
			}
		}))
		t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(callback) })
		done := realtimeFinancialStart(t, c, info)
		realtimeFinancialSend(t, upstream, realtimeFinancialTextUsage(20))
		result := realtimeFinancialWait(t, done)
		require.EqualValues(t, 1, hits.Load(), "a failed reservation must not retry by re-observing the same segment")
		require.Equal(t, 20, result.usage.TotalTokens)
		require.Equal(t, 20, result.usage.InputTokenDetails.TextTokens)
		require.NoError(t, model.DB.Callback().Update().Remove(callback))
		service.PostWssConsumeQuota(c, info, realtimeFinancialModel, result.usage, nil)
		realtimeFinancialAssertPaid(t, info, 20)
	})
	t.Run("tiered_audio_and_cache_facts", func(t *testing.T) {
		c, _, info, client, upstream := realtimeFinancialFixture(t)
		expr := `tier("actual", p + c + ai * 2 + ao * 3 + cr * 0.5)`
		info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1, QuotaPerUnit: 1_000_000}
		done := realtimeFinancialStart(t, c, info)
		usage := &dto.RealtimeUsage{TotalTokens: 150, InputTokens: 100, OutputTokens: 50,
			InputTokenDetails:  dto.InputTokenDetails{TextTokens: 60, AudioTokens: 40, CachedTokens: 20},
			OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 40, AudioTokens: 10}}
		realtimeFinancialSend(t, upstream, usage)
		realtimeFinancialReadForwarded(t, client)
		require.NoError(t, upstream.Close())
		result := realtimeFinancialWait(t, done)
		wantJSON, err := common.Marshal(usage)
		require.NoError(t, err)
		gotJSON, err := common.Marshal(result.usage)
		require.NoError(t, err)
		require.JSONEq(t, string(wantJSON), string(gotJSON), "preserve provider counters without constraining private normalized billing state")
		service.PostWssConsumeQuota(c, info, realtimeFinancialModel, result.usage, nil)
		// Opt-in normalization: P=100-40-20, C=50-10, AI=40, AO=10, CR=20.
		realtimeFinancialAssertPaid(t, info, 200)
	})
	t.Run("tiered_image_cache_presence", func(t *testing.T) {
		c, _, info, client, upstream := realtimeFinancialFixture(t)
		expr := `tier("actual", p + c + img * 2 + img_o * 3 + img_cr * 0.25 + cr * 0.5 + cc * 4)`
		info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1, QuotaPerUnit: 1_000_000}
		cachedText, cachedImage, explicitAudioZero := 10, 20, 0
		usage := &dto.RealtimeUsage{TotalTokens: 120, InputTokens: 100, OutputTokens: 20,
			InputTokenDetails: dto.InputTokenDetails{TextTokens: 60, ImageTokens: 40, CachedTokens: 30,
				CachedTokensDetails:  &dto.CachedTokenDetails{TextTokens: &cachedText, ImageTokens: &cachedImage, AudioTokens: &explicitAudioZero},
				CachedCreationTokens: 3, CacheWriteTokens: 5},
			OutputTokenDetails: dto.OutputTokenDetails{TextTokens: 10, ImageTokens: 10, ReasoningTokens: 2}}
		done := realtimeFinancialStart(t, c, info)
		for range 2 {
			realtimeFinancialSend(t, upstream, usage)
			realtimeFinancialReadForwarded(t, client)
		}
		require.NoError(t, upstream.Close())
		result := realtimeFinancialWait(t, done)
		require.Equal(t, 240, result.usage.TotalTokens)
		require.Equal(t, 200, result.usage.InputTokens)
		require.Equal(t, 40, result.usage.OutputTokens)
		require.Equal(t, 80, result.usage.InputTokenDetails.ImageTokens)
		require.Equal(t, 60, result.usage.InputTokenDetails.CachedTokens)
		require.Equal(t, 10, result.usage.InputTokenDetails.CacheCreationTokensTotal())
		details := result.usage.InputTokenDetails.CachedTokensDetails
		require.NotNil(t, details)
		require.NotNil(t, details.TextTokens)
		require.NotNil(t, details.ImageTokens)
		require.NotNil(t, details.AudioTokens, "explicit zero must not become an unreported modality")
		require.Equal(t, 20, *details.TextTokens)
		require.Equal(t, 40, *details.ImageTokens)
		require.Zero(t, *details.AudioTokens)
		require.Equal(t, 20, result.usage.OutputTokenDetails.ImageTokens)
		require.Equal(t, 4, result.usage.OutputTokenDetails.ReasoningTokens)
		service.PostWssConsumeQuota(c, info, realtimeFinancialModel, result.usage, nil)
		// Per segment: P=45,C=10,Img=20,ImgCR=20,CR=10,ImgO=10,CC=5 => 155.
		realtimeFinancialAssertPaid(t, info, 310)
	})
	t.Run("tiered_mixed_cache_presence", func(t *testing.T) {
		for _, knownFirst := range []bool{true, false} {
			name := "missing_first"
			if knownFirst {
				name = "known_first"
			}
			t.Run(name, func(t *testing.T) {
				c, _, info, client, upstream := realtimeFinancialFixture(t)
				expr := `tier("actual", p + c + img * 2 + img_cr * 0.25 + cr * 0.5)`
				info.TieredBillingSnapshot = &billingexpr.BillingSnapshot{BillingMode: "tiered_expr", ExprString: expr, ExprHash: billingexpr.ExprHashString(expr), GroupRatio: 1, QuotaPerUnit: 1_000_000}
				cachedImage := 10
				known := &dto.RealtimeUsage{TotalTokens: 40, InputTokens: 40,
					InputTokenDetails: dto.InputTokenDetails{TextTokens: 30, ImageTokens: 10, CachedTokens: 10,
						CachedTokensDetails: &dto.CachedTokenDetails{ImageTokens: &cachedImage}}}
				unknown := &dto.RealtimeUsage{TotalTokens: 40, InputTokens: 40,
					InputTokenDetails: dto.InputTokenDetails{TextTokens: 30, ImageTokens: 10, CachedTokens: 10}}
				segments := []*dto.RealtimeUsage{unknown, known}
				if knownFirst {
					segments = []*dto.RealtimeUsage{known, unknown}
				}
				done := realtimeFinancialStart(t, c, info)
				for _, segment := range segments {
					realtimeFinancialSend(t, upstream, segment)
					realtimeFinancialReadForwarded(t, client)
				}
				require.NoError(t, upstream.Close())
				result := realtimeFinancialWait(t, done)
				require.Equal(t, 80, result.usage.InputTokens)
				require.Equal(t, 20, result.usage.InputTokenDetails.ImageTokens)
				require.Equal(t, 20, result.usage.InputTokenDetails.CachedTokens)
				if details := result.usage.InputTokenDetails.CachedTokensDetails; details != nil {
					require.Nil(t, details.ImageTokens, "one unreported segment cannot become a complete cached-image measurement")
				}
				service.PostWssConsumeQuota(c, info, realtimeFinancialModel, result.usage, nil)
				// Existing missing-img_cr fallback: CR=20, Img=20, P=80-20-20.
				// A partial pointer sum of 10 would instead invent a 77.5 charge.
				realtimeFinancialAssertPaid(t, info, 90)
			})
		}
	})
}

func realtimeFinancialAssertSocketClosed(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err := conn.ReadMessage()
	require.Error(t, err, "handler return must terminate both reader sockets")
	var timeout net.Error
	if errors.As(err, &timeout) {
		require.False(t, timeout.Timeout(), "the handler returned while the peer's reader remained live")
	}
}

func TestRealtimeFinancialTermination(t *testing.T) {
	for _, termination := range []string{"request_cancel", "upstream_close", "client_close"} {
		t.Run(termination, func(t *testing.T) {
			c, cancel, info, client, upstream := realtimeFinancialFixture(t)
			done := realtimeFinancialStart(t, c, info)
			realtimeFinancialSend(t, upstream, realtimeFinancialTextUsage(7))
			realtimeFinancialReadForwarded(t, client)
			switch termination {
			case "request_cancel":
				cancel()
			case "upstream_close":
				require.NoError(t, upstream.Close())
			case "client_close":
				require.NoError(t, client.Close())
			}
			result := realtimeFinancialWait(t, done)
			require.Equal(t, 7, result.usage.TotalTokens)
			if termination != "client_close" {
				realtimeFinancialAssertSocketClosed(t, client)
			}
			if termination != "upstream_close" {
				realtimeFinancialAssertSocketClosed(t, upstream)
			}
			frozen := *result.usage
			_ = upstream.WriteJSON(dto.RealtimeEvent{Type: dto.RealtimeEventTypeResponseDone, Response: &dto.RealtimeResponse{Usage: realtimeFinancialTextUsage(900)}})
			require.Equal(t, frozen, *result.usage, "a returned usage snapshot cannot change after a late peer write")
			service.PostWssConsumeQuota(c, info, realtimeFinancialModel, result.usage, nil)
			realtimeFinancialAssertPaid(t, info, 7)
		})
	}
}

// Opt-ins name fresh, dedicated databases, never generic production/test DSNs.
func TestRealtimeFinancialConsistencyDatabaseMatrix(t *testing.T) {
	for _, dialect := range []struct {
		name common.DatabaseType
		env  string
	}{
		{common.DatabaseTypeSQLite, ""}, {common.DatabaseTypeMySQL, "TEST_FINALIZATION_REALTIME_MYSQL_DSN"}, {common.DatabaseTypePostgreSQL, "TEST_FINALIZATION_REALTIME_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.name), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect.name == common.DatabaseTypeMySQL {
				driver = mysql.Open(dsn)
			}
			if dialect.name == common.DatabaseTypePostgreSQL {
				driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			tables, err := db.Migrator().GetTables()
			require.NoError(t, err)
			require.Empty(t, tables, "refusing nonempty disposable realtime database")
			realtimeFinancialMatrixDB, realtimeFinancialMatrixDialect = db, dialect.name
			t.Cleanup(func() { realtimeFinancialMatrixDB = nil; realtimeFinancialMatrixDialect = "" })
			t.Run("financial", TestRealtimeFinancialConsistency)
			t.Run("termination", TestRealtimeFinancialTermination)
		})
	}
}
