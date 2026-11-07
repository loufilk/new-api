package model

import (
	"errors"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func newUserBenefitConfigFixture(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_MODEL_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires an owned PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN fixture")
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback())
	schema := "benefit_config_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: parsed.String(), PreferSimpleProtocol: true}), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	require.NoError(t, db.Exec("CREATE SCHEMA "+schema).Error)
	previous := DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB = db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		DB = previous
		common.SetDatabaseTypes(previousMain, previousLog)
		require.NoError(t, db.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&NewUserBenefitConfig{}, &NewUserBenefitConfigAudit{}))
	return db
}

func TestNewUserBenefitCNYValidation(t *testing.T) {
	for _, test := range []struct {
		input string
		cents int64
	}{
		{"0.1", 10}, {"0.10", 10}, {"1", 100}, {"1.00", 100},
		{"0.01", 1}, {"1000000.00", MaxNewUserBenefitCents},
	} {
		t.Run(test.input, func(t *testing.T) {
			actual, err := ParseNewUserBenefitCNY(test.input)
			require.NoError(t, err)
			assert.Equal(t, test.cents, actual)
		})
	}
	for _, input := range []string{"", "0", "-1", "+1", "1e2", "NaN", "Infinity", "0.001", "1.000", ".1", "1.", " 0.10", "01.00", "1000000.01", "9007199254740992"} {
		t.Run("invalid_"+input, func(t *testing.T) {
			_, err := ParseNewUserBenefitCNY(input)
			require.ErrorIs(t, err, ErrNewUserBenefitInvalidConfig)
		})
	}
	assert.Equal(t, "0.10", FormatNewUserBenefitCNY(10))
	assert.Equal(t, "1.00", FormatNewUserBenefitCNY(100))
	require.ErrorIs(t, ValidateNewUserBenefitConfig(NewUserBenefitConfig{PayAmountCents: 100, CreditAmountCents: 99}), ErrNewUserBenefitInvalidConfig)
}

func TestNewUserBenefitConfigPostgresAtomicVersionedSettings(t *testing.T) {
	db := newUserBenefitConfigFixture(t)
	initial, err := GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.False(t, initial.Enabled)
	assert.EqualValues(t, 10, initial.PayAmountCents)
	assert.EqualValues(t, 100, initial.CreditAmountCents)
	assert.Zero(t, initial.Version)

	next, err := UpdateNewUserBenefitConfig(t.Context(), 0, 7, NewUserBenefitConfig{Enabled: true, PayAmountCents: 10, CreditAmountCents: 100})
	require.NoError(t, err)
	assert.EqualValues(t, 1, next.Version)
	assert.Equal(t, 7, next.UpdatedBy)
	_, err = UpdateNewUserBenefitConfig(t.Context(), 0, 8, NewUserBenefitConfig{Enabled: false, PayAmountCents: 20, CreditAmountCents: 200})
	require.ErrorIs(t, err, ErrNewUserBenefitConfigChanged)
	_, err = UpdateNewUserBenefitConfig(t.Context(), 1, 8, NewUserBenefitConfig{PayAmountCents: 100, CreditAmountCents: 10})
	require.ErrorIs(t, err, ErrNewUserBenefitInvalidConfig)

	// An upgrade or repeated startup must preserve the administrator's offer.
	for range 2 {
		require.NoError(t, db.AutoMigrate(&NewUserBenefitConfig{}, &NewUserBenefitConfigAudit{}))
	}
	persisted, err := GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.Equal(t, next, persisted)
	var audit []NewUserBenefitConfigAudit
	require.NoError(t, db.Find(&audit).Error)
	require.Len(t, audit, 1)
	assert.EqualValues(t, 10, audit[0].PayAmountCents)
	assert.EqualValues(t, 100, audit[0].CreditAmountCents)
	assert.Equal(t, 7, audit[0].ActorID)

	// A failed audit insert must roll back all price and switch changes.
	require.NoError(t, db.Create(&NewUserBenefitConfigAudit{Version: 2, ActorID: 9}).Error)
	_, err = UpdateNewUserBenefitConfig(t.Context(), 1, 8, NewUserBenefitConfig{Enabled: false, PayAmountCents: 20, CreditAmountCents: 200})
	require.Error(t, err)
	persisted, err = GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.Equal(t, next, persisted)
}

func TestNewUserBenefitConfigPostgresConcurrentFirstSave(t *testing.T) {
	db := newUserBenefitConfigFixture(t)
	offers := []NewUserBenefitConfig{
		{Enabled: true, PayAmountCents: 10, CreditAmountCents: 100},
		{Enabled: false, PayAmountCents: 20, CreditAmountCents: 200},
	}
	start := make(chan struct{})
	errorsBySave := make([]error, len(offers))
	var finished sync.WaitGroup
	for index, offer := range offers {
		finished.Go(func() {
			<-start
			_, errorsBySave[index] = UpdateNewUserBenefitConfig(t.Context(), 0, index+1, offer)
		})
	}
	close(start)
	finished.Wait()
	winner := -1
	for index, err := range errorsBySave {
		if err == nil {
			require.Equal(t, -1, winner, "only one concurrent administrator save may win")
			winner = index
		} else {
			assert.True(t, errors.Is(err, ErrNewUserBenefitConfigChanged), "%v", err)
		}
	}
	require.NotEqual(t, -1, winner)
	persisted, err := GetNewUserBenefitConfig(t.Context())
	require.NoError(t, err)
	assert.Equal(t, offers[winner].Enabled, persisted.Enabled)
	assert.Equal(t, offers[winner].PayAmountCents, persisted.PayAmountCents)
	assert.Equal(t, offers[winner].CreditAmountCents, persisted.CreditAmountCents)
	assert.EqualValues(t, 1, persisted.Version)
	var count int64
	require.NoError(t, db.Model(&NewUserBenefitConfigAudit{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
