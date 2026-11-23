//go:build enterprise_lane_lab

package model

import (
	"errors"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func enterpriseLaneTokenFixture(t *testing.T) (Token, EnterpriseLaneIdentity) {
	t.Helper()
	if !common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		t.Skip("requires disposable loopback PostgreSQL TEST_MODEL_POSTGRES_DSN")
	}
	suffix := common.GetUUID()[:8]
	user := User{Username: "lane-key-" + suffix, AffCode: "lane-key-" + suffix, Password: "not-a-live-credential"}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{UserId: user.Id, Name: "lane test", Key: "synthetic-enterprise-lane-token-" + suffix, Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000, UsedQuota: 100}
	require.NoError(t, DB.Create(&token).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Unscoped().Delete(&Token{}, token.Id).Error)
		require.NoError(t, DB.Unscoped().Delete(&User{}, user.Id).Error)
	})
	return token, EnterpriseLaneIdentity{LaneID: "lab-" + suffix, Epoch: 1, UserID: user.Id, TokenID: token.Id, TokenFingerprint: BillingTokenFingerprint(token.Key)}
}

func TestEnterpriseLaneTokenEscrowAndReleasePostgres(t *testing.T) {
	token, identity := enterpriseLaneTokenFixture(t)
	escrow := func(amount int) error {
		return DB.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
			if err := LockFinancialUserTx(tx, token.UserId); err != nil {
				return err
			}
			return escrowEnterpriseLaneTokenTx(tx, identity, amount)
		})
	}
	read := func() Token {
		var got Token
		require.NoError(t, DB.Unscoped().First(&got, token.Id).Error)
		return got
	}
	require.NoError(t, escrow(700))
	escrowed := read()
	assert.Equal(t, 300, escrowed.RemainQuota)
	assert.Equal(t, 100, escrowed.UsedQuota, "escrow is not spend")
	assert.Equal(t, token.Status, escrowed.Status)
	require.ErrorIs(t, escrow(301), ErrBillingTokenInsufficient)
	tooLargeLegacy, err := TryReserveTokenQuota(token.Id, token.Key, 301, false)
	require.NoError(t, err)
	assert.False(t, tooLargeLegacy, "ordinary traffic cannot spend the extracted balance")
	legacySpent, err := TryReserveTokenQuota(token.Id, token.Key, 20, false)
	require.NoError(t, err)
	assert.True(t, legacySpent, "the laboratory helpers do not switch live key routing")
	require.NoError(t, DB.Delete(&Token{}, token.Id).Error)
	rollback := errors.New("synthetic drain rollback")
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error {
		if err := releaseEnterpriseLaneTokenTx(tx, identity, 450, 250); err != nil {
			return err
		}
		return rollback
	}), rollback)
	assert.Equal(t, 280, read().RemainQuota)
	assert.Equal(t, 120, read().UsedQuota)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return releaseEnterpriseLaneTokenTx(tx, identity, 450, 250)
	}))
	released := read()
	assert.Equal(t, 730, released.RemainQuota)
	assert.Equal(t, 370, released.UsedQuota)
	assert.Equal(t, token.RemainQuota+token.UsedQuota, released.RemainQuota+released.UsedQuota)
	assert.True(t, released.DeletedAt.Valid, "quota projection must not revive a revoked key")
}

func TestEnterpriseLaneTokenRejectsIdentityStatusAndBoundsPostgres(t *testing.T) {
	token, identity := enterpriseLaneTokenFixture(t)
	escrow := func(i EnterpriseLaneIdentity, amount int) error {
		return DB.Transaction(func(tx *gorm.DB) error { return escrowEnterpriseLaneTokenTx(tx, i, amount) })
	}
	release := func(i EnterpriseLaneIdentity, remaining, spent int) error {
		return DB.Transaction(func(tx *gorm.DB) error { return releaseEnterpriseLaneTokenTx(tx, i, remaining, spent) })
	}
	wrong := identity
	wrong.UserID++
	require.ErrorIs(t, escrow(wrong, 1), errEnterpriseLaneTokenIdentity)
	require.ErrorIs(t, release(wrong, 1, 0), errEnterpriseLaneTokenIdentity)
	wrong = identity
	wrong.TokenFingerprint = BillingTokenFingerprint("synthetic replacement key")
	require.ErrorIs(t, escrow(wrong, 1), errEnterpriseLaneTokenIdentity)
	require.ErrorIs(t, release(wrong, 1, 0), errEnterpriseLaneTokenIdentity)
	require.Error(t, escrow(identity, -1))
	require.Error(t, release(identity, 1, -1))
	require.Error(t, release(identity, common.MaxWalletQuota, 1))
	change := func(v map[string]any) {
		require.NoError(t, DB.Model(&Token{}).Where("id = ?", token.Id).Updates(v).Error)
	}
	change(map[string]any{"unlimited_quota": true})
	require.ErrorIs(t, escrow(identity, 1), errEnterpriseLaneTokenIdentity)
	change(map[string]any{"unlimited_quota": false, "status": common.TokenStatusDisabled})
	require.ErrorIs(t, escrow(identity, 1), errEnterpriseLaneTokenIdentity)
	change(map[string]any{"status": common.TokenStatusEnabled, "expired_time": common.GetTimestamp() - 1})
	require.ErrorIs(t, escrow(identity, 1), errEnterpriseLaneTokenIdentity)
	change(map[string]any{"expired_time": -1, "remain_quota": math.MaxInt, "used_quota": math.MaxInt})
	require.Error(t, release(identity, 1, 0))
	require.Error(t, release(identity, 0, 1))
	var got Token
	require.NoError(t, DB.First(&got, token.Id).Error)
	assert.Equal(t, math.MaxInt, got.RemainQuota)
	assert.Equal(t, math.MaxInt, got.UsedQuota)
	assert.Equal(t, token.CacheRevision, got.CacheRevision)
	require.NoError(t, DB.Unscoped().Delete(&Token{}, token.Id).Error)
	require.ErrorIs(t, release(identity, 1, 0), errEnterpriseLaneTokenIdentity)
}

func TestEnterpriseLaneTokenFenceRejectsStalePublicationAndRecoversRollbackPostgres(t *testing.T) {
	nativeAuthCacheFixture(t)
	token, identity := enterpriseLaneTokenFixture(t)
	stale, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	rollback := errors.New("synthetic grant rollback")
	require.ErrorIs(t, DB.Transaction(func(tx *gorm.DB) error {
		if err := escrowEnterpriseLaneTokenTx(tx, identity, 300); err != nil {
			return err
		}
		return rollback
	}), rollback)
	current, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 1000, current.RemainQuota)
	assert.Equal(t, stale.CacheRevision, current.CacheRevision)
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return escrowEnterpriseLaneTokenTx(tx, identity, 300)
	}))
	_, err = cacheInitToken(*stale)
	require.NoError(t, err)
	current, err = GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 700, current.RemainQuota)
	assert.Equal(t, 100, current.UsedQuota)
	assert.Greater(t, current.CacheRevision, stale.CacheRevision)
}
