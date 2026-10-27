// Command referral-cleanup explicitly removes the retired affiliate/referral
// schema after the new release is live and all old processes have stopped.
//
// It is never invoked at startup. Guards refuse to delete anything while legacy
// rewards, balances, or fund-ledger rows are non-zero; affiliate_reward_reconcile_states
// (pure scan/reconcile state) is allowed to be non-empty and is only reported.
//
// Usage:
//
//	go run ./cmd/referral-cleanup            # inspect only (default)
//	go run ./cmd/referral-cleanup -apply     # run guarded destructive cleanup
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/joho/godotenv"
)

func main() {
	os.Exit(run())
}

func run() int {
	apply := flag.Bool("apply", false, "execute the guarded destructive cleanup; without it only the inspection report is printed")
	flag.Parse()

	if err := godotenv.Load(".env"); err != nil {
		common.SysLog("No .env file found, using process environment variables")
	}
	common.InitEnv()

	// The command intentionally does not compose the extension registry: it must
	// not re-create retired or extension-owned tables. It only opens the core
	// database and operates on the legacy schema that is already present.
	if err := openDatabase(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize database: %v\n", err)
		return 1
	}
	defer closeDatabase()

	var report *model.LegacyAffiliateCleanupReport
	var err error
	if *apply {
		report, err = model.CleanupLegacyAffiliateData(model.DB)
	} else {
		report, err = model.InspectLegacyAffiliateData(model.DB)
	}
	printReport(report, *apply)

	if err != nil {
		if errors.Is(err, model.ErrLegacyAffiliateCleanupBlocked) {
			fmt.Fprintln(os.Stderr, "cleanup refused: legacy affiliate rewards, balances, or fund records are non-zero; nothing was deleted")
			return 2
		}
		fmt.Fprintf(os.Stderr, "referral cleanup failed: %v\n", err)
		return 1
	}
	return 0
}

// openDatabase opens the core database without running startup migrations:
// model.InitDB only migrates when common.IsMasterNode is set, so the operator
// command pins it to false before connecting. Inspection and cleanup must never
// create or alter tables via AutoMigrate; cleanup performs its own explicit,
// guarded DDL. The connection string itself is never logged.
func openDatabase() error {
	common.IsMasterNode = false
	return model.InitDB()
}

func closeDatabase() {
	if model.DB == nil {
		return
	}
	sqlDB, err := model.DB.DB()
	if err != nil {
		return
	}
	if err := sqlDB.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to close database: %v\n", err)
	}
}

func printReport(report *model.LegacyAffiliateCleanupReport, apply bool) {
	if report == nil {
		return
	}
	mode := "inspect"
	if apply {
		mode = "apply"
	}
	payload, err := common.Marshal(map[string]any{"mode": mode, "report": report})
	if err == nil {
		payload, err = common.IndentJson(payload)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "mode=%s report marshal failed: %v\n", mode, err)
		return
	}
	fmt.Println(string(payload))
}
