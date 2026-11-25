package model

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const groupCatalogOptionKey = "GroupCatalogMetadata"

// Keep independently customizable artwork out of the legacy, strictly decoded
// access-policy document. An older node must still load every exclusive group.
const groupCatalogIconsOptionKey = "GroupCatalogDisplayIcons"
const groupCatalogBadgesOptionKey = "GroupCatalogDisplayBadges"

const traceableGroupBadge = "traceable"

func isGroupCatalogOption(key string) bool {
	switch key {
	case groupCatalogOptionKey, groupCatalogIconsOptionKey, groupCatalogBadgesOptionKey:
		return true
	default:
		return false
	}
}

var (
	ErrInvalidGroupCatalog = errors.New("invalid group catalog configuration")
	ErrGroupCatalogGrants  = errors.New("remove all personal grants before removing exclusive access")
	groupCatalogRuntime    atomic.Pointer[map[string]GroupCatalogEntry]
)

// GroupCatalogEntry controls presentation separately from the per-account
// grants. Exclusive replaces every legacy visibility rule for this concrete
// group; publishing a label, selecting a platform, or setting badges cannot
// grant access.
type GroupCatalogEntry struct {
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	Platform    string   `json:"platform,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	SortOrder   int      `json:"sort_order,omitempty"`
	Exclusive   bool     `json:"exclusive,omitempty"`
	Badges      []string `json:"badges,omitempty"`
}

type GroupCatalogSnapshot struct {
	Revision int64                        `json:"revision"`
	Groups   map[string]GroupCatalogEntry `json:"groups"`
}

type UserGroupEntitlement struct {
	UserID    int    `gorm:"primaryKey;autoIncrement:false"`
	GroupName string `gorm:"primaryKey;type:varchar(128);index"`
}

type UserGroupEntitlementsSnapshot struct {
	Revision int64    `json:"revision"`
	Groups   []string `json:"groups"`
}

func validateCatalogText(value string, limit int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= limit &&
		!strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}

func isCatalogPlatformSlug(value string) bool {
	switch value {
	case "", "openai", "anthropic", "gemini", "deepseek", "moonshot", "xai", "zhipu", "azure", "other":
		return true
	}
	return false
}

func isLegacyGroupBadge(badge string) bool {
	switch badge {
	case "self_operated", "authentic", "benefit", "stable", "distill":
		return true
	default:
		return false
	}
}

func normalizeGroupBadges(values []string, allowDisplay bool) ([]string, bool) {
	badges := make([]string, 0, min(len(values), 6))
	for _, badge := range values {
		if (!isLegacyGroupBadge(badge) && !(allowDisplay && badge == traceableGroupBadge)) || slices.Contains(badges, badge) {
			return nil, false
		}
		badges = append(badges, badge)
	}
	return badges, true
}

func legacyGroupBadges(values []string) []string {
	return slices.DeleteFunc(slices.Clone(values), func(value string) bool {
		return !isLegacyGroupBadge(value)
	})
}

func normalizeGroupCatalog(groups map[string]GroupCatalogEntry) (map[string]GroupCatalogEntry, error) {
	if len(groups) > 2000 {
		return nil, ErrInvalidGroupCatalog
	}
	normalized := make(map[string]GroupCatalogEntry, len(groups))
	for group, item := range groups {
		name, err := normalizeGroupRenameName(group)
		if err != nil || group != name || group == "auto" || group == "all" || item.Exclusive && group == "default" {
			return nil, ErrInvalidGroupCatalog
		}
		item.Label = strings.TrimSpace(item.Label)
		item.Description = strings.TrimSpace(item.Description)
		if !validateCatalogText(item.Label, 64) || !validateCatalogText(item.Description, 240) ||
			item.SortOrder < -10000 || item.SortOrder > 10000 {
			return nil, ErrInvalidGroupCatalog
		}
		if !isCatalogPlatformSlug(item.Platform) || !isCatalogPlatformSlug(item.Icon) {
			return nil, ErrInvalidGroupCatalog
		}
		badges, valid := normalizeGroupBadges(item.Badges, true)
		if !valid {
			return nil, ErrInvalidGroupCatalog
		}
		item.Badges = badges
		normalized[group] = item
	}
	return normalized, nil
}

func parseGroupCatalog(raw, iconRaw, badgeRaw string) (map[string]GroupCatalogEntry, error) {
	groups := make(map[string]GroupCatalogEntry)
	if raw != "" {
		if err := common.DecodeJsonStrict(strings.NewReader(raw), &groups); err != nil || groups == nil {
			return nil, ErrInvalidGroupCatalog
		}
	}
	// Validate the original policy on its own, before overlaying presentation
	// metadata. An extended badge in the policy JSON would break older readers.
	for _, entry := range groups {
		if _, valid := normalizeGroupBadges(entry.Badges, false); !valid {
			return nil, ErrInvalidGroupCatalog
		}
	}
	groups, err := normalizeGroupCatalog(groups)
	if err != nil {
		return nil, err
	}
	// Artwork is not authority. Malformed/stale sidecar rows can never create a
	// group, make it exclusive, or prevent an otherwise valid policy loading.
	var icons map[string]string
	if iconRaw != "" && common.UnmarshalJsonStr(iconRaw, &icons) == nil {
		for name, icon := range icons {
			if entry, ok := groups[name]; ok && isCatalogPlatformSlug(icon) {
				entry.Icon = icon
				groups[name] = entry
			}
		}
	}
	var displayBadges map[string][]string
	if badgeRaw != "" && common.UnmarshalJsonStr(badgeRaw, &displayBadges) == nil {
		for name, values := range displayBadges {
			entry, exists := groups[name]
			if !exists || !slices.Contains(values, traceableGroupBadge) {
				continue
			}
			ordered, valid := normalizeGroupBadges(values, true)
			// If a rolled-back admin changed the legacy badge selection or
			// order, their real edit wins over this stale presentation row.
			if valid && slices.Equal(entry.Badges, legacyGroupBadges(ordered)) {
				entry.Badges = ordered
				groups[name] = entry
			}
		}
	}
	return groups, nil
}

func loadGroupCatalogRuntime(raw, icons, badges string) error {
	groups, err := parseGroupCatalog(raw, icons, badges)
	if err != nil {
		return err
	}
	groupCatalogRuntime.Store(&groups)
	return nil
}

func IsExclusiveGroup(group string) bool {
	groups := groupCatalogRuntime.Load()
	return groups != nil && (*groups)[group].Exclusive
}

// GetGroupCatalogProjection attaches only presentation fields for groups the
// caller is already authorized to see. It never reads or returns grants.
func GetGroupCatalogProjection(ctx context.Context, allowedGroups map[string]string) (map[string]GroupCatalogEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := make(map[string]GroupCatalogEntry)
	if groups := groupCatalogRuntime.Load(); groups != nil {
		for name := range allowedGroups {
			if entry, ok := (*groups)[name]; ok {
				entry.Badges = slices.Clone(entry.Badges)
				result[name] = entry
			}
		}
	}
	return result, nil
}

func ReadGroupCatalog(ctx context.Context) (*GroupCatalogSnapshot, error) {
	guard, err := BeginGroupConfigRead(ctx)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	groups := make(map[string]GroupCatalogEntry)
	if runtime := groupCatalogRuntime.Load(); runtime != nil {
		for name, entry := range *runtime {
			entry.Badges = slices.Clone(entry.Badges)
			groups[name] = entry
		}
	}
	if err := guard.Finish(); err != nil {
		return nil, err
	}
	return &GroupCatalogSnapshot{Revision: guard.Version, Groups: groups}, nil
}

func readGroupCatalogTx(tx *gorm.DB) (map[string]GroupCatalogEntry, error) {
	var rows []Option
	if err := tx.Where(map[string]any{"key": []string{groupCatalogOptionKey, groupCatalogIconsOptionKey, groupCatalogBadgesOptionKey}}).Find(&rows).Error; err != nil {
		return nil, err
	}
	var catalogRaw, iconsRaw, badgesRaw string
	for _, row := range rows {
		switch row.Key {
		case groupCatalogOptionKey:
			catalogRaw = row.Value
		case groupCatalogIconsOptionKey:
			iconsRaw = row.Value
		case groupCatalogBadgesOptionKey:
			badgesRaw = row.Value
		}
	}
	return parseGroupCatalog(catalogRaw, iconsRaw, badgesRaw)
}

func writeGroupCatalogTx(tx *gorm.DB, groups map[string]GroupCatalogEntry) error {
	legacy := maps.Clone(groups)
	icons := make(map[string]string)
	displayBadges := make(map[string][]string)
	for name, entry := range legacy {
		if entry.Icon != "" {
			icons[name] = entry.Icon
			entry.Icon = ""
		}
		if slices.Contains(entry.Badges, traceableGroupBadge) {
			displayBadges[name] = entry.Badges
			entry.Badges = legacyGroupBadges(entry.Badges)
		}
		legacy[name] = entry
	}
	encoded, err := common.Marshal(legacy)
	if err != nil {
		return err
	}
	if err := upsertGroupRenameOption(tx, groupCatalogOptionKey, string(encoded)); err != nil {
		return err
	}
	if err := writeGroupCatalogSidecarTx(tx, groupCatalogIconsOptionKey, icons); err != nil {
		return err
	}
	return writeGroupCatalogSidecarTx(tx, groupCatalogBadgesOptionKey, displayBadges)
}

func writeGroupCatalogSidecarTx[T any](tx *gorm.DB, key string, values map[string]T) error {
	if len(values) == 0 {
		return tx.Where(&Option{Key: key}).Delete(&Option{}).Error
	}
	encoded, err := common.Marshal(values)
	if err != nil {
		return err
	}
	return upsertGroupRenameOption(tx, key, string(encoded))
}

func groupRatioKeysTx(tx *gorm.DB) (map[string]float64, error) {
	values, err := lockAndLoadGroupRenameOptions(tx, []string{"GroupRatio", "group_ratio_setting.group_ratio"})
	if err != nil {
		return nil, err
	}
	if err := validateGroupOptionAliases(values, nil); err != nil {
		return nil, err
	}
	if values["group_ratio_setting.group_ratio"] != "" {
		values["GroupRatio"] = values["group_ratio_setting.group_ratio"]
	}
	configured := make(map[string]float64)
	err = common.UnmarshalJsonStr(values["GroupRatio"], &configured)
	return configured, err
}

func commitGroupCatalogVersion(version int64) {
	if err := applyGroupConfigInvalidation(version, nil, true); err != nil {
		common.SysError("group catalog runtime refresh failed: " + common.MaskSensitiveLogInfo(err.Error()))
	}
	if err := PublishGroupConfigInvalidation(version, nil); err != nil {
		common.SysLog("failed to publish group catalog invalidation: " + err.Error())
	}
}

func UpdateGroupCatalog(ctx context.Context, request GroupCatalogSnapshot, identity AuthSessionIdentity) (*GroupCatalogSnapshot, error) {
	if !common.IM21RolloutActive() {
		return nil, common.ErrIM21RolloutLocked
	}
	groups, err := normalizeGroupCatalog(request.Groups)
	if err != nil || request.Groups == nil || request.Revision < 0 {
		return nil, ErrInvalidGroupCatalog
	}
	var version int64
	err = DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		if err := authorizeGroupRenameTx(tx, &identity); err != nil {
			return err
		}
		if err := validateGroupConfigVersionTx(tx, &request.Revision); err != nil {
			return err
		}
		configured, err := groupRatioKeysTx(tx)
		if err != nil {
			return err
		}
		for group := range groups {
			if _, exists := configured[group]; !exists {
				return ErrInvalidGroupCatalog
			}
		}
		current, err := readGroupCatalogTx(tx)
		if err != nil {
			return err
		}
		for group, entry := range current {
			if !entry.Exclusive || groups[group].Exclusive {
				continue
			}
			found, err := groupRenameExists(tx, &UserGroupEntitlement{}, "group_name = ?", group)
			if err != nil {
				return err
			}
			if found {
				return ErrGroupCatalogGrants
			}
		}
		if err := writeGroupCatalogTx(tx, groups); err != nil {
			return err
		}
		version, err = bumpGroupConfigVersionTx(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	commitGroupCatalogVersion(version)
	return &GroupCatalogSnapshot{Revision: version, Groups: groups}, nil
}

func listUserExclusiveGroups(tx *gorm.DB, userID int) ([]string, error) {
	groups := make([]string, 0)
	err := tx.Model(&UserGroupEntitlement{}).Where("user_id = ?", userID).Order("group_name").Pluck("group_name", &groups).Error
	return groups, err
}

func HasExclusiveGroups() bool {
	if groups := groupCatalogRuntime.Load(); groups != nil {
		for _, entry := range *groups {
			if entry.Exclusive {
				return true
			}
		}
	}
	return false
}

func ListUserExclusiveGroups(ctx context.Context, userID int) ([]string, error) {
	if userID <= 0 || !HasExclusiveGroups() {
		return nil, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return listUserExclusiveGroups(DB.WithContext(bounded), userID)
}

func deleteUserGroupEntitlementsTx(tx *gorm.DB, userID int) error {
	// Older focused test schemas do not migrate this new table.
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) && !tx.Migrator().HasTable(&UserGroupEntitlement{}) {
		return nil
	}
	return tx.Where("user_id = ?", userID).Delete(&UserGroupEntitlement{}).Error
}

func HasUserExclusiveGroup(ctx context.Context, userID int, name string) (bool, error) {
	if userID <= 0 {
		return false, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var grant UserGroupEntitlement
	query := DB.WithContext(bounded).Select("user_id").Where("user_id = ? AND group_name = ?", userID, name).Limit(1).Find(&grant)
	return query.RowsAffected > 0, query.Error
}

func ReadUserGroupEntitlements(ctx context.Context, userID int) (*UserGroupEntitlementsSnapshot, error) {
	guard, err := BeginGroupConfigRead(ctx)
	if err != nil {
		return nil, err
	}
	defer guard.Close()
	var user User
	if err := DB.WithContext(ctx).Select("id").First(&user, userID).Error; err != nil {
		return nil, err
	}
	groups, err := listUserExclusiveGroups(DB.WithContext(ctx), userID)
	if err != nil {
		return nil, err
	}
	if err := guard.Finish(); err != nil {
		return nil, err
	}
	return &UserGroupEntitlementsSnapshot{Revision: guard.Version, Groups: groups}, nil
}

func UpdateUserGroupEntitlements(ctx context.Context, userID int, expected *int64, groups []string, identity AuthSessionIdentity) (*UserGroupEntitlementsSnapshot, error) {
	if !common.IM21RolloutActive() {
		return nil, common.ErrIM21RolloutLocked
	}
	if userID <= 0 || groups == nil || len(groups) > 200 || expected == nil || *expected < 0 {
		return nil, ErrInvalidGroupCatalog
	}
	groups = slices.Clone(groups)
	slices.Sort(groups)
	if len(slices.Compact(slices.Clone(groups))) != len(groups) {
		return nil, ErrInvalidGroupCatalog
	}
	var version int64
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		if err := authorizeGroupRenameTx(tx, &identity); err != nil {
			return err
		}
		if err := validateGroupConfigVersionTx(tx, expected); err != nil {
			return err
		}
		var user User
		if err := lockForUpdate(tx).Select("id").First(&user, userID).Error; err != nil {
			return err
		}
		configured, err := groupRatioKeysTx(tx)
		if err != nil {
			return err
		}
		catalog, err := readGroupCatalogTx(tx)
		if err != nil {
			return err
		}
		for _, group := range groups {
			if _, exists := configured[group]; !exists || !catalog[group].Exclusive {
				return ErrInvalidGroupCatalog
			}
		}
		current, err := listUserExclusiveGroups(tx, userID)
		if err != nil {
			return err
		}
		if slices.Equal(current, groups) {
			version = *expected
			return nil
		}
		if err := tx.Where("user_id = ?", userID).Delete(&UserGroupEntitlement{}).Error; err != nil {
			return err
		}
		rows := make([]UserGroupEntitlement, 0, len(groups))
		for _, group := range groups {
			rows = append(rows, UserGroupEntitlement{UserID: userID, GroupName: group})
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		version, err = bumpGroupConfigVersionTx(tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	if version != *expected {
		commitGroupCatalogVersion(version)
	}
	return &UserGroupEntitlementsSnapshot{Revision: version, Groups: groups}, nil
}

func renameGroupCatalogAndEntitlementsTx(tx *gorm.DB, mappings []GroupRenameMapping) error {
	if len(mappings) == 0 {
		return nil
	}
	current, err := readGroupCatalogTx(tx)
	if err != nil {
		return err
	}
	next := maps.Clone(current)
	changed := false
	for _, mapping := range mappings {
		entry, ok := current[mapping.From]
		if !ok {
			continue
		}
		delete(next, mapping.From)
		next[mapping.To] = entry
		changed = true
	}
	if !changed {
		return nil
	}
	for _, mapping := range mappings {
		if current[mapping.From].Exclusive {
			if err := tx.Model(&UserGroupEntitlement{}).Where("group_name = ?", mapping.From).Update("group_name", mapping.To).Error; err != nil {
				return err
			}
		}
	}
	return writeGroupCatalogTx(tx, next)
}
