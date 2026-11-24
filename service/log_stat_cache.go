package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

// Only display statistics use this cache. Authentication, range validation,
// pricing and all balance/settlement decisions must stay outside it.
const (
	selfLogHistoryTTL = 5 * time.Second
	selfLogRateTTL    = time.Second
)

type selfLogStatKey struct {
	filter       [32]byte
	logDB        *gorm.DB
	role         int
	maxRange     int64
	defaultStart bool
	rates        bool
}

type selfLogStatEntry struct {
	data    model.Stat
	expires time.Time
}

type selfLogStatWaiter struct {
	ctx context.Context
}

type selfLogStatBuild struct {
	done      chan struct{}
	cancel    context.CancelFunc
	waiters   map[*selfLogStatWaiter]struct{}
	abandoned bool
	data      model.Stat
	err       error
}

// Both retained keys and concurrently running database builds are bounded.
// At most 128 waiter contexts are retained per build. Other callers wait for
// completion/admission with a deadline in their existing request goroutines.
// A canceled leader does not
// cancel other callers, but the last departing waiter cancels the SQL work.
type selfLogStatCache struct {
	mu                    sync.Mutex
	entries               map[selfLogStatKey]selfLogStatEntry
	builds                map[selfLogStatKey]*selfLogStatBuild
	changed               chan struct{}
	maxEntries, maxBuilds int
	timeout               time.Duration
	now                   func() time.Time
}

func newSelfLogStatCache(maxEntries, maxBuilds int, timeout time.Duration) *selfLogStatCache {
	return &selfLogStatCache{
		entries: make(map[selfLogStatKey]selfLogStatEntry),
		builds:  make(map[selfLogStatKey]*selfLogStatBuild),
		changed: make(chan struct{}), maxEntries: maxEntries, maxBuilds: maxBuilds,
		timeout: timeout, now: time.Now,
	}
}

var selfLogStats = newSelfLogStatCache(2048, 32, 15*time.Second)

func (cache *selfLogStatCache) putLocked(key selfLogStatKey, data model.Stat, expires time.Time) {
	now := cache.now()
	if !now.Before(expires) {
		return
	}
	if current, ok := cache.entries[key]; ok && !current.expires.Before(expires) {
		return // A slower, older snapshot cannot overwrite a newer one.
	}
	if _, exists := cache.entries[key]; !exists && len(cache.entries) >= cache.maxEntries {
		// Bound eviction work as well as memory; never scan the log table or an
		// unbounded key set to find room. Prefer the earliest-expiring entry.
		var oldest selfLogStatKey
		var earliest time.Time
		for candidate, entry := range cache.entries {
			if earliest.IsZero() || entry.expires.Before(earliest) {
				oldest, earliest = candidate, entry.expires
			}
		}
		delete(cache.entries, oldest)
	}
	cache.entries[key] = selfLogStatEntry{data: data, expires: expires}
}

func (cache *selfLogStatCache) get(ctx context.Context, key selfLogStatKey, ttl time.Duration, seedRate *selfLogStatKey, load func(context.Context, time.Time) (model.Stat, error)) (model.Stat, error) {
	// Admission and duplicate waiting have a deadline too, not only SQL builds.
	ctx, cancel := context.WithTimeout(ctx, cache.timeout)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return model.Stat{}, err
		}
		cache.mu.Lock()
		if entry, ok := cache.entries[key]; ok && cache.now().Before(entry.expires) {
			cache.mu.Unlock()
			return entry.data, nil
		}
		build := cache.builds[key]
		if (build != nil && (build.abandoned || len(build.waiters) >= 128)) || (build == nil && len(cache.builds) >= cache.maxBuilds) {
			changed := cache.changed
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return model.Stat{}, ctx.Err()
			case <-changed:
				continue
			}
		}
		if build == nil {
			buildCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cache.timeout)
			build = &selfLogStatBuild{done: make(chan struct{}), cancel: cancel, waiters: make(map[*selfLogStatWaiter]struct{})}
			cache.builds[key] = build
			sampledAt := cache.now()
			go cache.run(buildCtx, key, build, ttl, sampledAt, seedRate, load)
		}
		waiter := &selfLogStatWaiter{ctx: ctx}
		build.waiters[waiter] = struct{}{}
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			cache.mu.Lock()
			delete(build.waiters, waiter)
			if len(build.waiters) == 0 {
				build.abandoned = true
				build.cancel()
			}
			cache.mu.Unlock()
			return model.Stat{}, ctx.Err()
		case <-build.done:
			if err := ctx.Err(); err != nil {
				return model.Stat{}, err
			}
			return build.data, build.err
		}
	}
}

func (cache *selfLogStatCache) run(ctx context.Context, key selfLogStatKey, build *selfLogStatBuild, ttl time.Duration, sampledAt time.Time, seedRate *selfLogStatKey, load func(context.Context, time.Time) (model.Stat, error)) {
	data, err := load(ctx, sampledAt)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err == nil {
		err = ctx.Err()
	}
	activeWaiter := false
	for waiter := range build.waiters {
		if waiter.ctx.Err() == nil {
			activeWaiter = true
			break
		}
	}
	if err == nil && !build.abandoned && activeWaiter {
		cache.putLocked(key, data, sampledAt.Add(ttl))
		if seedRate != nil {
			cache.putLocked(*seedRate, model.Stat{Rpm: data.Rpm, Tpm: data.Tpm}, sampledAt.Add(selfLogRateTTL))
		}
	}
	build.data, build.err = data, err
	build.cancel()
	delete(cache.builds, key)
	close(build.done)
	close(cache.changed)
	cache.changed = make(chan struct{})
}

// Hash parsed SQL filter values with byte-exact length framing. JSON replaces
// invalid UTF-8 and would alias a rejected raw filter with valid U+FFFD text.
func selfLogFilterHash(filter model.LogFilter) [32]byte {
	hash := sha256.New()
	var word [8]byte
	for _, value := range []int64{int64(filter.UserId), int64(filter.Type), filter.StartTimestamp, filter.EndTimestamp, int64(filter.Channel)} {
		binary.LittleEndian.PutUint64(word[:], uint64(value))
		_, _ = hash.Write(word[:])
	}
	for _, value := range []string{filter.ModelName, filter.Username, filter.TokenName, filter.Group, filter.RequestId, filter.UpstreamRequestId} {
		binary.LittleEndian.PutUint64(word[:], uint64(len(value)))
		_, _ = hash.Write(word[:])
		_, _ = hash.Write([]byte(value))
	}
	return [32]byte(hash.Sum(nil))
}

// GetSelfLogStat keeps the selected historical quota separate from rolling
// last-minute rates. A cold history read retains the existing combined query
// and seeds the rate cache. PostgreSQL needs one cold statement when the build
// finishes within the rate TTL; a slower build must refresh rates once more.
// role/maxRange identify the range policy checked by the caller on EVERY request;
// they are not cached authorization. Explicit timestamps are never rounded.
func GetSelfLogStat(ctx context.Context, filter model.LogFilter, role int, maxRange int64, defaultStart bool) (model.Stat, error) {
	if filter.UserId <= 0 || model.LOG_DB == nil {
		return model.Stat{}, common.NewMessage("Unauthorized, invalid user info")
	}
	filter.Type, filter.SkipTotal = model.LogTypeConsume, false
	keyFilter := filter
	if defaultStart {
		keyFilter.StartTimestamp = 0 // semantic rolling range, not the raw URL
	}
	key := selfLogStatKey{filter: selfLogFilterHash(keyFilter), logDB: model.LOG_DB, role: role, maxRange: maxRange, defaultStart: defaultStart}
	rateFilter := filter
	rateFilter.StartTimestamp, rateFilter.EndTimestamp = 0, 0
	rateKey := selfLogStatKey{filter: selfLogFilterHash(rateFilter), logDB: key.logDB, role: role, maxRange: maxRange, rates: true}
	historical, err := selfLogStats.get(ctx, key, selfLogHistoryTTL, &rateKey, func(buildCtx context.Context, sampledAt time.Time) (model.Stat, error) {
		queryFilter := filter
		if defaultStart {
			end := sampledAt.Unix()
			if queryFilter.EndTimestamp != 0 {
				end = min(end, queryFilter.EndTimestamp)
			}
			queryFilter.StartTimestamp = math.MinInt64
			if end >= math.MinInt64+maxRange {
				queryFilter.StartTimestamp = end - maxRange
			}
		}
		return model.SumUsedQuotaAt(key.logDB.WithContext(buildCtx), queryFilter, sampledAt)
	})
	if err != nil {
		return model.Stat{}, err
	}
	rates, err := selfLogStats.get(ctx, rateKey, selfLogRateTTL, nil, func(buildCtx context.Context, sampledAt time.Time) (model.Stat, error) {
		return model.SumRecentLogRateAt(key.logDB.WithContext(buildCtx), rateFilter, sampledAt)
	})
	if err != nil {
		return model.Stat{}, err
	}
	return model.Stat{Quota: historical.Quota, Rpm: rates.Rpm, Tpm: rates.Tpm}, nil
}
