package model

import (
	"context"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// RefreshUserQuotaCacheAfterBillingFinalization invalidates only derived data,
// after the transaction has returned (including an uncertain COMMIT result).
// Its independent budget must survive expiry of the transaction/request context.
// Callers may therefore spend up to 500 ms beyond their transaction budget.
// The two client DEL invocations retain go-redis's existing transport retries.
// This is bounded, synchronous best-effort compensation, NOT a durable retry
// queue: a process exit or exhausted attempts can leave the cache stale until a
// later invalidation/expiry. Profile refreshes can extend the existing TTL, so
// expiry is not a fixed staleness bound. No error here can reopen finalization.
func RefreshUserQuotaCacheAfterBillingFinalization(userID int) {
	if !common.RedisEnabled || userID <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var err error
	attempts := 0
	for range 2 {
		attempts++
		attemptCtx, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		err = common.RDB.Del(attemptCtx, getUserCacheKey(userID)).Err()
		stop()
		if err == nil {
			return
		}
		if attempts == 2 || ctx.Err() != nil {
			break
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	// Raw Redis errors can contain credentials or server-controlled text. Keep
	// this signal separate from financial retry/failure metadata and outcomes.
	code, _ := BillingRefundErrorCode(err)
	common.SysError(fmt.Sprintf("billing user cache invalidation exhausted: user_id=%d attempts=%d code=%s cache_only=true", userID, attempts, code))
}
