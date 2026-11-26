package controller

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// A subprocess has its own package-level mutex, just like a second API node.
// Both nodes enter their first write before either can create an option row.
func TestModelPricingFirstWritersPostgres(t *testing.T) {
	if name := os.Getenv("NEWAPI_TEST_FIRST_PRICE_MODEL"); name != "" {
		u, err := url.Parse(os.Getenv("SQL_DSN"))
		require.NoError(t, err)
		require.True(t, net.ParseIP(u.Hostname()).IsLoopback() && strings.HasPrefix(u.Path, "/newapi_audit_"), "first writer must use the loopback database created by this test")
		require.Equal(t, "model-pricing-first-writer", u.Query().Get("application_name"))
		common.IsMasterNode = false
		common.RedisEnabled = false
		common.OptionMap = make(map[string]string)
		require.NoError(t, model.InitDB())
		model.LOG_DB = model.DB
		err = model.UpdateModelPricing([]model.ModelPricingChange{{
			ModelName: name, ExpectedVersion: model.ModelPricingVersion(model.PricingValues{}),
			Pricing: model.PricingValues{"ModelPrice": float64(17)},
		}})
		if errors.Is(err, model.ErrModelPricingConflict) {
			t.Log("EXPECTED_STALE_VERSION")
			return
		}
		require.NoError(t, err)
		t.Log("PRICE_COMMITTED")
		return
	}
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set TEST_POSTGRES_DSN to a disposable loopback PostgreSQL instance")
	}
	for _, tc := range []struct {
		label     string
		names     [2]string
		conflicts int
	}{
		{"distinct_models", [2]string{"qa-first-a", "qa-first-b"}, 0},
		{"same_model", [2]string{"qa-first-a", "qa-first-a"}, 1},
	} {
		t.Run(tc.label, func(t *testing.T) {
			db, isolatedDSN := newAuditTestDatabase(t, "postgres", dsn)
			require.NoError(t, db.AutoMigrate(&model.Option{}, &model.SystemSyncTopic{}))
			// SHARE is compatible with both SELECTs (including FOR UPDATE), but
			// blocks INSERT until both writers are poised to create missing rows.
			gate := db.Begin()
			require.NoError(t, gate.Error)
			defer gate.Rollback()
			require.NoError(t, gate.Exec("LOCK TABLE options IN SHARE MODE").Error)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var logs [2]bytes.Buffer
			var cmds [2]*exec.Cmd
			for i, name := range tc.names {
				u, err := url.Parse(isolatedDSN)
				require.NoError(t, err)
				q := u.Query()
				q.Set("application_name", "model-pricing-first-writer")
				u.RawQuery = q.Encode()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModelPricingFirstWritersPostgres$", "-test.v")
				cmd.Env = append(os.Environ(), "SQL_DSN="+u.String(), "LOG_SQL_DSN=", "NEWAPI_TEST_FIRST_PRICE_MODEL="+name)
				cmd.Stdout, cmd.Stderr = &logs[i], &logs[i]
				cmds[i] = cmd
				require.NoError(t, cmd.Start())
			}
			require.Eventually(t, func() bool {
				var count int
				return db.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname=current_database()
					AND application_name='model-pricing-first-writer' AND wait_event_type='Lock'
					AND query LIKE 'INSERT INTO "options"%'`).Scan(&count).Error == nil && count == 2
			}, 15*time.Second, 20*time.Millisecond)
			require.NoError(t, gate.Commit().Error)
			conflicts := 0
			for i, cmd := range cmds {
				require.NoError(t, cmd.Wait(), logs[i].String())
				if bytes.Contains(logs[i].Bytes(), []byte("EXPECTED_STALE_VERSION")) {
					conflicts++
				} else {
					require.Contains(t, logs[i].String(), "PRICE_COMMITTED")
				}
			}
			require.Equal(t, tc.conflicts, conflicts, "one stale writer must not overwrite the winner")
			var values map[string]float64
			var option model.Option
			require.NoError(t, db.Where("key = ?", "ModelPrice").First(&option).Error)
			require.NoError(t, common.UnmarshalJsonStr(option.Value, &values))
			for _, name := range tc.names {
				require.Equal(t, float64(17), values[name], "acknowledged price vanished for %q", name)
			}
		})
	}
}
