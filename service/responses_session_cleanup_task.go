package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	responsesSessionCleanupTickInterval = 30 * time.Minute
	responsesSessionCleanupBatchSize    = 100
	responsesSessionCleanupMaxBatches   = 5
	responsesSessionDefaultRetention    = 7 * 24 * 60 * 60
)

var (
	responsesSessionCleanupOnce    sync.Once
	responsesSessionCleanupRunning atomic.Bool
)

func StartResponsesChatSessionCleanupTask() {
	responsesSessionCleanupOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			ticker := time.NewTicker(responsesSessionCleanupTickInterval)
			defer ticker.Stop()
			runResponsesChatSessionCleanupOnce()
			for range ticker.C {
				runResponsesChatSessionCleanupOnce()
			}
		})
	})
}

func runResponsesChatSessionCleanupOnce() {
	if !responsesSessionCleanupRunning.CompareAndSwap(false, true) {
		return
	}
	defer responsesSessionCleanupRunning.Store(false)

	retentionSeconds := int64(common.GetEnvOrDefault("RESPONSES_SESSION_RETENTION_SECONDS", responsesSessionDefaultRetention))
	if retentionSeconds <= 0 {
		return
	}
	var deleted int64
	for batch := 0; batch < responsesSessionCleanupMaxBatches; batch++ {
		n, err := model.CleanupResponsesChatSessions(retentionSeconds, responsesSessionCleanupBatchSize)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("responses session cleanup failed: %v", err))
			return
		}
		deleted += n
		if n < responsesSessionCleanupBatchSize {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if deleted > 0 {
		logger.LogInfo(context.Background(), fmt.Sprintf("responses session cleanup removed %d expired rows", deleted))
	}
	stats := model.ResponsesChatStorageStatsSnapshot()
	if stats.ShadowChecks > 0 || stats.IncrementalReads > 0 || stats.LegacyFallbacks > 0 ||
		stats.BlobErrors > 0 || stats.ShadowReadErrors > 0 || stats.ShadowWriteErrors > 0 ||
		stats.RebuildWaits > 0 {
		cacheTotal := stats.CacheHits + stats.CacheMisses
		cacheHitRate := uint64(0)
		if cacheTotal > 0 {
			cacheHitRate = stats.CacheHits * 100 / cacheTotal
		}
		logger.LogInfo(context.Background(), fmt.Sprintf(
			"responses session storage mode=%s shadow=%d/%d cache_hit_rate=%d%% incremental_reads=%d legacy_fallbacks=%d blob_errors=%d shadow_read_errors=%d shadow_write_errors=%d rebuild_active=%d/%d rebuild_waits=%d",
			stats.Mode,
			stats.ShadowMatches,
			stats.ShadowChecks,
			cacheHitRate,
			stats.IncrementalReads,
			stats.LegacyFallbacks,
			stats.BlobErrors,
			stats.ShadowReadErrors,
			stats.ShadowWriteErrors,
			stats.RebuildActive,
			stats.RebuildLimit,
			stats.RebuildWaits,
		))
	}
}
