package service

import (
	"fmt"
	"net"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain enforces a disposable loopback TEST_SERVICE_POSTGRES_DSN. Use a
// separate disposable loopback Redis as well, never a shared cache database.
func TestTrustedWalletReservationCachePostgres(t *testing.T) {
	dsn := os.Getenv("TEST_REDIS_DSN")
	if os.Getenv("TEST_SERVICE_POSTGRES_DSN") == "" || dsn == "" || !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable loopback TEST_SERVICE_POSTGRES_DSN and TEST_REDIS_DSN")
	}
	opts, err := redis.ParseURL(dsn)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(opts.Addr)
	require.NoError(t, err)
	require.True(t, net.ParseIP(host).IsLoopback(), "Redis must be loopback")
	client := redis.NewClient(opts)
	require.NoError(t, client.Ping(t.Context()).Err())
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	common.RDB, common.RedisEnabled = client, true
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = oldClient, oldEnabled
		require.NoError(t, client.Close())
	})

	for index, tc := range []struct {
		name    string
		trusted bool
		hold    int
		replay  bool
		lostACK bool
	}{
		{name: "fresh_trusted_and_replay", trusted: true, replay: true},
		{name: "positive_hold", hold: 100},
		{name: "untrusted_zero"},
		{name: "trusted_zero_lost_commit_ack", trusted: true, lostACK: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncate(t)
			userID, tokenID := 98200+index, 98300+index
			cacheKey := fmt.Sprintf("user:%d", userID)
			credential := fmt.Sprintf("synthetic-reservation-cache-%d", index)
			requestID := "reservation-cache-" + tc.name
			seedUser(t, userID, 1000)
			seedToken(t, tokenID, userID, credential, 1000)
			seedBonusEligibleModel(t, "reservation-cache-model", boolPtr(false))
			oldTrust := operation_setting.GetQuotaSetting().TrustQuotaUSD
			t.Cleanup(func() { operation_setting.GetQuotaSetting().TrustQuotaUSD = oldTrust })
			operation_setting.GetQuotaSetting().TrustQuotaUSD = 0
			if tc.trusted {
				operation_setting.GetQuotaSetting().TrustQuotaUSD = 0.0001
			}
			profile, err := model.GetUserCache(userID)
			require.NoError(t, err)
			require.Equal(t, 1000, profile.Quota)
			before, err := client.HGetAll(t.Context(), cacheKey).Result()
			require.NoError(t, err)
			require.NotEmpty(t, before["CacheMAC"], "assert against a valid warmed profile, not a dummy hash")

			if tc.lostACK {
				// Both the hold and its compensating refund admission actually
				// commit, but neither ACK reaches the caller. This keeps the
				// worker from running and masking the host invalidation below.
				pool := initialReservationCommitLoss(t, 2)
				t.Cleanup(func() { assert.GreaterOrEqual(t, pool.commits.Load(), int32(2)) })
			}
			c := testGinContext(t)
			c.Set("token_quota", 1000)
			newRelayInfo := func() *relaycommon.RelayInfo {
				return &relaycommon.RelayInfo{UserId: userID, TokenId: tokenID, TokenKey: credential, RequestId: requestID,
					OriginModelName: "reservation-cache-model", UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
			}
			requestedHold := tc.hold
			if tc.trusted {
				requestedHold = 100 // trust, not a free model, reduces the hold to zero
			}
			s, apiErr := NewBillingSession(c, newRelayInfo(), requestedHold)
			owner := lifecycleOwner(t, requestID)
			if tc.lostACK {
				require.Nil(t, s)
				require.NotNil(t, apiErr)
				require.ErrorContains(t, apiErr.Err, "synthetic initial COMMIT acknowledgement lost")
				assert.Equal(t, model.BillingRefundReady, owner.State)
			} else {
				require.Nil(t, apiErr)
				require.Equal(t, tc.trusted, s.trusted)
				assert.Equal(t, model.BillingHeld, owner.State)
				assert.Equal(t, tc.hold, s.GetPreConsumedQuota())
			}
			assert.Equal(t, tc.hold, owner.SourceHeld)
			assert.Equal(t, tc.hold, owner.TokenHeld)
			after, err := client.HGetAll(t.Context(), cacheKey).Result()
			require.NoError(t, err)
			if tc.trusted && !tc.lostACK {
				require.Equal(t, before, after, "fresh zero hold must not evict the existing authenticated profile/quota cache")
			} else {
				require.Empty(t, after, "nonzero, untrusted or uncertain holds retain prior invalidation")
			}

			if tc.replay {
				replayed, apiErr := NewBillingSession(c, newRelayInfo(), requestedHold)
				require.Nil(t, apiErr)
				require.NotNil(t, replayed)
				assert.Equal(t, owner, lifecycleOwner(t, requestID))
				got, err := client.Exists(t.Context(), cacheKey).Result()
				require.NoError(t, err)
				assert.Zero(t, got, "a replayed zero hold must retain prior cache invalidation")
			}
			assert.Equal(t, 1000-tc.hold, getUserBalances(t, userID).Quota)
			assert.Equal(t, 1000-tc.hold, getTokenRemainQuota(t, tokenID))
			var ledgerNet int
			require.NoError(t, model.DB.Model(&model.BalanceLedger{}).Where("request_id = ?", requestID).Select("COALESCE(SUM(delta), 0)").Scan(&ledgerNet).Error)
			assert.Equal(t, -tc.hold, ledgerNet)
		})
	}
}
