package model

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Exact pre-fingerprint ModelTestRun schema, preserving released columns and
// indexes while exercising the upgrade twice on representative artwork data.
type legacyFingerprintRun struct {
	ID              string `json:"id" gorm:"type:varchar(36);primaryKey"`
	UserID          int    `json:"-" gorm:"index:idx_model_test_owner_time,priority:1"`
	Model           string `json:"model" gorm:"type:varchar(255)"`
	Group           string `json:"group" gorm:"type:varchar(64)"`
	BenchmarkID     string `json:"benchmark_id" gorm:"type:varchar(40)"`
	Status          string `json:"status" gorm:"type:varchar(20);index"`
	Assessment      string `json:"assessment" gorm:"type:varchar(20)"`
	CreatedAt       int64  `json:"created_at" gorm:"index:idx_model_test_owner_time,priority:2"`
	CompletedAt     int64  `json:"completed_at"`
	ExpiresAt       int64  `json:"-"`
	MaxOutputTokens int    `json:"max_output_tokens"`
	EstimatedQuota  *int   `json:"estimated_quota"`
	RequestID       string `json:"request_id" gorm:"type:varchar(128)"`
	ErrorCode       string `json:"error_code" gorm:"type:varchar(50)"`
	CancelRequested bool   `json:"cancel_requested"`
	ArtworkHTML     string `json:"-" gorm:"type:text"`
}

func (legacyFingerprintRun) TableName() string { return "model_test_runs" }

func TestModelFingerprintPostgresMigrationOwnershipReplayAndJournal(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" || os.Getenv("NO_AGENT_FIXTURE_OWNED") != "1" {
		t.Skip("requires isolated owned PostgreSQL TEST_POSTGRES_DSN and NO_AGENT_FIXTURE_OWNED=1")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminConnection, err := admin.DB()
	require.NoError(t, err)
	defer adminConnection.Close()
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade_%t", upgrade), func(t *testing.T) {
			schema := "fingerprint_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
			t.Cleanup(func() { assert.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
			isolatedDSN := dsn
			if strings.Contains(dsn, "://") {
				parsed, err := url.Parse(dsn)
				require.NoError(t, err)
				query := parsed.Query()
				query.Set("search_path", schema)
				parsed.RawQuery = query.Encode()
				isolatedDSN = parsed.String()
			} else {
				isolatedDSN += " search_path=" + schema
			}
			db, err := gorm.Open(postgres.Open(isolatedDSN), &gorm.Config{})
			require.NoError(t, err)
			connection, err := db.DB()
			require.NoError(t, err)
			connection.SetMaxOpenConns(1)
			oldDB, oldType := DB, common.MainDatabaseType()
			DB = db
			common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
			initCol()
			t.Cleanup(func() {
				DB = oldDB
				common.SetMainDatabaseType(oldType)
				initCol()
				assert.NoError(t, connection.Close())
			})
			var version string
			require.NoError(t, db.Raw("SELECT version()").Scan(&version).Error)
			t.Log("PostgreSQL", version)
			if upgrade {
				require.NoError(t, db.AutoMigrate(&legacyFingerprintRun{}))
				require.NoError(t, db.Create(&legacyFingerprintRun{ID: "legacy", UserID: 1, Model: "legacy", Group: "default", BenchmarkID: "pelican-static-v1", Status: "completed", ArtworkHTML: "<svg/>"}).Error)
			}
			for range 2 {
				require.NoError(t, db.AutoMigrate(&User{}, &Option{}, &ModelTestRun{}, &ModelTestGate{}))
			}
			user := User{Username: "fingerprint-owner", AffCode: "fingerprint-owner", Status: common.UserStatusEnabled, Group: "default"}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&Option{Key: ModelTestEnabledKey, Value: "true"}).Error)
			if upgrade {
				old, err := GetModelTestRun(1, "legacy")
				require.NoError(t, err)
				assert.Equal(t, "user", old.Source)
				assert.Equal(t, "<svg/>", old.ArtworkHTML)
				assert.Nil(t, old.ConfirmationID)
			}
			digest := "test-confirmation"
			run := &ModelTestRun{ID: "new", UserID: user.Id, TokenID: 42, ConfirmationID: &digest, Model: "synthetic", Group: "default", BenchmarkID: "local-fingerprint-v1", Status: "running", CreatedAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix()}
			_, err = CreateModelTestRun(run)
			require.NoError(t, err)
			run.SamplesJSON = `[{"challenge_id":"first","status":"dispatching"}]`
			require.NoError(t, SaveModelTestSamples(run))
			stored, err := GetModelTestRun(user.Id, run.ID)
			require.NoError(t, err)
			assert.Equal(t, run.SamplesJSON, stored.SamplesJSON)
			_, err = GetModelTestRun(user.Id+1, run.ID)
			assert.Error(t, err)
			_, err = CancelModelTestRun(user.Id+1, run.ID)
			assert.Error(t, err)
			run.Status = "unknown"
			run.ErrorCode = "upstream_outcome_uncertain"
			run.FingerprintResult = JSONValue(`{"status":"unscorable"}`)
			require.NoError(t, FinishModelTestRun(run))
			replay := *run
			replayed, err := CreateModelTestRun(&replay)
			require.NoError(t, err)
			assert.True(t, replayed)
			assert.Equal(t, "unknown", replay.Status)
			duplicate := *run
			duplicate.ID = "second"
			duplicate.Status = "running"
			_, err = CreateModelTestRun(&duplicate)
			require.ErrorIs(t, err, ErrModelTestConflict)
			require.Error(t, SaveModelTestSamples(run))
			stored, err = GetModelTestRun(user.Id, run.ID)
			require.NoError(t, err)
			assert.JSONEq(t, `{"status":"unscorable"}`, string(stored.FingerprintResult))
			encoded, err := common.Marshal(stored)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "confirmation")
			assert.NotContains(t, string(encoded), "dispatching")
			assert.Contains(t, string(encoded), `"fingerprint_result":{"status":"unscorable"}`)
		})
	}
}
