package common

import (
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"
)

// parseRedisOptions keeps the v8 wire protocol, timeouts and retry budget while
// moving to v9. Explicit URL values (including TLS and legacy duration aliases)
// remain authoritative. Unsupported URL options fail rather than being ignored.
func parseRedisOptions(connectionString string) (*redis.Options, error) {
	opt, err := redis.ParseURL(connectionString)
	if err != nil {
		// ParseURL errors may contain credentials from a malformed URL.
		return nil, errors.New("invalid Redis connection string or unsupported option")
	}
	if opt.Protocol == 0 {
		opt.Protocol = 2
	}
	if opt.Protocol != 2 && opt.Protocol != 3 {
		return nil, errors.New("Redis protocol must be 2 or 3")
	}
	opt.ContextTimeoutEnabled = true
	opt.DisableIdentity = true
	opt.DialerRetries = 1 // v8 made one dial attempt per command attempt.
	opt.MaintNotificationsConfig = &maintnotifications.Config{Mode: maintnotifications.ModeDisabled}
	if opt.ReadTimeout == 0 {
		opt.ReadTimeout = 3 * time.Second
	}
	if opt.PoolTimeout == 0 {
		// A disabled socket deadline still has the former one-second pool wait.
		opt.PoolTimeout = max(opt.ReadTimeout, 0) + time.Second
	}
	if opt.MinRetryBackoff == 0 {
		opt.MinRetryBackoff = 8 * time.Millisecond
	}
	if opt.MaxRetryBackoff == 0 {
		opt.MaxRetryBackoff = 512 * time.Millisecond
	}
	if opt.ConnMaxIdleTime == 0 {
		opt.ConnMaxIdleTime = 5 * time.Minute
	}
	// Do not implicitly multiply per-connection buffers during a client upgrade.
	opt.ReadBufferSize = 4096
	opt.WriteBufferSize = 4096
	return opt, nil
}
