package model

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareRebuildBonusCache(t *testing.T) Model {
	t.Helper()
	resetPricingEndpointTestTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	InvalidateModelBonusEligibleCache()
	t.Cleanup(InvalidateModelBonusEligibleCache)
	row := Model{ModelName: "rebuild-bonus-cache", Status: 1, SyncOfficial: 1, BonusEligible: boolPtr(true)}
	require.NoError(t, row.Insert())
	insertPricingEndpointChannel(t, 19001, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 19001, row.ModelName)
	InitChannelCache()
	InvalidatePricingCache()
	return row
}

func TestBonusEligibilityExpiresPreviouslyTrueCache(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	modelBonusEligibleLock.RLock()
	cached := modelBonusEligibleMap[row.ModelName]
	modelBonusEligibleLock.RUnlock()
	require.True(t, cached, "exercise the positive cache, not exact-row fallback")
	// An independent instance commits the revocation. Local cache clocks are
	// advanced past TTL without sleeping; this remains a real SQLite write/read.
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", row.Id).Update("bonus_eligible", false).Error)
	expired := time.Now().Add(-2 * modelBonusEligibleCacheTTL)
	modelBonusEligibleLock.Lock()
	modelBonusEligibleTime = expired
	modelBonusEligibleLock.Unlock()
	updatePricingLock.Lock()
	lastGetPricingTime = expired
	updatePricingLock.Unlock()
	require.False(t, IsModelBonusEligible(row.ModelName), "a previous true must not bypass TTL after a committed revocation")
}

func TestBonusEligibilityDoesNotRepublishInvalidatedSnapshot(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	captured := make(chan struct{})
	resume := make(chan struct{})
	var pauseOnce, resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	const callback = "rebuild_bonus_cache_capture"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "models" {
			pauseOnce.Do(func() { close(captured); <-resume })
		}
	}))
	result := make(chan bool, 1)
	joined := false
	t.Cleanup(func() {
		release()
		if !joined {
			select {
			case <-result:
			case <-time.After(3 * time.Second):
				t.Error("owned cache loader did not join during cleanup")
			}
		}
		_ = DB.Callback().Query().Remove(callback)
	})
	go func() { result <- IsModelBonusEligible(row.ModelName) }()
	select {
	case <-captured:
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("pricing loader did not reach actual model query")
	}
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", row.Id).Update("bonus_eligible", false).Error)
	InvalidateModelBonusEligibleCache()
	release()
	select {
	case eligible := <-result:
		joined = true
		require.False(t, eligible, "an older in-flight snapshot must not republish true after invalidation")
	case <-time.After(3 * time.Second):
		t.Fatal("pricing loader did not join")
	}
	RefreshPricing()
	require.False(t, IsModelBonusEligible(row.ModelName))
}

func TestBonusEligibilityCommittedMetadataRefreshControls(t *testing.T) {
	for _, operation := range []string{"update", "sync", "delete"} {
		t.Run(operation, func(t *testing.T) {
			row := prepareRebuildBonusCache(t)
			require.True(t, IsModelBonusEligible(row.ModelName))
			switch operation {
			case "update":
				row.BonusEligible = boolPtr(false)
				require.NoError(t, row.Update())
				RefreshPricing() // Existing public admin path.
			case "sync":
				_, err := ApplyMetadataSync([]MetadataSyncUpdate{{MetadataSyncSelection: MetadataSyncSelection{
					ModelName: row.ModelName, RecordVersion: MetadataRecordVersion(&row, nil, nil), Fields: []string{"bonus_eligible"},
				}, Values: MetadataValues{Status: 1, BonusEligible: boolPtr(false)}}}, nil)
				require.NoError(t, err)
			case "delete":
				require.NoError(t, row.Delete())
			}
			require.False(t, IsModelBonusEligible(row.ModelName))
		})
	}
}

func TestBonusEligibilityDoesNotExtendPricingSnapshotLifetime(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	GetPricing()
	originalTime := time.Now().Add(-modelBonusEligibleCacheTTL / 2)
	updatePricingLock.Lock()
	lastGetPricingTime = originalTime
	updatePricingLock.Unlock()
	require.True(t, IsModelBonusEligible(row.ModelName))
	modelBonusEligibleLock.RLock()
	cacheTime := modelBonusEligibleTime
	modelBonusEligibleLock.RUnlock()
	require.Equal(t, originalTime, cacheTime, "eligibility must inherit the actual pricing snapshot age")
}

func TestBonusEligibilityModelUpdateInvalidatesAfterCommit(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	row.BonusEligible = boolPtr(false)
	require.NoError(t, row.Update())
	// No controller RefreshPricing is needed for a committed model mutation.
	require.False(t, IsModelBonusEligible(row.ModelName))
}

func TestBonusEligibilityRejectedMutationKeepsEligibility(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	row.VendorID, row.BonusEligible = 999999, boolPtr(false)
	require.Error(t, row.Update())
	require.True(t, IsModelBonusEligible(row.ModelName))
}

func TestBonusEligibilityCommitACKLossInvalidates(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	row.BonusEligible = boolPtr(false)
	subscriptionLoseCommitAck(t, DB, row.Update)
	var stored Model
	require.NoError(t, DB.First(&stored, row.Id).Error)
	require.NotNil(t, stored.BonusEligible)
	require.False(t, *stored.BonusEligible, "revocation really committed before its acknowledgement was lost")
	require.False(t, IsModelBonusEligible(row.ModelName), "an unknown COMMIT must make the old positive cache cold without waiting for TTL")
}

// Every public eligibility writer must evict both layers after its transaction,
// including an unknown COMMIT outcome. A cold grant cache alone can otherwise
// republish a still-fresh positive entry from the pricing snapshot.
func TestBonusEligibilityMutationCommitBoundaries(t *testing.T) {
	for _, scenario := range []string{"batch_commit", "batch_lost_ack", "delete_lost_ack", "delete_pricing_lost_ack", "sync_bonus_lost_ack", "sync_name_rule_lost_ack"} {
		t.Run(scenario, func(t *testing.T) {
			row := prepareRebuildBonusCache(t)
			probe := row.ModelName
			if scenario == "sync_name_rule_lost_ack" {
				row.NameRule = NameRulePrefix
				require.NoError(t, row.Update())
				probe += "-child"
				insertPricingEndpointAbility(t, 19001, probe)
				InitChannelCache()
			}
			if scenario == "batch_commit" || scenario == "batch_lost_ack" {
				require.NoError(t, DB.AutoMigrate(&ModelBonusEligibleAudit{}))
				t.Cleanup(func() {
					require.NoError(t, DB.Where("model_id = ?", row.Id).Delete(&ModelBonusEligibleAudit{}).Error)
				})
			}
			if scenario == "delete_pricing_lost_ack" {
				// The delete+pricing branch persists option maps in the same real
				// transaction. Restore only its exact keys; never leak test options.
				var previous []Option
				require.NoError(t, DB.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Find(&previous).Error)
				t.Cleanup(func() {
					require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
						if err := tx.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Delete(&Option{}).Error; err != nil {
							return err
						}
						if len(previous) > 0 {
							return tx.Create(&previous).Error
						}
						return nil
					}))
				})
			}
			var original Model
			require.NoError(t, DB.First(&original, row.Id).Error)
			require.True(t, IsModelBonusEligible(probe))
			modelBonusEligibleLock.RLock()
			warmGrant := modelBonusEligibleMap[probe] && modelBonusEligibleCacheFresh()
			modelBonusEligibleLock.RUnlock()
			require.True(t, warmGrant, "the original positive cache, not exact-row fallback, must be exercised")

			apply := func() error {
				switch scenario {
				case "batch_commit", "batch_lost_ack":
					changed, err := BatchUpdateModelBonusEligible([]int{row.Id}, boolPtr(false), 42, "fixture-admin", t.Name())
					if err == nil {
						require.Equal(t, 1, changed)
					}
					return err
				case "delete_lost_ack", "delete_pricing_lost_ack":
					_, err := DeleteModelMetadata([]int{row.Id}, false, scenario == "delete_pricing_lost_ack")
					return err
				default:
					field, values := "bonus_eligible", MetadataValues{BonusEligible: boolPtr(false)}
					if scenario == "sync_name_rule_lost_ack" {
						field, values = "name_rule", MetadataValues{NameRule: NameRuleExact}
					}
					_, err := ApplyMetadataSync([]MetadataSyncUpdate{{MetadataSyncSelection: MetadataSyncSelection{
						ModelName: row.ModelName, RecordVersion: MetadataRecordVersion(&original, nil, nil), Fields: []string{field},
					}, Values: values}}, nil)
					return err
				}
			}
			if scenario == "batch_commit" {
				require.NoError(t, apply())
			} else {
				subscriptionLoseCommitAck(t, DB, apply) // proves one real COMMIT before the error
			}

			var stored Model
			if scenario == "delete_lost_ack" || scenario == "delete_pricing_lost_ack" {
				require.ErrorIs(t, DB.First(&stored, row.Id).Error, gorm.ErrRecordNotFound)
			} else {
				require.NoError(t, DB.First(&stored, row.Id).Error)
				if scenario == "sync_name_rule_lost_ack" {
					require.Equal(t, NameRuleExact, stored.NameRule)
					require.NotNil(t, stored.BonusEligible)
					require.True(t, *stored.BonusEligible, "only matching policy changed, not the explicit grant")
				} else {
					require.NotNil(t, stored.BonusEligible)
					require.False(t, *stored.BonusEligible)
				}
			}
			if scenario == "batch_commit" || scenario == "batch_lost_ack" {
				var audits int64
				require.NoError(t, DB.Model(&ModelBonusEligibleAudit{}).Where("model_id = ?", row.Id).Count(&audits).Error)
				require.EqualValues(t, 1, audits, "the original audit transaction remains committed exactly once")
			}
			modelBonusEligibleLock.RLock()
			grantCold := modelBonusEligibleTime.IsZero()
			modelBonusEligibleLock.RUnlock()
			updatePricingLock.Lock()
			pricingCold := lastGetPricingTime.IsZero()
			updatePricingLock.Unlock()
			require.False(t, IsModelBonusEligible(probe), "a committed revocation must not wait for either cache TTL")
			require.True(t, grantCold, "the eligibility layer must have been evicted before readback")
			require.True(t, pricingCold, "the pricing layer must have been evicted before readback")
		})
	}
}
