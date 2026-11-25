package model

import (
	"cmp"
	"errors"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

// None of these option rows are access policy, pricing inputs, or generic
// writable settings. A rollback binary can ignore all three without migration.
const (
	groupMarketDataKey    = "GroupCatalogMarketActivity"
	groupMarketCursorKey  = "GroupCatalogMarketActivityCursor"
	groupMarketArchiveKey = "GroupCatalogMarketActivityRecovery"
	marketMaxGroups       = 2000
	marketMaxChanges      = 10
	marketRecentSeconds   = 7 * 24 * 60 * 60
	marketHistorySeconds  = 90 * 24 * 60 * 60
)

type GroupMarketRateChange struct {
	OldRatio    *float64 `json:"old_ratio"`
	NewRatio    *float64 `json:"new_ratio"`
	EffectiveAt int64    `json:"effective_at"`
}

type marketRecord struct {
	FirstConfiguredAt int64                   `json:"first_configured_at,omitempty"`
	RateChanges       []GroupMarketRateChange `json:"rate_changes,omitempty"`
}

type GroupMarketActivity struct {
	FirstConfiguredAt int64                   `json:"first_configured_at,omitempty"`
	IsNew             bool                    `json:"is_new,omitempty"`
	RateChanges       []GroupMarketRateChange `json:"rate_changes,omitempty"`
	RecentRateChange  bool                    `json:"recent_rate_change,omitempty"`
}

type marketSnapshot struct {
	revision int64
	records  map[string]marketRecord
}

var marketRuntime atomic.Pointer[marketSnapshot]
var errInvalidMarketActivity = errors.New("invalid marketplace activity metadata")

func isGroupMarketOption(key string) bool {
	return key == groupMarketDataKey || key == groupMarketCursorKey || key == groupMarketArchiveKey
}

func validMarketRatio(value *float64) bool {
	return value != nil && *value >= 0 && !math.IsNaN(*value) && !math.IsInf(*value, 0)
}

func parseMarketSnapshot(values map[string]string) (*marketSnapshot, error) {
	raw, hasData := values[groupMarketDataKey]
	cursor, hasCursor := values[groupMarketCursorKey]
	if !hasData && !hasCursor {
		return &marketSnapshot{}, nil // Pre-feature groups have no invented dates or price history.
	}
	revision, err := strconv.ParseInt(cursor, 10, 64)
	if !hasData || err != nil || revision < 1 || len(raw) > 4<<20 {
		return nil, errInvalidMarketActivity
	}
	var records map[string]marketRecord
	if err := common.DecodeJsonStrict(strings.NewReader(raw), &records); err != nil || records == nil || len(records) > marketMaxGroups {
		return nil, errInvalidMarketActivity
	}
	for group, rec := range records {
		if !validPersonalGroupName(group) || rec.FirstConfiguredAt < 0 || len(rec.RateChanges) > marketMaxChanges {
			return nil, errInvalidMarketActivity
		}
		for i, change := range rec.RateChanges {
			if !validMarketRatio(change.OldRatio) || !validMarketRatio(change.NewRatio) || *change.OldRatio == *change.NewRatio || change.EffectiveAt <= 0 ||
				i > 0 && *rec.RateChanges[i-1].OldRatio != *change.NewRatio {
				return nil, errInvalidMarketActivity
			}
		}
	}
	return &marketSnapshot{revision: revision, records: records}, nil
}

func loadMarketRuntime(values map[string]string) {
	snapshot, err := parseMarketSnapshot(values)
	if err != nil {
		// Never fail an ACL/price reload because optional display data is damaged.
		common.SysError("invalid marketplace activity metadata; notices suppressed")
	}
	marketRuntime.Store(snapshot)
}

type marketTransition struct {
	before   map[string]string
	after    map[string]string
	mappings []GroupRenameMapping
}

func (change *marketTransition) apply(records map[string]marketRecord, now int64) error {
	previous := change.before["GroupRatio"]
	if alias, ok := change.before["group_ratio_setting.group_ratio"]; ok && previous != alias {
		// An explicit save can reconcile unequal legacy aliases. It does not
		// establish which old rate was effective; don't manufacture a delta.
		if err := validateGroupOptionAliases(map[string]string{"GroupRatio": previous, "group_ratio_setting.group_ratio": alias}, nil); err != nil {
			return errInvalidMarketActivity
		}
	}
	var before, after map[string]*float64
	if err := common.UnmarshalJsonStr(previous, &before); err != nil || before == nil {
		return errInvalidMarketActivity
	}
	if err := common.UnmarshalJsonStr(change.after["GroupRatio"], &after); err != nil || after == nil {
		return errInvalidMarketActivity
	}
	for _, mapping := range change.mappings {
		if rate, ok := before[mapping.From]; ok {
			before[mapping.To] = rate
			delete(before, mapping.From)
		}
		if rec, ok := records[mapping.From]; ok {
			records[mapping.To] = rec
			delete(records, mapping.From)
		}
	}
	for name := range records {
		if !validMarketRatio(after[name]) {
			delete(records, name) // removal retires this identity; name reuse starts fresh
		}
	}
	for name, newRate := range after {
		if !validPersonalGroupName(name) || !validMarketRatio(newRate) {
			continue
		}
		oldRate, existed := before[name]
		rec := records[name]
		switch {
		case !existed:
			rec = marketRecord{FirstConfiguredAt: now}
		case !validMarketRatio(oldRate):
			rec.RateChanges = nil
		case *oldRate != *newRate:
			// Preserve only the verified contiguous chain. An out-of-band
			// write cannot turn a stale previous change into current history.
			if len(rec.RateChanges) != 0 && *rec.RateChanges[0].NewRatio != *oldRate {
				rec.RateChanges = nil
			}
			rec.RateChanges = append([]GroupMarketRateChange{{OldRatio: oldRate, NewRatio: newRate, EffectiveAt: now}}, rec.RateChanges...)
		}
		rec.RateChanges = slices.DeleteFunc(rec.RateChanges, func(c GroupMarketRateChange) bool { return c.EffectiveAt <= now-marketHistorySeconds })
		rec.RateChanges = rec.RateChanges[:min(len(rec.RateChanges), marketMaxChanges)]
		if rec.FirstConfiguredAt != 0 || len(rec.RateChanges) > 0 {
			records[name] = rec
		} else {
			delete(records, name)
		}
	}
	if len(records) > marketMaxGroups {
		// Keep recent facts first; deterministic name ordering breaks ties.
		names := slices.Sorted(maps.Keys(records))
		last := func(rec marketRecord) int64 {
			if len(rec.RateChanges) > 0 {
				return max(rec.FirstConfiguredAt, rec.RateChanges[0].EffectiveAt)
			}
			return rec.FirstConfiguredAt
		}
		slices.SortStableFunc(names, func(a, b string) int { return cmp.Compare(last(records[b]), last(records[a])) })
		for _, name := range names[marketMaxGroups:] {
			delete(records, name)
		}
	}
	return nil
}

// Called under the namespace lock, inside the same transaction as the actual
// option writes and revision. Non-price updates only advance a small cursor.
// A missed legacy writer produces a gap even if a name or rate later returns
// to the same value; never reattach the previous generation on that evidence.
func recordMarketRevisionTx(tx *gorm.DB, current, next int64, change *marketTransition) error {
	nextRaw := strconv.FormatInt(next, 10)
	if change == nil || change.after["GroupRatio"] == "" {
		return tx.Model(&Option{}).Where(&Option{Key: groupMarketCursorKey, Value: strconv.FormatInt(current, 10)}).Update("value", nextRaw).Error
	}
	var rows []Option
	if err := tx.Where(map[string]any{"key": []string{groupMarketDataKey, groupMarketCursorKey}}).Find(&rows).Error; err != nil {
		return err
	}
	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	snapshot, err := parseMarketSnapshot(values)
	if err != nil {
		return err // Preserve corrupt/unknown data for operator recovery.
	}
	records := snapshot.records
	gap := records != nil && snapshot.revision != current
	if records == nil || gap {
		records = make(map[string]marketRecord)
	}
	if err := change.apply(records, time.Now().Unix()); err != nil {
		return err
	}
	if len(records) == 0 && (snapshot.records == nil || gap) {
		return nil // No new facts to publish; retain any quarantined generation.
	}
	encoded, err := common.Marshal(records)
	if err != nil {
		return err
	}
	if gap {
		if err := upsertGroupRenameOption(tx, groupMarketArchiveKey, values[groupMarketDataKey]); err != nil {
			return err
		}
		common.SysLog("marketplace activity continuity gap; retiring previous observation generation")
	}
	if string(encoded) != values[groupMarketDataKey] {
		if err := upsertGroupRenameOption(tx, groupMarketDataKey, string(encoded)); err != nil {
			return err
		}
	}
	return upsertGroupRenameOption(tx, groupMarketCursorKey, nextRaw)
}

// Only call under a fenced GroupConfigRead. scopes lists already-authorized
// concrete groups; a false value permits listing facts but not base-rate facts
// (the user has personal/override pricing). Unknown or stale history is absent.
func ProjectGroupMarketActivity(revision int64, scopes map[string]bool) (map[string]GroupMarketActivity, bool) {
	snapshot := marketRuntime.Load()
	result := make(map[string]GroupMarketActivity)
	if snapshot == nil || snapshot.records != nil && revision != snapshot.revision {
		return result, false
	}
	now := time.Now().Unix()
	if len(snapshot.records) == 0 {
		return result, true
	}
	ratios := ratio_setting.GetGroupRatioCopy()
	for group, showRates := range scopes {
		rec, ok := snapshot.records[group]
		ratio, configured := ratios[group]
		if !ok || !configured {
			continue
		}
		var out GroupMarketActivity
		if rec.FirstConfiguredAt > 0 && rec.FirstConfiguredAt <= now {
			out.FirstConfiguredAt = rec.FirstConfiguredAt
			out.IsNew = now-rec.FirstConfiguredAt < marketRecentSeconds
		}
		if showRates && len(rec.RateChanges) != 0 && rec.RateChanges[0].EffectiveAt <= now && *rec.RateChanges[0].NewRatio == ratio {
			for _, change := range rec.RateChanges {
				if change.EffectiveAt <= now && now-change.EffectiveAt < marketHistorySeconds {
					out.RateChanges = append(out.RateChanges, change)
				}
			}
			out.RecentRateChange = len(out.RateChanges) != 0 && now-rec.RateChanges[0].EffectiveAt < marketRecentSeconds
		}
		if out.FirstConfiguredAt > 0 || len(out.RateChanges) > 0 {
			result[group] = out
		}
	}
	return result, true
}
