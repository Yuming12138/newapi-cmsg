package model

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
)

const (
	channelTemporaryUnschedulableNamespace = "new-api:channel_scheduler:temp_unsched:v1"
	channelSchedulerTopK                   = 3
	channelSchedulerLatencyAlpha           = 0.25
	channelSchedulerErrorAlpha             = 0.35
	channelSchedulerDefaultLatencyMs       = 800.0
	channelSchedulerDefaultWeightBias      = 10.0
)

type ChannelTemporaryUnschedulable struct {
	UntilUnix  int64  `json:"until_unix"`
	Reason     string `json:"reason,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

type channelTemporaryUnschedulableCodec struct{}

func (channelTemporaryUnschedulableCodec) Encode(v ChannelTemporaryUnschedulable) (string, error) {
	raw, err := common.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func (channelTemporaryUnschedulableCodec) Decode(s string) (ChannelTemporaryUnschedulable, error) {
	var state ChannelTemporaryUnschedulable
	if err := common.UnmarshalJsonStr(s, &state); err != nil {
		return ChannelTemporaryUnschedulable{}, err
	}
	return state, nil
}

type channelRuntimeStats struct {
	InFlight        int
	LatencyEWMA     float64
	ErrorEWMA       float64
	HasLatencyEWMA  bool
	HasErrorEWMA    bool
	LastStatusCode  int
	LastFailureUnix int64
	LastSuccessUnix int64
	SuccessCount    int64
	FailureCount    int64
}

type ChannelRuntimeStatsSnapshot struct {
	ChannelID                 int                            `json:"channel_id"`
	InFlight                  int                            `json:"in_flight"`
	LatencyEWMAMs             float64                        `json:"latency_ewma_ms"`
	ErrorEWMA                 float64                        `json:"error_ewma"`
	HasLatencyEWMA            bool                           `json:"has_latency_ewma"`
	HasErrorEWMA              bool                           `json:"has_error_ewma"`
	LastStatusCode            int                            `json:"last_status_code"`
	LastFailureUnix           int64                          `json:"last_failure_unix"`
	LastSuccessUnix           int64                          `json:"last_success_unix"`
	SuccessCount              int64                          `json:"success_count"`
	FailureCount              int64                          `json:"failure_count"`
	AttemptCount              int64                          `json:"attempt_count"`
	FailureRate               float64                        `json:"failure_rate"`
	Score                     float64                        `json:"score,omitempty"`
	TemporaryUnschedulable    *ChannelTemporaryUnschedulable `json:"temporary_unschedulable,omitempty"`
	TemporaryUnschedulableNow bool                           `json:"temporary_unschedulable_now"`
}

type scoredChannelCandidate struct {
	Channel *Channel
	Score   float64
}

var (
	channelTemporaryUnschedulableCacheOnce sync.Once
	channelTemporaryUnschedulableCache     *cachex.HybridCache[ChannelTemporaryUnschedulable]

	channelRuntimeStatsLock sync.RWMutex
	channelRuntimeStatsMap  = make(map[int]*channelRuntimeStats)
)

func getChannelTemporaryUnschedulableCache() *cachex.HybridCache[ChannelTemporaryUnschedulable] {
	channelTemporaryUnschedulableCacheOnce.Do(func() {
		channelTemporaryUnschedulableCache = cachex.NewHybridCache[ChannelTemporaryUnschedulable](cachex.HybridCacheConfig[ChannelTemporaryUnschedulable]{
			Namespace:  cachex.Namespace(channelTemporaryUnschedulableNamespace),
			Redis:      common.RDB,
			RedisCodec: channelTemporaryUnschedulableCodec{},
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			Memory: func() *hot.HotCache[string, ChannelTemporaryUnschedulable] {
				return hot.NewHotCache[string, ChannelTemporaryUnschedulable](hot.LRU, 50_000).
					WithTTL(10 * time.Minute).
					WithJanitor().
					Build()
			},
		})
	})
	return channelTemporaryUnschedulableCache
}

func channelTemporaryUnschedulableKey(channelID int) string {
	if channelID <= 0 {
		return ""
	}
	return strconv.Itoa(channelID)
}

func MarkChannelTemporarilyUnschedulable(channelID int, ttl time.Duration, state ChannelTemporaryUnschedulable) error {
	if channelID <= 0 {
		return nil
	}
	if ttl <= 0 {
		return nil
	}
	if state.UntilUnix <= 0 {
		state.UntilUnix = time.Now().Add(ttl).Unix()
	}
	return getChannelTemporaryUnschedulableCache().SetWithTTL(channelTemporaryUnschedulableKey(channelID), state, ttl)
}

func ClearChannelTemporarilyUnschedulable(channelID int) {
	if channelID <= 0 {
		return
	}
	_, _ = getChannelTemporaryUnschedulableCache().DeleteMany([]string{channelTemporaryUnschedulableKey(channelID)})
}

func GetChannelTemporaryUnschedulable(channelID int) (ChannelTemporaryUnschedulable, bool, error) {
	if channelID <= 0 {
		return ChannelTemporaryUnschedulable{}, false, nil
	}
	state, found, err := getChannelTemporaryUnschedulableCache().Get(channelTemporaryUnschedulableKey(channelID))
	if err != nil || !found {
		return state, found, err
	}
	if state.UntilUnix > 0 && time.Now().Unix() > state.UntilUnix {
		ClearChannelTemporarilyUnschedulable(channelID)
		return ChannelTemporaryUnschedulable{}, false, nil
	}
	return state, true, nil
}

func IsChannelTemporarilyUnschedulable(channelID int) (bool, *ChannelTemporaryUnschedulable) {
	state, found, err := GetChannelTemporaryUnschedulable(channelID)
	if err != nil || !found {
		return false, nil
	}
	return true, &state
}

func BeginChannelAttempt(channelID int) {
	if channelID <= 0 {
		return
	}
	channelRuntimeStatsLock.Lock()
	defer channelRuntimeStatsLock.Unlock()

	stats := getOrCreateChannelRuntimeStatsLocked(channelID)
	stats.InFlight++
}

func FinishChannelAttempt(channelID int, success bool, latency time.Duration, statusCode int) {
	if channelID <= 0 {
		return
	}
	channelRuntimeStatsLock.Lock()
	defer channelRuntimeStatsLock.Unlock()

	stats := getOrCreateChannelRuntimeStatsLocked(channelID)
	if stats.InFlight > 0 {
		stats.InFlight--
	}

	if latency > 0 {
		stats.LatencyEWMA = nextEWMA(stats.LatencyEWMA, latency.Seconds()*1000, channelSchedulerLatencyAlpha, stats.HasLatencyEWMA)
		stats.HasLatencyEWMA = true
	}

	if success {
		stats.ErrorEWMA = nextEWMA(stats.ErrorEWMA, 0, channelSchedulerErrorAlpha, stats.HasErrorEWMA)
		stats.HasErrorEWMA = true
		stats.LastStatusCode = 0
		stats.LastSuccessUnix = time.Now().Unix()
		stats.SuccessCount++
		return
	}

	stats.ErrorEWMA = nextEWMA(stats.ErrorEWMA, 1, channelSchedulerErrorAlpha, stats.HasErrorEWMA)
	stats.HasErrorEWMA = true
	stats.LastStatusCode = statusCode
	stats.LastFailureUnix = time.Now().Unix()
	stats.FailureCount++
}

func GetChannelRuntimeStatsSnapshot(channel *Channel) ChannelRuntimeStatsSnapshot {
	channelID := 0
	if channel != nil {
		channelID = channel.Id
	}
	stats := snapshotChannelRuntimeStats(channelID)
	snapshot := buildChannelRuntimeStatsSnapshot(channelID, stats)
	if channel != nil {
		score := channelSelectionScore(channel)
		if !math.IsNaN(score) && !math.IsInf(score, 0) && score > 0 {
			snapshot.Score = score
		}
	}
	if blocked, state := IsChannelTemporarilyUnschedulable(channelID); blocked && state != nil {
		snapshot.TemporaryUnschedulable = state
		snapshot.TemporaryUnschedulableNow = true
	}
	return snapshot
}

func GetChannelRuntimeStatsSnapshots(channels []*Channel, includeIdle bool) []ChannelRuntimeStatsSnapshot {
	if len(channels) == 0 {
		return nil
	}
	snapshots := make([]ChannelRuntimeStatsSnapshot, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		snapshot := GetChannelRuntimeStatsSnapshot(channel)
		if !includeIdle && snapshot.AttemptCount == 0 && snapshot.InFlight == 0 && !snapshot.TemporaryUnschedulableNow {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func channelSelectionScore(channel *Channel) float64 {
	if channel == nil {
		return 0
	}

	stats := snapshotChannelRuntimeStats(channel.Id)

	weightFactor := float64(channel.GetWeight()) + channelSchedulerDefaultWeightBias
	if weightFactor <= 0 {
		weightFactor = channelSchedulerDefaultWeightBias
	}

	latencyMs := channelSchedulerDefaultLatencyMs
	if stats.HasLatencyEWMA && stats.LatencyEWMA > 0 {
		latencyMs = stats.LatencyEWMA
	} else if channel.ResponseTime > 0 {
		latencyMs = float64(channel.ResponseTime)
	}
	latencyFactor := 1.0 / (1.0 + latencyMs/1500.0)
	if latencyFactor < 0.2 {
		latencyFactor = 0.2
	}

	errorFactor := 1.0
	if stats.HasErrorEWMA {
		errorFactor = 1 - math.Min(stats.ErrorEWMA, 0.95)
	}
	if errorFactor < 0.05 {
		errorFactor = 0.05
	}

	inFlightFactor := 1.0 / (1.0 + float64(stats.InFlight)*0.7)
	if inFlightFactor < 0.1 {
		inFlightFactor = 0.1
	}

	return weightFactor * latencyFactor * errorFactor * inFlightFactor
}

func rankChannelsForScheduling(channels []*Channel) []scoredChannelCandidate {
	if len(channels) == 0 {
		return nil
	}
	scored := make([]scoredChannelCandidate, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		score := channelSelectionScore(channel)
		if math.IsNaN(score) || math.IsInf(score, 0) || score <= 0 {
			score = 1
		}
		scored = append(scored, scoredChannelCandidate{
			Channel: channel,
			Score:   score,
		})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Channel.Id < scored[j].Channel.Id
		}
		return scored[i].Score > scored[j].Score
	})
	return scored
}

func selectChannelByRuntimeScore(channels []*Channel) *Channel {
	scored := rankChannelsForScheduling(channels)
	if len(scored) == 0 {
		return nil
	}

	topK := channelSchedulerTopK
	if topK <= 0 || topK > len(scored) {
		topK = len(scored)
	}

	totalScore := 0.0
	for i := 0; i < topK; i++ {
		totalScore += scored[i].Score
	}
	if totalScore <= 0 {
		return scored[0].Channel
	}

	randomScore := rand.Float64() * totalScore
	for i := 0; i < topK; i++ {
		randomScore -= scored[i].Score
		if randomScore <= 0 {
			return scored[i].Channel
		}
	}
	return scored[0].Channel
}

func filterChannelsForScheduling(channels []*Channel, excluded map[int]struct{}) []*Channel {
	if len(channels) == 0 {
		return nil
	}
	filtered := make([]*Channel, 0, len(channels))
	temporarilyBlocked := make([]*Channel, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if _, blocked := excluded[channel.Id]; blocked {
			continue
		}
		if blocked, _ := IsChannelTemporarilyUnschedulable(channel.Id); blocked {
			temporarilyBlocked = append(temporarilyBlocked, channel)
			continue
		}
		filtered = append(filtered, channel)
	}
	if len(filtered) == 0 && len(temporarilyBlocked) == 1 {
		return temporarilyBlocked
	}
	return filtered
}

func normalizeExcludedChannelIDs(maps []map[int]struct{}) map[int]struct{} {
	if len(maps) == 0 || maps[0] == nil {
		return nil
	}
	return maps[0]
}

func getOrCreateChannelRuntimeStatsLocked(channelID int) *channelRuntimeStats {
	stats, ok := channelRuntimeStatsMap[channelID]
	if ok {
		return stats
	}
	stats = &channelRuntimeStats{}
	channelRuntimeStatsMap[channelID] = stats
	return stats
}

func snapshotChannelRuntimeStats(channelID int) channelRuntimeStats {
	channelRuntimeStatsLock.RLock()
	defer channelRuntimeStatsLock.RUnlock()
	if stats, ok := channelRuntimeStatsMap[channelID]; ok && stats != nil {
		return *stats
	}
	return channelRuntimeStats{}
}

func buildChannelRuntimeStatsSnapshot(channelID int, stats channelRuntimeStats) ChannelRuntimeStatsSnapshot {
	attempts := stats.SuccessCount + stats.FailureCount
	failureRate := 0.0
	if attempts > 0 {
		failureRate = float64(stats.FailureCount) / float64(attempts)
	}
	return ChannelRuntimeStatsSnapshot{
		ChannelID:       channelID,
		InFlight:        stats.InFlight,
		LatencyEWMAMs:   stats.LatencyEWMA,
		ErrorEWMA:       stats.ErrorEWMA,
		HasLatencyEWMA:  stats.HasLatencyEWMA,
		HasErrorEWMA:    stats.HasErrorEWMA,
		LastStatusCode:  stats.LastStatusCode,
		LastFailureUnix: stats.LastFailureUnix,
		LastSuccessUnix: stats.LastSuccessUnix,
		SuccessCount:    stats.SuccessCount,
		FailureCount:    stats.FailureCount,
		AttemptCount:    attempts,
		FailureRate:     failureRate,
	}
}

func nextEWMA(current float64, sample float64, alpha float64, initialized bool) float64 {
	if !initialized {
		return sample
	}
	if alpha <= 0 || alpha >= 1 {
		alpha = 0.5
	}
	return alpha*sample + (1-alpha)*current
}

func resetChannelSchedulerRuntimeStateForTest() {
	channelRuntimeStatsLock.Lock()
	defer channelRuntimeStatsLock.Unlock()
	channelRuntimeStatsMap = make(map[int]*channelRuntimeStats)
}

func clearChannelTemporaryUnschedulableCacheForTest(channelIDs ...int) {
	if len(channelIDs) == 0 {
		return
	}
	keys := make([]string, 0, len(channelIDs))
	for _, channelID := range channelIDs {
		if key := channelTemporaryUnschedulableKey(channelID); key != "" {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return
	}
	if _, err := getChannelTemporaryUnschedulableCache().DeleteMany(keys); err != nil {
		common.SysError(fmt.Sprintf("failed to clear temporary unschedulable cache in test: %v", err))
	}
}
