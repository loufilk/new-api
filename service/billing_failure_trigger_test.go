package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// installBillingUpdateFailure injects a real database error, including a
// transaction-aborting PostgreSQL exception. A zero rowID matches every row.
// The returned cleanup permits retrying after the fault is removed; Cleanup
// also removes the trigger and its PostgreSQL function if the test fails early.
func installBillingUpdateFailure(t *testing.T, db *gorm.DB, name, table string, rowID int, message string) func() {
	t.Helper()
	require.GreaterOrEqual(t, rowID, 0)
	dialect := db.Dialector.Name()
	require.Contains(t, []string{"postgres", "sqlite"}, dialect)
	statement := &gorm.Statement{DB: db}
	triggerName := statement.Quote(name)
	tableName := statement.Quote(table)
	functionName := statement.Quote(name + "_fn")
	messageLiteral := "'" + strings.ReplaceAll(message, "'", "''") + "'"
	condition := ""
	if rowID > 0 {
		condition = fmt.Sprintf(" WHEN (OLD.id = %d)", rowID)
	}

	dropTrigger := "DROP TRIGGER IF EXISTS " + triggerName
	if dialect == "postgres" {
		dropTrigger += " ON " + tableName
	}
	var triggerCreated, functionCreated bool
	remove := func() {
		t.Helper()
		if triggerCreated {
			require.NoError(t, db.Exec(dropTrigger).Error)
			triggerCreated = false
		}
		if functionCreated {
			require.NoError(t, db.Exec("DROP FUNCTION IF EXISTS "+functionName+"()").Error)
			functionCreated = false
		}
	}
	t.Cleanup(remove)

	createTrigger := fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE ON %s FOR EACH ROW%s ", triggerName, tableName, condition)
	if dialect == "postgres" {
		body := "BEGIN RAISE EXCEPTION USING MESSAGE = " + messageLiteral + "; RETURN NEW; END;"
		bodyLiteral := "'" + strings.ReplaceAll(body, "'", "''") + "'"
		require.NoError(t, db.Exec("CREATE FUNCTION "+functionName+"() RETURNS trigger LANGUAGE plpgsql AS "+bodyLiteral).Error)
		functionCreated = true
		createTrigger += "EXECUTE FUNCTION " + functionName + "()"
	} else {
		createTrigger += "BEGIN SELECT RAISE(ABORT, " + messageLiteral + "); END"
	}
	require.NoError(t, db.Exec(createTrigger).Error)
	triggerCreated = true
	return remove
}
