package wallet

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/moneyport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFundingReplayRestoresDebitReceiptBeforeRefund(t *testing.T) {
	db := setupWalletTestDB(t)
	user := model.User{
		Username: "wallet-replay-user", Password: "password", AffCode: "wallet-replay-aff",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default",
		Quota: 100, QuotaPaid: 0, QuotaBonus: 100,
	}
	require.NoError(t, db.Create(&user).Error)

	request := moneyport.FactoryRequest{
		UserID:       user.Id,
		RequestID:    "wallet-replay-receipt",
		AccountOrder: []string{string(model.AccountBonus), string(model.AccountPaid)},
	}
	first, err := NewFunding(request)
	require.NoError(t, err)
	require.NoError(t, first.PreConsume(100))
	assert.Equal(t, []moneyport.AccountShare{{Account: string(model.AccountBonus), Amount: 100}}, first.Receipt())

	paid, bonus, _, err := model.GetUserAccountBalances(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, paid)
	assert.Equal(t, 0, bonus)

	// Simulate reconstructing the funding object after process/session state was
	// lost. The second preconsume is a ledger replay, not another debit.
	reconstructed, err := NewFunding(request)
	require.NoError(t, err)
	require.NoError(t, reconstructed.PreConsume(100))
	assert.Equal(t, []moneyport.AccountShare{{Account: string(model.AccountBonus), Amount: 100}}, reconstructed.Receipt())

	// Repeating the same replay on the same object must not double the in-memory
	// receipt even though the ledger correctly returns the original rows again.
	require.NoError(t, reconstructed.PreConsume(100))
	assert.Equal(t, []moneyport.AccountShare{{Account: string(model.AccountBonus), Amount: 100}}, reconstructed.Receipt())

	require.NoError(t, reconstructed.Refund())
	paid, bonus, _, err = model.GetUserAccountBalances(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 0, paid, "replay refund must not move bonus quota into paid")
	assert.Equal(t, 100, bonus, "refund must restore the original debit account")
}

func TestFundingRefundFailsClosedWithoutDebitReceipt(t *testing.T) {
	setupWalletTestDB(t)
	funding := &Funding{userID: 1, requestID: "missing-receipt", consumed: 100}
	assert.ErrorIs(t, funding.Refund(), ErrMissingDebitReceipt)

	_, err := funding.SettleInTx(model.DB, -50)
	assert.ErrorIs(t, err, ErrMissingDebitReceipt)
}
