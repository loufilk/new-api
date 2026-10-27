package model

import (
	"testing"

	"github.com/QuantumNous/new-api/extension"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This is the exact AutoMigrate argument sequence before M4 replaced the five
// trailing ChuanAPI models with registry expansion. A changed index is a schema
// ordering change and must be reviewed explicitly rather than hidden inside a
// composition refactor.
func TestMainAutoMigrateModelsPreservesPreRegistrySequence(t *testing.T) {
	reg := extension.NewRegistry()
	require.NoError(t, reg.RegisterModelSet(extension.ModelSet{
		Extension: "migration-sequence-test",
		Name:      "chuanapi-wallet-models",
		Phase:     extension.PhaseAfterCoreModels,
		Models: []any{
			&BalanceLedgerOperation{},
			&BalanceLedger{},
			&ModelBonusEligibleAudit{},
			&ReferralCommission{},
		},
	}))

	models := mainAutoMigrateModels(reg)
	expected := []any{
		&Channel{},
		&Token{},
		&User{},
		&UserSession{},
		&AuthFlow{},
		&ExternalIdentityClaim{},
		&PasskeyCredential{},
		&Option{},
		&LoginEncryptionKey{},
		&Redemption{},
		&Ability{},
		&Log{},
		&Midjourney{},
		&TopUp{},
		&QuotaData{},
		&Task{},
		&TaskPlugin{},
		&Model{},
		&Vendor{},
		&PrefillGroup{},
		&Setup{},
		&TwoFA{},
		&TwoFABackupCode{},
		&Checkin{},
		&SubscriptionOrder{},
		&UserSubscription{},
		&SubscriptionPreConsumeRecord{},
		&CustomOAuthProvider{},
		&UserOAuthBinding{},
		&PerfMetric{},
		&SystemInstance{},
		&SystemTask{},
		&SystemTaskLock{},
		&CasbinRule{},
		&AuthzRole{},
		&UserAccessToken{},
		&BillingFinalization{},
		&BalanceLedgerOperation{},
		&BalanceLedger{},
		&ModelBonusEligibleAudit{},
		&ReferralCommission{},
	}

	require.Len(t, models, len(expected))
	for i, want := range expected {
		assert.IsType(t, want, models[i], "AutoMigrate model at index %d changed", i)
	}
}

func TestMainAutoMigrateModelsWithoutRegistryKeepsCoreModels(t *testing.T) {
	models := mainAutoMigrateModels(nil)
	require.Len(t, models, 37)
	assert.IsType(t, &Channel{}, models[0])
	assert.IsType(t, &BillingFinalization{}, models[len(models)-1])
}
