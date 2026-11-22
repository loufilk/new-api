package model

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomerInvoiceFeeRounding(t *testing.T) {
	for _, c := range []struct {
		principal int64
		rate      int
		fee       int64
	}{
		{49, 100, 0}, {50, 100, 1}, {501, 333, 17}, {math.MaxInt64, 10000, math.MaxInt64},
		{math.MaxInt64, 5000, 4611686018427387904}, {math.MaxInt64, 0, 0},
	} {
		got, err := customerInvoiceFeeMinor(c.principal, c.rate)
		require.NoError(t, err)
		assert.Equal(t, c.fee, got)
	}
}

func TestCustomerInvoiceSettingsCASPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	first := customerInvoiceTestUser(t, common.RoleRootUser)
	second := customerInvoiceTestUser(t, common.RoleRootUser)
	regular := customerInvoiceTestUser(t, common.RoleCommonUser)
	t.Setenv("CUSTOMER_INVOICE_ENABLED", "false")
	start, err := GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, defaultCustomerInvoiceSettings(), start)
	_, err = UpdateCustomerInvoiceSettings(ctx, regular, start)
	require.ErrorIs(t, err, ErrCustomerInvoiceNotFound)
	noChange, err := UpdateCustomerInvoiceSettings(ctx, first, start)
	require.NoError(t, err)
	assert.Equal(t, start, noChange)
	for _, invalid := range []CustomerInvoiceSettings{
		{Ordinary: CustomerInvoiceTypeSettings{FeeRateBPS: -1}},
		{Ordinary: CustomerInvoiceTypeSettings{TaxRateBPS: 10001}},
		{Special: CustomerInvoiceTypeSettings{FeeRateBPS: 10001}},
		{Special: CustomerInvoiceTypeSettings{TaxRateBPS: -1}},
	} {
		_, err = UpdateCustomerInvoiceSettings(ctx, first, invalid)
		require.ErrorIs(t, err, ErrCustomerInvoiceInvalid)
	}
	type outcome struct {
		actor    int
		settings CustomerInvoiceSettings
		err      error
	}
	res := make(chan outcome, 2)
	run := make(chan struct{})
	for _, actor := range []int{first, second} {
		go func() {
			next := start
			next.Special.TaxRateBPS = 600
			if actor == first {
				next.Ordinary.Enabled = false
			} else {
				next.Special.Enabled = false
			}
			<-run
			written, e := UpdateCustomerInvoiceSettings(ctx, actor, next)
			res <- outcome{actor, written, e}
		}()
	}
	close(run)
	a, b := <-res, <-res
	if a.err != nil {
		a, b = b, a
	}
	require.NoError(t, a.err)
	require.ErrorIs(t, b.err, ErrCustomerInvoiceSettingsConflict)
	assert.EqualValues(t, 1, a.settings.Version)
	observed, err := GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, a.settings, observed)
	var log CustomerInvoiceSettingsRevision
	require.NoError(t, db.Where("version = ?", 1).Take(&log).Error)
	assert.Equal(t, a.actor, log.ActorID)
	assert.Positive(t, log.CreatedAt)
	logged := log.CustomerInvoiceSettings
	require.NotNil(t, log.CenterEnabledValue)
	logged.CenterEnabled = *log.CenterEnabledValue
	assert.Equal(t, observed, logged)
	require.ErrorIs(t, db.Model(&log).Update("actor_id", regular).Error, ErrLedgerImmutable)
	require.ErrorIs(t, db.Delete(&log).Error, ErrLedgerImmutable)
}

func TestCustomerInvoiceSettingsLegacyCenterDefaultsEnabledAndMigrationPreservesRevisionPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	root := customerInvoiceTestUser(t, common.RoleRootUser)
	settings := defaultCustomerInvoiceSettings()
	settings.Special.TaxRateBPS = 600
	saved, err := UpdateCustomerInvoiceSettings(ctx, root, settings)
	require.NoError(t, err)
	require.EqualValues(t, 1, saved.Version)

	require.NoError(t, db.Migrator().DropColumn(&CustomerInvoiceSettingsRevision{}, "center_enabled"))
	require.NoError(t, db.AutoMigrate(&CustomerInvoiceSettingsRevision{}))
	require.NoError(t, db.AutoMigrate(&CustomerInvoiceSettingsRevision{}))

	var legacyRevision struct {
		CenterEnabled *bool `gorm:"column:center_enabled"`
	}
	require.NoError(t, db.Model(&CustomerInvoiceSettingsRevision{}).Select("center_enabled").Where("version = ?", saved.Version).Take(&legacyRevision).Error)
	assert.Nil(t, legacyRevision.CenterEnabled, "upgrade must not rewrite historical revision data")
	current, err := GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	assert.True(t, current.CenterEnabled)
	assert.Equal(t, saved.Version, current.Version)
	assert.Equal(t, saved.Special, current.Special)
	var revision CustomerInvoiceSettingsRevision
	require.NoError(t, db.Where("version = ?", saved.Version).Take(&revision).Error)
	assert.Equal(t, root, revision.ActorID)
	assert.Equal(t, saved.Version, revision.Version)
}

func TestCustomerInvoiceSettingsLegacySavePreservesLatestExplicitCenterSwitchPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	root := customerInvoiceTestUser(t, common.RoleRootUser)
	settings := defaultCustomerInvoiceSettings()
	settings.CenterEnabled = false
	settings, err := UpdateCustomerInvoiceSettings(ctx, root, settings)
	require.NoError(t, err)

	// Simulate an older binary appending a complete settings revision without
	// the new column, after the newer binary has already closed intake.
	legacy := settings
	legacy.Version++
	legacy.Special.TaxRateBPS = 600
	legacy.Categories = CustomerInvoiceCategories{}
	require.NoError(t, db.Omit("CenterEnabledValue").Create(&CustomerInvoiceSettingsRevision{CustomerInvoiceSettings: legacy, ActorID: root}).Error)
	current, err := GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, legacy, current, "inherit only the switch; honor the latest tax rate and explicit empty category list")

	current.CenterEnabled = true
	current, err = UpdateCustomerInvoiceSettings(ctx, root, current)
	require.NoError(t, err)
	legacy = current
	legacy.Version++
	legacy.Ordinary.TaxRateBPS = 300
	require.NoError(t, db.Omit("CenterEnabledValue").Create(&CustomerInvoiceSettingsRevision{CustomerInvoiceSettings: legacy, ActorID: root}).Error)
	current, err = GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, legacy, current, "an explicit reopen must supersede the previous disable")

	var revision CustomerInvoiceSettingsRevision
	require.NoError(t, db.Where("version = ?", legacy.Version).Take(&revision).Error)
	assert.Nil(t, revision.CenterEnabledValue, "reading legacy settings must not rewrite immutable revision data")
}

func TestCustomerInvoiceFeeAdmissionAndSnapshotPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	t.Setenv("CUSTOMER_INVOICE_WALLET_QUOTA_PER_CNY_CENT", "2") // rate alone is not payment compliance authorization
	ctx := t.Context()
	root, owner := customerInvoiceTestUser(t, common.RoleRootUser), customerInvoiceTestUser(t, common.RoleCommonUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	// At 1%, each line rounds to zero, but the invoice total rounds to one cent.
	one, two := customerInvoiceTestOrder(t, owner, "fee-part-a", 25, nil), customerInvoiceTestOrder(t, owner, "fee-part-b", 25, nil)
	next := defaultCustomerInvoiceSettings()
	next.Ordinary = CustomerInvoiceTypeSettings{Enabled: true, FeeEnabled: true, FeeRateBPS: 100, TaxRateBPS: 1300}
	next.Special = CustomerInvoiceTypeSettings{Enabled: false, FeeRateBPS: 500, TaxRateBPS: 600}
	next, err := UpdateCustomerInvoiceSettings(ctx, root, next)
	require.NoError(t, err)
	in := customerInvoiceTestInput("fee-admission", one.Id, two.Id)
	_, err = SubmitCustomerInvoice(ctx, owner, policy, in)
	require.ErrorIs(t, err, ErrCustomerInvoiceSettingsConflict)
	ver := next.Version
	in.SettingsVersion = &ver
	_, err = SubmitCustomerInvoice(ctx, owner, policy, in)
	require.ErrorIs(t, err, ErrCustomerInvoiceFeePaymentUnavailable)
	company := in
	company.InvoiceType, company.TitleType, company.TaxID = "special", "company", "TEST00000000000123"
	company.Address, company.Phone, company.BankName, company.BankAccount = "Synthetic office", "000-0000", "Synthetic bank", "123456"
	_, err = SubmitCustomerInvoice(ctx, owner, policy, company)
	require.ErrorIs(t, err, ErrCustomerInvoiceTypeDisabled)
	totals, err := SummarizeCustomerInvoices(ctx, owner, policy)
	require.NoError(t, err)
	assert.Equal(t, "50", totals.AvailableMinor)
	assert.Zero(t, totals.PendingCount)
	var count int64
	for _, entity := range []any{&CustomerInvoiceApplication{}, &CustomerInvoiceItem{}, &BalanceLedger{}, &BalanceLedgerOperation{}} {
		require.NoError(t, db.Model(entity).Count(&count).Error)
		assert.Zero(t, count)
	}
	next.Special.Enabled = true
	next, err = UpdateCustomerInvoiceSettings(ctx, root, next)
	require.NoError(t, err)
	_, err = SubmitCustomerInvoice(ctx, owner, policy, company)
	require.ErrorIs(t, err, ErrCustomerInvoiceSettingsConflict)
	ver = next.Version
	app, err := SubmitCustomerInvoice(ctx, owner, policy, company)
	require.NoError(t, err)
	assert.Equal(t, "50", app.AmountMinor)
	assert.Equal(t, "0", app.FeeMinor)
	assert.Zero(t, app.FeeRateBPS, "disabled fees have effective rate zero; don't silently apply a saved rate")
	assert.Equal(t, 600, app.TaxRateBPS)
	assert.Equal(t, ver, app.SettingsVersion)

	next.Special = CustomerInvoiceTypeSettings{Enabled: false, FeeEnabled: true, FeeRateBPS: 10000, TaxRateBPS: 0}
	_, err = UpdateCustomerInvoiceSettings(ctx, root, next)
	require.NoError(t, err)
	replay, err := SubmitCustomerInvoice(ctx, owner, policy, company)
	require.NoError(t, err)
	assert.Equal(t, app.ID, replay.ID)
	assert.Equal(t, 600, replay.TaxRateBPS)
	rows, _, err := ListCustomerInvoices(ctx, owner, false, CustomerInvoiceQuery{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, replay.CustomerInvoiceSummary, rows[0])
	_, err = ClaimCustomerInvoice(ctx, root, policy, app.ID)
	require.NoError(t, err, "settings changes do not reprice an accepted application")

	// A nonzero snapshot created outside the current admission path must not
	// be finance-claimable/issuable; a changed amount without independent settlement is not paid.
	require.NoError(t, db.Model(&CustomerInvoiceApplication{}).Where("id = ?", app.ID).Updates(map[string]any{"fee_rate_bps": 200, "fee_minor": 1}).Error)
	_, err = ClaimCustomerInvoice(ctx, root, policy, app.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceFeePaymentUnavailable)
	pdf, err := NewCustomerInvoicePDF(bytes.NewReader(customerInvoicePDFTestData()))
	require.NoError(t, err)
	_, err = IssueCustomerInvoice(ctx, root, policy, app.ID, "FEE-NOT-PAID", pdf)
	require.ErrorIs(t, err, ErrCustomerInvoiceFeePaymentUnavailable)
	require.NoError(t, db.Model(&CustomerInvoiceDocument{}).Count(&count).Error)
	assert.Zero(t, count)
	saved, err := GetCustomerInvoice(ctx, owner, false, app.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", saved.Status)
}

func TestCustomerInvoiceCenterSwitchBlocksOnlyNewApplicationsPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	root, owner := customerInvoiceTestUser(t, common.RoleRootUser), customerInvoiceTestUser(t, common.RoleCommonUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	order := customerInvoiceTestOrder(t, owner, "center-off", 1500, nil)

	settings, err := GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	settings.CenterEnabled = false
	settings, err = UpdateCustomerInvoiceSettings(ctx, root, settings)
	require.NoError(t, err)
	blocked, err := SubmitCustomerInvoice(ctx, owner, policy, customerInvoiceTestInput("center-off-blocked", order.Id))
	require.ErrorIs(t, err, ErrCustomerInvoiceDisabled)
	assert.Nil(t, blocked)
	var applications int64
	require.NoError(t, db.Model(&CustomerInvoiceApplication{}).Count(&applications).Error)
	assert.Zero(t, applications)

	settings.CenterEnabled = true
	settings, err = UpdateCustomerInvoiceSettings(ctx, root, settings)
	require.NoError(t, err)
	input := customerInvoiceTestInput("center-off-accepted", order.Id)
	version := settings.Version
	input.SettingsVersion = &version
	application, err := SubmitCustomerInvoice(ctx, owner, policy, input)
	require.NoError(t, err)

	settings.CenterEnabled = false
	_, err = UpdateCustomerInvoiceSettings(ctx, root, settings)
	require.NoError(t, err)
	replay, err := SubmitCustomerInvoice(ctx, owner, policy, input)
	require.NoError(t, err, "closing intake must not break an idempotent retry of an accepted application")
	assert.Equal(t, application.ID, replay.ID)
	stored, err := GetCustomerInvoice(ctx, owner, false, application.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", stored.Status)
	claimed, err := ClaimCustomerInvoice(ctx, root, policy, application.ID)
	require.NoError(t, err, "closing intake must not block finance processing")
	assert.True(t, claimed.Processing)
}

func TestCustomerInvoiceSettingsIM14UpgradePostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	owner, root := customerInvoiceTestUser(t, common.RoleCommonUser), customerInvoiceTestUser(t, common.RoleRootUser)
	order := customerInvoiceTestOrder(t, owner, "legacy-fee", 1500, nil)
	in := customerInvoiceTestInput("legacy-fee", order.Id)
	app, err := SubmitCustomerInvoice(ctx, owner, policy, in)
	require.NoError(t, err)
	// Emulate an authentic im.14 personal-form retry, including the precise
	// pre-version wire payload used to derive its persisted request hash.
	in.TitleType, in.TaxID = "personal", ""
	payload := fmt.Sprintf(`{"order_ids":[%d],"idempotency_key":"legacy-fee","invoice_type":"ordinary","title_type":"personal","title":"Invoice Fixture","category":"technical_service","email":"receipt@example.test"}`, order.Id)
	require.NoError(t, db.Model(&CustomerInvoiceApplication{}).Where("id = ?", app.ID).Updates(map[string]any{
		"title_type": "personal", "tax_id": "", "request_hash": fmt.Sprintf("%x", sha256.Sum256([]byte(payload))),
	}).Error)
	// Reconstruct the im.14 schema in this isolated schema, retaining the
	// actual pending application, verified receipt, unique claim and request hash.
	require.NoError(t, db.Migrator().DropTable(&CustomerInvoiceSettingsRevision{}))
	for _, col := range []string{"settings_version", "fee_rate_bps", "tax_rate_bps", "fee_minor", "fee_state", "fee_rail", "fee_paid_minor", "fee_paid_at", "wallet_quota_per_cent", "fee_trade_no", "fee_payment_method", "fee_gateway", "fee_receipt_id"} {
		require.NoError(t, db.Migrator().DropColumn(&CustomerInvoiceApplication{}, col))
	}
	for range 2 {
		require.NoError(t, db.AutoMigrate(CustomerInvoiceModels()...))
	}
	var explicitZero int64
	in.SettingsVersion = &explicitZero
	cfg := defaultCustomerInvoiceSettings()
	cfg.Ordinary.Enabled = false
	_, err = UpdateCustomerInvoiceSettings(ctx, root, cfg)
	require.NoError(t, err)
	old, err := SubmitCustomerInvoice(ctx, owner, policy, in)
	require.NoError(t, err)
	assert.Equal(t, "personal", old.TitleType)
	fresh := in
	fresh.IdempotencyKey = "new-personal"
	_, err = SubmitCustomerInvoice(ctx, owner, policy, fresh)
	require.ErrorIs(t, err, ErrCustomerInvoiceInvalid)
	assert.Equal(t, app.ID, old.ID)
	assert.Equal(t, "1500", old.AmountMinor)
	assert.Equal(t, "0", old.FeeMinor)
	assert.Zero(t, old.SettingsVersion)
	assert.Zero(t, old.TaxRateBPS)
	_, err = ClaimCustomerInvoice(ctx, root, policy, app.ID)
	require.ErrorIs(t, err, ErrCustomerInvoiceInvalid)
	rejected, err := RejectCustomerInvoice(ctx, root, app.ID, "Company applications only")
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)
}
