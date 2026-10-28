package model

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFinancialTokenCounterBounds(t *testing.T) {
	setupLedgerTestState(t)
	runFinancialTokenCounterBoundsCases(t)
}

// Reused by the dedicated native matrix without the broad package fixture cleanup.
func runFinancialTokenCounterBoundsCases(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name                string
		remain, used, delta int
	}{
		{"remaining_overflow", math.MaxInt - 50, 100, 100},
		{"remaining_underflow", math.MinInt + 50, 0, -100},
		{"used_overflow", 1000, math.MaxInt - 50, -100},
		{"used_underflow", 1000, math.MinInt + 50, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			token := Token{UserId: 40961, Key: "financial-bounds-" + tc.name, UnlimitedQuota: true, RemainQuota: tc.remain, UsedQuota: tc.used}
			require.NoError(t, DB.Create(&token).Error)
			t.Cleanup(func() { require.NoError(t, DB.Unscoped().Delete(&token).Error) })
			err := DB.Transaction(func(tx *gorm.DB) error { return ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, tc.delta) })
			require.Error(t, err)
			code, class := BillingRefundErrorCode(err)
			require.Equal(t, "token_counter_bounds", code)
			require.Equal(t, billingRefundReview, class)
			var after Token
			require.NoError(t, DB.First(&after, token.Id).Error, "no SQLite REAL overflow may commit")
			require.Equal(t, token.RemainQuota, after.RemainQuota)
			require.Equal(t, token.UsedQuota, after.UsedQuota)
		})
	}
	t.Run("exact_integer_boundary", func(t *testing.T) {
		token := Token{UserId: 40961, Key: "financial-exact-boundary", UnlimitedQuota: true, RemainQuota: math.MaxInt - 100, UsedQuota: 100}
		require.NoError(t, DB.Create(&token).Error)
		t.Cleanup(func() { require.NoError(t, DB.Unscoped().Delete(&token).Error) })
		require.NoError(t, DB.Transaction(func(tx *gorm.DB) error { return ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, 100) }))
		var after Token
		require.NoError(t, DB.First(&after, token.Id).Error)
		require.Equal(t, math.MaxInt, after.RemainQuota)
		require.Zero(t, after.UsedQuota)
	})
	for _, deleted := range []bool{false, true} {
		name := "unscoped_debit_active"
		if deleted {
			name = "unscoped_debit_soft_deleted"
		}
		t.Run(name, func(t *testing.T) {
			token := Token{UserId: 40961, Key: "financial-" + name, RemainQuota: 1000}
			require.NoError(t, DB.Create(&token).Error)
			t.Cleanup(func() { require.NoError(t, DB.Unscoped().Delete(&token).Error) })
			if deleted {
				require.NoError(t, DB.Delete(&token).Error)
			}
			callback := "observe_" + name
			var statement string
			hits := 0
			require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.ModelType == reflect.TypeOf(Token{}) {
					hits++
					statement = tx.Statement.SQL.String()
				}
			}))
			t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callback)) })
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				return ApplyTokenQuotaDeltaTx(tx.Unscoped(), token.Id, token.Key, -20)
			}))
			require.Equal(t, 1, hits)
			require.NotEmpty(t, statement, "capture the actual UPDATE, not a dry-run reconstruction")
			require.NotContains(t, strings.ToUpper(statement), " FROM ", "the preceding SELECT must not contaminate this UPDATE")
			var after Token
			require.NoError(t, DB.Unscoped().First(&after, token.Id).Error)
			require.Equal(t, 980, after.RemainQuota)
			require.Equal(t, 20, after.UsedQuota)
			require.Equal(t, deleted, after.DeletedAt.Valid, "original soft-deleted authorization is not revived")
		})
	}

}
