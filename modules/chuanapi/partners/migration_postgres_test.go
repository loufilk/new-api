package partners

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/extension"
	"github.com/QuantumNous/new-api/model"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func partnerMigrationPostgres(t *testing.T, dsnEnv, expectedVersion string) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("requires a disposable PostgreSQL %s migration fixture in %s", expectedVersion, dsnEnv)
	}
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	ip := net.ParseIP(parsed.Hostname())
	require.NotNil(t, ip)
	require.True(t, ip.IsLoopback(), "fixture must never select production")

	admin, err := gorm.Open(
		postgres.New(postgres.Config{DSN: dsn, PreferSimpleProtocol: true}),
		&gorm.Config{},
	)
	require.NoError(t, err)
	var version string
	require.NoError(t, admin.Raw("SHOW server_version").Scan(&version).Error)
	require.Regexp(t, regexp.MustCompile(fmt.Sprintf("^%s(\\s|$)", regexp.QuoteMeta(expectedVersion))), version)

	schema := fmt.Sprintf("partner_migration_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec(fmt.Sprintf("CREATE SCHEMA %q", schema)).Error)
	config, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	config.RuntimeParams["search_path"] = schema
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	sqlDB := stdlib.OpenDB(*config)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	oldDB := model.DB
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	model.DB = db
	common.SetDatabaseTypes(common.DatabaseTypePostgreSQL, common.DatabaseTypePostgreSQL)
	t.Cleanup(func() {
		model.DB = oldDB
		common.SetDatabaseTypes(oldMain, oldLog)
		require.NoError(t, sqlDB.Close())
		require.NoError(t, admin.Exec(fmt.Sprintf("DROP SCHEMA %q CASCADE", schema)).Error)
		pool, cleanupErr := admin.DB()
		require.NoError(t, cleanupErr)
		require.NoError(t, pool.Close())
	})
	return db
}

func TestPartnerApplicationMigrationAndReviewPostgres(t *testing.T) {
	for _, fixture := range []struct {
		name            string
		dsnEnv          string
		expectedVersion string
	}{
		{name: "PostgreSQL 18.7 candidate", dsnEnv: "TEST_PARTNER_MIGRATION_POSTGRES_DSN", expectedVersion: "18.7"},
		{name: "PostgreSQL 18.6 baseline", dsnEnv: "TEST_PARTNER_MIGRATION_POSTGRES_18_6_DSN", expectedVersion: "18.6"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			db := partnerMigrationPostgres(t, fixture.dsnEnv, fixture.expectedVersion)
			testPartnerApplicationMigrationAndReview(t, db)
		})
	}
}

func testPartnerApplicationMigrationAndReview(t *testing.T, db *gorm.DB) {
	fixtureUser := model.User{
		Username:  "partner-migration-user",
		AffCode:   "partner-migration-user",
		Role:      common.RoleCommonUser,
		Status:    common.UserStatusEnabled,
		Quota:     150,
		QuotaPaid: 100,
	}
	adminUser := model.User{
		Username: "partner-migration-admin",
		AffCode:  "partner-migration-admin",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.Create(&fixtureUser).Error)
	require.NoError(t, db.Create(&adminUser).Error)

	registry := extension.NewRegistry()
	require.NoError(t, Register(registry))
	require.NoError(t, registry.Freeze())
	for range 2 {
		require.NoError(t, db.AutoMigrate(
			registry.ModelsFor(extension.PhaseAfterCoreModels)...,
		))
		require.NoError(t, registry.RunStepsFor(db, extension.PhaseAfterCoreData))
	}
	var settingCount int64
	require.NoError(t, db.Model(&model.PartnerApplicationSettings{}).Count(&settingCount).Error)
	assert.EqualValues(t, 1, settingCount, "repeated startup must keep one default intake policy")

	input := model.PartnerApplicationInput{
		SiteName:    "Example Provider",
		SiteURL:     "https://provider.example",
		ContactType: model.PartnerContactEmail,
		Contact:     "sales@provider.example",
		Quotes: []model.PartnerQuote{{
			Platform: model.PartnerPlatformOpenAI,
			Group:    "standard",
			Rate:     "1.00",
		}},
	}
	created, err := model.CreatePartnerApplication(
		t.Context(), fixtureUser.Id, "partner-request-key-1", input,
	)
	require.NoError(t, err)
	replayed, err := model.CreatePartnerApplication(
		t.Context(), fixtureUser.Id, "partner-request-key-1", input,
	)
	require.NoError(t, err)
	assert.Equal(t, created.ID, replayed.ID, "replaying an idempotency key must not create another application")
	_, err = model.CreatePartnerApplication(
		t.Context(), fixtureUser.Id, "partner-request-key-2", input,
	)
	require.ErrorIs(t, err, model.ErrPartnerApplicationConflict, "one user may have only one active application")

	allowReview := func(_ *gorm.DB, _ *model.User) error { return nil }
	reviewing, err := model.ReviewPartnerApplication(
		t.Context(), adminUser.Id, created.ID, true,
		model.PartnerApplicationReview{
			ExpectedRevision: created.Revision,
			Action:           "review",
		},
		allowReview,
	)
	require.NoError(t, err)
	assert.Equal(t, model.PartnerApplicationReviewing, reviewing.Status)

	approved, err := model.ReviewPartnerApplication(
		t.Context(), adminUser.Id, created.ID, true,
		model.PartnerApplicationReview{
			ExpectedRevision: reviewing.Revision,
			Action:           "approve",
			Note:             "Verified supplier terms.",
		},
		allowReview,
	)
	require.NoError(t, err)
	assert.Equal(t, model.PartnerApplicationApproved, approved.Status)

	customerView, err := model.GetPartnerApplication(t.Context(), fixtureUser.Id, created.ID, false)
	require.NoError(t, err)
	assert.Empty(t, customerView.ReviewNote)
	for _, event := range customerView.Events {
		assert.Empty(t, event.Note, "customer history must not expose internal review notes")
	}
	var preserved model.User
	require.NoError(t, db.First(&preserved, fixtureUser.Id).Error)
	assert.Equal(
		t,
		[]int{fixtureUser.Quota, fixtureUser.QuotaPaid},
		[]int{preserved.Quota, preserved.QuotaPaid},
	)
}
