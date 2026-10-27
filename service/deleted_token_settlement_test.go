package service

import (
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWalletSettlementSurvivesTokenDeletion(t *testing.T) {
	for _, deletion := range []string{"soft", "hard"} {
		for _, amount := range []struct {
			name            string
			reserve, actual int
			trust           bool
		}{
			{"trusted", 1000, 3000, true}, {"charge", 1000, 3000, false}, {"refund", 3000, 1000, false},
		} {
			t.Run(deletion+"/"+amount.name, func(t *testing.T) {
				truncate(t)
				const userID, tokenID = 29801, 29802
				const tokenKey = "synthetic-deleted-token-settlement"
				seedBonusEligibleModel(t, "deleted-token-model", boolPtr(false))
				seedBonusUser(t, userID, 0, 10000)
				seedToken(t, tokenID, userID, tokenKey, 10000)
				quotaSetting := operation_setting.GetQuotaSetting()
				originalTrust := quotaSetting.TrustQuotaUSD
				t.Cleanup(func() { quotaSetting.TrustQuotaUSD = originalTrust })
				quotaSetting.TrustQuotaUSD = 0
				if amount.trust {
					quotaSetting.TrustQuotaUSD = 0.001
				}
				c := testGinContext(t)
				c.Set("token_quota", 10000)
				info := &relaycommon.RelayInfo{UserId: userID, TokenId: tokenID, TokenKey: tokenKey, RequestId: "settle-" + deletion + "-" + amount.name, OriginModelName: "deleted-token-model", UserSetting: dto.UserSetting{BillingPreference: "wallet_only"}}
				session, apiErr := NewBillingSession(c, info, amount.reserve)
				require.Nil(t, apiErr)
				if amount.trust {
					require.Zero(t, session.GetPreConsumedQuota())
				} else {
					require.Equal(t, amount.reserve, session.GetPreConsumedQuota())
				}
				require.NoError(t, model.DeleteTokenById(tokenID, userID))
				if deletion == "hard" {
					require.NoError(t, model.DB.Unscoped().Delete(&model.Token{}, tokenID).Error)
				}
				var wg sync.WaitGroup
				results := make(chan error, 2)
				for range 2 {
					wg.Go(func() { results <- session.Settle(amount.actual) })
				}
				wg.Wait()
				close(results)
				for err := range results {
					require.NoError(t, err)
				}
				require.NoError(t, session.Settle(amount.actual))
				session.Refund(c)
				assertUserBalances(t, userID, 0, 10000-amount.actual, 0)
				var token model.Token
				if deletion == "soft" {
					require.NoError(t, model.DB.Unscoped().First(&token, tokenID).Error)
					assert.True(t, token.DeletedAt.Valid)
					assert.Equal(t, 10000-amount.actual, token.RemainQuota)
					assert.Equal(t, amount.actual, token.UsedQuota)
					_, err := model.GetTokenById(tokenID)
					require.Error(t, err)
				} else {
					var count int64
					require.NoError(t, model.DB.Unscoped().Model(&model.Token{}).Where("id = ?", tokenID).Count(&count).Error)
					assert.Zero(t, count)
				}
				assert.False(t, session.NeedsRefund())
			})
		}
	}
}

func TestWalletSettlementRejectsChangedTokenIdentity(t *testing.T) {
	for _, mismatch := range []string{"owner", "credential"} {
		t.Run(mismatch, func(t *testing.T) {
			truncate(t)
			require.NoError(t, model.DB.AutoMigrate(&model.Ability{}))
			seedBonusEligibleModel(t, "changed-token-model", boolPtr(false))
			seedBonusUser(t, 29801, 0, 10000)
			seedToken(t, 29802, 29801, "original-key", 10000)
			c := testGinContext(t)
			c.Set("token_quota", 10000)
			info := &relaycommon.RelayInfo{UserId: 29801, TokenId: 29802, TokenKey: "original-key", RequestId: "changed-token-" + mismatch, OriginModelName: "changed-token-model", ForcePreConsume: true, UserSetting: dto.UserSetting{BillingPreference: "wallet_only", QuotaWarningThreshold: -1}}
			session, apiErr := NewBillingSession(c, info, 0)
			require.Nil(t, apiErr)
			info.Billing = session
			var held model.BillingFinalization
			require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&held).Error)
			require.Equal(t, model.BillingHeld, held.State)
			require.Zero(t, held.SourceHeld)
			require.Zero(t, held.TokenHeld)
			require.Equal(t, model.BillingTokenFingerprint("original-key"), held.TokenFingerprint)
			update := map[string]any{"user_id": 29803}
			if mismatch == "credential" {
				update = map[string]any{"key": "replacement-key"}
			}
			require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", 29802).Updates(update).Error)
			require.NoError(t, model.DB.Delete(&model.Token{}, 29802).Error)
			err := session.Settle(3000)
			require.ErrorContains(t, err, "identity mismatch")
			code, _ := model.BillingRefundErrorCode(err)
			require.Equal(t, "identity_mismatch", code, "the real held owner must reach the original-token guard")
			assertUserBalances(t, 29801, 0, 10000, 0)
			assert.Empty(t, ledgerRowsFor(t, 29801))
			var token model.Token
			require.NoError(t, model.DB.Unscoped().First(&token, 29802).Error)
			assert.Equal(t, 10000, token.RemainQuota)
			assert.Zero(t, token.UsedQuota)
			require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&held).Error)
			assert.Equal(t, model.BillingHeld, held.State, "identity failure cannot publish a terminal owner")
		})
	}
}
