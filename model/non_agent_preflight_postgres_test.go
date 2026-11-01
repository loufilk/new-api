package model

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func nonAgentPreflightFixture(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_NON_AGENT_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires isolated PostgreSQL 18.6")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(u.Hostname()).IsLoopback(), "fixture must use loopback")
	admin, err := gorm.Open(postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	var version int
	require.NoError(t, admin.Raw("SHOW server_version_num").Scan(&version).Error)
	require.Equal(t, 180006, version)
	schema := "non_agent_" + common.GetUUID()
	require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: u.String(), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
		require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error)
		adminDB, _ := admin.DB()
		_ = adminDB.Close()
	})
	return db
}

func TestNonAgentPreflightFreshAndHistoricalIdentityAreReadOnly(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	for range 2 {
		blocks, err := NonAgentCutoverPreflight(db)
		require.NoError(t, err)
		assert.Empty(t, blocks)
	}
	require.NoError(t, db.Exec("CREATE TABLE agent_profiles (user_id integer, status text); INSERT INTO agent_profiles VALUES (7, 'active')").Error)
	for range 2 {
		blocks, err := NonAgentCutoverPreflight(db)
		require.NoError(t, err)
		assert.Empty(t, blocks)
	}
	var status string
	require.NoError(t, db.Table("agent_profiles").Select("status").Scan(&status).Error)
	assert.Equal(t, "active", status, "the compatibility guard never rewrites existing identities")
}

func TestNonAgentPreflightBlocksUnsettledLiabilitiesWithoutMutation(t *testing.T) {
	cases := []struct{ name, table, ddl, values, settle string }{
		{"reserved sales quota", "agent_redemption_accounts", "available_quota bigint, reserved_quota bigint, allocated_quota bigint, spent_quota bigint", "0, 10, 10, 0", "available_quota=0, reserved_quota=0, spent_quota=10"},
		{"cash debt", "agent_commission_accounts", "pending_minor bigint, available_minor bigint, reserved_minor bigint, held_minor bigint, debt_minor bigint", "0,0,0,0,5", "debt_minor=0"},
		{"pending outbox", "agent_commission_outboxes", "status text", "'failed'", "status='done'"},
		{"refund clawback exposure", "agent_commission_awards", "reward_minor bigint, reversed_minor bigint", "100,0", "reversed_minor=100"},
		{"written off debt is not cleared", "agent_credit_accounts", "reserved_minor bigint, outstanding_minor bigint, written_off_minor bigint", "0,0,100", "written_off_minor=0"},
		{"deposit liability", "agent_deposit_accounts", "available_minor bigint, held_minor bigint, received_minor bigint, offset_minor bigint, refunded_minor bigint", "0,100,100,0,0", "held_minor=0, refunded_minor=100"},
		{"unknown payout", "agent_commission_withdrawals", "status text", "'unknown'", "status='succeeded'"},
		{"refund pending", "agent_purchase_refunds", "state text", "'payment_pending'", "state='succeeded'"},
		{"purchase pending", "agent_purchase_orders", "status text", "'verified'", "status='credited'"},
		{"bound customer rights", "agent_deliveries", "state text", "'claimed'", "state='redeemed'"},
		{"unbalanced drained sales budget", "agent_redemption_accounts", "available_quota bigint, reserved_quota bigint, allocated_quota bigint, spent_quota bigint", "0,0,100,90", "spent_quota=100"},
		{"negative award is not settled", "agent_commission_awards", "reward_minor bigint, reversed_minor bigint", "-1,-1", "reward_minor=0, reversed_minor=0"},
		{"negative debt is not settled", "agent_commission_debts", "amount_minor bigint, recovered_minor bigint", "-1,-1", "amount_minor=0, recovered_minor=0"},
		{"unbalanced drained deposit", "agent_deposit_accounts", "available_minor bigint, held_minor bigint, received_minor bigint, offset_minor bigint, refunded_minor bigint", "0,0,100,0,90", "refunded_minor=100"},
		{"unbalanced deposit receipt", "agent_deposit_receipts", "state text, available_minor bigint, held_minor bigint, amount_minor bigint, offset_minor bigint, refunded_minor bigint", "'confirmed',0,0,100,0,90", "refunded_minor=100"},
		{"unbalanced settled receivable", "agent_receivables", "state text, principal_minor bigint, paid_minor bigint, offset_minor bigint, corrected_minor bigint, written_off_minor bigint", "'settled',100,90,0,0,0", "paid_minor=100"},
		{"reserved campaign budget", "agent_campaign_budgets", "reserved_quota bigint, spent_quota bigint, limit_quota bigint", "1,10,100", "reserved_quota=0"},
		{"unreconciled statement", "agent_statement_periods", "state text", "'open'", "state='closed'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := nonAgentPreflightFixture(t)
			require.NoError(t, db.Exec("CREATE TABLE "+tc.table+" ("+tc.ddl+")").Error)
			require.NoError(t, db.Exec("INSERT INTO "+tc.table+" VALUES ("+tc.values+")").Error)
			for range 2 {
				blocks, err := NonAgentCutoverPreflight(db)
				require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
				assert.Equal(t, []NonAgentCutoverBlock{{Table: tc.table, Rows: 1}}, blocks)
			}
			// Only the fixture's simulated original-version reconciliation changes
			// the state; repeated preflight calls above never do so themselves.
			require.NoError(t, db.Exec("UPDATE "+tc.table+" SET "+tc.settle).Error)
			blocks, err := NonAgentCutoverPreflight(db)
			require.NoError(t, err)
			assert.Empty(t, blocks)
		})
	}
}

func TestNonAgentPreflightUnknownAndIncompleteSchemaFailClosed(t *testing.T) {
	for _, table := range []string{"agent_future_liabilities", "agent_commission_accounts", "agent_credit_draws", "agent_fund_batches", "agent_fund_slices", "agent_fund_economics", "agent_outbound_payment_receipts"} {
		t.Run(table, func(t *testing.T) {
			db := nonAgentPreflightFixture(t)
			require.NoError(t, db.Exec("CREATE TABLE "+table+" (id integer); INSERT INTO "+table+" VALUES (1)").Error)
			_, err := NonAgentCutoverPreflight(db)
			require.Error(t, err)
			var count int64
			require.NoError(t, db.Table(table).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestNonAgentPreflightProtectsLiveCodesButAllowsRetiredCodeHistory(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.NoError(t, db.Exec("CREATE TABLE redemptions (budget_agent_id integer, budget_reserved boolean, source_protected boolean, status integer)").Error)
	require.NoError(t, db.Exec("INSERT INTO redemptions VALUES (0,false,false,1), (7,false,true,2), (7,false,true,3)").Error)
	_, err := NonAgentCutoverPreflight(db)
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO redemptions VALUES (7,true,true,1)").Error)
	blocks, err := NonAgentCutoverPreflight(db)
	require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
	assert.Equal(t, []NonAgentCutoverBlock{{Table: "redemptions", Rows: 1}}, blocks)
}

func TestNonAgentPreflightKeepsOrdinaryPaymentEvidenceButBlocksUnverifiedBusinessPurpose(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.NoError(t, db.Exec("CREATE TABLE agent_payment_receipts (purpose text); INSERT INTO agent_payment_receipts VALUES ('topup')").Error)
	for range 2 {
		_, err := NonAgentCutoverPreflight(db)
		require.NoError(t, err)
	}
	require.NoError(t, db.Exec("INSERT INTO agent_payment_receipts VALUES ('purchase')").Error)
	blocks, err := NonAgentCutoverPreflight(db)
	require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
	assert.Equal(t, []NonAgentCutoverBlock{{Table: "agent_payment_receipts", Rows: 1}}, blocks)
}

func TestNonAgentPreflightRejectsNullLegacyMarkersEvenOnDisabledCode(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.NoError(t, db.Exec("CREATE TABLE redemptions (source_protected boolean, status integer); INSERT INTO redemptions VALUES (NULL, 2)").Error)
	_, err := NonAgentCutoverPreflight(db)
	require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
}

func TestNonAgentPreflightCannotHideUnreadableLegacyTable(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	role := "non_agent_reader_" + common.GetUUID()
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE ROLE %q", role)).Error)
	t.Cleanup(func() {
		require.NoError(t, db.Exec("RESET ROLE").Error)
		require.NoError(t, db.Exec(fmt.Sprintf("DROP OWNED BY %q", role)).Error)
		require.NoError(t, db.Exec(fmt.Sprintf("DROP ROLE %q", role)).Error)
	})
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&schema).Error)
	require.NoError(t, db.Exec("CREATE TABLE agent_commission_outboxes (status text); INSERT INTO agent_commission_outboxes VALUES ('pending')").Error)
	require.NoError(t, db.Exec(fmt.Sprintf("GRANT USAGE ON SCHEMA %q TO %q", schema, role)).Error)
	require.NoError(t, db.Exec(fmt.Sprintf("SET ROLE %q", role)).Error)
	_, err = NonAgentCutoverPreflight(db)
	require.ErrorContains(t, err, "cannot inspect agent_commission_outboxes")
}

func TestNonAgentPreflightKeepsOrdinaryBonusButBlocksCampaignRights(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.NoError(t, db.Exec("CREATE TABLE bonus_source_lots (id bigint PRIMARY KEY, user_id bigint, grant_quota bigint, kind text, campaign_id bigint, claim_id bigint); CREATE TABLE bonus_source_states (lot_id bigint PRIMARY KEY, user_id bigint, available bigint, outstanding bigint, expired bigint, revoked bigint, legacy_restored bigint)").Error)
	// Ordinary user gifts and refunds retain their original source accounting.
	require.NoError(t, db.Exec("INSERT INTO bonus_source_lots VALUES (1,7,100,'legacy_nonexpiring',0,0); INSERT INTO bonus_source_states VALUES (1,7,40,60,0,0,0)").Error)
	_, err := NonAgentCutoverPreflight(db)
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO bonus_source_lots VALUES (2,8,100,'campaign_grant',1,1); INSERT INTO bonus_source_states VALUES (2,8,0,60,40,0,0)").Error)
	for range 2 {
		blocks, err := NonAgentCutoverPreflight(db)
		require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
		assert.Equal(t, []NonAgentCutoverBlock{{Table: "bonus_source_lots", Rows: 1}}, blocks)
	}
	require.NoError(t, db.Exec("UPDATE bonus_source_states SET outstanding=0, expired=100 WHERE lot_id=2").Error)
	_, err = NonAgentCutoverPreflight(db)
	require.NoError(t, err)
	require.NoError(t, db.Exec("DELETE FROM bonus_source_states WHERE lot_id=2").Error)
	_, err = NonAgentCutoverPreflight(db)
	require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
}

func TestNonAgentPreflightNeverInfersRetirementFromOrphanedSourceTerminalState(t *testing.T) {
	db := nonAgentPreflightFixture(t)
	require.NoError(t, db.Exec("CREATE TABLE agent_fund_slices (state text); INSERT INTO agent_fund_slices VALUES ('spent')").Error)
	blocks, err := NonAgentCutoverPreflight(db)
	require.ErrorIs(t, err, ErrNonAgentCutoverBlocked)
	assert.Equal(t, []NonAgentCutoverBlock{{Table: "agent_fund_slices", Rows: 1}}, blocks)
	var state string
	require.NoError(t, db.Table("agent_fund_slices").Select("state").Scan(&state).Error)
	assert.Equal(t, "spent", state)
}
