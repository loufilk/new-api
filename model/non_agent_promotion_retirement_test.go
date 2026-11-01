package model

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func promotionRetirementFixture(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	var driver gorm.Dialector
	switch dialect {
	case "sqlite":
		driver = sqlite.Open(":memory:")
	case "mysql":
		dsn := os.Getenv("TEST_RETIREMENT_MYSQL_DSN")
		if dsn == "" {
			t.Skip("requires disposable loopback MySQL retirement database")
		}
		require.Contains(t, dsn, "@tcp(127.0.0.1:")
		require.Contains(t, dsn, ")/no_agent_retirement?")
		driver = mysql.Open(dsn)
	case "postgres":
		dsn := os.Getenv("TEST_RETIREMENT_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("requires disposable loopback PostgreSQL retirement database")
		}
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		require.True(t, net.ParseIP(u.Hostname()).IsLoopback())
		require.Equal(t, "/no_agent_retirement", u.Path)
		driver = postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true})
	}
	db, err := gorm.Open(driver, &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	ddl := map[string]string{
		"users":                       "id bigint, role integer",
		"agent_profiles":              "user_id bigint, status text, sales_frozen boolean, settlement_frozen boolean, version bigint",
		"agent_fund_batches":          "id bigint, agent_user_id bigint, source_key text, source_kind text, original_quota bigint, original_paid_minor text, payment_receipt_id bigint, purchase_order_id bigint, cash_refundable boolean, currency text, currency_exponent integer, wallet_account text",
		"agent_fund_slices":           "id bigint, batch_id bigint, agent_user_id bigint, start_quota bigint, quota bigint, state text, redemption_id bigint, refund_id bigint, reclassification_id bigint",
		"agent_fund_ledgers":          "event_key text, batch_id bigint, agent_user_id bigint, start_quota bigint, quota bigint, cost_minor text, from_state text, to_state text, redemption_id bigint, refund_id bigint, reclassification_id bigint, currency text, currency_exponent integer",
		"agent_redemption_accounts":   "agent_user_id bigint, available_quota bigint, reserved_quota bigint, spent_quota bigint, allocated_quota bigint",
		"agent_redemption_ledgers":    "operation_key text, agent_user_id bigint, kind text, delta bigint, allocated_delta bigint, reserved_delta bigint, spent_delta bigint, available_after bigint, allocated_after bigint, reserved_after bigint, spent_after bigint, redemption_id bigint, reason text, request_id text",
		"agent_redemption_operations": "operation_key text, agent_user_id bigint, kind text, signature text, result text",
	}
	for table, columns := range ddl {
		require.NoError(t, db.Exec("DROP TABLE IF EXISTS "+table).Error)
		require.NoError(t, db.Exec("CREATE TABLE "+table+" ("+columns+")").Error)
	}
	for _, sql := range []string{
		"INSERT INTO users VALUES (7,1)",
		"INSERT INTO agent_profiles VALUES (7,'exited',TRUE,TRUE,3)",
		"INSERT INTO agent_fund_batches VALUES (1,7,'allocation:grant','promotion',50000000,'0',0,0,FALSE,'',0,'paid')",
		"INSERT INTO agent_fund_slices VALUES (1,1,7,0,50000000,'reclaimed',0,0,0)",
		"INSERT INTO agent_fund_ledgers VALUES ('allocation:grant:create:1:0',1,7,0,50000000,'0','','available',0,0,0,'',0), ('allocation:revoke:1:0',1,7,0,50000000,'0','available','reclaimed',0,0,0,'',0)",
		"INSERT INTO agent_redemption_accounts VALUES (7,0,0,0,0)",
		"INSERT INTO agent_redemption_ledgers VALUES ('allocation:grant',7,'allocation',50000000,50000000,0,0,50000000,50000000,0,0,0,'Test grant','grant'), ('allocation:revoke',7,'allocation',-50000000,-50000000,0,0,0,0,0,0,0,'Operator authorized retirement','revoke')",
		"INSERT INTO agent_redemption_operations VALUES ('allocation:grant',7,'allocation','signature','{}'), ('allocation:revoke',7,'allocation','signature','{}')",
	} {
		require.NoError(t, db.Exec(sql).Error)
	}
	return db
}

func TestNonAgentPromotionRetirementMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			cases := []struct{ name, mutation string }{
				{"complete unused promotion", ""},
				{"available source", "UPDATE agent_fund_slices SET state='available'"},
				{"partial reclaim", "UPDATE agent_fund_slices SET quota=49999999"},
				{"paid provenance", "UPDATE agent_fund_batches SET original_paid_minor='1'"},
				{"purchase provenance", "UPDATE agent_fund_batches SET source_kind='purchase'"},
				{"unknown provenance", "UPDATE agent_fund_batches SET payment_receipt_id=NULL"},
				{"cash refundable", "UPDATE agent_fund_batches SET cash_refundable=TRUE"},
				{"reserved funds", "UPDATE agent_redemption_accounts SET reserved_quota=1,allocated_quota=1"},
				{"ordinary account still entitled", "UPDATE agent_profiles SET settlement_frozen=FALSE"},
				{"incomplete source ledger", "DELETE FROM agent_fund_ledgers WHERE from_state='available'"},
				{"unrelated source operation", "UPDATE agent_fund_ledgers SET event_key='unrelated' WHERE from_state='available'"},
				{"missing receipt", "DELETE FROM agent_redemption_operations WHERE operation_key='allocation:revoke'"},
				{"unbalanced budget ledger", "UPDATE agent_redemption_ledgers SET allocated_delta=0 WHERE delta<0"},
				{"previous use hidden by net zero", "UPDATE agent_redemption_ledgers SET spent_delta=1 WHERE delta>0"},
				{"additional source transition", "INSERT INTO agent_fund_ledgers SELECT * FROM agent_fund_ledgers WHERE from_state='available'"},
				{"extra orphan source", "INSERT INTO agent_fund_slices VALUES (2,2,7,0,1,'available',0,0,0)"},
				{"unknown liability", "CREATE TABLE agent_future_liability (amount bigint)"},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					db := promotionRetirementFixture(t, dialect)
					if tc.mutation != "" {
						require.NoError(t, db.Exec(tc.mutation).Error)
					}
					if tc.name == "unknown liability" {
						require.NoError(t, db.Exec("INSERT INTO agent_future_liability VALUES (1)").Error)
						t.Cleanup(func() { require.NoError(t, db.Exec("DROP TABLE agent_future_liability").Error) })
					}
					before := retirementSnapshot(t, db)
					for range 2 {
						blocks, err := NonAgentCutoverPreflight(db)
						if tc.mutation == "" {
							require.NoError(t, err)
							assert.Empty(t, blocks)
						} else {
							require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
							assert.NotEmpty(t, blocks)
						}
					}
					assert.Equal(t, before, retirementSnapshot(t, db), "preflight must preserve balances and all historical evidence")
				})
			}
		})
	}
}
func retirementSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var result strings.Builder
	for _, table := range []string{"users", "agent_profiles", "agent_fund_batches", "agent_fund_slices", "agent_fund_ledgers", "agent_redemption_accounts", "agent_redemption_ledgers", "agent_redemption_operations"} {
		var rows []map[string]any
		require.NoError(t, db.Table(table).Find(&rows).Error)
		fmt.Fprint(&result, rows)
	}
	return result.String()
}
