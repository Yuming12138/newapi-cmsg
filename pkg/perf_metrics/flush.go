package perfmetrics

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/perf_metrics_setting"
)

func flushLoop() {
	for {
		interval := perf_metrics_setting.GetFlushIntervalMinutes()
		time.Sleep(time.Duration(interval) * time.Minute)
		setting := perf_metrics_setting.GetSetting()
		if !setting.Enabled {
			continue
		}
		flushCompletedBuckets()
		cleanupExpiredMetrics(setting.RetentionDays)
	}
}

// consecutiveFlushFailures guards against the retry storm that pinned a core
// on 2026-10-18: when the metrics table or its unique index does not match the
// binary (schema drift, failed migration), every flush failed, the drained
// counters were added back, and the next cycle retried forever. Metrics are a
// side feature: after a handful of consecutive failures the circuit opens and
// buckets are dropped instead of retried, so the relay path is never the thing
// that suffers.
const maxConsecutiveFlushFailures = 5

var consecutiveFlushFailures atomic.Int32

func flushCompletedBuckets() {
	currentBucket := bucketStart(time.Now().Unix())
	hotBuckets.Range(func(key, value any) bool {
		k := key.(bucketKey)
		if k.bucketTs >= currentBucket {
			return true
		}

		bucket := value.(*atomicBucket)
		drained := bucket.drain()
		if drained.requestCount == 0 {
			deleteOldEmptyBucket(k, key)
			return true
		}

		err := model.UpsertPerfMetricUpstream(&model.PerfMetricUpstream{
			ModelName:      k.model,
			UpstreamModel:  k.upstreamModel,
			Group:          k.group,
			BucketTs:       k.bucketTs,
			RequestCount:   drained.requestCount,
			SuccessCount:   drained.successCount,
			TotalLatencyMs: drained.totalLatencyMs,
			TtftSumMs:      drained.ttftSumMs,
			TtftCount:      drained.ttftCount,
			OutputTokens:   drained.outputTokens,
			GenerationMs:   drained.generationMs,
		})
		if err != nil {
			failures := consecutiveFlushFailures.Add(1)
			if failures >= maxConsecutiveFlushFailures {
				// Circuit open: drop the drained counters and forget the bucket
				// instead of retrying every cycle.
				hotBuckets.Delete(key)
				common.SysError(fmt.Sprintf("perf metric flush circuit open after %d consecutive failures (model=%s group=%s bucket=%d): %s; buckets are dropped until restart", failures, k.model, k.group, k.bucketTs, err.Error()))
				return true
			}
			bucket.addCounters(drained)
			common.SysError(fmt.Sprintf("failed to flush perf metric bucket model=%s group=%s bucket=%d (attempt %d/%d): %s", k.model, k.group, k.bucketTs, failures, maxConsecutiveFlushFailures, err.Error()))
			return true
		}
		consecutiveFlushFailures.Store(0)

		deleteOldEmptyBucket(k, key)
		return true
	})
}

func deleteOldEmptyBucket(k bucketKey, rawKey any) {
	if k.bucketTs < bucketStart(time.Now().Add(-24*time.Hour).Unix()) {
		hotBuckets.Delete(rawKey)
	}
}

func cleanupExpiredMetrics(retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour).Unix()
	if err := model.DeletePerfMetricsBefore(cutoff); err != nil {
		common.SysError("failed to cleanup expired perf metrics: " + err.Error())
	}
}

func redisCounters(values map[string]string) counters {
	return counters{
		requestCount:   parseRedisInt(values["req"]),
		successCount:   parseRedisInt(values["ok"]),
		totalLatencyMs: parseRedisInt(values["lat"]),
		ttftSumMs:      parseRedisInt(values["ttft"]),
		ttftCount:      parseRedisInt(values["ttft_n"]),
		outputTokens:   parseRedisInt(values["out"]),
		generationMs:   parseRedisInt(values["gen_ms"]),
	}
}

func parseRedisInt(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}
