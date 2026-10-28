package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxGroupRenameCount         = 50
	maxGroupNameRuneCount       = 64
	maxGroupRenameOptionUpdates = 32
	groupRenameTokenBatchSize   = 500
	groupRenameActiveStatus     = "active"
)

var (
	ErrGroupRenameEmpty             = errors.New("group rename requires at least one mapping")
	ErrGroupRenameTooMany           = errors.New("too many group rename mappings")
	ErrGroupRenameSelfMapping       = errors.New("source and target group are identical")
	ErrGroupRenameDuplicateFrom     = errors.New("duplicate rename source")
	ErrGroupRenameDuplicateTo       = errors.New("duplicate rename target")
	ErrGroupRenameChain             = errors.New("ambiguous chained rename")
	ErrGroupRenameReservedName      = errors.New("reserved group name")
	ErrGroupRenameInvalidName       = errors.New("invalid group name")
	ErrGroupRenameUnknownSource     = errors.New("unknown source group")
	ErrGroupRenameCollision         = errors.New("target group already exists")
	ErrGroupRenameStale             = errors.New("group settings changed since they were loaded")
	ErrGroupRenameUnmappedChange    = errors.New("group key change requires an explicit rename mapping")
	ErrGroupRenameReferencedGroup   = errors.New("group is still referenced by another setting")
	ErrGroupRenameInvalidAutoGroups = errors.New("token auto groups must be a JSON array of group names")
	ErrGroupRenameInvalidOption     = errors.New("option value is not valid for group rename")
)

var groupRenameOptionKeys = []string{
	"GroupRatio",
	"TopupGroupRatio",
	"UserUsableGroups",
	"GroupGroupRatio",
	"AutoGroups",
	"ModelRequestRateLimitGroup",
	"group_ratio_setting.group_special_usable_group",
	setting.SensitiveCheckExemptGroupsOptionKey,
}

// groupRenameWritableOptionKeys are the option keys a rename request may persist
// in the same transaction. The group-reference keys hold group names; the other
// two only need to stay atomic with the rename.
var groupRenameWritableOptionKeys = map[string]struct{}{
	"GroupRatio":                 {},
	"TopupGroupRatio":            {},
	"UserUsableGroups":           {},
	"GroupGroupRatio":            {},
	"AutoGroups":                 {},
	"ModelRequestRateLimitGroup": {},
	"group_ratio_setting.group_special_usable_group": {},
	"MaxTokenAutoGroups":                             {},
	"DefaultUseAutoGroup":                            {},
	setting.SensitiveCheckExemptGroupsOptionKey:      {},
}

var groupRenameReferenceOptionKeys = map[string]struct{}{
	"GroupRatio":                 {},
	"TopupGroupRatio":            {},
	"UserUsableGroups":           {},
	"GroupGroupRatio":            {},
	"AutoGroups":                 {},
	"ModelRequestRateLimitGroup": {},
	"group_ratio_setting.group_special_usable_group": {},
	setting.SensitiveCheckExemptGroupsOptionKey:      {},
}

func isGroupRenameOptionKey(key string) bool {
	_, ok := groupRenameWritableOptionKeys[key]
	return ok
}

var reservedGroupNames = map[string]struct{}{
	"auto":    {},
	"all":     {},
	"default": {},
}

var groupRenameTxFailureHook func() error

// groupRenameRuntimeApplyMutex serializes the post-commit in-process runtime
// refresh with a fresh database read, so a late runtime apply cannot overwrite
// newer committed option state.
var groupRenameRuntimeApplyMutex sync.Mutex

type GroupRenameMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// GroupRenameApplyRequest carries an optional rename mapping plus the group
// settings the caller edited from the same baseline. Options values are the
// final post-rename values; Expected holds the option values the caller loaded
// before editing, and lets the transaction reject stale edits.
type GroupRenameApplyRequest struct {
	Mappings []GroupRenameMapping
	Options  map[string]string
	Expected map[string]string
}

type GroupRenameResult struct {
	Renames           []GroupRenameMapping `json:"renames"`
	Options           int                  `json:"options"`
	Channels          int                  `json:"channels"`
	Abilities         int64                `json:"abilities"`
	Users             int                  `json:"users"`
	Tokens            int                  `json:"tokens"`
	SubscriptionPlans int64                `json:"subscription_plans"`
	UserSubscriptions int64                `json:"user_subscriptions"`
	AppliedOptions    map[string]string    `json:"applied_options,omitempty"`
}

type groupRenamePlan struct {
	mappings []GroupRenameMapping
	sources  map[string]string
}

func (plan *groupRenamePlan) sourceNames() []string {
	names := make([]string, 0, len(plan.mappings))
	for _, mapping := range plan.mappings {
		names = append(names, mapping.From)
	}
	return names
}

type groupRenameAutoToken struct {
	Id  int
	Key string
}

type groupRenameTxState struct {
	affectedUserIds     []int
	subscriptionUserIds []int
	affectedTokenKeys   []string
	affectedOptionKeys  []string
	appliedOptions      map[string]string
	affectedPlanIds     []int
	groupConfigVersion  int64
	result              GroupRenameResult
}

// RenameGroups keeps the original simple API: rename groups everywhere they are
// referenced. It is a thin wrapper over ApplyGroupRename without extra options.
func RenameGroups(mappings []GroupRenameMapping) (*GroupRenameResult, error) {
	return ApplyGroupRename(GroupRenameApplyRequest{Mappings: mappings})
}

// ApplyGroupRename migrates every effective group reference and writes the
// provided option values in one transaction. Missing option rows are
// materialized before locking so concurrent initial operations serialize.
func ApplyGroupRename(request GroupRenameApplyRequest) (*GroupRenameResult, error) {
	if len(request.Mappings) == 0 && len(request.Options) == 0 {
		return nil, ErrGroupRenameEmpty
	}
	if len(request.Mappings) > maxGroupRenameCount {
		return nil, ErrGroupRenameTooMany
	}
	if len(request.Options) > maxGroupRenameOptionUpdates {
		return nil, fmt.Errorf("%w: too many option updates", ErrGroupRenameInvalidOption)
	}
	for key := range request.Options {
		if _, ok := groupRenameWritableOptionKeys[key]; !ok {
			return nil, fmt.Errorf("%w: unsupported option %s", ErrGroupRenameInvalidOption, key)
		}
	}
	for key := range request.Expected {
		if _, ok := groupRenameWritableOptionKeys[key]; !ok {
			return nil, fmt.Errorf("%w: unsupported option %s", ErrGroupRenameInvalidOption, key)
		}
	}
	if raw, ok := request.Options[setting.SensitiveCheckExemptGroupsOptionKey]; ok {
		normalized, err := setting.NormalizeSensitiveCheckExemptGroups(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrGroupRenameInvalidOption, err)
		}
		request.Options = maps.Clone(request.Options)
		request.Options[setting.SensitiveCheckExemptGroupsOptionKey] = normalized
	}
	state := &groupRenameTxState{appliedOptions: make(map[string]string)}
	var plan *groupRenamePlan
	optionKeys := groupRenameAffectedOptionKeys(request)
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockGroupNamespace(tx); err != nil {
			return err
		}
		optionValues, err := lockAndLoadGroupRenameOptions(tx, optionKeys)
		if err != nil {
			return err
		}
		for key, expected := range request.Expected {
			if optionValues[key] != expected {
				return fmt.Errorf("%w: %s", ErrGroupRenameStale, key)
			}
		}
		knownGroups, err := collectKnownGroupNames(tx, optionValues)
		if err != nil {
			return err
		}
		plan, err = buildGroupRenamePlan(request.Mappings, knownGroups)
		if err != nil {
			return err
		}
		if err := recordGroupRenameAliasesTx(tx, plan.mappings); err != nil {
			return err
		}
		unmappedRemovals, err := collectGroupRenameUnmappedRemovals(request, optionValues, plan)
		if err != nil {
			return err
		}
		var autoTokens []groupRenameAutoToken
		autoRefs := map[string]struct{}{}
		if len(plan.mappings) > 0 || len(unmappedRemovals) > 0 {
			autoTokens, autoRefs, err = scanGroupRenameTokenAutoGroups(tx, plan)
			if err != nil {
				return err
			}
		}
		if err := validateGroupRenameOptionUpdates(tx, request, optionValues, plan, autoRefs); err != nil {
			return err
		}
		renamedOptions, err := rewriteGroupRenameOptionValues(optionValues, request.Options, plan)
		if err != nil {
			return err
		}
		// Validate final writes too: automatic renames can rewrite special rules
		// not supplied by the caller. Historical conflicting values are neither
		// normalized nor changed by an unrelated settings edit.
		for key, value := range renamedOptions {
			if err := validateOptionValue(key, value); err != nil {
				return fmt.Errorf("%w: %s: %w", ErrGroupRenameInvalidOption, key, err)
			}
			groups, err := groupRenameOptionGroupKeys(key, value)
			if err != nil {
				return fmt.Errorf("%w: %s: %v", ErrGroupRenameInvalidOption, key, err)
			}
			for _, group := range groups {
				if target, mapped := plan.sources[group]; mapped {
					return fmt.Errorf("%w: %s -> %s in %s", ErrGroupRenameUnmappedChange, group, target, key)
				}
			}
		}
		// The token cache fence must be raised before any database mutation so a
		// concurrent reader cannot repopulate pre-rename token metadata.
		tokenKeys, err := collectGroupRenameTokenKeys(tx, plan, autoTokens)
		if err != nil {
			return err
		}
		for _, key := range tokenKeys {
			if err := invalidateTokenCacheForMutation(key); err != nil {
				return err
			}
		}
		if err := renameGroupChannelReferences(tx, plan, &state.result); err != nil {
			return err
		}
		if err := renameGroupAbilityReferences(tx, plan, &state.result); err != nil {
			return err
		}
		if err := renameGroupUserReferences(tx, plan, state); err != nil {
			return err
		}
		if err := renameGroupTokenReferences(tx, plan, autoTokens, state); err != nil {
			return err
		}
		if err := renameGroupSubscriptionReferences(tx, plan, state); err != nil {
			return err
		}
		for key, value := range renamedOptions {
			if optionValues[key] != value {
				if err := upsertGroupRenameOption(tx, key, value); err != nil {
					return err
				}
				state.result.Options++
			}
			state.appliedOptions[key] = value
		}
		state.affectedOptionKeys = optionKeys
		state.groupConfigVersion, err = bumpGroupConfigVersionTx(tx)
		if err != nil {
			return err
		}
		if groupRenameTxFailureHook != nil {
			if err := groupRenameTxFailureHook(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	applyGroupRenameRuntime(state)
	setGroupConfigAppliedVersion(state.groupConfigVersion)
	if err := PublishGroupConfigInvalidation(state.groupConfigVersion, plan.mappings); err != nil {
		common.SysLog("failed to publish group config invalidation: " + err.Error())
	}
	state.result.Renames = plan.mappings
	if len(state.appliedOptions) > 0 {
		state.result.AppliedOptions = state.appliedOptions
	}
	return &state.result, nil
}

func groupRenameAffectedOptionKeys(request GroupRenameApplyRequest) []string {
	seen := make(map[string]struct{}, len(groupRenameOptionKeys)+len(request.Options)+len(request.Expected))
	keys := make([]string, 0, len(groupRenameOptionKeys)+len(request.Options)+len(request.Expected))
	appendKey := func(key string) {
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for _, key := range groupRenameOptionKeys {
		appendKey(key)
	}
	for key := range request.Options {
		appendKey(key)
	}
	for key := range request.Expected {
		appendKey(key)
	}
	slices.Sort(keys)
	return keys
}

// lockAndLoadGroupRenameOptions materializes absent rows with the effective
// runtime value and then locks every affected row in deterministic key order.
// Without the materialization, a FOR UPDATE on a nonexistent row locks nothing
// and two concurrent initial operations would both proceed.
func lockAndLoadGroupRenameOptions(tx *gorm.DB, keys []string) (map[string]string, error) {
	for _, key := range keys {
		option := Option{Key: key, Value: groupRenameMemoryOptionValue(key)}
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "key"}},
			DoNothing: true,
		}).Create(&option).Error; err != nil {
			return nil, err
		}
	}
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		var option Option
		if err := lockForUpdate(tx).Where(&Option{Key: key}).First(&option).Error; err != nil {
			return nil, err
		}
		values[key] = option.Value
	}
	return values, nil
}

func groupRenameMemoryOptionValue(key string) string {
	switch key {
	case "GroupRatio":
		return ratio_setting.GroupRatio2JSONString()
	case "TopupGroupRatio":
		return common.TopupGroupRatio2JSONString()
	case "UserUsableGroups":
		return setting.UserUsableGroups2JSONString()
	case "GroupGroupRatio":
		return ratio_setting.GroupGroupRatio2JSONString()
	case "AutoGroups":
		return setting.AutoGroups2JsonString()
	case "ModelRequestRateLimitGroup":
		return setting.ModelRequestRateLimitGroup2JSONString()
	case "group_ratio_setting.group_special_usable_group":
		return ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.MarshalJSONString()
	case "MaxTokenAutoGroups":
		return strconv.Itoa(setting.GetMaxTokenAutoGroups())
	case "DefaultUseAutoGroup":
		return strconv.FormatBool(setting.DefaultUseAutoGroup)
	case setting.SensitiveCheckExemptGroupsOptionKey:
		return setting.SensitiveCheckExemptGroupsToString()
	default:
		return "{}"
	}
}

func buildGroupRenamePlan(mappings []GroupRenameMapping, knownGroups map[string]struct{}) (*groupRenamePlan, error) {
	plan := &groupRenamePlan{
		mappings: make([]GroupRenameMapping, 0, len(mappings)),
		sources:  make(map[string]string, len(mappings)),
	}
	if len(mappings) > maxGroupRenameCount {
		return nil, ErrGroupRenameTooMany
	}
	seenFrom := make(map[string]struct{}, len(mappings))
	seenTo := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		from, err := normalizeGroupRenameName(mapping.From)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrGroupRenameInvalidName, mapping.From)
		}
		to, err := normalizeGroupRenameName(mapping.To)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrGroupRenameInvalidName, mapping.To)
		}
		if _, reserved := reservedGroupNames[from]; reserved {
			return nil, fmt.Errorf("%w: %s", ErrGroupRenameReservedName, from)
		}
		if _, reserved := reservedGroupNames[to]; reserved {
			return nil, fmt.Errorf("%w: %s", ErrGroupRenameReservedName, to)
		}
		if from == to {
			return nil, fmt.Errorf("%w: %s", ErrGroupRenameSelfMapping, from)
		}
		if _, duplicate := seenFrom[from]; duplicate {
			return nil, fmt.Errorf("%w: %s", ErrGroupRenameDuplicateFrom, from)
		}
		if _, duplicate := seenTo[to]; duplicate {
			return nil, fmt.Errorf("%w: %s", ErrGroupRenameDuplicateTo, to)
		}
		seenFrom[from] = struct{}{}
		seenTo[to] = struct{}{}
		plan.mappings = append(plan.mappings, GroupRenameMapping{From: from, To: to})
		plan.sources[from] = to
	}
	for _, mapping := range plan.mappings {
		if _, chained := seenFrom[mapping.To]; chained {
			return nil, fmt.Errorf("%w: %s -> %s", ErrGroupRenameChain, mapping.From, mapping.To)
		}
	}
	for _, mapping := range plan.mappings {
		if _, ok := knownGroups[mapping.From]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrGroupRenameUnknownSource, mapping.From)
		}
	}
	for _, mapping := range plan.mappings {
		if _, collision := knownGroups[mapping.To]; collision {
			return nil, fmt.Errorf("%w: %s", ErrGroupRenameCollision, mapping.To)
		}
	}
	return plan, nil
}

func normalizeGroupRenameName(raw string) (string, error) {
	if raw == "" || raw != strings.TrimSpace(raw) {
		return "", ErrGroupRenameInvalidName
	}
	if utf8.RuneCountInString(raw) > maxGroupNameRuneCount {
		return "", ErrGroupRenameInvalidName
	}
	if strings.Contains(raw, ",") {
		return "", ErrGroupRenameInvalidName
	}
	if strings.HasPrefix(raw, "+:") || strings.HasPrefix(raw, "-:") {
		return "", ErrGroupRenameInvalidName
	}
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", ErrGroupRenameInvalidName
		}
	}
	return raw, nil
}

func collectKnownGroupNames(tx *gorm.DB, optionValues map[string]string) (map[string]struct{}, error) {
	known := make(map[string]struct{})
	if err := collectGroupNamesFromOptions(optionValues, known); err != nil {
		return nil, err
	}
	channelGroups := make([]string, 0)
	if err := tx.Model(&Channel{}).Pluck("group", &channelGroups).Error; err != nil {
		return nil, err
	}
	for _, joined := range channelGroups {
		for group := range strings.SplitSeq(joined, ",") {
			if group != "" {
				known[group] = struct{}{}
			}
		}
	}
	for _, target := range []any{&Ability{}, &User{}, &Token{}} {
		if err := collectDistinctColumnValues(tx, target, "group", known); err != nil {
			return nil, err
		}
	}
	autoGroups := make([]string, 0)
	if err := tx.Model(&Token{}).Where("auto_groups <> ?", "").Distinct().Pluck("auto_groups", &autoGroups).Error; err != nil {
		return nil, err
	}
	for _, raw := range autoGroups {
		var groups []string
		if err := common.UnmarshalJsonStr(raw, &groups); err != nil {
			continue
		}
		for _, group := range groups {
			if group != "" {
				known[group] = struct{}{}
			}
		}
	}
	for _, column := range []string{"upgrade_group", "downgrade_group"} {
		if err := collectDistinctColumnValues(tx, &SubscriptionPlan{}, column, known); err != nil {
			return nil, err
		}
	}
	for _, column := range []string{"upgrade_group", "prev_user_group", "downgrade_group"} {
		if err := collectDistinctColumnValues(tx, &UserSubscription{}, column, known); err != nil {
			return nil, err
		}
	}
	return known, nil
}

func collectDistinctColumnValues(tx *gorm.DB, dest any, column string, known map[string]struct{}) error {
	values := make([]string, 0)
	if err := tx.Model(dest).Distinct().Pluck(column, &values).Error; err != nil {
		return err
	}
	for _, value := range values {
		if value != "" {
			known[value] = struct{}{}
		}
	}
	return nil
}

func collectGroupNamesFromOptions(optionValues map[string]string, known map[string]struct{}) error {
	for _, key := range groupRenameReferenceOptionKeys2Slice() {
		raw := optionValues[key]
		if strings.TrimSpace(raw) == "" {
			continue
		}
		groups, err := groupRenameOptionGroupKeys(key, raw)
		if err != nil {
			return fmt.Errorf("invalid %s option: %w", key, err)
		}
		for _, group := range groups {
			if group != "" {
				known[group] = struct{}{}
			}
		}
	}
	return nil
}

func groupRenameReferenceOptionKeys2Slice() []string {
	keys := make([]string, 0, len(groupRenameReferenceOptionKeys))
	for key := range groupRenameReferenceOptionKeys {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// scanGroupRenameTokenAutoGroups parses auto_groups structurally, so escaped
// group names (for example `o\u006cd`) are found where a text LIKE would miss
// them. Malformed JSON aborts the operation before any write.
func scanGroupRenameTokenAutoGroups(tx *gorm.DB, plan *groupRenamePlan) ([]groupRenameAutoToken, map[string]struct{}, error) {
	affected := make([]groupRenameAutoToken, 0)
	autoRefs := make(map[string]struct{})
	rows := make([]Token, 0)
	err := tx.Model(&Token{}).
		Select("id", "key", "auto_groups").
		Where("auto_groups <> ?", "").
		FindInBatches(&rows, groupRenameTokenBatchSize, func(batchTx *gorm.DB, batch int) error {
			for i := range rows {
				var groups []string
				if err := common.UnmarshalJsonStr(rows[i].AutoGroups, &groups); err != nil {
					return fmt.Errorf("%w: token %d", ErrGroupRenameInvalidAutoGroups, rows[i].Id)
				}
				containsSource := false
				for _, group := range groups {
					if group == "" {
						continue
					}
					autoRefs[group] = struct{}{}
					if _, ok := plan.sources[group]; ok {
						containsSource = true
					}
				}
				if containsSource {
					affected = append(affected, groupRenameAutoToken{Id: rows[i].Id, Key: rows[i].Key})
				}
			}
			return nil
		}).Error
	if err != nil {
		return nil, nil, err
	}
	return affected, autoRefs, nil
}

func collectGroupRenameTokenKeys(tx *gorm.DB, plan *groupRenamePlan, autoTokens []groupRenameAutoToken) ([]string, error) {
	if len(plan.mappings) == 0 {
		return nil, nil
	}
	keys := make([]string, 0)
	if err := tx.Model(&Token{}).Where(map[string]any{"group": plan.sourceNames()}).Pluck("key", &keys).Error; err != nil {
		return nil, err
	}
	for _, token := range autoTokens {
		keys = append(keys, token.Key)
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

func collectGroupRenameUnmappedRemovals(request GroupRenameApplyRequest, current map[string]string, plan *groupRenamePlan) (map[string]struct{}, error) {
	removed := make(map[string]struct{})
	for key, value := range request.Options {
		// GroupRatio defines groups; the other fields only reference them.
		// Revoking a permission or removing an override is not group deletion.
		if key != "GroupRatio" {
			continue
		}
		removedKeys, _, err := groupRenameOptionKeyDelta(key, current[key], value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrGroupRenameInvalidOption, key, err)
		}
		for _, group := range removedKeys {
			if _, mapped := plan.sources[group]; mapped {
				continue
			}
			removed[group] = struct{}{}
		}
	}
	return removed, nil
}

func validateGroupRenameOptionUpdates(tx *gorm.DB, request GroupRenameApplyRequest, current map[string]string, plan *groupRenamePlan, autoRefs map[string]struct{}) error {
	effective := maps.Clone(current)
	for key, value := range request.Options {
		effective[key] = value
	}
	for key, value := range request.Options {
		if err := validateOptionValue(key, value); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrGroupRenameInvalidOption, key, err)
		}
		if key == setting.SensitiveCheckExemptGroupsOptionKey {
			// Explicit options are post-rename values. Revocations are allowed,
			// but a stale source name must not leave a bypass for its recreation.
			definitions := maps.Clone(effective)
			delete(definitions, setting.SensitiveCheckExemptGroupsOptionKey)
			configured := make(map[string]struct{})
			if err := collectGroupNamesFromOptions(definitions, configured); err != nil {
				return err
			}
			for _, mapping := range plan.mappings {
				configured[mapping.To] = struct{}{}
			}
			for group := range strings.SplitSeq(value, "\n") {
				if target, mapped := plan.sources[group]; mapped {
					return fmt.Errorf("%w: %s -> %s in %s", ErrGroupRenameUnmappedChange, group, target, key)
				}
				if _, introduced := configured[group]; !introduced {
					if err := validateGroupNamesForWriteTx(tx, group); err != nil {
						return err
					}
				}
			}
			continue
		}
		if key != "GroupRatio" {
			continue
		}
		removed, added, err := groupRenameOptionKeyDelta(key, current[key], value)
		if err != nil {
			return fmt.Errorf("%w: %s: %v", ErrGroupRenameInvalidOption, key, err)
		}
		for _, group := range removed {
			if target, mapped := plan.sources[group]; mapped {
				kept, err := groupRenameOptionContainsKey(key, value, target)
				if err != nil {
					return fmt.Errorf("%w: %s: %v", ErrGroupRenameInvalidOption, key, err)
				}
				if !kept {
					return fmt.Errorf("%w: %s -> %s must keep %s in %s", ErrGroupRenameUnmappedChange, group, target, target, key)
				}
				continue
			}
			if len(added) > 0 {
				return fmt.Errorf("%w: %s removed from %s while %s added", ErrGroupRenameUnmappedChange, group, key, strings.Join(added, ", "))
			}
			referenced, err := groupRenameNameStillReferenced(tx, group, effective, key, autoRefs)
			if err != nil {
				return err
			}
			if referenced {
				return fmt.Errorf("%w: %s", ErrGroupRenameReferencedGroup, group)
			}
		}
	}
	return nil
}

// groupRenameNameStillReferenced answers whether a group key that is about to be
// removed from one option still appears in another option or database surface.
// "exceptKey" is the option being edited; effective already contains the
// caller's provided values, so a coordinated removal from two options is safe.
func groupRenameNameStillReferenced(tx *gorm.DB, group string, effective map[string]string, exceptKey string, autoRefs map[string]struct{}) (bool, error) {
	if _, ok := autoRefs[group]; ok {
		return true, nil
	}
	for _, key := range groupRenameOptionKeys {
		if key == exceptKey {
			continue
		}
		raw, ok := effective[key]
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		contains, err := groupRenameOptionContainsKey(key, raw, group)
		if err != nil {
			return false, fmt.Errorf("%w: %s: %v", ErrGroupRenameInvalidOption, key, err)
		}
		if contains {
			return true, nil
		}
	}
	checks := []struct {
		model any
		query string
		args  []any
	}{
		{&SubscriptionPlan{}, "upgrade_group = ? OR downgrade_group = ?", []any{group, group}},
		{&UserSubscription{}, "status = ? AND (upgrade_group = ? OR prev_user_group = ? OR downgrade_group = ?)", []any{groupRenameActiveStatus, group, group, group}},
	}
	for _, check := range checks {
		exists, err := groupRenameExists(tx, check.model, check.query, check.args...)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	var count int64
	for _, target := range []any{&Ability{}, &User{}, &Token{}} {
		if err := tx.Model(target).Where(map[string]any{"group": group}).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	channelGroups := make([]string, 0)
	if err := tx.Model(&Channel{}).Pluck("group", &channelGroups).Error; err != nil {
		return false, err
	}
	for _, joined := range channelGroups {
		for channelGroup := range strings.SplitSeq(joined, ",") {
			if channelGroup == group {
				return true, nil
			}
		}
	}
	return false, nil
}

func groupRenameExists(tx *gorm.DB, dest any, query string, args ...any) (bool, error) {
	var count int64
	if err := tx.Model(dest).Where(query, args...).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// groupRenameOptionKeyDelta returns the group keys removed from and added to a
// group-reference option. Inner keys of nested maps and prefixed special usable
// keys are compared as plain group names.
func groupRenameOptionKeyDelta(key, before, after string) (removed, added []string, err error) {
	beforeKeys, err := groupRenameOptionGroupKeys(key, before)
	if err != nil {
		return nil, nil, err
	}
	afterKeys, err := groupRenameOptionGroupKeys(key, after)
	if err != nil {
		return nil, nil, err
	}
	beforeSet := make(map[string]struct{}, len(beforeKeys))
	for _, group := range beforeKeys {
		beforeSet[group] = struct{}{}
	}
	afterSet := make(map[string]struct{}, len(afterKeys))
	for _, group := range afterKeys {
		afterSet[group] = struct{}{}
	}
	for _, group := range beforeKeys {
		if _, ok := afterSet[group]; !ok {
			removed = append(removed, group)
		}
	}
	for _, group := range afterKeys {
		if _, ok := beforeSet[group]; !ok {
			added = append(added, group)
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(removed))), slices.Compact(slices.Sorted(slices.Values(added))), nil
}

func groupRenameOptionContainsKey(key, raw, group string) (bool, error) {
	keys, err := groupRenameOptionGroupKeys(key, raw)
	if err != nil {
		return false, err
	}
	return slices.Contains(keys, group), nil
}

// groupRenameOptionGroupKeys extracts the group names referenced by a
// group-reference option value without losing numeric precision.
func groupRenameOptionGroupKeys(key, raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	switch key {
	case setting.SensitiveCheckExemptGroupsOptionKey:
		normalized, err := setting.NormalizeSensitiveCheckExemptGroups(raw)
		if err != nil || normalized == "" {
			return nil, err
		}
		return strings.Split(normalized, "\n"), nil
	case "GroupRatio", "TopupGroupRatio", "UserUsableGroups", "ModelRequestRateLimitGroup":
		values := make(map[string]json.RawMessage)
		if err := common.UnmarshalJsonStr(raw, &values); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(values))
		for name := range values {
			if name != "" {
				keys = append(keys, name)
			}
		}
		slices.Sort(keys)
		return keys, nil
	case "AutoGroups":
		values := make([]string, 0)
		if err := common.UnmarshalJsonStr(raw, &values); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(values))
		for _, name := range values {
			if name != "" {
				keys = append(keys, name)
			}
		}
		slices.Sort(keys)
		return slices.Compact(keys), nil
	case "GroupGroupRatio":
		values := make(map[string]map[string]json.RawMessage)
		if err := common.UnmarshalJsonStr(raw, &values); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(values))
		for userGroup, overrides := range values {
			if userGroup != "" {
				keys = append(keys, userGroup)
			}
			for usingGroup := range overrides {
				if usingGroup != "" {
					keys = append(keys, usingGroup)
				}
			}
		}
		slices.Sort(keys)
		return slices.Compact(keys), nil
	case "group_ratio_setting.group_special_usable_group":
		values := make(map[string]map[string]string)
		if err := common.UnmarshalJsonStr(raw, &values); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(values))
		for userGroup, rules := range values {
			if userGroup != "" {
				keys = append(keys, userGroup)
			}
			for rawKey := range rules {
				group := strings.TrimPrefix(strings.TrimPrefix(rawKey, "-:"), "+:")
				if group != "" {
					keys = append(keys, group)
				}
			}
		}
		slices.Sort(keys)
		return slices.Compact(keys), nil
	default:
		return nil, nil
	}
}

func rewriteGroupRenameOptionValues(optionValues, provided map[string]string, plan *groupRenamePlan) (map[string]string, error) {
	renamed := make(map[string]string)
	if len(plan.mappings) > 0 {
		for _, key := range groupRenameOptionKeys {
			if _, ok := provided[key]; ok {
				// The caller already applied the mapping to the final value.
				continue
			}
			raw, ok := optionValues[key]
			if !ok || strings.TrimSpace(raw) == "" {
				continue
			}
			value, changed, err := rewriteGroupRenameOptionValue(key, raw, plan)
			if err != nil {
				return nil, fmt.Errorf("invalid %s option: %w", key, err)
			}
			if changed {
				renamed[key] = value
			}
		}
	}
	maps.Copy(renamed, provided)
	return renamed, nil
}

func rewriteGroupRenameOptionValue(key, raw string, plan *groupRenamePlan) (string, bool, error) {
	switch key {
	case setting.SensitiveCheckExemptGroupsOptionKey:
		groups, err := groupRenameOptionGroupKeys(key, raw)
		if err != nil {
			return "", false, err
		}
		for i, group := range groups {
			if target, mapped := plan.sources[group]; mapped {
				groups[i] = target
			}
		}
		normalized, err := setting.NormalizeSensitiveCheckExemptGroups(strings.Join(groups, "\n"))
		return normalized, normalized != raw, err
	case "GroupRatio", "TopupGroupRatio", "UserUsableGroups", "ModelRequestRateLimitGroup":
		return renameGroupKeyedValues(raw, plan)
	case "AutoGroups":
		return renameGroupListValues(raw, plan)
	case "GroupGroupRatio":
		return renameGroupNestedRatioKeys(raw, plan)
	case "group_ratio_setting.group_special_usable_group":
		return renameGroupSpecialUsableKeys(raw, plan)
	default:
		return raw, false, nil
	}
}

// renameGroupKeyedValues rewrites the top-level keys of a JSON object while
// preserving each value byte-for-byte, so exact ratio decimals survive.
func renameGroupKeyedValues(raw string, plan *groupRenamePlan) (string, bool, error) {
	values := make(map[string]json.RawMessage)
	if err := common.UnmarshalJsonStr(raw, &values); err != nil {
		return "", false, err
	}
	renamed := make(map[string]json.RawMessage, len(values))
	changed := false
	for name, value := range values {
		if target, ok := plan.sources[name]; ok {
			name = target
			changed = true
		}
		renamed[name] = value
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := common.Marshal(renamed)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

func renameGroupListValues(raw string, plan *groupRenamePlan) (string, bool, error) {
	values := make([]string, 0)
	if err := common.UnmarshalJsonStr(raw, &values); err != nil {
		return "", false, err
	}
	renamed := make([]string, 0, len(values))
	changed := false
	seen := make(map[string]struct{}, len(values))
	for _, group := range values {
		if target, ok := plan.sources[group]; ok {
			group = target
			changed = true
		}
		if _, duplicate := seen[group]; duplicate {
			changed = true
			continue
		}
		seen[group] = struct{}{}
		renamed = append(renamed, group)
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := common.Marshal(renamed)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

// renameGroupNestedRatioKeys rewrites outer and inner keys of the nested ratio
// map while preserving each ratio literal exactly.
func renameGroupNestedRatioKeys(raw string, plan *groupRenamePlan) (string, bool, error) {
	values := make(map[string]map[string]json.RawMessage)
	if err := common.UnmarshalJsonStr(raw, &values); err != nil {
		return "", false, err
	}
	renamed := make(map[string]map[string]json.RawMessage, len(values))
	changed := false
	for userGroup, overrides := range values {
		if target, ok := plan.sources[userGroup]; ok {
			userGroup = target
			changed = true
		}
		renamedOverrides := make(map[string]json.RawMessage, len(overrides))
		for usingGroup, ratio := range overrides {
			if target, ok := plan.sources[usingGroup]; ok {
				usingGroup = target
				changed = true
			}
			renamedOverrides[usingGroup] = ratio
		}
		renamed[userGroup] = renamedOverrides
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := common.Marshal(renamed)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

func renameGroupSpecialUsableKeys(raw string, plan *groupRenamePlan) (string, bool, error) {
	values := make(map[string]map[string]string)
	if err := common.UnmarshalJsonStr(raw, &values); err != nil {
		return "", false, err
	}
	renamed := make(map[string]map[string]string, len(values))
	changed := false
	for userGroup, rules := range values {
		if target, ok := plan.sources[userGroup]; ok {
			userGroup = target
			changed = true
		}
		renamedRules := make(map[string]string, len(rules))
		for rawKey, description := range rules {
			renamedKey := rawKey
			if withoutPrefix, ok := strings.CutPrefix(rawKey, "-:"); ok {
				if target, renamedTarget := plan.sources[withoutPrefix]; renamedTarget {
					renamedKey = "-:" + target
				}
			} else if withoutPrefix, ok := strings.CutPrefix(rawKey, "+:"); ok {
				if target, renamedTarget := plan.sources[withoutPrefix]; renamedTarget {
					renamedKey = "+:" + target
				}
			} else if target, renamedTarget := plan.sources[rawKey]; renamedTarget {
				renamedKey = target
			}
			if renamedKey != rawKey {
				changed = true
			}
			renamedRules[renamedKey] = description
		}
		renamed[userGroup] = renamedRules
	}
	if !changed {
		return raw, false, nil
	}
	encoded, err := common.Marshal(renamed)
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

func upsertGroupRenameOption(tx *gorm.DB, key, value string) error {
	option := Option{Key: key, Value: value}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&option).Error
}

func renameGroupChannelReferences(tx *gorm.DB, plan *groupRenamePlan, result *GroupRenameResult) error {
	if len(plan.mappings) == 0 {
		return nil
	}
	channels := make([]Channel, 0)
	if err := lockForUpdate(tx).Select("id", "group").Order("id").Find(&channels).Error; err != nil {
		return err
	}
	for i := range channels {
		groups := strings.Split(channels[i].Group, ",")
		changed := false
		for j := range groups {
			if target, ok := plan.sources[groups[j]]; ok {
				groups[j] = target
				changed = true
			}
		}
		if !changed {
			continue
		}
		if err := tx.Model(&Channel{}).Where("id = ?", channels[i].Id).Update("group", strings.Join(groups, ",")).Error; err != nil {
			return err
		}
		result.Channels++
	}
	return nil
}

// renameGroupAbilityReferences updates the group column in place so Ability
// attributes (priority, weight, tag, enabled) are preserved exactly.
func renameGroupAbilityReferences(tx *gorm.DB, plan *groupRenamePlan, result *GroupRenameResult) error {
	for _, mapping := range plan.mappings {
		update := tx.Model(&Ability{}).Where(map[string]any{"group": mapping.From}).Update("group", mapping.To)
		if update.Error != nil {
			return update.Error
		}
		result.Abilities += update.RowsAffected
	}
	return nil
}

func renameGroupUserReferences(tx *gorm.DB, plan *groupRenamePlan, state *groupRenameTxState) error {
	if len(plan.mappings) == 0 {
		return nil
	}
	users := make([]User, 0)
	if err := lockForUpdate(tx).Select("id", "group").Where(map[string]any{"group": plan.sourceNames()}).Order("id").Find(&users).Error; err != nil {
		return err
	}
	for i := range users {
		target, ok := plan.sources[users[i].Group]
		if !ok {
			continue
		}
		if err := advanceUserPermissionRevisionTx(tx, users[i].Id); err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", users[i].Id).Update("group", target).Error; err != nil {
			return err
		}
		state.affectedUserIds = append(state.affectedUserIds, users[i].Id)
		state.result.Users++
	}
	return nil
}

func renameGroupTokenReferences(tx *gorm.DB, plan *groupRenamePlan, autoTokens []groupRenameAutoToken, state *groupRenameTxState) error {
	if len(plan.mappings) == 0 {
		return nil
	}
	tokensById := make(map[int]*Token)
	groupTokens := make([]Token, 0)
	if err := lockForUpdate(tx).Select("id", "key", "group", "auto_groups").
		Where(map[string]any{"group": plan.sourceNames()}).Order("id").Find(&groupTokens).Error; err != nil {
		return err
	}
	for i := range groupTokens {
		tokensById[groupTokens[i].Id] = &groupTokens[i]
	}
	if len(autoTokens) > 0 {
		ids := make([]int, 0, len(autoTokens))
		for _, token := range autoTokens {
			if _, exists := tokensById[token.Id]; !exists {
				ids = append(ids, token.Id)
			}
		}
		slices.Sort(ids)
		for chunk := range slices.Chunk(ids, groupRenameTokenBatchSize) {
			locked := make([]Token, 0, len(chunk))
			if err := lockForUpdate(tx).Select("id", "key", "group", "auto_groups").
				Where("id IN ?", chunk).Order("id").Find(&locked).Error; err != nil {
				return err
			}
			for i := range locked {
				tokensById[locked[i].Id] = &locked[i]
			}
		}
	}
	ids := make([]int, 0, len(tokensById))
	for id := range tokensById {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		token := tokensById[id]
		groupChanged := false
		if target, ok := plan.sources[token.Group]; ok {
			token.Group = target
			groupChanged = true
		}
		autoGroupsChanged := false
		if strings.TrimSpace(token.AutoGroups) != "" {
			var groups []string
			if err := common.UnmarshalJsonStr(token.AutoGroups, &groups); err != nil {
				return fmt.Errorf("%w: token %d", ErrGroupRenameInvalidAutoGroups, token.Id)
			}
			renamed := make([]string, 0, len(groups))
			seen := make(map[string]struct{}, len(groups))
			for _, group := range groups {
				if target, ok := plan.sources[group]; ok {
					group = target
					autoGroupsChanged = true
				}
				if _, duplicate := seen[group]; duplicate {
					autoGroupsChanged = true
					continue
				}
				seen[group] = struct{}{}
				renamed = append(renamed, group)
			}
			if autoGroupsChanged {
				encoded, err := common.Marshal(renamed)
				if err != nil {
					return err
				}
				token.AutoGroups = string(encoded)
			}
		}
		if !groupChanged && !autoGroupsChanged {
			continue
		}
		if err := advanceTokenPermissionRevisionTx(tx, token.Id); err != nil {
			return err
		}
		if err := tx.Model(&Token{}).Where("id = ?", token.Id).Updates(map[string]any{
			"group":       token.Group,
			"auto_groups": token.AutoGroups,
		}).Error; err != nil {
			return err
		}
		state.affectedTokenKeys = append(state.affectedTokenKeys, token.Key)
		state.result.Tokens++
	}
	return nil
}

// renameGroupSubscriptionReferences migrates only effective (active)
// subscriptions. Expired and cancelled rows are historical records and stay
// untouched; active rows whose end_time already passed are included because the
// expiry reconciler still derives the downgrade group from them.
func renameGroupSubscriptionReferences(tx *gorm.DB, plan *groupRenamePlan, state *groupRenameTxState) error {
	if len(plan.mappings) == 0 {
		return nil
	}
	sources := plan.sourceNames()
	plans := make([]SubscriptionPlan, 0)
	if err := lockForUpdate(tx).Where("upgrade_group IN ? OR downgrade_group IN ?", sources, sources).Order("id").Find(&plans).Error; err != nil {
		return err
	}
	subscriptions := make([]UserSubscription, 0)
	if err := lockForUpdate(tx).Where("status = ?", groupRenameActiveStatus).
		Where("upgrade_group IN ? OR prev_user_group IN ? OR downgrade_group IN ?", sources, sources, sources).
		Order("id").Find(&subscriptions).Error; err != nil {
		return err
	}
	for _, mapping := range plan.mappings {
		for _, column := range []string{"upgrade_group", "downgrade_group"} {
			update := tx.Model(&SubscriptionPlan{}).Where(map[string]any{column: mapping.From}).Update(column, mapping.To)
			if update.Error != nil {
				return update.Error
			}
			state.result.SubscriptionPlans += update.RowsAffected
		}
		for _, column := range []string{"upgrade_group", "prev_user_group", "downgrade_group"} {
			update := tx.Model(&UserSubscription{}).Where("status = ?", groupRenameActiveStatus).
				Where(map[string]any{column: mapping.From}).Update(column, mapping.To)
			if update.Error != nil {
				return update.Error
			}
			state.result.UserSubscriptions += update.RowsAffected
		}
	}
	for i := range plans {
		state.affectedPlanIds = append(state.affectedPlanIds, plans[i].Id)
	}
	for i := range subscriptions {
		state.subscriptionUserIds = append(state.subscriptionUserIds, subscriptions[i].UserId)
	}
	return nil
}

// applyGroupRenameRuntime refreshes in-process state after commit. Each option
// value is re-read from the database while holding a package mutex, so a late
// runtime apply cannot overwrite state written by a newer committed change.
func applyGroupRenameRuntime(state *groupRenameTxState) {
	// The transaction has committed before taking the policy mutex: policy
	// writers take this mutex before their namespace lock, never the reverse.
	requestPolicyOptionMutex.Lock()
	groupRenameRuntimeApplyMutex.Lock()
	for _, key := range state.affectedOptionKeys {
		var option Option
		if err := DB.Where(&Option{Key: key}).First(&option).Error; err != nil {
			common.SysError("group rename: failed to reload option " + key + ": " + err.Error())
			continue
		}
		if err := updateOptionMap(key, option.Value); err != nil {
			common.SysError("group rename: failed to refresh option " + key + ": " + err.Error())
		}
	}
	if err := refreshRequestPolicySnapshot(); err != nil {
		common.SysError("group rename: failed to refresh request policy: " + err.Error())
	}
	groupRenameRuntimeApplyMutex.Unlock()
	requestPolicyOptionMutex.Unlock()
	InvalidatePricingCache()
	InitChannelCache()
	for _, planId := range slices.Compact(slices.Sorted(slices.Values(state.affectedPlanIds))) {
		InvalidateSubscriptionPlanCache(planId)
	}
	for _, userId := range slices.Compact(slices.Sorted(slices.Values(state.affectedUserIds))) {
		if err := RefreshUserGroupCache(userId); err != nil {
			common.SysError(fmt.Sprintf("group rename: failed to refresh user %d group cache: %s", userId, err.Error()))
		}
	}
	for _, userId := range slices.Compact(slices.Sorted(slices.Values(state.subscriptionUserIds))) {
		refreshSubscriptionUserGroupCache(userId, "group rename")
	}
	for _, key := range slices.Compact(slices.Sorted(slices.Values(state.affectedTokenKeys))) {
		if err := invalidateTokenCacheForMutation(key); err != nil {
			common.SysError("group rename: failed to invalidate token cache: " + err.Error())
		}
	}
}
