package wallet

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFundingReplayRestoresPreconsumeAndReserveReceipts(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "wallet-reserve-replay", Password: "password", AffCode: "wallet-reserve-replay-aff",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		Quota: 200, QuotaPaid: 100, QuotaBonus: 100,
	}
	require.NoError(t, db.Create(&user).Error)

	request := moneyport.FactoryRequest{
		UserID:       user.Id,
		RequestID:    "wallet-pre-reserve-replay",
		AccountOrder: []string{string(model.AccountBonus), string(model.AccountPaid)},
	}
	first, err := NewFunding(request)
	require.NoError(t, err)
	require.NoError(t, first.PreConsume(100))
	require.NoError(t, first.Reserve(200))
	expectedReceipt := []moneyport.AccountShare{
		{Account: string(model.AccountBonus), Amount: 100},
		{Account: string(model.AccountPaid), Amount: 100},
	}
	assert.Equal(t, expectedReceipt, first.Receipt())

	reconstructed, err := NewFunding(request)
	require.NoError(t, err)
	require.NoError(t, reconstructed.PreConsume(100))
	require.NoError(t, reconstructed.Reserve(200))
	assert.Equal(t, expectedReceipt, reconstructed.Receipt(), "each persisted debit operation must hydrate exactly once")

	require.NoError(t, reconstructed.Reserve(200))
	assert.Equal(t, expectedReceipt, reconstructed.Receipt(), "same-object replay/no-op must not double the receipt")

	require.NoError(t, reconstructed.Refund())
	paid, bonus, _, err := model.GetUserAccountBalances(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 100, bonus)
	assert.Equal(t, 100, paid, "reserve replay refund must restore the paid share too")
}

func TestFundingReplayRestoresMultipleReserveOperations(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "wallet-multi-reserve-replay", Password: "password", AffCode: "wallet-multi-reserve-replay-aff",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		Quota: 150, QuotaPaid: 100, QuotaBonus: 50,
	}
	require.NoError(t, db.Create(&user).Error)

	request := moneyport.FactoryRequest{
		UserID:       user.Id,
		RequestID:    "wallet-multi-reserve-replay",
		AccountOrder: []string{string(model.AccountBonus), string(model.AccountPaid)},
	}
	first, err := NewFunding(request)
	require.NoError(t, err)
	require.NoError(t, first.PreConsume(50))
	require.NoError(t, first.Reserve(100))
	require.NoError(t, first.Reserve(150))

	reconstructed, err := NewFunding(request)
	require.NoError(t, err)
	require.NoError(t, reconstructed.PreConsume(50))
	require.NoError(t, reconstructed.Reserve(100))
	require.NoError(t, reconstructed.Reserve(150))
	assert.Equal(t, first.Receipt(), reconstructed.Receipt())
	assert.Equal(t, []moneyport.AccountShare{
		{Account: string(model.AccountBonus), Amount: 50},
		{Account: string(model.AccountPaid), Amount: 100},
	}, reconstructed.Receipt())
}

func TestFundingRefundFailsClosedWhenReceiptDoesNotCoverConsumedQuota(t *testing.T) {
	setupWalletTestDB(t)
	funding := &Funding{
		userID:    1,
		requestID: "short-receipt",
		consumed:  100,
		debitOrder: []model.LedgerAccount{
			model.AccountBonus,
			model.AccountPaid,
		},
		debitShares: map[model.LedgerAccount]int{model.AccountBonus: 50},
	}
	assert.ErrorIs(t, funding.Refund(), ErrMissingDebitReceipt)
}
