package model

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func prepareMarketActivityPG(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_MODEL_POSTGRES_DSN") == "" {
		t.Skip("requires isolated PostgreSQL 18.6 TEST_MODEL_POSTGRES_DSN")
	}
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"existing":1}`)
}

func marketProjection(t *testing.T, scopes map[string]bool) map[string]GroupMarketActivity {
	t.Helper()
	guard, err := BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	defer guard.Close()
	groups, available := ProjectGroupMarketActivity(guard.Version, scopes)
	require.True(t, available)
	require.NoError(t, guard.Finish())
	return groups
}

func TestMarketActivityAutomaticLifecycle(t *testing.T) {
	prepareMarketActivityPG(t)
	scopes := map[string]bool{"existing": true, "brand-new": true}
	require.Empty(t, marketProjection(t, scopes), "opening the catalogue never backfills existing groups")

	start := time.Now().Unix()
	require.NoError(t, UpdateOption("GroupRatio", `{"existing":1,"brand-new":0.5}`))
	view := marketProjection(t, scopes)
	require.NotContains(t, view, "existing")
	created := view["brand-new"].FirstConfiguredAt
	require.GreaterOrEqual(t, created, start)
	require.True(t, view["brand-new"].IsNew)
	require.Empty(t, view["brand-new"].RateChanges, "first definition is not a price change")

	require.NoError(t, UpdateOptionsBulk(map[string]string{"group_ratio_setting.group_ratio": `{"existing":0,"brand-new":1.25}`}))
	view = marketProjection(t, scopes)
	require.Len(t, view["existing"].RateChanges, 1)
	require.Equal(t, float64(0), *view["existing"].RateChanges[0].NewRatio)
	require.True(t, view["existing"].RecentRateChange)
	require.Zero(t, view["existing"].FirstConfiguredAt)
	require.Equal(t, created, view["brand-new"].FirstConfiguredAt)
	require.Equal(t, 0.5, *view["brand-new"].RateChanges[0].OldRatio)
	require.Equal(t, 1.25, *view["brand-new"].RateChanges[0].NewRatio)

	// Grant decisions are made by the caller. Neither private prices nor
	// unrequested group names appear, even when a personal rate equals base.
	require.Empty(t, marketProjection(t, map[string]bool{"existing": false}))
	require.Empty(t, marketProjection(t, map[string]bool{"brand-new": false})["brand-new"].RateChanges)
	require.NotContains(t, marketProjection(t, map[string]bool{"brand-new": true}), "existing")

	// A semantically equivalent save and an unrelated namespace edit advance
	// the cursor but don't duplicate events or reset the original stamp.
	require.NoError(t, UpdateOption("GroupRatio", `{"brand-new":1.250,"existing":0.0}`))
	require.NoError(t, UpdateOption("TopupGroupRatio", `{"existing":2}`))
	require.Equal(t, view, marketProjection(t, scopes))
	before, err := readGroupConfigVersion(t.Context())
	require.NoError(t, err)
	_, err = ApplyGroupRename(GroupRenameApplyRequest{Options: map[string]string{"GroupRatio": `{"brand-new":1.75,"existing":0}`}, Expected: map[string]string{"GroupRatio": "stale"}})
	require.ErrorIs(t, err, ErrGroupRenameStale)
	forced := errors.New("force outer transaction rollback")
	groupRenameTxFailureHook = func() error { return forced }
	t.Cleanup(func() { groupRenameTxFailureHook = nil })
	require.ErrorIs(t, UpdateOption("GroupRatio", `{"brand-new":1.75,"existing":0}`), forced)
	groupRenameTxFailureHook = nil
	next, err := readGroupConfigVersion(t.Context())
	require.NoError(t, err)
	require.Equal(t, before, next)
	require.Equal(t, view, marketProjection(t, scopes))

	_, err = ApplyGroupRename(GroupRenameApplyRequest{
		Mappings: []GroupRenameMapping{{From: "brand-new", To: "renamed"}},
		Options:  map[string]string{"GroupRatio": `{"renamed":1.5,"existing":0}`},
	})
	require.NoError(t, err)
	scopes["renamed"] = true
	view = marketProjection(t, scopes)
	require.NotContains(t, view, "brand-new")
	require.Equal(t, created, view["renamed"].FirstConfiguredAt)
	require.Len(t, view["renamed"].RateChanges, 2)
	require.Equal(t, 1.25, *view["renamed"].RateChanges[0].OldRatio)
	require.Equal(t, 1.5, *view["renamed"].RateChanges[0].NewRatio)

	require.NoError(t, UpdateOption("group_ratio_setting.group_ratio", `{"existing":0}`))
	require.NotContains(t, marketProjection(t, scopes), "renamed")
	require.NoError(t, UpdateOption("GroupRatio", `{"existing":0,"renamed":2}`))
	require.Empty(t, marketProjection(t, scopes)["renamed"].RateChanges, "reused name cannot inherit retired history")
}

func TestMarketActivityContinuityAndCorruption(t *testing.T) {
	prepareMarketActivityPG(t)
	scopes := map[string]bool{"existing": true}
	require.NoError(t, UpdateOption("GroupRatio", `{"existing":2}`))
	require.Len(t, marketProjection(t, scopes)["existing"].RateChanges, 1)
	// Model an older writer rolling the price away and back, skipping the cursor.
	last, err := readGroupConfigVersion(t.Context())
	require.NoError(t, err)
	require.NoError(t, upsertGroupRenameOption(DB, groupConfigVersionOptionKey, strconv.FormatInt(last+2, 10)))
	guard, err := BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	projected, ok := ProjectGroupMarketActivity(guard.Version, scopes)
	guard.Close()
	require.False(t, ok)
	require.Empty(t, projected)
	require.NoError(t, UpdateOption("GroupRatio", `{"existing":3}`))
	view := marketProjection(t, scopes)
	require.Len(t, view["existing"].RateChanges, 1)
	require.Equal(t, 2.0, *view["existing"].RateChanges[0].OldRatio)
	var archive Option
	require.NoError(t, DB.Where(&Option{Key: groupMarketArchiveKey}).First(&archive).Error)
	require.Contains(t, archive.Value, `"new_ratio":2`)

	for _, key := range []string{groupMarketCursorKey, groupMarketDataKey, groupMarketArchiveKey} {
		require.Error(t, UpdateOption(key, "{}"))
		require.Error(t, UpdateOptionsBulk(map[string]string{key: "bad"}))
	}
	require.NoError(t, upsertGroupRenameOption(DB, groupMarketDataKey, "corrupt"))
	require.NoError(t, UpdateOption("GroupRatio", `{"existing":4}`), "optional history never blocks a valid price write")
	require.Equal(t, 4.0, ratio_setting.GetGroupRatio("existing"))
	require.NoError(t, LoadOptionsFromDatabase())
	guard, err = BeginGroupConfigRead(t.Context())
	require.NoError(t, err, "invalid display data does not disable admission")
	projected, ok = ProjectGroupMarketActivity(guard.Version, scopes)
	guard.Close()
	require.False(t, ok)
	require.Empty(t, projected)
	var kept Option
	require.NoError(t, DB.Where(&Option{Key: groupMarketDataKey}).First(&kept).Error)
	require.Equal(t, "corrupt", kept.Value, "never silently replace an unreadable history")
	common.OptionMapRWMutex.RLock()
	for _, key := range []string{groupMarketCursorKey, groupMarketDataKey, groupMarketArchiveKey} {
		_, exists := common.OptionMap[key]
		require.False(t, exists)
	}
	common.OptionMapRWMutex.RUnlock()
}

func TestMarketActivityBoundaries(t *testing.T) {
	prepareMarketActivityPG(t)
	now := time.Now().Unix()
	records := make(map[string]marketRecord)
	change := func(before, after string) *marketTransition {
		return &marketTransition{before: map[string]string{"GroupRatio": before}, after: map[string]string{"GroupRatio": after}}
	}
	require.NoError(t, change(`{"existing":0}`, `{"existing":1}`).apply(records, now-marketHistorySeconds-100))
	for i := 1; i <= 12; i++ {
		require.NoError(t, change(fmt.Sprintf(`{"existing":%d}`, i), fmt.Sprintf(`{"existing":%d}`, i+1)).apply(records, now-marketRecentSeconds-100))
	}
	require.Len(t, records["existing"].RateChanges, marketMaxChanges)
	rec := records["existing"]
	rec.FirstConfiguredAt = now - marketRecentSeconds - 1
	records["existing"] = rec
	snap := &marketSnapshot{revision: 1, records: records}
	marketRuntime.Store(snap)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"existing":13}`))
	projected, ok := ProjectGroupMarketActivity(1, map[string]bool{"existing": true})
	require.True(t, ok)
	require.False(t, projected["existing"].IsNew)
	require.False(t, projected["existing"].RecentRateChange)
	require.Len(t, projected["existing"].RateChanges, marketMaxChanges)
	require.Equal(t, 12.0, *projected["existing"].RateChanges[0].OldRatio)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"existing":50}`))
	projected, ok = ProjectGroupMarketActivity(1, map[string]bool{"existing": true})
	require.True(t, ok)
	require.Empty(t, projected["existing"].RateChanges, "latest observation is not effective if current base differs")
	rec.RateChanges[0].EffectiveAt = now - marketHistorySeconds - 1
	rec.RateChanges = rec.RateChanges[:1]
	records["existing"] = rec
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"existing":13}`))
	projected, _ = ProjectGroupMarketActivity(1, map[string]bool{"existing": true})
	require.Empty(t, projected["existing"].RateChanges)
	// Conflicting legacy aliases cannot establish an authoritative old price.
	before := maps.Clone(records)
	err := (&marketTransition{before: map[string]string{"GroupRatio": `{"existing":13}`, "group_ratio_setting.group_ratio": `{"existing":2}`}, after: map[string]string{"GroupRatio": `{"existing":14}`}}).apply(records, now)
	require.Error(t, err)
	require.Equal(t, before, records)
}

func TestMarketActivityMalformedOrSyntheticData(t *testing.T) {
	for _, raw := range []string{`{"existing":{"rate_changes":[{"new_ratio":2,"effective_at":1}]}}`, `{"existing":{"rate_changes":[{"old_ratio":2,"new_ratio":2,"effective_at":1}]}}`, `{"auto":{"first_configured_at":1}}`, `{"ghost":{"unexpected":"date"}}`, `null`, "", `{"existing":{"rate_changes":[{"old_ratio":0,"new_ratio":-3,"effective_at":1}]}}`} {
		_, err := parseMarketSnapshot(map[string]string{groupMarketDataKey: raw, groupMarketCursorKey: "1"})
		require.Error(t, err, raw)
	}
}

func TestMarketActivitySidecarSQLFailureIsIsolated(t *testing.T) {
	prepareMarketActivityPG(t)
	require.NoError(t, UpdateOption("GroupRatio", `{"existing":2}`))
	// A real SQL error aborts a PostgreSQL transaction unless the change is
	// enclosed in a savepoint. Do not use a Go-only forced error for this case.
	require.NoError(t, DB.Exec(`ALTER TABLE options ADD CONSTRAINT test_market_sidecar_unavailable CHECK ("key" <> 'GroupCatalogMarketActivity') NOT VALID`).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Exec(`ALTER TABLE options DROP CONSTRAINT IF EXISTS test_market_sidecar_unavailable`).Error)
	})
	require.NoError(t, UpdateOption("GroupRatio", `{"existing":4}`))
	var opt Option
	require.NoError(t, DB.Where(&Option{Key: "GroupRatio"}).First(&opt).Error)
	require.JSONEq(t, `{"existing":4}`, opt.Value)
	guard, err := BeginGroupConfigRead(t.Context())
	require.NoError(t, err)
	defer guard.Close()
	projected, available := ProjectGroupMarketActivity(guard.Version, map[string]bool{"existing": true})
	require.False(t, available)
	require.Empty(t, projected)
}
