package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRollbackStartupRequiresExplicitPostgresMaster(t *testing.T) {
	t.Setenv("STARTUP_MIGRATIONS", "verify-only")
	t.Setenv("NODE_TYPE", "master")
	t.Setenv("SQL_DSN", "postgres://localhost/primary")
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, requireRollbackConfig())
	for _, setting := range []struct{ key, bad string }{
		{"STARTUP_MIGRATIONS", ""}, {"STARTUP_MIGRATIONS", "true"}, {"NODE_TYPE", "slave"},
		{"SQL_DSN", ""}, {"LOG_SQL_DSN", "local"},
	} {
		t.Run(setting.key+"="+setting.bad, func(t *testing.T) {
			t.Setenv(setting.key, setting.bad)
			require.Error(t, requireRollbackConfig())
		})
	}
}

func TestRollbackChecksAllFourUnrestrictedReportDimensionsPostgres(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.Error(t, verifyRollbackQuotaColumns(db), "missing table is not an upgraded database")
	require.NoError(t, db.Exec("CREATE TABLE quota_data (username text, model_name text, use_group text, node_name text)").Error)
	for _, col := range []string{"username", "model_name", "use_group", "node_name"} {
		require.NoError(t, db.Exec("ALTER TABLE quota_data ALTER COLUMN "+col+" TYPE varchar(64)").Error)
		require.Error(t, verifyRollbackQuotaColumns(db), col)
		require.NoError(t, db.Exec("ALTER TABLE quota_data ALTER COLUMN "+col+" TYPE text").Error)
	}
	names := []string{strings.Repeat("u", 79) + "甲", strings.Repeat("m", 80) + "乙", strings.Repeat("g", 81) + "丙", strings.Repeat("n", 82) + "丁"}
	require.NoError(t, db.Exec("INSERT INTO quota_data VALUES (?,?,?,?)", names[0], names[1], names[2], names[3]).Error)
	for range 2 {
		require.NoError(t, verifyRollbackQuotaColumns(db))
	}
	var got [4]string
	require.NoError(t, db.Raw("SELECT username, model_name, use_group, node_name FROM quota_data").Row().Scan(&got[0], &got[1], &got[2], &got[3]))
	assert.Equal(t, names, got[:])
}

func TestRollbackPreservesOnlyLinkedInvoiceFeeReceiptsPostgres(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.NoError(t, db.AutoMigrate(&PaymentReceipt{}))
	require.NoError(t, db.Exec(`CREATE TABLE customer_invoice_applications
		(fee_receipt_id bigint,user_id bigint,merchant_scope text,fee_trade_no text,fee_rail text,fee_payment_method text,
		fee_gateway text,fee_minor bigint,fee_paid_minor bigint,fee_paid_at bigint,status text,fee_state text)`).Error)
	trade := "CIF" + strings.Repeat("1", 29)
	r := PaymentReceipt{Provider: PaymentProviderEpay, ProviderReference: "synthetic-reference", MerchantScope: "fixture", Environment: "live",
		Purpose: "invoice_fee", LocalOrderKey: trade, OwnerUserID: 7, Currency: "CNY", CurrencyExponent: 2, PaidMinor: 12, PaidAt: 101, VerifiedAt: 102, ReceiptKey: strings.Repeat("a", 64)}
	require.NoError(t, db.Create(&r).Error)
	_, err := nonAgentCutoverPreflight(db, true)
	require.ErrorIs(t, err, ErrNonAgentCutoverBlocked, "do not authorize an orphan")
	require.NoError(t, db.Exec("INSERT INTO customer_invoice_applications VALUES(?,7,'fixture',?,'epay','wxpay','fixture',12,12,101,'pending','paid')", r.ID, trade).Error)
	_, err = nonAgentCutoverPreflight(db, true)
	require.NoError(t, err)
	require.NoError(t, db.Exec("UPDATE customer_invoice_applications SET fee_paid_minor=13").Error)
	_, err = nonAgentCutoverPreflight(db, true)
	require.ErrorIs(t, err, ErrNonAgentCutoverBlocked, "mismatched money is not a valid link")
	var retained string
	require.NoError(t, db.Raw("SELECT purpose || ':' || paid_minor::text FROM agent_payment_receipts WHERE id=?", r.ID).Row().Scan(&retained))
	assert.Equal(t, fmt.Sprintf("invoice_fee:%d", r.PaidMinor), retained)
}

// Agent public summaries contain derived projection fields (-:migration). Their
// absence is normal; loss of a real persisted field must still fail startup.
func TestRollbackChecksOnlyPhysicalAccountColumnsPostgres(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.NoError(t, db.AutoMigrate(&AgentRedemptionAccount{}))
	require.NoError(t, verifyRollbackReadableColumns(db, []any{&AgentRedemptionAccount{}}))
	require.NoError(t, db.Exec("ALTER TABLE agent_redemption_accounts DROP COLUMN reserved_quota").Error)
	require.Error(t, verifyRollbackReadableColumns(db, []any{&AgentRedemptionAccount{}}))
}
