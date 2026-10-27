// Package dbpool validates bounded SQL connection settings independently of
// the ORM and database dialect. Values are per pool, not a global DB budget.
package dbpool

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	MaxOpen  int
	MaxIdle  int
	Lifetime time.Duration
	IdleTime time.Duration
}

// Load retains SQL_* overrides. A separately configured log DB can override
// them with LOG_SQL_*. An unset/empty log value inherits the corresponding
// SQL value. Zero explicitly disables idle connections or lifetime limits;
// zero MaxOpen is rejected rather than silently enabling an unlimited pool.
func Load(lookup func(string) (string, bool), logDB bool) (Config, error) {
	values := []struct {
		key      string
		fallback int64
		minimum  int64
		maximum  int64
	}{
		{"SQL_MAX_OPEN_CONNS", 100, 1, math.MaxInt32},
		{"SQL_MAX_IDLE_CONNS", 20, 0, math.MaxInt32},
		{"SQL_MAX_LIFETIME", 1800, 0, math.MaxInt64 / int64(time.Second)},
		{"SQL_MAX_IDLE_TIME", 300, 0, math.MaxInt64 / int64(time.Second)},
	}
	parsed := make([]int64, len(values))
	for i, field := range values {
		key := field.key
		raw, set := lookup(key)
		if logDB {
			if override, ok := lookup("LOG_" + key); ok && strings.TrimSpace(override) != "" {
				key, raw, set = "LOG_"+key, override, true
			}
		}
		value := field.fallback
		if set && strings.TrimSpace(raw) != "" {
			var err error
			value, err = strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil || value < field.minimum || value > field.maximum {
				return Config{}, fmt.Errorf("%s must be an integer between %d and %d", key, field.minimum, field.maximum)
			}
		}
		parsed[i] = value
	}
	// database/sql already clips idle capacity to the open capacity. Make the
	// same adjustment visible in Config and diagnostics, including inherited
	// log settings with a smaller per-log-DB open limit.
	return Config{
		MaxOpen: int(parsed[0]), MaxIdle: int(min(parsed[1], parsed[0])),
		Lifetime: time.Duration(parsed[2]) * time.Second,
		IdleTime: time.Duration(parsed[3]) * time.Second,
	}, nil
}

func (c Config) Apply(db *sql.DB) {
	db.SetMaxOpenConns(c.MaxOpen)
	db.SetMaxIdleConns(c.MaxIdle)
	db.SetConnMaxLifetime(c.Lifetime)
	db.SetConnMaxIdleTime(c.IdleTime)
}
