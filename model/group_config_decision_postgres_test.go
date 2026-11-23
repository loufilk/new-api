package model

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func TestDeferredGroupConfigDecisionPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_MODEL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable loopback PostgreSQL 18.6")
	}
	// TestMain refuses non-loopback DSNs and validates native PG versions.
	require.True(t, common.UsingMainDatabase(common.DatabaseTypePostgreSQL))
	setupGroupRenameTest(t)
	admit := func(t *testing.T) *gin.Context {
		t.Helper()
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
		current, err := BeginGroupConfigRead(c.Request.Context())
		require.NoError(t, err)
		require.NoError(t, current.Finish())
		c.Set(GroupConfigVersionContextKey, current.Version)
		return c
	}

	t.Run("only explicitly admitted decisions omit the redundant opening query", func(t *testing.T) {
		ordinary, optedIn := admit(t), admit(t)
		PermitDeferredGroupConfigDecision(optedIn)
		reads := 0
		const hook = "count_deferred_group_config_primary_reads"
		require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table == "options" {
				reads++
			}
		}))
		t.Cleanup(func() { require.NoError(t, DB.Callback().Query().Remove(hook)) })

		conventional, err := BeginRequestGroupConfigDecision(ordinary)
		require.NoError(t, err)
		defer conventional.Close()
		require.Equal(t, 1, reads)
		require.NoError(t, conventional.Finish())
		require.Equal(t, 2, reads)

		decision, err := BeginRequestGroupConfigDecision(optedIn)
		require.NoError(t, err)
		defer decision.Close()
		require.Equal(t, 2, reads)
		require.NoError(t, decision.Finish())
		require.Equal(t, 3, reads)
		// The marker is not permission to weaken any other existing fence.
		critical, err := BeginRequestGroupConfigRead(optedIn)
		require.NoError(t, err)
		defer critical.Close()
		require.NoError(t, critical.Finish())
		require.Equal(t, 5, reads)
	})

	t.Run("fresh primary closing check sees a missed remote invalidation", func(t *testing.T) {
		remote, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
		require.NoError(t, err)
		conn, err := remote.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		advance := func(value string) {
			t.Helper()
			// Use another connection pool, without our runtime lock or Pub/Sub.
			op := Option{Key: groupConfigVersionOptionKey, Value: value}
			require.NoError(t, remote.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value"}),
			}).Create(&op).Error)
		}

		inFlight := admit(t)
		PermitDeferredGroupConfigDecision(inFlight)
		guard, err := BeginRequestGroupConfigDecision(inFlight)
		require.NoError(t, err)
		defer guard.Close()
		advance("1")
		require.ErrorIs(t, guard.Finish(), ErrGroupConfigChanged)
		// Even if the commit was before opening the next guard, and an
		// earlier checkpoint passed, no request's old decision may escape.
		guard, err = BeginRequestGroupConfigDecision(inFlight)
		require.NoError(t, err)
		defer guard.Close()
		require.ErrorIs(t, guard.Finish(), ErrGroupConfigChanged)
		require.ErrorIs(t, CheckRequestGroupConfigVersion(inFlight), ErrGroupConfigChanged)

		fresh := admit(t)
		PermitDeferredGroupConfigDecision(fresh)
		guard, err = BeginRequestGroupConfigDecision(fresh)
		require.NoError(t, err)
		defer guard.Close()
		require.EqualValues(t, 1, guard.Version)
		require.NoError(t, guard.Finish())
		advance("2")
		guard, err = BeginRequestGroupConfigDecision(fresh)
		require.NoError(t, err)
		defer guard.Close()
		require.ErrorIs(t, guard.Finish(), ErrGroupConfigChanged, "one successful read cannot authorize a later decision")
	})

	t.Run("invalid admission, cancellation, and primary errors never authorize", func(t *testing.T) {
		c := admit(t)
		PermitDeferredGroupConfigDecision(c)
		c.Set(GroupConfigVersionContextKey, int64(0))
		_, err := BeginRequestGroupConfigDecision(c)
		require.ErrorIs(t, err, ErrGroupConfigChanged)

		c = admit(t)
		PermitDeferredGroupConfigDecision(c)
		cancelCtx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		c.Request = c.Request.WithContext(cancelCtx)
		guard, err := BeginRequestGroupConfigDecision(c)
		require.NoError(t, err)
		defer guard.Close()
		cancel()
		require.ErrorIs(t, guard.Finish(), context.Canceled)
		_, err = BeginRequestGroupConfigDecision(c)
		require.ErrorIs(t, err, context.Canceled)

		c = admit(t)
		PermitDeferredGroupConfigDecision(c)
		const hook = "reject_deferred_group_config_primary_read"
		failure := errors.New("disposable primary read failure")
		require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
			if tx.Statement.Table == "options" {
				tx.AddError(failure)
			}
		}))
		t.Cleanup(func() { require.NoError(t, DB.Callback().Query().Remove(hook)) })
		guard, err = BeginRequestGroupConfigDecision(c)
		require.NoError(t, err)
		defer guard.Close()
		require.ErrorIs(t, guard.Finish(), failure)
	})
}
