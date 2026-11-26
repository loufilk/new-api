package model

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
)

const publicModelLinesOptionKey = "GroupCatalogPublicLines"
const maxPublicModelLines = 200

var ErrInvalidPublicModelLines = errors.New("invalid public model lines: choose an eligible group, a distinct public name, and an available representative model")

// PublicModelLineSettings is writeable only through the dedicated Root control.
// The server generates identifiers and all publication timestamps.
type PublicModelLineSettings struct {
	Label               string   `json:"label"`
	SortOrder           int      `json:"sort_order,omitempty"`
	Badges              []string `json:"badges,omitempty"`
	RepresentativeModel string   `json:"representative_model,omitempty"`
	PublishHealth       bool     `json:"publish_health,omitempty"`
}

func (settings PublicModelLineSettings) Equal(other PublicModelLineSettings) bool {
	return settings.Label == other.Label && settings.SortOrder == other.SortOrder &&
		settings.RepresentativeModel == other.RepresentativeModel &&
		settings.PublishHealth == other.PublishHealth && slices.Equal(settings.Badges, other.Badges)
}

type PublicModelLineRecord struct {
	PublicModelLineSettings
	ID            string `json:"id"`
	PublishedAt   int64  `json:"published_at"`
	ObservedSince int64  `json:"observed_since,omitempty"`
}

type PublicModelLineConfig struct {
	Revision int64                            `json:"revision"`
	Groups   map[string]PublicModelLineRecord `json:"groups"`
}

func eligiblePublicLineGroup(group string, ratios map[string]float64, usable map[string]string, exclusive bool, restricted map[string]bool) bool {
	ratio, priced := ratios[group]
	_, general := usable[group]
	// Never turn known test, personal or hidden naming conventions into public
	// offers, even if a legacy configuration accidentally marks them usable.
	lower := strings.ToLower(group)
	tokens := strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' ' || r == '/' || r == ':'
	})
	for _, word := range tokens {
		switch word {
		case "test", "testing", "private", "internal", "hidden", "personal", "sandbox", "exclusive", "dev", "staging", "qa":
			return false
		}
	}
	for _, marker := range []string{"测试", "專屬", "专属", "內部", "内部", "隐藏", "隱藏", "私有"} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return validPersonalGroupName(group) && general && priced && ratio >= 0 && !math.IsNaN(ratio) &&
		!math.IsInf(ratio, 0) && !exclusive && !restricted[group]
}

func publicLineRestrictedGroups(rules map[string]map[string]string) map[string]bool {
	restricted := make(map[string]bool)
	for _, groupRules := range rules {
		for name := range groupRules {
			if name, hidden := strings.CutPrefix(name, "-:"); hidden {
				restricted[name] = true
			}
		}
	}
	return restricted
}

// LivePublicModelLineGroups reevaluates current policy under the caller's
// GroupConfigRead guard. This never considers individual grants or discounts.
func LivePublicModelLineGroups(config map[string]PublicModelLineRecord) map[string]float64 {
	result := make(map[string]float64)
	ratios := ratio_setting.GetGroupRatioCopy()
	usable := setting.GetUserUsableGroupsCopy()
	restricted := publicLineRestrictedGroups(ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.ReadAll())
	for group := range config {
		if eligiblePublicLineGroup(group, ratios, usable, IsExclusiveGroup(group), restricted) {
			result[group] = ratios[group]
		}
	}
	return result
}

func readPublicModelLineRecords(tx *gorm.DB) (map[string]PublicModelLineRecord, error) {
	groups := make(map[string]PublicModelLineRecord)
	var option Option
	res := tx.Where(&Option{Key: publicModelLinesOptionKey}).Limit(1).Find(&option)
	if res.Error != nil || res.RowsAffected == 0 {
		return groups, res.Error
	}
	if len(option.Value) > 128<<10 || common.DecodeJsonStrict(strings.NewReader(option.Value), &groups) != nil || groups == nil || len(groups) > maxPublicModelLines {
		return nil, ErrInvalidPublicModelLines
	}
	ids := make(map[string]bool, len(groups))
	for group, record := range groups {
		id, ok := strings.CutPrefix(record.ID, "line-")
		raw, err := hex.DecodeString(id)
		if !ok || err != nil || len(raw) != 16 || ids[record.ID] || record.PublishedAt <= 0 ||
			record.ObservedSince < 0 || !validPersonalGroupName(group) || !validPublicModelLineSettings(record.PublicModelLineSettings) {
			return nil, ErrInvalidPublicModelLines
		}
		ids[record.ID] = true
	}
	return groups, nil
}

func validPublicModelLineSettings(item PublicModelLineSettings) bool {
	if item.Label == "" || strings.TrimSpace(item.Label) != item.Label || !validateCatalogText(item.Label, 64) ||
		item.SortOrder < -10000 || item.SortOrder > 10000 || !validateCatalogText(item.RepresentativeModel, 128) ||
		strings.TrimSpace(item.RepresentativeModel) != item.RepresentativeModel || item.PublishHealth && item.RepresentativeModel == "" ||
		strings.ContainsAny(item.Label, "/\\@<>") || strings.Contains(strings.ToLower(item.Label), "sk-") {
		return false
	}
	_, valid := normalizeGroupBadges(item.Badges, true)
	return valid
}

// ReadPublicModelLineRecords is for a dedicated low-volume model square or
// operator request. It must be fenced by a GroupConfigRead from the caller.
func ReadPublicModelLineRecords(ctx context.Context) (map[string]PublicModelLineRecord, error) {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return readPublicModelLineRecords(DB.WithContext(bounded))
}

// UpdatePublicModelLines uses the same namespace/version fence and authenticated
// Root policy as Group Catalog; this is presentation data, not a quote input.
func UpdatePublicModelLines(ctx context.Context, revision int64, requested map[string]PublicModelLineSettings, identity AuthSessionIdentity) (*PublicModelLineConfig, error) {
	if !common.IM21RolloutActive() {
		return nil, common.ErrIM21RolloutLocked
	}
	if revision < 0 || requested == nil || len(requested) > maxPublicModelLines {
		return nil, ErrInvalidPublicModelLines
	}
	groups := make(map[string]PublicModelLineRecord, len(requested))
	seenLabels := make(map[string]bool, len(requested))
	var version int64
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		if err := authorizeGroupRenameTx(tx, &identity); err != nil {
			return err
		}
		if err := validateGroupConfigVersionTx(tx, &revision); err != nil {
			return err
		}
		previous, err := readPublicModelLineRecords(tx)
		if err != nil {
			return err
		}
		if len(requested) > 0 {
			ratios, err := groupRatioKeysTx(tx)
			if err != nil {
				return err
			}
			visibility, err := lockAndLoadGroupRenameOptions(tx, []string{"UserUsableGroups", "group_ratio_setting.group_special_usable_group"})
			if err != nil {
				return err
			}
			usable := setting.GetUserUsableGroupsCopy()
			if raw := visibility["UserUsableGroups"]; raw != "" {
				usable = make(map[string]string)
				if err := common.UnmarshalJsonStr(raw, &usable); err != nil {
					return ErrInvalidPublicModelLines
				}
			}
			var rules map[string]map[string]string
			if raw := visibility["group_ratio_setting.group_special_usable_group"]; raw != "" {
				if err := common.UnmarshalJsonStr(raw, &rules); err != nil {
					return ErrInvalidPublicModelLines
				}
			}
			restricted := publicLineRestrictedGroups(rules)
			catalog, err := readGroupCatalogTx(tx)
			if err != nil {
				return err
			}
			internalLabels := make(map[string]bool, len(ratios))
			for name := range ratios {
				internalLabels[strings.ToLower(name)] = true
			}
			now := time.Now().Unix()
			for group, item := range requested {
				if old := previous[group]; old.ID != "" && item.Equal(old.PublicModelLineSettings) {
					seenLabels[strings.ToLower(item.Label)] = true
				}
			}
			for group, item := range requested {
				item.Label = strings.TrimSpace(item.Label)
				item.RepresentativeModel = strings.TrimSpace(item.RepresentativeModel)
				label := strings.ToLower(item.Label)
				old := previous[group]
				if old.ID != "" && requested[group].Equal(old.PublicModelLineSettings) {
					groups[group] = old
					continue
				}
				if !eligiblePublicLineGroup(group, ratios, usable, catalog[group].Exclusive, restricted) ||
					!validPublicModelLineSettings(item) || internalLabels[label] || seenLabels[label] {
					return ErrInvalidPublicModelLines
				}
				seenLabels[label] = true
				if old.ID == "" {
					var id [16]byte
					if _, err := rand.Read(id[:]); err != nil {
						return err
					}
					old.ID = "line-" + hex.EncodeToString(id[:])
					old.PublishedAt = now
				}
				if !item.PublishHealth {
					old.ObservedSince = 0
				} else if !old.PublishHealth || old.RepresentativeModel != item.RepresentativeModel {
					old.ObservedSince = max(now, old.ObservedSince+1)
				}
				old.PublicModelLineSettings = item
				groups[group] = old
			}
		}
		if err := writeGroupCatalogSidecarTx(tx, publicModelLinesOptionKey, groups); err != nil {
			return err
		}
		version, err = bumpGroupConfigVersionTx(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	commitGroupCatalogVersion(version)
	return &PublicModelLineConfig{Revision: version, Groups: groups}, nil
}

// Retire a publication when a group ceases to be the same generally available
// group. Reusing an internal group name must not resurrect an old public URL.
func retirePublicModelLinesTx(tx *gorm.DB, retire func(string) bool) error {
	groups, err := readPublicModelLineRecords(tx)
	if err != nil {
		if errors.Is(err, ErrInvalidPublicModelLines) {
			// Corrupt presentation must not block an access-policy write.
			return tx.Where(&Option{Key: publicModelLinesOptionKey}).Delete(&Option{}).Error
		}
		return err
	}
	before := len(groups)
	for name := range groups {
		if retire(name) {
			delete(groups, name)
		}
	}
	if len(groups) == before {
		return nil
	}
	return writeGroupCatalogSidecarTx(tx, publicModelLinesOptionKey, groups)
}

func retireChangedPublicModelLinesTx(tx *gorm.DB, change *marketTransition) error {
	var ratios map[string]float64
	var usable map[string]string
	var rules map[string]map[string]string
	if common.UnmarshalJsonStr(change.after["GroupRatio"], &ratios) != nil || ratios == nil {
		return ErrInvalidPublicModelLines
	}
	usable = setting.GetUserUsableGroupsCopy()
	if raw := change.after["UserUsableGroups"]; raw != "" && common.UnmarshalJsonStr(raw, &usable) != nil {
		return ErrInvalidPublicModelLines
	}
	if raw := change.after["group_ratio_setting.group_special_usable_group"]; raw != "" && common.UnmarshalJsonStr(raw, &rules) != nil {
		return ErrInvalidPublicModelLines
	}
	restricted := publicLineRestrictedGroups(rules)
	return retirePublicModelLinesTx(tx, func(name string) bool {
		return !eligiblePublicLineGroup(name, ratios, usable, false, restricted) ||
			slices.ContainsFunc(change.mappings, func(mapping GroupRenameMapping) bool { return mapping.From == name })
	})
}

// LivePublicLineModelMembership is the shared live visibility fence for
// publication and reads. A pricing snapshot alone can be a minute old.
func LivePublicLineModelMembership(ctx context.Context, groups []string) (map[string]map[string]bool, error) {
	result := make(map[string]map[string]bool, len(groups))
	abilities, err := GetEnabledAbilitiesWithChannelsForGroups(groups)
	if err != nil || len(abilities) == 0 {
		return result, err
	}
	names := make(map[string]bool)
	for _, ability := range abilities {
		names[ability.Model] = true
	}
	visible, err := visiblePublicLineModels(ctx, slices.Collect(maps.Keys(names)))
	if err != nil {
		return nil, err
	}
	for _, ability := range abilities {
		if !visible[ability.Model] {
			continue
		}
		if result[ability.Group] == nil {
			result[ability.Group] = make(map[string]bool)
		}
		result[ability.Group][ability.Model] = true
	}
	return result, nil
}

// visiblePublicLineModels freshly checks the Model Square metadata policy
// before using cached pricing details. A removed or hidden model cannot remain
// on the dedicated publication endpoint due to an older catalogue snapshot.
func visiblePublicLineModels(ctx context.Context, names []string) (map[string]bool, error) {
	var rows []Model
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := DB.WithContext(bounded).Select("model_name", "name_rule", "status").Find(&rows).Error; err != nil {
		return nil, err
	}
	metadata := resolveModelMetadata(rows, names)
	result := make(map[string]bool, len(names))
	for _, name := range names {
		result[name] = metadata[name] == nil || metadata[name].Status == 1
	}
	return result, nil
}
