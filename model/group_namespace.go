package model

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	groupConfigVersionOptionKey = "GroupConfigVersion"
	groupRenameAliasesOptionKey = "GroupRenameAliases"
	groupConfigRedisChannel     = "new-api:group-config:invalidate"

	// Stable application-scoped PostgreSQL advisory lock id ("GRPNAME").
	groupNamespaceAdvisoryLockID int64 = 0x4752504e414d45
)

var (
	ErrGroupReferenceRenamed = errors.New("group reference was renamed")

	groupConfigAppliedVersion     atomic.Int64
	groupConfigVersionInitialized atomic.Bool
	groupConfigApplyMutex         sync.Mutex
	groupConfigSubscriberOnce     sync.Once
)

type groupConfigInvalidationEvent struct {
	Version  int64                `json:"version"`
	Mappings []GroupRenameMapping `json:"mappings,omitempty"`
}

func groupNamespaceMetadataAvailable(tx *gorm.DB) bool {
	if tx == nil {
		return false
	}
	// Focused SQLite unit fixtures may intentionally migrate only the model
	// under test. Production schemas always contain options. Skip namespace
	// metadata only for those incomplete SQLite fixtures; PostgreSQL/MySQL stay
	// fail-closed.
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		return tx.Migrator().HasTable(&Option{})
	}
	return true
}

// lockGroupNamespace serializes every write that can change a group reference.
// PostgreSQL uses a transaction-scoped advisory lock so inserts and updates
// serialize even when no row exists yet. MySQL falls back to a locked option
// row; SQLite keeps its existing single-writer transaction behavior.
func lockGroupNamespace(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("group namespace lock requires a transaction")
	}
	if !groupNamespaceMetadataAvailable(tx) {
		return nil
	}
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		return tx.Exec("SELECT pg_advisory_xact_lock(?)", groupNamespaceAdvisoryLockID).Error
	}

	option := Option{Key: groupConfigVersionOptionKey, Value: "0"}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoNothing: true,
	}).Create(&option).Error; err != nil {
		return err
	}
	if common.UsingMainDatabase(common.DatabaseTypeSQLite) {
		return nil
	}
	return lockForUpdate(tx).Where(&Option{Key: groupConfigVersionOptionKey}).First(&option).Error
}

func loadGroupRenameAliasesTx(tx *gorm.DB) (map[string]string, error) {
	if tx == nil {
		tx = DB
	}
	var option Option
	query := tx.Where(&Option{Key: groupRenameAliasesOptionKey}).Limit(1).Find(&option)
	if query.Error != nil {
		return nil, query.Error
	}
	if query.RowsAffected == 0 || strings.TrimSpace(option.Value) == "" {
		return map[string]string{}, nil
	}
	aliases := make(map[string]string)
	if err := common.UnmarshalJsonStr(option.Value, &aliases); err != nil {
		return nil, fmt.Errorf("invalid %s option: %w", groupRenameAliasesOptionKey, err)
	}
	return aliases, nil
}

func loadConfiguredGroupNamesTx(tx *gorm.DB) (map[string]struct{}, error) {
	values := make(map[string]string, len(groupRenameOptionKeys))
	for _, key := range groupRenameOptionKeys {
		values[key] = groupRenameMemoryOptionValue(key)
	}
	var options []Option
	if err := tx.Where(map[string]any{"key": groupRenameOptionKeys}).Find(&options).Error; err != nil {
		return nil, err
	}
	for _, option := range options {
		values[option.Key] = option.Value
	}
	// Policy membership is a reference, not a definition or authorization to
	// reintroduce a renamed group into subscriptions or other group writers.
	delete(values, setting.SensitiveCheckExemptGroupsOptionKey)
	configured := make(map[string]struct{})
	if err := collectGroupNamesFromOptions(values, configured); err != nil {
		return nil, err
	}
	return configured, nil
}

// validateGroupNamesForWriteTx runs only after lockGroupNamespace. A source key
// from a committed rename is rejected unless an administrator explicitly
// reintroduced that key into current group settings.
func validateGroupNamesForWriteTx(tx *gorm.DB, groups ...string) error {
	if !groupNamespaceMetadataAvailable(tx) {
		return nil
	}
	aliases, err := loadGroupRenameAliasesTx(tx)
	if err != nil || len(aliases) == 0 {
		return err
	}
	configured, err := loadConfiguredGroupNamesTx(tx)
	if err != nil {
		return err
	}
	for _, raw := range groups {
		group := strings.TrimSpace(raw)
		if group == "" {
			continue
		}
		target, renamed := aliases[group]
		if !renamed {
			continue
		}
		if _, reintroduced := configured[group]; reintroduced {
			continue
		}
		return fmt.Errorf("%w: %s -> %s; reload and retry", ErrGroupReferenceRenamed, group, target)
	}
	return nil
}

// validateConfiguredGroupNamesForWriteTx validates the values that a writer
// will actually persist. This is stricter than the alias check used by legacy
// writers: a group must still be present in the committed group options.
func validateConfiguredGroupNamesForWriteTx(tx *gorm.DB, groups ...string) error {
	if err := validateGroupNamesForWriteTx(tx, groups...); err != nil {
		return err
	}
	configured, err := loadConfiguredGroupNamesTx(tx)
	if err != nil {
		return err
	}
	for _, raw := range groups {
		group := strings.TrimSpace(raw)
		if group == "" {
			continue
		}
		if _, ok := configured[group]; !ok {
			return fmt.Errorf("group %q is not configured", group)
		}
	}
	return nil
}

func validateJoinedGroupsForWriteTx(tx *gorm.DB, joined string) error {
	if strings.TrimSpace(joined) == "" {
		return nil
	}
	groups := make([]string, 0)
	for group := range strings.SplitSeq(strings.Trim(joined, ","), ",") {
		if trimmed := strings.TrimSpace(group); trimmed != "" {
			groups = append(groups, trimmed)
		}
	}
	return validateGroupNamesForWriteTx(tx, groups...)
}

func validateTokenGroupsForWriteTx(tx *gorm.DB, token *Token) error {
	if token == nil {
		return nil
	}
	groups := []string{token.Group}
	if strings.TrimSpace(token.AutoGroups) != "" {
		var autoGroups []string
		if err := common.UnmarshalJsonStr(token.AutoGroups, &autoGroups); err != nil {
			return fmt.Errorf("%w: token auto_groups", ErrGroupRenameInvalidAutoGroups)
		}
		groups = append(groups, autoGroups...)
	}
	return validateGroupNamesForWriteTx(tx, groups...)
}

func recordGroupRenameAliasesTx(tx *gorm.DB, mappings []GroupRenameMapping) error {
	if len(mappings) == 0 {
		return nil
	}
	aliases, err := loadGroupRenameAliasesTx(tx)
	if err != nil {
		return err
	}
	for _, mapping := range mappings {
		for source, target := range aliases {
			if target == mapping.From {
				aliases[source] = mapping.To
			}
		}
		aliases[mapping.From] = mapping.To
	}
	data, err := common.Marshal(aliases)
	if err != nil {
		return err
	}
	option := Option{Key: groupRenameAliasesOptionKey, Value: string(data)}
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&option).Error
}

func bumpGroupConfigVersionTx(tx *gorm.DB) (int64, error) {
	option := Option{Key: groupConfigVersionOptionKey, Value: "0"}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoNothing: true,
	}).Create(&option).Error; err != nil {
		return 0, err
	}
	if err := lockForUpdate(tx).Where(&Option{Key: groupConfigVersionOptionKey}).First(&option).Error; err != nil {
		return 0, err
	}
	current, err := strconv.ParseInt(strings.TrimSpace(option.Value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid group config version %q: %w", option.Value, err)
	}
	next := current + 1
	if err := tx.Model(&Option{}).Where(&Option{Key: groupConfigVersionOptionKey}).
		Update("value", strconv.FormatInt(next, 10)).Error; err != nil {
		return 0, err
	}
	return next, nil
}

func updateUserGroupTx(tx *gorm.DB, userID int, group string) error {
	if err := lockGroupNamespace(tx); err != nil {
		return err
	}
	if err := validateGroupNamesForWriteTx(tx, group); err != nil {
		return err
	}
	if err := advanceUserPermissionRevisionTx(tx, userID); err != nil {
		return err
	}
	return tx.Model(&User{}).Where("id = ?", userID).Update("group", group).Error
}

func groupConfigTopic(database int) string {
	return fmt.Sprintf("%s:db:%d", groupConfigRedisChannel, database)
}

func PublishGroupConfigInvalidation(version int64, mappings []GroupRenameMapping) error {
	if version <= 0 || !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	payload, err := common.Marshal(groupConfigInvalidationEvent{Version: version, Mappings: mappings})
	if err != nil {
		return err
	}
	return common.RDB.Publish(context.Background(), groupConfigTopic(common.RDB.Options().DB), payload).Err()
}

func StartGroupConfigSubscriber(ctx context.Context) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	groupConfigSubscriberOnce.Do(func() { go RunGroupConfigSubscriber(ctx) })
}

// RunGroupConfigSubscriber blocks until cancellation so the composition root
// can join the subscriber before closing Redis or the SQL connection pools.
func RunGroupConfigSubscriber(ctx context.Context) {
	if !common.RedisEnabled || common.RDB == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pubsub := common.RDB.Subscribe(ctx, groupConfigTopic(common.RDB.Options().DB))
	defer pubsub.Close()
	receiveGroupConfigEvents(ctx, pubsub.Channel())
}

func receiveGroupConfigEvents(ctx context.Context, ch <-chan *redis.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var event groupConfigInvalidationEvent
			if err := common.Unmarshal([]byte(msg.Payload), &event); err != nil {
				common.SysLog("failed to decode group config invalidation: " + err.Error())
				continue
			}
			if err := applyGroupConfigInvalidation(event.Version, event.Mappings, true); err != nil {
				common.SysLog("failed to apply group config invalidation: " + err.Error())
			}
		}
	}
}

func reloadGroupRuntimeOptions() error {
	// The caller owns requestPolicyOptionMutex before groupConfigApplyMutex.
	groupRenameRuntimeApplyMutex.Lock()
	defer groupRenameRuntimeApplyMutex.Unlock()
	var options []Option
	if err := DB.Where(map[string]any{"key": groupRenameOptionKeys}).Find(&options).Error; err != nil {
		return err
	}
	for _, option := range options {
		if err := updateOptionMap(option.Key, option.Value); err != nil {
			return err
		}
	}
	return refreshRequestPolicySnapshot()
}

func applyGroupConfigInvalidation(version int64, mappings []GroupRenameMapping, reloadOptions bool) error {
	if version <= 0 || version <= groupConfigAppliedVersion.Load() {
		return nil
	}
	if reloadOptions {
		// Periodic option loads already own the policy mutex and pass false.
		// Keep the same lock order for Pub/Sub to avoid an inversion with them.
		requestPolicyOptionMutex.Lock()
		defer requestPolicyOptionMutex.Unlock()
	}
	groupConfigApplyMutex.Lock()
	defer groupConfigApplyMutex.Unlock()
	if version <= groupConfigAppliedVersion.Load() {
		return nil
	}
	if reloadOptions {
		if err := reloadGroupRuntimeOptions(); err != nil {
			return err
		}
	}
	if err := refreshGroupRuntimeCaches(mappings, len(mappings) == 0); err != nil {
		return err
	}
	groupConfigAppliedVersion.Store(version)
	groupConfigVersionInitialized.Store(true)
	return nil
}

func setGroupConfigAppliedVersion(version int64) {
	groupConfigVersionInitialized.Store(true)
	for version > 0 {
		current := groupConfigAppliedVersion.Load()
		if version <= current || groupConfigAppliedVersion.CompareAndSwap(current, version) {
			return
		}
	}
}

// syncGroupConfigVersionAfterOptionLoad is the periodic-sync fallback for
// missed Pub/Sub events. The options have already been loaded into runtime, so
// only dependent caches need rebuilding.
func syncGroupConfigVersionAfterOptionLoad(options []*Option) {
	var version int64
	for _, option := range options {
		if option == nil || option.Key != groupConfigVersionOptionKey {
			continue
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(option.Value), 10, 64)
		if err == nil && parsed >= 0 {
			version = parsed
		}
		break
	}

	// Mark even version zero as an initialized runtime snapshot. A node may
	// start before the first rename creates GroupConfigVersion; if it then
	// misses that first Pub/Sub event, the later 0 -> 1 periodic observation
	// must rebuild dependent caches instead of being mistaken for first init.
	if !groupConfigVersionInitialized.Swap(true) {
		groupConfigAppliedVersion.Store(version)
		return
	}

	current := groupConfigAppliedVersion.Load()
	if version > current {
		if err := applyGroupConfigInvalidation(version, nil, false); err != nil {
			common.SysLog("failed to recover group config after periodic option sync: " + err.Error())
		}
	}
}

func refreshGroupRuntimeCaches(mappings []GroupRenameMapping, full bool) error {
	InvalidatePricingCache()
	if err := RefreshChannelCache(context.Background()); err != nil {
		return err // Do not acknowledge an invalidation whose snapshot was not applied.
	}

	var userIDs []int
	var tokenKeys []string
	var planIDs []int
	if full {
		if err := DB.Model(&User{}).Pluck("id", &userIDs).Error; err != nil {
			return err
		}
		if err := DB.Model(&Token{}).Pluck("key", &tokenKeys).Error; err != nil {
			return err
		}
		if err := DB.Model(&SubscriptionPlan{}).Pluck("id", &planIDs).Error; err != nil {
			return err
		}
	} else {
		targets := make([]string, 0, len(mappings))
		for _, mapping := range mappings {
			targets = append(targets, mapping.To)
		}
		if err := DB.Model(&User{}).Where(commonGroupCol+" IN ?", targets).Pluck("id", &userIDs).Error; err != nil {
			return err
		}
		if err := DB.Model(&Token{}).Where(commonGroupCol+" IN ?", targets).Pluck("key", &tokenKeys).Error; err != nil {
			return err
		}
		var autoTokens []Token
		if err := DB.Select("key", "auto_groups").Where("auto_groups <> ?", "").Find(&autoTokens).Error; err != nil {
			return err
		}
		targetSet := make(map[string]struct{}, len(targets))
		for _, target := range targets {
			targetSet[target] = struct{}{}
		}
		for _, token := range autoTokens {
			var groups []string
			if err := common.UnmarshalJsonStr(token.AutoGroups, &groups); err != nil {
				continue
			}
			for _, group := range groups {
				if _, ok := targetSet[group]; ok {
					tokenKeys = append(tokenKeys, token.Key)
					break
				}
			}
		}
		if err := DB.Model(&SubscriptionPlan{}).
			Where("upgrade_group IN ? OR downgrade_group IN ?", targets, targets).
			Pluck("id", &planIDs).Error; err != nil {
			return err
		}
		var subscriptionUsers []int
		if err := DB.Model(&UserSubscription{}).Where("status = ?", groupRenameActiveStatus).
			Where("upgrade_group IN ? OR prev_user_group IN ? OR downgrade_group IN ?", targets, targets, targets).
			Pluck("user_id", &subscriptionUsers).Error; err != nil {
			return err
		}
		userIDs = append(userIDs, subscriptionUsers...)
	}

	for _, planID := range compactSortedInts(planIDs) {
		InvalidateSubscriptionPlanCache(planID)
	}
	for _, userID := range compactSortedInts(userIDs) {
		if err := RefreshUserGroupCache(userID); err != nil && common.RedisEnabled {
			return err
		}
	}
	for _, key := range compactSortedStrings(tokenKeys) {
		if err := invalidateTokenCacheForMutation(key); err != nil {
			return err
		}
	}
	return nil
}

func compactSortedInts(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sort.Ints(values)
	return slices.Compact(values)
}

func compactSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	return slices.Compact(values)
}
