package model

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

var group2model2channels map[string]map[string][]int // enabled channel
var channelsIDM map[int]*Channel                     // all channels include disabled
var channel2advancedCustomConfig map[int]*dto.AdvancedCustomConfig
var channelSyncLock sync.RWMutex

// Out-of-band guards (for example the CPA quota guard) update the channels and
// abilities tables directly.  Keep a small single-flight refresh gate so a
// request that observes a stale in-memory cache can repair it without making a
// database query on every concurrent retry.
var channelCacheRefreshInFlight atomic.Bool

func InitChannelCache() {
	if !common.MemoryCacheEnabled {
		return
	}
	newChannelId2channel := make(map[int]*Channel)
	newChannel2advancedCustomConfig := make(map[int]*dto.AdvancedCustomConfig)
	var channels []*Channel
	DB.Find(&channels)
	for _, channel := range channels {
		newChannelId2channel[channel.Id] = channel
		if channel.Type == constant.ChannelTypeAdvancedCustom {
			if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
				newChannel2advancedCustomConfig[channel.Id] = config
			}
		}
	}
	var abilities []*Ability
	DB.Where("enabled = ?", true).Find(&abilities)
	newGroup2model2channels := make(map[string]map[string][]int)
	for _, ability := range abilities {
		channel, ok := newChannelId2channel[ability.ChannelId]
		if !ok {
			continue
		}
		if channel.Status != common.ChannelStatusEnabled {
			continue // skip disabled channels
		}
		if _, ok := newGroup2model2channels[ability.Group]; !ok {
			newGroup2model2channels[ability.Group] = make(map[string][]int)
		}
		newGroup2model2channels[ability.Group][ability.Model] = append(
			newGroup2model2channels[ability.Group][ability.Model],
			channel.Id,
		)
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

	channelSyncLock.Lock()
	group2model2channels = newGroup2model2channels
	//channelsIDM = newChannelId2channel
	for i, channel := range newChannelId2channel {
		if channel.ChannelInfo.IsMultiKey {
			channel.Keys = channel.GetKeys()
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
	channelSyncLock.Unlock()
	common.SysLog("channels synced from database")
}

func SyncChannelCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		common.SysLog("syncing channels from database")
		InitChannelCache()
	}
}

func GetRandomSatisfiedChannel(group string, model string, retry int, excludedIDs ...map[int]struct{}) (*Channel, error) {
	return GetRandomSatisfiedChannelForRequestPath(group, model, retry, "", excludedIDs...)
}

func GetRandomSatisfiedChannelForRequestPath(group string, model string, retry int, requestPath string, excludedIDs ...map[int]struct{}) (*Channel, error) {
	// if memory cache is disabled, get channel directly from database
	if !common.MemoryCacheEnabled {
		return GetChannelForRequestPath(group, model, retry, requestPath, excludedIDs...)
	}

	channelSyncLock.RLock()
	// First, try to find channels with the exact model name.
	channels := filterChannelIDsByRequestPath(group2model2channels[group][model], requestPath)

	// If no channels found, try to find channels with the normalized model name.
	if len(channels) == 0 {
		normalizedModel := ratio_setting.FormatMatchingModelName(model)
		channels = filterChannelIDsByRequestPath(group2model2channels[group][normalizedModel], requestPath)
	}

	if len(channels) == 0 {
		channelSyncLock.RUnlock()
		// The quota/ability guards may have changed the database between cache
		// sync ticks.  Re-check the authoritative tables before reporting 503;
		// this closes the short stale-cache window that otherwise makes an
		// already-restored channel look unavailable.
		selected, err := getAuthoritativeChannelForRequestPath(group, model, retry, requestPath, excludedIDs...)
		if selected != nil {
			refreshChannelCacheAsync()
		}
		return selected, err
	}
	defer channelSyncLock.RUnlock()

	excluded := normalizeExcludedChannelIDs(excludedIDs)

	uniquePriorities := make(map[int]bool)
	for _, channelId := range channels {
		if channel, ok := channelsIDM[channelId]; ok {
			uniquePriorities[int(channel.GetPriority())] = true
		} else {
			return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
		}
	}
	var sortedUniquePriorities []int
	for priority := range uniquePriorities {
		sortedUniquePriorities = append(sortedUniquePriorities, priority)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(sortedUniquePriorities)))

	if retry < 0 {
		retry = 0
	}
	if retry >= len(sortedUniquePriorities) {
		retry = len(sortedUniquePriorities) - 1
	}

	for priorityIdx := retry; priorityIdx < len(sortedUniquePriorities); priorityIdx++ {
		targetPriority := int64(sortedUniquePriorities[priorityIdx])
		var targetChannels []*Channel
		for _, channelId := range channels {
			channel, ok := channelsIDM[channelId]
			if !ok {
				return nil, fmt.Errorf("数据库一致性错误，渠道# %d 不存在，请联系管理员修复", channelId)
			}
			if channel.GetPriority() != targetPriority {
				continue
			}
			targetChannels = append(targetChannels, channel)
		}

		targetChannels = filterChannelsForScheduling(targetChannels, excluded)
		if len(targetChannels) == 0 {
			continue
		}

		selected := selectChannelByRuntimeScore(targetChannels)
		if selected != nil {
			return selected, nil
		}
	}

	return nil, errors.New(fmt.Sprintf("no channel found, group: %s, model: %s", group, model))
}

// getAuthoritativeChannelForRequestPath is used only when the in-memory
// ability cache has no entry.  Querying the enabled-ability count first keeps
// the normal "no ability" result as (nil, nil) instead of surfacing the
// database helper's historical consistency error, and also mirrors the cache
// path's normalized-model lookup.
func getAuthoritativeChannelForRequestPath(group string, modelName string, retry int, requestPath string, excludedIDs ...map[int]struct{}) (*Channel, error) {
	if DB == nil {
		return nil, nil
	}
	models := []string{modelName}
	if normalized := ratio_setting.FormatMatchingModelName(modelName); normalized != "" && normalized != modelName {
		models = append(models, normalized)
	}
	for _, candidateModel := range models {
		var count int64
		if err := DB.Model(&Ability{}).
			Where(commonGroupCol+" = ? AND model = ? AND enabled = ?", group, candidateModel, true).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			continue
		}
		selected, err := GetChannelForRequestPath(group, candidateModel, retry, requestPath, excludedIDs...)
		if err != nil || selected != nil {
			return selected, err
		}
	}
	return nil, nil
}

func refreshChannelCacheAsync() {
	if !common.MemoryCacheEnabled || !channelCacheRefreshInFlight.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer channelCacheRefreshInFlight.Store(false)
		InitChannelCache()
	}()
}

func filterChannelIDsByRequestPath(channels []int, requestPath string) []int {
	if requestPath == "" || len(channels) == 0 {
		return channels
	}
	filtered := make([]int, 0, len(channels))
	for _, channelId := range channels {
		channel, ok := channelsIDM[channelId]
		if !ok {
			filtered = append(filtered, channelId)
			continue
		}
		if channel.Type != constant.ChannelTypeAdvancedCustom {
			filtered = append(filtered, channelId)
			continue
		}
		config := channel2advancedCustomConfig[channelId]
		if config == nil {
			config = channel.GetOtherSettings().AdvancedCustom
		}
		if config != nil && config.SupportsPath(requestPath) {
			filtered = append(filtered, channelId)
		}
	}
	return filtered
}

func CacheGetChannel(id int) (*Channel, error) {
	if !common.MemoryCacheEnabled {
		return GetChannelById(id, true)
	}
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
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
	channelSyncLock.RLock()
	defer channelSyncLock.RUnlock()

	c, ok := channelsIDM[id]
	if !ok {
		return nil, fmt.Errorf("渠道# %d，已不存在", id)
	}
	return &c.ChannelInfo, nil
}

func CacheUpdateChannelStatus(id int, status int) {
	if !common.MemoryCacheEnabled {
		return
	}
	channelSyncLock.Lock()
	defer channelSyncLock.Unlock()
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
	defer channelSyncLock.Unlock()
	if channel == nil {
		return
	}

	println("CacheUpdateChannel:", channel.Id, channel.Name, channel.Status, channel.ChannelInfo.MultiKeyPollingIndex)

	println("before:", channelsIDM[channel.Id].ChannelInfo.MultiKeyPollingIndex)
	channelsIDM[channel.Id] = channel
	if channel.Type == constant.ChannelTypeAdvancedCustom {
		if channel2advancedCustomConfig == nil {
			channel2advancedCustomConfig = make(map[int]*dto.AdvancedCustomConfig)
		}
		if config := channel.GetOtherSettings().AdvancedCustom; config != nil {
			channel2advancedCustomConfig[channel.Id] = config
		} else {
			delete(channel2advancedCustomConfig, channel.Id)
		}
	} else if channel2advancedCustomConfig != nil {
		delete(channel2advancedCustomConfig, channel.Id)
	}
	println("after :", channelsIDM[channel.Id].ChannelInfo.MultiKeyPollingIndex)
}
