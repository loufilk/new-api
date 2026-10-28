package dbpool

import (
	"testing"
	"time"
)

func TestPoolSettingsAndLogOverrides(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values map[string]string
		log    bool
		want   Config
	}{
		{"bounded defaults", nil, false, Config{100, 20, 30 * time.Minute, 5 * time.Minute}},
		{"legacy explicit overrides", map[string]string{"SQL_MAX_OPEN_CONNS": "80", "SQL_MAX_IDLE_CONNS": "10", "SQL_MAX_LIFETIME": "60"}, false, Config{80, 10, time.Minute, 5 * time.Minute}},
		{"independent log pool", map[string]string{"SQL_MAX_OPEN_CONNS": "80", "SQL_MAX_LIFETIME": "600", "LOG_SQL_MAX_OPEN_CONNS": "12", "LOG_SQL_MAX_IDLE_CONNS": "4", "LOG_SQL_MAX_IDLE_TIME": "20"}, true, Config{12, 4, 10 * time.Minute, 20 * time.Second}},
		{"log inherits missing values", map[string]string{"SQL_MAX_OPEN_CONNS": "8", "SQL_MAX_IDLE_CONNS": "4", "LOG_SQL_MAX_OPEN_CONNS": "", "LOG_SQL_MAX_IDLE_CONNS": "0"}, true, Config{8, 0, 30 * time.Minute, 5 * time.Minute}},
		{"log settings do not affect main", map[string]string{"LOG_SQL_MAX_OPEN_CONNS": "2"}, false, Config{100, 20, 30 * time.Minute, 5 * time.Minute}},
		{"idle capacity cannot exceed open", map[string]string{"SQL_MAX_OPEN_CONNS": "3"}, false, Config{3, 3, 30 * time.Minute, 5 * time.Minute}},
		{"explicit zero idle and lifetimes", map[string]string{"SQL_MAX_IDLE_CONNS": "0", "SQL_MAX_LIFETIME": "0", "SQL_MAX_IDLE_TIME": "0"}, false, Config{100, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Load(func(k string) (string, bool) { v, ok := tc.values[k]; return v, ok }, tc.log)
			if err != nil || got != tc.want {
				t.Fatalf("got %#v, %v; want %#v", got, err, tc.want)
			}
		})
	}
}

func TestPoolRejectsInvalidLimitsAndDurationOverflow(t *testing.T) {
	for _, key := range []string{"SQL_MAX_OPEN_CONNS", "SQL_MAX_IDLE_CONNS", "SQL_MAX_LIFETIME", "SQL_MAX_IDLE_TIME", "LOG_SQL_MAX_OPEN_CONNS", "LOG_SQL_MAX_LIFETIME"} {
		for _, invalid := range []string{"-1", "bad", "1.5", "9223372036854775807", "999999999999999999999999999"} {
			t.Run(key+"/"+invalid, func(t *testing.T) {
				_, err := Load(func(k string) (string, bool) { return invalid, k == key }, true)
				if err == nil {
					t.Fatal("invalid value accepted")
				}
			})
		}
	}
	_, err := Load(func(k string) (string, bool) { return "0", k == "SQL_MAX_OPEN_CONNS" }, false)
	if err == nil {
		t.Fatal("unlimited pool unexpectedly accepted")
	}
}
