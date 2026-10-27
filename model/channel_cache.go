package model

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	kitdto "github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
// channel2advancedCustomConfig caches parsed Advanced Custom (type 58) configs so
// path-aware selection avoids re-parsing JSON per request. Refreshed on full sync.
var channel2advancedCustomConfig map[int]*kitdto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

// Serialize refreshes, but never hold the cache read/write lock during SQL IO.
var channelRefreshLock sync.Mutex
var channelCacheRevision uint64 // protected by channelSyncLock
var channelCacheLastRefresh atomic.Int64
var channelCacheRefreshFailures atomic.Uint64

// ErrChannelCacheStale means routing data is temporarily unavailable, not that
// a channel was deleted. Existing upstream tasks must remain pending for retry.
var ErrChannelCacheStale = errors.New("channel cache snapshot exceeded staleness budget")

// ErrChannelCacheMiss is snapshot absence, not proof of database deletion.
var ErrChannelCacheMiss = errors.New("channel is absent from cache snapshot")

// ChannelCacheHealth exposes counters, not channel IDs, keys, or DSNs.
func ChannelCacheHealth() (lastSuccess time.Time, failures uint64) {
	if ts := channelCacheLastRefresh.Load(); ts != 0 {
		lastSuccess = time.Unix(0, ts)
	}
	return lastSuccess, channelCacheRefreshFailures.Load()
}

func channelCacheExpired() bool {
	last := channelCacheLastRefresh.Load()
	// An uninitialized cache is handled by startup's explicit refresh. Tests and
	// embedded hosts that seed their own cache retain the existing API contract.
	if last == 0 {
		return false
	}
	defaultSeconds := max(300, min(common.SyncFrequency, 28800)*3)
	seconds := common.GetEnvOrDefault("CHANNEL_CACHE_MAX_STALE_SECONDS", defaultSeconds)
	if seconds <= 0 || seconds > 86400 {
		seconds = defaultSeconds
	}
	return time.Since(time.Unix(0, last)) > time.Duration(seconds)*time.Second
}

func InitChannelCache() {
	if err := RefreshChannelCache(context.Background()); err != nil {
		common.SysError("channel cache refresh failed; previous snapshot retained: " + common.MaskSensitiveLogInfo(err.Error()))
	}
}

// RefreshChannelCache publishes only a fully loaded and validated snapshot.
// On failure readers retain the previous snapshot for a bounded time, then
// fail closed after the staleness budget instead of serving revoked routes forever.
func RefreshChannelCache(ctx context.Context) (err error) {
	channelRefreshLock.Lock()
	defer channelRefreshLock.Unlock()
	defer func() {
		if err != nil {
			channelCacheRefreshFailures.Add(1)
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if !common.MemoryCacheEnabled {
		InvalidatePricingCache()
		rebuildTaskAliasView()
		return nil
	}
	if DB == nil {
		return errors.New("channel cache database is not initialized")
	}
	channelSyncLock.RLock()
	revision := channelCacheRevision
	channelSyncLock.RUnlock()
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*kitdto.AdvancedCustomConfig)
	var channels []*Channel
	if err := DB.WithContext(ctx).Find(&channels).Error; err != nil {
		return fmt.Errorf("load channels: %w", err)
	}
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	if err := DB.WithContext(ctx).Find(&abilities).Error; err != nil {
		return fmt.Errorf("load channel abilities: %w", err)
	}
	groups := make(map[string]bool)
	for _, ability := range abilities {
		groups[ability.Group] = true
	}
	newGroup2model2channels := make(map[string]map[string][]int)
	for group := range groups {
		newGroup2model2channels[group] = make(map[string][]int)
	}
	for _, channel := range channels {
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		models := channel.GetModels()
		if len(models) == 0 {
			continue
		}
		groups := strings.SplitSeq(channel.Group, ",")
		for group := range groups {
			if _, ok := newGroup2model2channels[group]; !ok {
				return fmt.Errorf("channel %d references a group absent from abilities", channel.Id)
			}
			for _, model := range models {
				if _, ok := newGroup2model2channels[group][model]; !ok {
					newGroup2model2channels[group][model] = make([]int, 0)
				}
				newGroup2model2channels[group][model] = append(newGroup2model2channels[group][model], channel.Id)
			}
		}
	}

	// sort by priority
	for group, model2channels := range newGroup2model2channels {
		for model, channels := range model2channels {
			sort.Slice(channels, func(i, j int) bool {
				return newChannelId2channel[channels[i]].GetPriority() > newChannelId2channel[channels[j]].GetPriority()
			})
			newGroup2model2channels[group][model] = channels
		}
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	// Key selection locks a channel's polling mutex before channelSyncLock.
	// Preserve that order and lock IDs in a stable order while carrying the
	// in-memory cursor forward; no SQL or pricing-cache work occurs under them.
	var pollingIDs []int
	for id, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				pollingIDs = append(pollingIDs, id)
			}
		}
	}
	sort.Ints(pollingIDs)
	pollingLocks := make([]*sync.Mutex, 0, len(pollingIDs))
	for _, id := range pollingIDs {
		lock := GetChannelPollingLock(id)
		lock.Lock()
		pollingLocks = append(pollingLocks, lock)
	}
	channelSyncLock.Lock()
	if revision != channelCacheRevision {
		channelSyncLock.Unlock()
		for i := len(pollingLocks) - 1; i >= 0; i-- {
			pollingLocks[i].Unlock()
		}
		return errors.New("channel state changed during refresh; retry on next reconciliation")
	}
	group2model2channels = newGroup2model2channels
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			if channel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
				if oldChannel, ok := channelsIDM[i]; ok {
					// 存在旧的渠道，如果是多key且轮询，保留轮询索引信息
					if oldChannel.ChannelInfo.IsMultiKey && oldChannel.ChannelInfo.MultiKeyMode == constant.MultiKeyModePolling {
						channel.ChannelInfo.MultiKeyPollingIndex = oldChannel.ChannelInfo.MultiKeyPollingIndex
					}
				}
			}
		}
	}
	channelsIDM = newChannelId2channel
	channel2advancedCustomConfig = newChannel2advancedCustomConfig
	channelCacheRevision++
	channelCacheLastRefresh.Store(time.Now().UnixNano())
	channelSyncLock.Unlock()
	for i := len(pollingLocks) - 1; i >= 0; i-- {
		pollingLocks[i].Unlock()
	}
	// Lock ordering: InvalidatePricingCache acquires updatePricingLock, and
	// GetPricing (holding updatePricingLock) nests channelSyncLock.RLock via
	// loadPricingAdvancedCustomConfigs. channelSyncLock MUST be released before
	// invalidating the pricing cache, otherwise the reversed order deadlocks.
	InvalidatePricingCache()
	rebuildTaskAliasView()
	common.SysLog("channels synced from database")
	return nil
}

func SyncChannelCache(frequency int) {
	RunChannelCacheSync(context.Background(), frequency)
}

func RunChannelCacheSync(ctx context.Context, frequency int) {
	if frequency <= 0 {
		frequency = 60
	}
	interval := time.Duration(frequency) * time.Second
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if ctx.Err() != nil {
				return
			}
			if err := RefreshChannelCache(ctx); err != nil && ctx.Err() == nil {
				common.SysError("channel cache refresh failed; retaining previous snapshot: " + common.MaskSensitiveLogInfo(err.Error()))
			}
			timer.Reset(interval)
		}
	}
}

func GetRandomSatisfiedChannel(
	group string,
	model string,
	retry int,
	filters []dto.ChannelFilter,
) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannel(group, model, retry, filters)
	}

	if channelCacheExpired() {
		return nil, ErrChannelCacheStale
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	// First, try to find channels with the exact model name.
	channels, _ := filterCandidateIDs(group2model2channels[group][model], model, filters)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.RoutingMatchModelName(model)
		channels, _ = filterCandidateIDs(group2model2channels[group][normalizedModel], model, filters)
	}

	if len(channels) == 0 {
		return nil, nil
	}

	if len(channels) == 1 {
		if channel, ok := channelsIDM[channels[0]]; ok {
			return channel, nil
		}
		return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channels[0])
	}

	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry >= len(uniquePriorities) {
		retry = len(uniquePriorities) - 1
	}
	targetPriority := int64(sortedUniquePriorities[retry])

	// get the priority for the given retry number
	var sumWeight = 0
	var targetChannels []*Channel
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			if channel.GetPriority() == targetPriority {
				sumWeight += channel.GetWeight()
				targetChannels = append(targetChannels, channel)
			}
		} else {
			return nil, fmt.Errorf("database consistency error: channel #%d does not exist, please contact the administrator", channelId)
		}
	}

	if len(targetChannels) == 0 {
		return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s, priority: %d", group, model, targetPriority))
	}

	// smoothing factor and adjustment
	smoothingFactor := 1
	smoothingAdjustment := 0

	if sumWeight == 0 {
		// when all channels have weight 0, set sumWeight to the number of channels and set smoothing adjustment to 100
		// each channel's effective weight = 100
		sumWeight = len(targetChannels) * 100
		smoothingAdjustment = 100
	} else if sumWeight/len(targetChannels) < 10 {
		// when the average weight is less than 10, set smoothing factor to 100
		smoothingFactor = 100
	}

	// Calculate the total weight of all channels up to endIdx
	totalWeight := sumWeight * smoothingFactor

	// Generate a random value in the range [0, totalWeight)
	randomWeight := rand.Intn(totalWeight)

	// Find a channel based on its weight
	for _, channel := range targetChannels {
		randomWeight -= channel.GetWeight()*smoothingFactor + smoothingAdjustment
		if randomWeight < 0 {
			return channel, nil
		}
	}
	// return null if no channel is not found
	return nil, errors.New("channel not found")
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	if channelCacheExpired() {
		return nil, ErrChannelCacheStale
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d is absent from cache: %w", id, ErrChannelCacheMiss)
	}
	return c, nil
}

func CacheGetChannelInfo(id int) (*ChannelInfo, error) {
	if !common.MemoryCacheEnabled {
		channel, err := GetChannelById(id, true)
		if err != nil {
			return nil, err
		}
		return &channel.ChannelInfo, nil
	}
	if channelCacheExpired() {
		return nil, ErrChannelCacheStale
	}

	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("channel #%d is absent from cache: %w", id, ErrChannelCacheMiss)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
	channelCacheRevision++
	if channel, ok := channelsIDM[id]; ok {
		channel.Status = status
	}
	if status != common.ChannelStatusEnabled {
		// delete the channel from group2model2channels
		for group, model2channels := range group2model2channels {
			for model, channels := range model2channels {
				for i, channelId := range channels {
					if channelId == id {
						// remove the channel from the slice
						group2model2channels[group][model] = append(channels[:i], channels[i+1:]...)
						break
					}
				}
			}
		}
	}
}

func CacheUpdateChannel(channel *Channel) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	if channel == nil {
		channelSyncLock.Unlock()
		return
	}

	channelCacheRevision++
	if channelsIDM == nil {
		channelsIDM = make(map[int]*Channel)
	}
	if oldChannel, ok := channelsIDM[channel.Id]; ok {
		logger.LogDebug(nil, "CacheUpdateChannel before: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, oldChannel.ChannelInfo.MultiKeyPollingIndex)
	}
	channelsIDM[channel.Id] = channel
	if channel2advancedCustomConfig == nil {
		channel2advancedCustomConfig = make(map[int]*kitdto.AdvancedCustomConfig)
	}
	delete(channel2advancedCustomConfig, channel.Id)
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		}
	}
	logger.LogDebug(nil, "CacheUpdateChannel after: id=%d, name=%s, status=%d, polling_index=%d", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)
	// Lock ordering: do NOT hold channelSyncLock while calling
	// InvalidatePricingCache. GetPricing acquires updatePricingLock first and then
	// channelSyncLock.RLock (via loadPricingAdvancedCustomConfigs); acquiring
	// updatePricingLock while holding channelSyncLock would be an AB-BA deadlock.
	channelSyncLock.Unlock()
	InvalidatePricingCache()
}
