package wallet

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestWalletRefundRemainderNeverExceedsOriginalAccount(t *testing.T) {
	shares := []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountReward, Amount: 1}, {Account: model.AccountPaid, Amount: 1}}
	first, err := model.AllocateWalletRefund(shares, nil, 2)
	require.NoError(t, err)
	require.Equal(t, []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountReward, Amount: 1}}, first)
	last, err := model.AllocateWalletRefund(shares, first, 1)
	require.NoError(t, err)
	require.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 1}}, last)
	_, err = model.AllocateWalletRefund(shares, append(first, last...), 1)
	require.ErrorIs(t, err, model.ErrLedgerInvalidEntry)
}

func TestWalletRefundAllocationUsesExactWideIntegerProduct(t *testing.T) {
	// MaxWalletQuota is 2^53-1; both intermediate products exceed int64.
	// These expected integers are exact, including the one-unit remainder.
	const unit = common.MaxWalletQuota / 3
	shares := []model.LedgerShare{
		{Account: model.AccountBonus, Amount: unit},
		{Account: model.AccountReward, Amount: unit},
		{Account: model.AccountPaid, Amount: unit + 1},
	}
	refund, err := model.AllocateWalletRefund(shares, nil, common.MaxWalletQuota-1)
	require.NoError(t, err)
	require.Equal(t, []model.LedgerShare{
		{Account: model.AccountBonus, Amount: unit},
		{Account: model.AccountReward, Amount: unit},
		{Account: model.AccountPaid, Amount: unit},
	}, refund)
	last, err := model.AllocateWalletRefund(shares, refund, 1)
	require.NoError(t, err)
	require.Equal(t, []model.LedgerShare{{Account: model.AccountPaid, Amount: 1}}, last)
}

func TestWalletRefundAllocationRejectsInvalidProvenance(t *testing.T) {
	valid := []model.LedgerShare{{Account: model.AccountBonus, Amount: 2}, {Account: model.AccountPaid, Amount: 1}}
	for _, tc := range []struct {
		name     string
		original []model.LedgerShare
		prior    []model.LedgerShare
		amount   int
	}{
		{name: "missing", amount: 1},
		{name: "negative amount", original: valid, amount: -1},
		{name: "excess amount", original: valid, amount: 4},
		{name: "unknown account", original: []model.LedgerShare{{Account: "unknown", Amount: 1}}, amount: 1},
		{name: "duplicate original account", original: []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountBonus, Amount: 1}}, amount: 1},
		{name: "negative original", original: []model.LedgerShare{{Account: model.AccountBonus, Amount: -1}}, amount: 1},
		{name: "oversized original", original: []model.LedgerShare{{Account: model.AccountBonus, Amount: common.MaxWalletQuota}, {Account: model.AccountPaid, Amount: 1}}, amount: 1},
		{name: "missing prior source", original: valid, prior: []model.LedgerShare{{Account: model.AccountReward, Amount: 1}}, amount: 1},
		{name: "excess prior source", original: valid, prior: []model.LedgerShare{{Account: model.AccountPaid, Amount: 2}}, amount: 1},
		{name: "negative prior source", original: valid, prior: []model.LedgerShare{{Account: model.AccountPaid, Amount: -1}}, amount: 1},
		{name: "duplicate prior source", original: valid, prior: []model.LedgerShare{{Account: model.AccountBonus, Amount: 1}, {Account: model.AccountBonus, Amount: 1}}, amount: 1},
		{name: "excess outstanding", original: valid, prior: []model.LedgerShare{{Account: model.AccountBonus, Amount: 2}}, amount: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := model.AllocateWalletRefund(tc.original, tc.prior, tc.amount)
			require.ErrorIs(t, err, model.ErrLedgerInvalidEntry)
			require.Nil(t, got)
		})
	}
}
