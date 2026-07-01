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

func TestChannelRuntimeStatsSnapshotTracksPassiveAttempts(t *testing.T) {
	resetChannelSchedulerRuntimeStateForTest()
	defer resetChannelSchedulerRuntimeStateForTest()

	priority := int64(10)
	weight := uint(50)
	channel := &Channel{Id: 11, Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight}

	BeginChannelAttempt(channel.Id)
	FinishChannelAttempt(channel.Id, true, 200*time.Millisecond, 0)
	BeginChannelAttempt(channel.Id)
	FinishChannelAttempt(channel.Id, false, 900*time.Millisecond, http.StatusBadGateway)

	snapshot := GetChannelRuntimeStatsSnapshot(channel)
	require.Equal(t, 11, snapshot.ChannelID)
	require.Equal(t, int64(1), snapshot.SuccessCount)
	require.Equal(t, int64(1), snapshot.FailureCount)
	require.Equal(t, int64(2), snapshot.AttemptCount)
	require.Equal(t, http.StatusBadGateway, snapshot.LastStatusCode)
	require.True(t, snapshot.HasLatencyEWMA)
	require.True(t, snapshot.HasErrorEWMA)
	require.InDelta(t, 0.5, snapshot.FailureRate, 0.001)
	require.Greater(t, snapshot.LatencyEWMAMs, 0.0)
	require.Greater(t, snapshot.Score, 0.0)
}

func TestChannelRuntimeStatsSnapshotsFiltersIdleButKeepsTemporaryUnschedulable(t *testing.T) {
	resetChannelSchedulerRuntimeStateForTest()
	defer func() {
		clearChannelTemporaryUnschedulableCacheForTest(21, 22)
		resetChannelSchedulerRuntimeStateForTest()
	}()

	priority := int64(10)
	weight := uint(50)
	active := &Channel{Id: 21, Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight}
	blocked := &Channel{Id: 22, Status: common.ChannelStatusEnabled, Priority: &priority, Weight: &weight}

	BeginChannelAttempt(active.Id)
	FinishChannelAttempt(active.Id, true, 100*time.Millisecond, 0)
	require.NoError(t, MarkChannelTemporarilyUnschedulable(blocked.Id, time.Minute, ChannelTemporaryUnschedulable{
		UntilUnix:  time.Now().Add(time.Minute).Unix(),
		Reason:     "rate_limit",
		StatusCode: http.StatusTooManyRequests,
	}))

	snapshots := GetChannelRuntimeStatsSnapshots([]*Channel{active, blocked}, false)
	require.Len(t, snapshots, 2)
	require.Equal(t, 21, snapshots[0].ChannelID)
	require.Equal(t, 22, snapshots[1].ChannelID)
	require.True(t, snapshots[1].TemporaryUnschedulableNow)
	require.Equal(t, "rate_limit", snapshots[1].TemporaryUnschedulable.Reason)
}
