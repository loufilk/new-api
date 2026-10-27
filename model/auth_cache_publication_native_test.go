package model

import (
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This adversarial ordering was found during independent review after the
// initial green suite: an empty nonce is not a publication lease across flush.
func TestAuthCacheNativeDelayedPublicationAcrossFlush(t *testing.T) {
	for _, entity := range []string{"token", "user"} {
		t.Run(entity, func(t *testing.T) {
			nativeAuthCacheFixture(t)
			user := nativeAuthUser(t, "enterprise")
			token := nativeAuthToken(t, user)
			_, err := GetUserCache(user.Id)
			require.NoError(t, err)
			delay := &nativeDelayedAuthority{match: "ARGV[20] == '1'"}
			if entity == "user" {
				delay.match = "ARGV[14] == '1'"
			}
			common.RDB.AddHook(delay)
			delay.armed.Store(true)
			_, tokenFloor := tokenPermissionKeys(token.Key)
			_, userFloor := userPermissionKeys(user.Id)
			require.NoError(t, common.RDB.Del(t.Context(), getTokenCacheKey(token.Key), getUserCacheKey(user.Id), tokenFloor, userFloor).Err())
			if entity == "token" {
				_, err = GetTokenByKey(token.Key, false)
			} else {
				_, err = GetUserCache(user.Id)
			}
			require.NoError(t, err)
			require.NotEmpty(t, delay.command)
			token.Status = common.TokenStatusDisabled
			require.NoError(t, token.Update())
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return updateUserGroupTx(tx, user.Id, "default") }))
			_, err = GetTokenByKey(token.Key, false)
			require.NoError(t, err)
			_, err = GetUserCache(user.Id)
			require.NoError(t, err)
			require.NoError(t, common.RDB.FlushDB(t.Context()).Err())
			result, err := common.RDB.Do(t.Context(), delay.command...).Int()
			require.NoError(t, err)
			assert.Equal(t, -1, result, "flush destroys the old publication lease; a delayed command must not re-seed it")
			gotToken, err := GetTokenByKey(token.Key, false)
			require.NoError(t, err)
			gotUser, err := GetUserCache(user.Id)
			require.NoError(t, err)
			assert.Equal(t, common.TokenStatusDisabled, gotToken.Status)
			assert.Equal(t, "default", gotUser.Group)
		})
	}
}

func TestAuthCacheNativeSidebarInitializationCannotRestoreAuthority(t *testing.T) {
	for _, mode := range []string{"finish_insert", "oauth_finalize"} {
		t.Run(mode, func(t *testing.T) {
			nativeAuthCacheFixture(t)
			user := nativeAuthUser(t, "enterprise")
			// Synthetic pre-revocation role; no genuine user/admin identity is used.
			user.Role = common.RoleAdminUser
			require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Update("role", user.Role).Error)
			read, resume := make(chan struct{}), make(chan struct{})
			var intercepted atomic.Bool
			const callback = "native:sidebar_snapshot_barrier"
			require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "users" && intercepted.CompareAndSwap(false, true) {
					close(read)
					<-resume
				}
			}))
			t.Cleanup(func() { _ = DB.Callback().Query().Remove(callback) })
			done := make(chan struct{})
			go func() {
				if mode == "finish_insert" {
					user.finishInsert(0)
				} else {
					user.FinalizeOAuthUserCreation(0)
				}
				close(done)
			}()
			<-read
			revoked := User{Id: user.Id, Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusDisabled}
			require.NoError(t, revoked.Update(false))
			close(resume)
			<-done
			var current User
			require.NoError(t, DB.First(&current, user.Id).Error)
			assert.Equal(t, "default", current.Group)
			assert.Equal(t, common.RoleCommonUser, current.Role)
			assert.Equal(t, common.UserStatusDisabled, current.Status)
			assert.Equal(t, revoked.AuthVersion, current.AuthVersion, "a setting-only background task must not restore credentials or bump sessions")
			assert.Equal(t, revoked.CacheRevision, current.CacheRevision)
			assert.NotEmpty(t, current.GetSetting().SidebarModules)
		})
	}
}
