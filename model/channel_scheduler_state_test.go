package model

import (
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGetRandomSatisfiedChannelSkipsExcludedAndTemporaryUnschedulable(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalGroupMap := group2model2channels
	originalChannelMap := channelsIDM
	common.MemoryCacheEnabled = true
	defer func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		group2model2channels = originalGroupMap
		channelsIDM = originalChannelMap
		clearChannelTemporaryUnschedulableCacheForTest(1, 2, 3)
		resetChannelSchedulerRuntimeStateForTest()
	}()

	priority := int64(10)
	weight := uint(100)
	group2model2channels = map[string]map[string][]int{
		"default": {
			"gpt-5": {1, 2, 3},
		},
	}
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Group: "default", Models: "gpt-5", Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight},
		2: {Id: 2, Group: "default", Models: "gpt-5", Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight},
		3: {Id: 3, Group: "default", Models: "gpt-5", Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight},
	}

	err := MarkChannelTemporarilyUnschedulable(1, time.Minute, ChannelTemporaryUnschedulable{
		UntilUnix:  time.Now().Add(time.Minute).Unix(),
		Reason:     "quota_or_disabled",
		StatusCode: http.StatusForbidden,
	})
	require.NoError(t, err)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-5", 0, map[int]struct{}{2: {}})
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 3, channel.Id)
}

func TestGetRandomSatisfiedChannelAllowsSingleTemporaryUnschedulableFallback(t *testing.T) {
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalGroupMap := group2model2channels
	originalChannelMap := channelsIDM
	common.MemoryCacheEnabled = true
	defer func() {
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		group2model2channels = originalGroupMap
		channelsIDM = originalChannelMap
		clearChannelTemporaryUnschedulableCacheForTest(1)
		resetChannelSchedulerRuntimeStateForTest()
	}()

	priority := int64(10)
	weight := uint(100)
	group2model2channels = map[string]map[string][]int{
		"default": {
			"gpt-5": {1},
		},
	}
	channelsIDM = map[int]*Channel{
		1: {Id: 1, Group: "default", Models: "gpt-5", Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight},
	}

	err := MarkChannelTemporarilyUnschedulable(1, time.Minute, ChannelTemporaryUnschedulable{
		UntilUnix:  time.Now().Add(time.Minute).Unix(),
		Reason:     "upstream_unavailable",
		StatusCode: http.StatusInternalServerError,
	})
	require.NoError(t, err)

	channel, err := GetRandomSatisfiedChannel("default", "gpt-5", 0)
	require.NoError(t, err)
	require.NotNil(t, channel)
	require.Equal(t, 1, channel.Id)

	channel, err = GetRandomSatisfiedChannel("default", "gpt-5", 0, map[int]struct{}{1: {}})
	require.Error(t, err)
	require.Nil(t, channel)
}

func TestRankChannelsForSchedulingPrefersHealthierCandidate(t *testing.T) {
	resetChannelSchedulerRuntimeStateForTest()
	defer resetChannelSchedulerRuntimeStateForTest()

	priority := int64(10)
	weight := uint(50)
	ch1 := &Channel{Id: 1, Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight, ResponseTime: 120}
	ch2 := &Channel{Id: 2, Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight, ResponseTime: 120}

	channelRuntimeStatsLock.Lock()
	channelRuntimeStatsMap[1] = &channelRuntimeStats{
		LatencyEWMA:    120,
		HasLatencyEWMA: true,
		ErrorEWMA:      0.05,
		HasErrorEWMA:   true,
	}
	channelRuntimeStatsMap[2] = &channelRuntimeStats{
		LatencyEWMA:    900,
		HasLatencyEWMA: true,
		ErrorEWMA:      0.8,
		HasErrorEWMA:   true,
		InFlight:       2,
	}
	channelRuntimeStatsLock.Unlock()

	ranked := rankChannelsForScheduling([]*Channel{ch2, ch1})
	require.Len(t, ranked, 2)
	require.Equal(t, 1, ranked[0].Channel.Id)
	require.Greater(t, ranked[0].Score, ranked[1].Score)
}
