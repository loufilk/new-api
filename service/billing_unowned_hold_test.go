package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Old source/token debits alone are not proof of an atomic request owner or of
// unused delivery. Both terminal APIs and task handoff must refuse to invent it.
func TestBillingUnownedHoldRequiresReview(t *testing.T) {
	if runRefundFixtureChild(t) {
		return
	}
	for _, action := range []string{"refund", "settle_zero", "settle_more", "handoff_zero", "handoff_more"} {
		t.Run(action, func(t *testing.T) {
			s := legacyRefundSession(t, "unowned-"+action, 100, 100)
			s.relayInfo.Billing = s
			s.relayInfo.BillingSource = BillingSourceWallet
			s.relayInfo.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 31203}
			seedChannel(t, 31203)
			before := ledgerRowsFor(t, 31201)
			var err error
			switch action {
			case "refund":
				s.Refund(testGinContext(t))
			case "settle_zero":
				err = s.Settle(0)
			case "settle_more":
				err = s.Settle(150)
			case "handoff_zero":
				err = PersistTaskBilling(s.relayInfo, financialTaskForSubmission(s.relayInfo, 0))
			case "handoff_more":
				err = PersistTaskBilling(s.relayInfo, financialTaskForSubmission(s.relayInfo, 150))
			}
			if action != "refund" {
				assert.Error(t, err, "missing original hold must not reach a financial terminal callback")
				code, _ := model.BillingRefundErrorCode(err)
				assert.Equal(t, "owner_missing", code, "unrelated fixture errors cannot establish the missing-owner guard")
			}
			observedSettlement := action == "settle_zero" || action == "settle_more"
			if observedSettlement {
				actual := 0
				if action == "settle_more" {
					actual = 150
				}
				assert.ErrorIs(t, s.Reserve(200), model.ErrBillingFinalized)
				assert.ErrorContains(t, s.Settle(actual+1), "replay amount mismatch")
				err := s.Settle(actual)
				require.Error(t, err, "same-amount replay cannot manufacture the missing original owner")
				code, _ := model.BillingRefundErrorCode(err)
				assert.Equal(t, "owner_missing", code)
				// Failed persistence of observed usage is not refund authority.
				// All accounting and absent-owner assertions below run after this
				// late failure-defer path as well as the explicit settlement replay.
				s.Refund(testGinContext(t))
			}
			waitRefundCallback(t, s)
			assertUserBalances(t, 31201, 0, 900, 0)
			var token model.Token
			var user model.User
			require.NoError(t, model.DB.First(&token, 31202).Error)
			require.NoError(t, model.DB.First(&user, 31201).Error)
			assert.Equal(t, 900, token.RemainQuota)
			assert.Equal(t, 100, token.UsedQuota)
			assert.Zero(t, user.UsedQuota)
			assert.Zero(t, user.RequestCount)
			assert.Equal(t, before, ledgerRowsFor(t, 31201))
			var owners, tasks int64
			require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", s.relayInfo.RequestId).Count(&owners).Error)
			require.NoError(t, model.DB.Model(&model.Task{}).Where("user_id = ?", 31201).Count(&tasks).Error)
			assert.Zero(t, owners, "provisional private claim must roll back")
			assert.Zero(t, tasks)
			if observedSettlement {
				assert.False(t, s.NeedsRefund(), "unresolved observed settlement requires review, not refund")
			} else {
				assert.True(t, s.NeedsRefund(), "retain unresolved legacy accounting for review")
			}
		})
	}
	t.Run("free_task_zero", func(t *testing.T) {
		truncate(t)
		seedBonusUser(t, 31201, 0, 0)
		seedToken(t, 31202, 31201, "unowned-free-token", 1000)
		seedChannel(t, 31203)
		info := &relaycommon.RelayInfo{UserId: 31201, TokenId: 31202, TokenKey: "unowned-free-token", RequestId: "unowned-free-task-control", BillingSource: BillingSourceWallet, ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 31203}}
		// The lower model API must also reject a nonzero task even if a caller
		// passes zero actual and a wholly zero monetary receipt.
		bad := financialTaskForSubmission(info, 1)
		called := false
		_, err := model.CommitBillingTask(t.Context(), model.BillingFinalization{RequestID: info.RequestId, UserID: info.UserId, Source: BillingSourceWallet, TokenID: info.TokenId, TokenFingerprint: model.BillingTokenFingerprint(info.TokenKey)}, 0, bad, nil, func(*gorm.DB, *model.Task) error { called = true; return nil })
		require.Error(t, err)
		code, _ := model.BillingRefundErrorCode(err)
		require.Equal(t, "owner_missing", code)
		require.False(t, called, "nonzero task must be rejected before any financial callback")
		var ownerCount int64
		require.NoError(t, model.DB.Model(&model.BillingFinalization{}).Where("request_id = ?", info.RequestId).Count(&ownerCount).Error)
		require.Zero(t, ownerCount)
		task := financialTaskForSubmission(info, 0)
		require.NoError(t, PersistTaskBilling(info, task), "genuinely free tasks do not need a monetary hold")
		require.NoError(t, PersistTaskBilling(info, task), "same free task replays without another request count")
		require.Error(t, PersistTaskBilling(info, financialTaskForSubmission(info, 0)), "another task cannot take the same free request owner")
		var owner model.BillingFinalization
		require.NoError(t, model.DB.Where("request_id = ?", info.RequestId).First(&owner).Error)
		require.Equal(t, model.BillingTaskOwned, owner.State)
		require.Zero(t, owner.ActualQuota)
		require.Zero(t, owner.SourceHeld)
		require.Zero(t, owner.TokenHeld)
		require.Zero(t, owner.PreConsumed)
		require.Empty(t, owner.Shares())
		var user model.User
		var token model.Token
		require.NoError(t, model.DB.First(&user, 31201).Error)
		require.NoError(t, model.DB.First(&token, 31202).Error)
		require.Equal(t, 1, user.RequestCount)
		require.Zero(t, user.UsedQuota)
		require.Zero(t, user.Quota)
		require.Equal(t, 1000, token.RemainQuota)
		require.Zero(t, token.UsedQuota)
		require.Empty(t, ledgerRowsFor(t, 31201))
	})
}
