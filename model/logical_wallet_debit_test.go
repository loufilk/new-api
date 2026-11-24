package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestPlanWalletPaidIdleDebit(t *testing.T) {
	user := User{Quota: 165, QuotaPaid: 100, QuotaBonus: 40, QuotaReward: 25}
	for _, tc := range []struct {
		name   string
		order  []LedgerAccount
		amount int
		shares []accountDelta
		need   int
		err    error
	}{
		{"eligible_preserves_bonus_then_reward_then_paid", []LedgerAccount{AccountBonus, AccountReward, AccountPaid}, 190,
			[]accountDelta{{AccountBonus, -40}, {AccountReward, -25}, {AccountPaid, -125}}, 25, nil},
		{"noneligible_never_uses_bonus", []LedgerAccount{AccountReward, AccountPaid}, 150,
			[]accountDelta{{AccountReward, -25}, {AccountPaid, -125}}, 25, nil},
		{"do_not_reclaim_if_raw_accounts_cover_order", []LedgerAccount{AccountBonus, AccountReward, AccountPaid}, 165,
			[]accountDelta{{AccountBonus, -40}, {AccountReward, -25}, {AccountPaid, -100}}, 0, nil},
		{"do_not_force_paid_into_order", []LedgerAccount{AccountReward}, 20, []accountDelta{{AccountReward, -20}}, 0, nil},
		{"paid_first_is_not_reordered_to_reward", []LedgerAccount{AccountPaid, AccountReward}, 150,
			[]accountDelta{{AccountPaid, -150}}, 50, nil},
		{"held_is_not_spendable", []LedgerAccount{AccountPaid}, 156, nil, 0, ErrLedgerInsufficientBalance},
		{"bonus_not_implicitly_added", []LedgerAccount{AccountReward, AccountPaid}, 181, nil, 0, ErrLedgerInsufficientBalance},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := user.QuotaPaid
			plan, need, err := planWalletPaidIdleDebit(&user, tc.amount, tc.order, 55, 25)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.shares, plan)
			require.Equal(t, tc.need, need)
			require.Equal(t, before, user.QuotaPaid, "a funding plan is not a mutation")
		})
	}
}

func TestPlanWalletPaidIdleDebitRejectsInvalidQuota(t *testing.T) {
	user := User{Quota: common.MaxWalletQuota - 3, QuotaPaid: common.MaxWalletQuota - 3}
	_, _, err := planWalletPaidIdleDebit(&user, 1, []LedgerAccount{AccountPaid}, 2, 2)
	require.ErrorIs(t, err, ErrWalletQuotaLimitExceeded)
	_, _, err = planWalletPaidIdleDebit(&user, 1, []LedgerAccount{AccountPaid}, -1, 0)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, _, err = planWalletPaidIdleDebit(&user, common.MaxQuota+1, []LedgerAccount{AccountPaid}, 0, 0)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	_, _, err = planWalletPaidIdleDebit(&user, 1, []LedgerAccount{AccountPaid, AccountPaid}, 0, 0)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
	user.Quota--
	_, _, err = planWalletPaidIdleDebit(&user, 1, []LedgerAccount{AccountPaid}, 0, 0)
	require.ErrorIs(t, err, ErrLedgerInvalidEntry)
}
