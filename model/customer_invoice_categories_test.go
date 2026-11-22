package model

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCustomerInvoiceCategoryLimitsPostgres(t *testing.T) {
	setupCustomerInvoicePG(t)
	ctx := t.Context()
	root := customerInvoiceTestUser(t, common.RoleRootUser)
	maximum := make(CustomerInvoiceCategories, 30)
	for i := range maximum {
		maximum[i] = CustomerInvoiceCategory{ID: fmt.Sprintf("cat_%02d", i), Name: fmt.Sprintf("%02d", i), Enabled: true}
	}
	maximum[0] = CustomerInvoiceCategory{ID: strings.Repeat("a", 32), Name: strings.Repeat("界", 60), Enabled: true}
	invalidLists := []CustomerInvoiceCategories{
		append(slices.Clone(maximum), CustomerInvoiceCategory{ID: "other", Name: "other"}),
		{{ID: strings.Repeat("a", 33), Name: "Valid"}},
		{{ID: "ID", Name: "Valid"}}, {{ID: "_id", Name: "Valid"}}, {{ID: "a-b", Name: "Valid"}},
		{{ID: "one", Name: "  "}}, {{ID: "one", Name: strings.Repeat("界", 61)}},
		{{ID: "one", Name: "name\x00"}}, {{ID: "one", Name: "\nname"}},
		{{ID: "one", Name: "A", Enabled: true}, {ID: "one", Name: "B"}},
		{{ID: "one", Name: " Ｓtraße ", Enabled: true}, {ID: "two", Name: "strasse"}},
		{{ID: "one", Name: "Cafe\u0301", Enabled: true}, {ID: "two", Name: "Café"}},
	}
	for _, categories := range invalidLists {
		cfg := defaultCustomerInvoiceSettings()
		cfg.Categories = categories
		_, err := UpdateCustomerInvoiceSettings(ctx, root, cfg)
		require.ErrorIs(t, err, ErrCustomerInvoiceInvalid)
	}
	cfg := defaultCustomerInvoiceSettings()
	cfg.Categories = maximum
	saved, err := UpdateCustomerInvoiceSettings(ctx, root, cfg)
	require.NoError(t, err)
	assert.Equal(t, maximum, saved.Categories)
	assert.EqualValues(t, 1, saved.Version)
}

// Start with a representative im.15 schema including a real pre-category
// revision and application with an active order, then migrate/restart twice.
func TestCustomerInvoiceCategoryIM15UpgradeAndAdmissionPostgres(t *testing.T) {
	db := setupCustomerInvoicePG(t)
	ctx := t.Context()
	root, owner := customerInvoiceTestUser(t, common.RoleRootUser), customerInvoiceTestUser(t, common.RoleCommonUser)
	policy := CustomerInvoicePolicy{Enabled: true, MerchantScope: invoiceTestMerchant}
	cfg := defaultCustomerInvoiceSettings()
	cfg.Special.TaxRateBPS = 600
	cfg, err := UpdateCustomerInvoiceSettings(ctx, root, cfg)
	require.NoError(t, err)
	im15Version := cfg.Version
	oldOrder := customerInvoiceTestOrder(t, owner, "im15-category", 432, nil)
	oldInput := customerInvoiceTestInput("im15-category", oldOrder.Id)
	oldInput.SettingsVersion = &im15Version
	oldApp, err := SubmitCustomerInvoice(ctx, owner, policy, oldInput)
	require.NoError(t, err)
	for _, col := range []struct {
		table any
		name  string
	}{
		{&CustomerInvoiceSettingsRevision{}, "categories"},
		{&CustomerInvoiceApplication{}, "category_name"},
	} {
		require.NoError(t, db.Migrator().DropColumn(col.table, col.name))
	}
	for range 2 {
		require.NoError(t, db.AutoMigrate(CustomerInvoiceModels()...))
	}
	cfg, err = GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, CustomerInvoiceCategories{
		{ID: "technical_service", Name: "技术服务费", Enabled: true},
		{ID: "software_development", Name: "软件开发费", Enabled: true},
	}, cfg.Categories)
	assert.Equal(t, im15Version, cfg.Version)
	assert.Equal(t, 600, cfg.Special.TaxRateBPS)

	cfg.Categories = CustomerInvoiceCategories{
		{ID: "consulting", Name: "  咨询服务费  ", Enabled: true},
		{ID: "technical_service", Name: "技术顾问费", Enabled: true},
		{ID: "software_development", Name: "软件开发费", Enabled: false},
	}
	cfg, err = UpdateCustomerInvoiceSettings(ctx, root, cfg)
	require.NoError(t, err)
	assert.Equal(t, "咨询服务费", cfg.Categories[0].Name)
	customVersion := cfg.Version
	// Legacy clients editing only existing rate switches preserve the catalog.
	legacyEdit := cfg
	legacyEdit.Categories = nil
	same, err := UpdateCustomerInvoiceSettings(ctx, root, legacyEdit)
	require.NoError(t, err)
	require.Equal(t, cfg, same)
	newOrder := customerInvoiceTestOrder(t, owner, "im16-category", 999, nil)
	in := customerInvoiceTestInput("im16-category", newOrder.Id)
	in.SettingsVersion = &customVersion
	for _, unavailable := range []string{"unknown", "software_development"} {
		in.Category = unavailable
		_, err = SubmitCustomerInvoice(ctx, owner, policy, in)
		require.ErrorIs(t, err, ErrCustomerInvoiceCategoryUnavailable)
	}
	in.Category = "consulting"
	app, err := SubmitCustomerInvoice(ctx, owner, policy, in)
	require.NoError(t, err)
	assert.Equal(t, "咨询服务费", app.CategoryName)
	order := customerInvoiceTestOrder(t, owner, "renamed-builtin", 1000, nil)
	builtInInput := customerInvoiceTestInput("renamed-builtin", order.Id)
	builtInInput.SettingsVersion = &customVersion
	builtIn, err := SubmitCustomerInvoice(ctx, owner, policy, builtInInput)
	require.NoError(t, err)
	assert.Equal(t, "技术顾问费", builtIn.CategoryName)

	cfg.Categories[0].Name = "变更后的名称"
	cfg.Categories[1].Enabled = false
	cfg, err = UpdateCustomerInvoiceSettings(ctx, root, cfg)
	require.NoError(t, err)
	_, err = UpdateCustomerInvoiceSettings(ctx, root, legacyEdit)
	require.ErrorIs(t, err, ErrCustomerInvoiceSettingsConflict, "a stale rates editor cannot overwrite the catalog")
	futureOrder := customerInvoiceTestOrder(t, owner, "future-category", 655, nil)
	future := customerInvoiceTestInput("future-category", futureOrder.Id)
	future.Category, future.SettingsVersion = "consulting", &customVersion
	_, err = SubmitCustomerInvoice(ctx, owner, policy, future)
	require.ErrorIs(t, err, ErrCustomerInvoiceSettingsConflict)
	renamedVersion := cfg.Version
	future.SettingsVersion = &renamedVersion
	renamed, err := SubmitCustomerInvoice(ctx, owner, policy, future)
	require.NoError(t, err)
	assert.Equal(t, "变更后的名称", renamed.CategoryName)

	cfg.Categories = CustomerInvoiceCategories{}
	empty, err := UpdateCustomerInvoiceSettings(ctx, root, cfg)
	require.NoError(t, err)
	require.NotNil(t, empty.Categories)
	legacyEdit = empty
	legacyEdit.Categories = nil
	legacyEdit.Special.TaxRateBPS = 0 // write a new revision without categories
	closed, err := UpdateCustomerInvoiceSettings(ctx, root, legacyEdit)
	require.NoError(t, err)
	assert.Greater(t, closed.Version, empty.Version)
	cfg, err = GetCustomerInvoiceSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, CustomerInvoiceCategories{}, cfg.Categories)
	lastOrder := customerInvoiceTestOrder(t, owner, "closed-category", 654, nil)
	blocked := customerInvoiceTestInput("closed-category", lastOrder.Id)
	blocked.SettingsVersion = &cfg.Version
	_, err = SubmitCustomerInvoice(ctx, owner, policy, blocked)
	require.ErrorIs(t, err, ErrCustomerInvoiceCategoryUnavailable)
	totals, err := SummarizeCustomerInvoices(ctx, owner, policy)
	require.NoError(t, err)
	assert.Equal(t, "654", totals.AvailableMinor, "failed admission does not reserve the order")
	snapshots := map[int64]string{oldApp.ID: "", app.ID: "咨询服务费", builtIn.ID: "技术顾问费", renamed.ID: "变更后的名称"}
	for _, admin := range []bool{false, true} {
		actor := owner
		if admin {
			actor = root
		}
		list, _, err := ListCustomerInvoices(ctx, actor, admin, CustomerInvoiceQuery{})
		require.NoError(t, err)
		require.Len(t, list, len(snapshots))
		for _, summary := range list {
			require.Contains(t, snapshots, summary.ID)
			assert.Equal(t, snapshots[summary.ID], summary.CategoryName)
			detail, err := GetCustomerInvoice(ctx, actor, admin, summary.ID)
			require.NoError(t, err)
			assert.Equal(t, summary.CategoryName, detail.CategoryName)
		}
	}
	for _, tc := range []struct {
		input CustomerInvoiceInput
		app   *CustomerInvoiceDetail
	}{{oldInput, oldApp}, {in, app}, {builtInInput, builtIn}, {future, renamed}} {
		replayed, err := SubmitCustomerInvoice(ctx, owner, policy, tc.input)
		require.NoError(t, err)
		assert.Equal(t, tc.app.ID, replayed.ID)
		assert.Equal(t, tc.app.Category, replayed.Category)
		assert.Equal(t, tc.app.CategoryName, replayed.CategoryName)
	}
}
