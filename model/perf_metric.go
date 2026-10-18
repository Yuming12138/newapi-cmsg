package model

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PerfMetric stores aggregated relay performance metrics keyed by the client
// model name. Kept for rollback compatibility: the current build writes
// PerfMetricUpstream instead and never touches this table's indexes.
type PerfMetric struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	ModelName      string `json:"model_name" gorm:"size:128;uniqueIndex:idx_perf_model_group_bucket,priority:1"`
	Group          string `json:"group" gorm:"column:group;size:64;uniqueIndex:idx_perf_model_group_bucket,priority:2"`
	BucketTs       int64  `json:"bucket_ts" gorm:"uniqueIndex:idx_perf_model_group_bucket,priority:3;index:idx_perf_bucket_ts"`
	RequestCount   int64  `json:"-" gorm:"default:0"`
	SuccessCount   int64  `json:"-" gorm:"default:0"`
	TotalLatencyMs int64  `json:"-" gorm:"default:0"`
	TtftSumMs      int64  `json:"-" gorm:"default:0"`
	TtftCount      int64  `json:"-" gorm:"default:0"`
	OutputTokens   int64  `json:"-" gorm:"default:0"`
	GenerationMs   int64  `json:"-" gorm:"default:0"`
}

func (PerfMetric) TableName() string {
	return "perf_metrics"
}

// PerfMetricUpstream stores aggregated relay performance metrics keyed by the
// model actually called after channel model mapping (e.g. gpt-6.1-sol ->
// deepseek-flash). It lives in its own table so the legacy perf_metrics table
// and its unique index stay untouched: builds before and after this change can
// roll back and forth without index conflicts.
type PerfMetricUpstream struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	ModelName      string `json:"model_name" gorm:"size:128;uniqueIndex:idx_perf_upstream_model_group_bucket,priority:1"`
	UpstreamModel  string `json:"upstream_model" gorm:"size:128;uniqueIndex:idx_perf_upstream_model_group_bucket,priority:2"`
	Group          string `json:"group" gorm:"column:group;size:64;uniqueIndex:idx_perf_upstream_model_group_bucket,priority:3"`
	BucketTs       int64  `json:"bucket_ts" gorm:"uniqueIndex:idx_perf_upstream_model_group_bucket,priority:4;index:idx_perf_upstream_bucket_ts"`
	RequestCount   int64  `json:"-" gorm:"default:0"`
	SuccessCount   int64  `json:"-" gorm:"default:0"`
	TotalLatencyMs int64  `json:"-" gorm:"default:0"`
	TtftSumMs      int64  `json:"-" gorm:"default:0"`
	TtftCount      int64  `json:"-" gorm:"default:0"`
	OutputTokens   int64  `json:"-" gorm:"default:0"`
	GenerationMs   int64  `json:"-" gorm:"default:0"`
}

func (PerfMetricUpstream) TableName() string {
	return "perf_metrics_upstream"
}

func perfMetricIncrementExpr(column string, delta int64) clause.Expr {
	return gorm.Expr("? + ?", clause.Column{Table: (PerfMetricUpstream{}).TableName(), Name: column}, delta)
}

func UpsertPerfMetricUpstream(metric *PerfMetricUpstream) error {
	if metric == nil || metric.RequestCount == 0 {
		return nil
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "model_name"},
			{Name: "upstream_model"},
			{Name: "group"},
			{Name: "bucket_ts"},
		},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"request_count":    perfMetricIncrementExpr("request_count", metric.RequestCount),
			"success_count":    perfMetricIncrementExpr("success_count", metric.SuccessCount),
			"total_latency_ms": perfMetricIncrementExpr("total_latency_ms", metric.TotalLatencyMs),
			"ttft_sum_ms":      perfMetricIncrementExpr("ttft_sum_ms", metric.TtftSumMs),
			"ttft_count":       perfMetricIncrementExpr("ttft_count", metric.TtftCount),
			"output_tokens":    perfMetricIncrementExpr("output_tokens", metric.OutputTokens),
			"generation_ms":    perfMetricIncrementExpr("generation_ms", metric.GenerationMs),
		}),
	}).Create(metric).Error
}

func GetPerfMetricsUpstream(modelName string, group string, startTs int64, endTs int64) ([]PerfMetricUpstream, error) {
	var metrics []PerfMetricUpstream
	query := DB.Model(&PerfMetricUpstream{}).
		Where("(model_name = ? OR upstream_model = ?) AND bucket_ts >= ? AND bucket_ts <= ?", modelName, modelName, startTs, endTs)
	if group != "" {
		query = query.Where(commonGroupCol+" = ?", group)
	}
	err := query.Order("bucket_ts ASC").Find(&metrics).Error
	return metrics, err
}

type PerfMetricUpstreamSummary struct {
	UpstreamModel string `json:"upstream_model"`
	RequestCount  int64  `json:"request_count"`
	SuccessCount  int64  `json:"success_count"`
	TotalLatencyMs int64 `json:"total_latency_ms"`
	OutputTokens  int64  `json:"output_tokens"`
	GenerationMs  int64  `json:"generation_ms"`
}

func GetPerfMetricsUpstreamSummaryAll(startTs int64, endTs int64) ([]PerfMetricUpstreamSummary, error) {
	var summaries []PerfMetricUpstreamSummary
	err := DB.Model(&PerfMetricUpstream{}).
		Select("COALESCE(NULLIF(upstream_model, ''), model_name) as upstream_model, SUM(request_count) as request_count, SUM(success_count) as success_count, SUM(total_latency_ms) as total_latency_ms, SUM(output_tokens) as output_tokens, SUM(generation_ms) as generation_ms").
		Where("bucket_ts >= ? AND bucket_ts <= ?", startTs, endTs).
		Group("COALESCE(NULLIF(upstream_model, ''), model_name)").
		Having("SUM(request_count) > 0").
		Find(&summaries).Error
	return summaries, err
}

func DeletePerfMetricsBefore(cutoffTs int64) error {
	if cutoffTs <= 0 {
		return nil
	}
	if err := DB.Where("bucket_ts < ?", cutoffTs).Delete(&PerfMetric{}).Error; err != nil {
		return err
	}
	// The upstream table is the live one; the legacy table is only written by
	// older builds, so both are swept to avoid orphaned rows.
	return DB.Where("bucket_ts < ?", cutoffTs).Delete(&PerfMetricUpstream{}).Error
}

func PerfMetricStartTime(hours int) int64 {
	if hours <= 0 {
		hours = 24
	}
	return time.Now().Add(-time.Duration(hours) * time.Hour).Unix()
}
