package model

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Only short authorization/routing/quote snapshots hold this lock. No upstream
// call, stream, wallet settlement or HTTP response may run while it is held.
// Runtime writers acquire requestPolicyOptionMutex before this mutex.
var groupConfigRuntimeMutex sync.RWMutex

const GroupConfigVersionContextKey = "group_config_admission_version"

var ErrGroupConfigChanged = errors.New("group configuration changed; retry the request")

type GroupConfigRead struct {
	Version int64
	ctx     context.Context
	locked  bool
}

func readGroupConfigVersion(ctx context.Context) (int64, error) {
	if DB == nil {
		return 0, ErrGroupConfigChanged
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var option Option
	result := DB.WithContext(bounded).Where(&Option{Key: groupConfigVersionOptionKey}).Limit(1).Find(&option)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected == 0 {
		return 0, nil
	}
	version, err := strconv.ParseInt(strings.TrimSpace(option.Value), 10, 64)
	if err != nil || version < 0 {
		return 0, ErrGroupConfigChanged
	}
	return version, nil
}

// BeginGroupConfigRead refreshes a missed invalidation before reading identity
// and policy. The final version check detects a remote rename committed while
// those snapshots were read. Concurrent changes are rejected, not aliased.
func BeginGroupConfigRead(ctx context.Context) (*GroupConfigRead, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// The common path can establish its opening fence with one primary read
	// under the local read lock. A stale or invalid runtime must release the
	// read lock before refreshing, then establish a new primary fence.
	groupConfigRuntimeMutex.RLock()
	version, err := readGroupConfigVersion(ctx)
	if err != nil {
		groupConfigRuntimeMutex.RUnlock()
		return nil, err
	}
	if version == groupConfigAppliedVersion.Load() && groupConfigVersionInitialized.Load() && !groupConfigRuntimeInvalid.Load() {
		return &GroupConfigRead{Version: version, ctx: ctx, locked: true}, nil
	}
	groupConfigRuntimeMutex.RUnlock()
	if version > groupConfigAppliedVersion.Load() || !groupConfigVersionInitialized.Load() || groupConfigRuntimeInvalid.Load() {
		if err := applyGroupConfigInvalidation(version, nil, true); err != nil {
			return nil, err
		}
	}
	groupConfigRuntimeMutex.RLock()
	guard := &GroupConfigRead{Version: version, ctx: ctx, locked: true}
	if version != groupConfigAppliedVersion.Load() || groupConfigRuntimeInvalid.Load() || !groupConfigVersionInitialized.Load() {
		guard.Close()
		return nil, ErrGroupConfigChanged
	}
	if err := guard.Check(); err != nil {
		guard.Close()
		return nil, err
	}
	return guard, nil
}

// BeginRequestGroupConfigRead fences later routing/price decisions to the
// authentication snapshot. Internal callers with no HTTP admission snapshot
// retain their existing contract; public ingress sets the version explicitly.
func BeginRequestGroupConfigRead(c *gin.Context) (*GroupConfigRead, error) {
	guard, err := lockRequestGroupConfigRead(c)
	if err != nil {
		return nil, err
	}
	if err := guard.Check(); err != nil {
		guard.Close()
		return nil, err
	}
	return guard, nil
}

const groupConfigDecisionVersionContextKey = "group_config_decision_version"

// PermitDeferredGroupConfigDecision binds the optimization to the exact
// group version that a trusted ingress has already checked on the primary.
// Only the build-tagged enterprise lane ingress opts in, after canonical
// authorization. The marker is request-local and never grants access.
func PermitDeferredGroupConfigDecision(c *gin.Context) {
	if c == nil {
		return
	}
	value, _ := c.Get(GroupConfigVersionContextKey)
	if version, ok := value.(int64); ok && version >= 0 {
		c.Set(groupConfigDecisionVersionContextKey, version)
	}
}

// BeginRequestGroupConfigDecision is for short, read-only calculations whose
// result and effects are withheld until Finish succeeds. Do not use it around
// upstream I/O, routing selection, reservations, or externally visible effects.
// A bounded primary read and request-local scratch state are fine. For opted
// in requests, the local read lock pins runtime state at the admitted version;
// Finish still reads the authoritative version from the primary. Versions
// only advance, so a missed remote invalidation is rejected even when it
// committed before this calculation. Other requests keep both SQL fences.
func BeginRequestGroupConfigDecision(c *gin.Context) (*GroupConfigRead, error) {
	if c != nil {
		if value, enabled := c.Get(groupConfigDecisionVersionContextKey); enabled {
			version, ok := value.(int64)
			if !ok || version < 0 {
				return nil, ErrGroupConfigChanged
			}
			guard, err := lockRequestGroupConfigRead(c)
			if err != nil {
				return nil, err
			}
			if !guard.locked || guard.Version != version {
				guard.Close()
				return nil, ErrGroupConfigChanged
			}
			if err := guard.ctx.Err(); err != nil {
				guard.Close()
				return nil, err
			}
			return guard, nil
		}
	}
	return BeginRequestGroupConfigRead(c)
}

// CheckRequestGroupConfigVersion is a point-in-time fence. Use Begin and
// Finish instead when there are intervening policy or routing reads.
func CheckRequestGroupConfigVersion(c *gin.Context) error {
	guard, err := lockRequestGroupConfigRead(c)
	if err != nil {
		return err
	}
	return guard.Finish()
}

func lockRequestGroupConfigRead(c *gin.Context) (*GroupConfigRead, error) {
	if c == nil {
		return &GroupConfigRead{}, nil
	}
	value, exists := c.Get(GroupConfigVersionContextKey)
	if !exists {
		return &GroupConfigRead{}, nil
	}
	version, ok := value.(int64)
	if !ok || c.Request == nil {
		return nil, ErrGroupConfigChanged
	}
	groupConfigRuntimeMutex.RLock()
	guard := &GroupConfigRead{Version: version, ctx: c.Request.Context(), locked: true}
	if version != groupConfigAppliedVersion.Load() || groupConfigRuntimeInvalid.Load() || !groupConfigVersionInitialized.Load() {
		guard.Close()
		return nil, ErrGroupConfigChanged
	}
	return guard, nil
}

func (guard *GroupConfigRead) Check() error {
	if guard == nil || !guard.locked {
		return nil
	}
	version, err := readGroupConfigVersion(guard.ctx)
	if err != nil {
		return err
	}
	if version != guard.Version {
		return ErrGroupConfigChanged
	}
	return nil
}

func (guard *GroupConfigRead) Close() {
	if guard != nil && guard.locked {
		guard.locked = false
		groupConfigRuntimeMutex.RUnlock()
	}
}

func (guard *GroupConfigRead) Finish() error {
	defer guard.Close()
	return guard.Check()
}

// GroupRenameAliasesForRead is metadata only, never an authorization alias.
// Callers must not translate client-selected scopes or grant group access with it.
func GroupRenameAliasesForRead(ctx context.Context) (map[string]string, error) {
	if DB == nil {
		return nil, ErrGroupConfigChanged
	}
	if ctx == nil {
		ctx = context.Background()
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return loadGroupRenameAliasesTx(DB.WithContext(bounded))
}

// Call only while holding the namespace transaction lock, before any write.
func validateGroupConfigVersionTx(tx *gorm.DB, expected *int64) error {
	if expected == nil {
		return nil
	}
	var option Option
	result := tx.Where(&Option{Key: groupConfigVersionOptionKey}).Limit(1).Find(&option)
	if result.Error != nil {
		return result.Error
	}
	var current int64
	if result.RowsAffected != 0 {
		var err error
		current, err = strconv.ParseInt(strings.TrimSpace(option.Value), 10, 64)
		if err != nil || current < 0 {
			return ErrGroupConfigChanged
		}
	}
	if current != *expected {
		return ErrGroupConfigChanged
	}
	return nil
}
