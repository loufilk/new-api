package model

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func tokenMutationFixture(t *testing.T, tokenHeld int) BillingFinalization {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "token-mutation.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	oldDB, oldRedis := DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB, common.RedisEnabled = db, false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() { DB, common.RedisEnabled = oldDB, oldRedis; common.SetDatabaseTypes(oldMain, oldLog) })
	require.NoError(t, db.AutoMigrate(&User{}, &Token{}, &BalanceLedgerOperation{}, &BalanceLedger{}, &BillingFinalization{}))
	require.NoError(t, db.Create(&User{Id: 93001, Username: "token-mutation", AffCode: "token-mutation", Quota: 1000, QuotaPaid: 1000}).Error)
	_, err = DebitQuota(LedgerEntry{UserId: 93001, Delta: 100, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: "token-mutation", RequestId: "token-mutation", IdempotencyKey: "billing:preconsume:token-mutation"}, AccountPaid)
	require.NoError(t, err)
	r := BillingFinalization{RequestID: "token-mutation", UserID: 93001, Source: "wallet", SourceHeld: 100, PreConsumed: 100, Paid: 100}
	if tokenHeld > 0 {
		token := Token{Id: 93002, UserId: r.UserID, Key: "synthetic-deadline-token", Status: common.TokenStatusDisabled, RemainQuota: 1000 - tokenHeld, UsedQuota: tokenHeld}
		require.NoError(t, db.Create(&token).Error)
		r.TokenID, r.TokenHeld, r.TokenFingerprint = token.Id, tokenHeld, BillingTokenFingerprint(token.Key)
	}
	return r
}

func TestBillingTokenContextPreservesFence(t *testing.T) {
	r := tokenMutationFixture(t, 100)
	server := useUserCacheMiniRedis(t)
	var oldToken Token
	require.NoError(t, DB.First(&oldToken, r.TokenID).Error)
	_, err := GetTokenByKey(oldToken.Key, true)
	require.NoError(t, err)
	require.True(t, server.Exists(getTokenCacheKey(oldToken.Key)))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return ApplyTokenQuotaDeltaTx(tx, r.TokenID, "synthetic-deadline-token", r.TokenHeld)
	}))
	assert.True(t, server.Exists(getTokenCacheFenceKey(oldToken.Key)))
	assert.False(t, server.Exists(getTokenCacheKey(oldToken.Key)))
	published, err := cacheInitToken(oldToken)
	require.NoError(t, err)
	assert.Zero(t, published, "pre-refund snapshot cannot repopulate a fenced token cache")
	var token Token
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
}

func TestBillingTokenRedisErrorRetainsLegacyLiveContextPolicy(t *testing.T) {
	r := tokenMutationFixture(t, 100)
	server := useUserCacheMiniRedis(t)
	server.SetError("ERR synthetic Redis unavailable")
	// Historically a Redis command error is logged, while the authoritative
	// database transaction continues. Context propagation does not change that
	// policy; an expired transaction context is a different, rollback case.
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ApplyTokenQuotaDeltaTx(tx, r.TokenID, "synthetic-deadline-token", r.TokenHeld)
	}))

	var token Token
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 1000, token.RemainQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
}

// Covers the nonrefund token transaction: an absolute deadline can pass before
// a context implementation notifies Err/Done. Official ForRefund is not used.
type tokenDelayedDeadlineContext struct {
	context.Context
	deadline time.Time
}

func (c tokenDelayedDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }

func TestBillingTokenExpiredDeadlineBeforeNotificationRollsBackSource(t *testing.T) {
	r := tokenMutationFixture(t, 100)
	ctx := tokenDelayedDeadlineContext{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	require.NoError(t, ctx.Err())
	require.Nil(t, ctx.Done())
	sourceTouched := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, err := CreditQuotaSharesTx(tx, LedgerEntry{UserId: r.UserID, Reason: LedgerReasonBillingSettle, ReferenceType: "relay", ReferenceId: r.RequestID, RequestId: r.RequestID, IdempotencyKey: "billing:settle:expired-absolute-deadline"}, []LedgerShare{{Account: AccountPaid, Amount: 100}})
		if err != nil {
			return err
		}
		sourceTouched = true
		return ApplyTokenQuotaDeltaTx(tx, r.TokenID, "synthetic-deadline-token", 100)
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, sourceTouched, "failure must come from the token deadline check after real source SQL")
	var user User
	require.NoError(t, DB.First(&user, r.UserID).Error)
	assert.Equal(t, 900, user.QuotaPaid)
	assert.Equal(t, 900, user.Quota)
	var token Token
	require.NoError(t, DB.First(&token, r.TokenID).Error)
	assert.Equal(t, 900, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
	assert.Equal(t, common.TokenStatusDisabled, token.Status)
	var count int64
	require.NoError(t, DB.Model(&BalanceLedgerOperation{}).Where("idempotency_key = ?", "billing:settle:expired-absolute-deadline").Count(&count).Error)
	assert.Zero(t, count)
}
