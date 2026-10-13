package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"golang.org/x/sync/singleflight"
)

const (
	codexRadarPublicEndpoint   = "https://codexradar.com/api/radar-bench-score"
	codexRadarSourceURL        = "https://codexradar.com/"
	codexRadarScoreVersion     = "radar-bench-v2-150"
	codexRadarCacheTTL         = 10 * time.Minute
	codexRadarStaleTTL         = 24 * time.Hour
	codexRadarRetryDelay       = time.Minute
	codexRadarHTTPTimeout      = 15 * time.Second
	codexRadarRefreshTimeout   = 60 * time.Second
	codexRadarConcurrency      = 4
	codexRadarPointAttempts    = 2
	codexRadarPointRetryWait   = 500 * time.Millisecond
	codexRadarStoreTimeout     = 2 * time.Second
	codexRadarSnapshotKey      = "cmsg:codex-radar:radar-bench-v2-150:four-models:v1"
	codexRadarMaxSnapshotBytes = 128 << 10
	// Summaries replace the old multi-megabyte dataset. Limit both the wire
	// and decoded body to protect this low-resource gateway.
	codexRadarMaxBodyBytes = 1 << 20
)

var codexRadarModels = []struct{ Model, Label string }{
	{"gpt-6-astra", "GPT-6 Astra"},
	{"gpt-6.1-sol", "GPT-6.1 Sol"},
	{"gpt-6-sol", "GPT-6 Sol"},
	{"gpt-5.6-sol", "GPT-5.6 Sol"},
}
var codexRadarEfforts = []string{"ultra", "max", "xhigh", "high", "medium", "low"}

type CodexRadarMetric struct {
	Key                string   `json:"key"`
	Label              string   `json:"label"`
	Model              string   `json:"model"`
	Family             string   `json:"family"`
	ReasoningEffort    string   `json:"reasoning_effort"`
	Score              *float64 `json:"score"`
	ScoreMax           float64  `json:"score_max"`
	Status             string   `json:"status"`
	Coverage           int      `json:"coverage"`
	Tasks              int      `json:"tasks"`
	GradedRunsCount    int      `json:"graded_runs_count"`
	InsufficientData   bool     `json:"insufficient_data"`
	AverageCostUSD     *float64 `json:"average_cost_usd"`
	AverageTaskSeconds *float64 `json:"average_task_seconds"`
}

type CodexRadarOverview struct {
	SchemaVersion string             `json:"schema_version"`
	Benchmark     string             `json:"benchmark"`
	UpdatedAt     string             `json:"updated_at"`
	FetchedAt     string             `json:"fetched_at"`
	SourceURL     string             `json:"source_url"`
	Attribution   string             `json:"attribution"`
	Stale         bool               `json:"stale"`
	Metrics       []CodexRadarMetric `json:"metrics"`
}

// Accept integer-valued JSON numbers (59 or 59.0), never silently truncate.
type codexRadarCount int

func (count *codexRadarCount) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	value, err := strconv.ParseFloat(raw, 64)
	maxInt := int64(^uint(0) >> 1)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || value < 0 || math.Trunc(value) != value || value >= float64(maxInt) {
		return fmt.Errorf("codex radar count must be a non-negative integer: %q", raw)
	}
	*count = codexRadarCount(int(value))
	return nil
}

type codexRadarStatistic struct {
	Mean         *float64        `json:"mean"`
	KnownSamples codexRadarCount `json:"known_samples"`
}
type codexRadarSourcePoint struct {
	Schema          string          `json:"schema"`
	Benchmark       string          `json:"benchmark"`
	ScoreVersion    string          `json:"score_version"`
	TaskSetSHA256   string          `json:"task_set_sha256"`
	Model           string          `json:"model"`
	Effort          string          `json:"effort"`
	Score           *float64        `json:"score"`
	ScoreMax        float64         `json:"score_max"`
	Coverage        codexRadarCount `json:"coverage"`
	RequiredTasks   codexRadarCount `json:"required_tasks"`
	ScoreStatus     string          `json:"score_status"`
	GradedRunsCount codexRadarCount `json:"graded_runs_count"`
	Statistics      struct {
		Metrics struct {
			ElapsedMS        codexRadarStatistic `json:"elapsed_ms"`
			APIEquivalentUSD codexRadarStatistic `json:"api_equivalent_usd"`
		} `json:"metrics"`
	} `json:"statistics"`
	History struct {
		UpdatedBucket string `json:"updated_bucket"`
	} `json:"history"`
}
type codexRadarCacheEntry struct {
	Overview CodexRadarOverview
	Fetched  time.Time
}

// Persist the validated source points, not presentation fields. On restore we
// rebuild the overview and check the same complete-catalog/benchmark contract.
type codexRadarSnapshot struct {
	Fetched time.Time               `json:"fetched_at"`
	Points  []codexRadarSourcePoint `json:"points"`
}
type codexRadarSnapshotStore interface {
	Load(context.Context) ([]byte, error)
	Save(context.Context, []byte, time.Duration) error
}
type codexRadarRedisStore struct{}

func (codexRadarRedisStore) Load(ctx context.Context) ([]byte, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return nil, nil
	}
	data, err := common.RDB.Get(ctx, codexRadarSnapshotKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	return data, err
}
func (codexRadarRedisStore) Save(ctx context.Context, data []byte, ttl time.Duration) error {
	if !common.RedisEnabled || common.RDB == nil {
		return nil
	}
	return common.RDB.Set(ctx, codexRadarSnapshotKey, data, ttl).Err()
}

type codexRadarProvider struct {
	endpoint           string
	client             *http.Client
	cacheTTL, staleTTL time.Duration
	now                func() time.Time
	mu                 sync.RWMutex
	cache              *codexRadarCacheEntry
	retryAfter         time.Time
	lastError          error
	group              singleflight.Group
	store              codexRadarSnapshotStore
	storeLoaded        bool // Accessed only by the serialized singleflight refresh.
	pointRetryWait     time.Duration
}

func newCodexRadarProvider(endpoint string, client *http.Client) *codexRadarProvider {
	return &codexRadarProvider{endpoint: endpoint, client: client, cacheTTL: codexRadarCacheTTL, staleTTL: codexRadarStaleTTL, now: time.Now, pointRetryWait: codexRadarPointRetryWait}
}

var defaultCodexRadarProvider = func() *codexRadarProvider {
	provider := newCodexRadarProvider(codexRadarPublicEndpoint, &http.Client{Timeout: codexRadarHTTPTimeout})
	provider.store = codexRadarRedisStore{}
	return provider
}()

func GetCodexRadarOverview(ctx context.Context) (CodexRadarOverview, error) {
	return defaultCodexRadarProvider.Get(ctx)
}

func (provider *codexRadarProvider) Get(ctx context.Context) (CodexRadarOverview, error) {
	if err := ctx.Err(); err != nil {
		return CodexRadarOverview{}, err
	}
	if overview, ok := provider.cachedOverview(provider.now(), false); ok {
		return overview, nil
	}
	result := provider.group.DoChan("overview", func() (interface{}, error) {
		now := provider.now()
		if !provider.storeLoaded {
			provider.storeLoaded = true
			provider.restoreSnapshot(context.WithoutCancel(ctx), now)
		}
		if overview, ok := provider.cachedOverview(now, false); ok {
			return overview, nil
		}
		provider.mu.RLock()
		retryAfter, lastError := provider.retryAfter, provider.lastError
		provider.mu.RUnlock()
		if lastError != nil && now.Before(retryAfter) {
			if stale, ok := provider.cachedOverview(now, true); ok {
				return stale, nil
			}
			return CodexRadarOverview{}, lastError
		}
		// A closed dashboard tab cannot cancel the shared refresh for others.
		// Callers can leave immediately; the refresh has its own timeout.
		refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), codexRadarRefreshTimeout)
		defer cancel()
		overview, points, err := provider.fetch(refreshCtx)
		now = provider.now()
		if err != nil {
			provider.mu.Lock()
			provider.retryAfter, provider.lastError = now.Add(CodexRadarRetryDelay(err)), err
			provider.mu.Unlock()
			if stale, ok := provider.cachedOverview(now, true); ok {
				return stale, nil
			}
			return CodexRadarOverview{}, err
		}
		overview.FetchedAt = now.UTC().Format(time.RFC3339)
		provider.mu.Lock()
		provider.cache = &codexRadarCacheEntry{Overview: overview, Fetched: now}
		provider.retryAfter, provider.lastError = time.Time{}, nil
		provider.mu.Unlock()
		provider.saveSnapshot(context.WithoutCancel(ctx), codexRadarSnapshot{Fetched: now, Points: points})
		return overview, nil
	})
	select {
	case <-ctx.Done():
		return CodexRadarOverview{}, ctx.Err()
	case res := <-result:
		if res.Err != nil {
			return CodexRadarOverview{}, res.Err
		}
		overview, ok := res.Val.(CodexRadarOverview)
		if !ok {
			return CodexRadarOverview{}, fmt.Errorf("unexpected codex radar cache result")
		}
		return overview, nil
	}
}

func (provider *codexRadarProvider) restoreSnapshot(ctx context.Context, now time.Time) {
	if provider.store == nil {
		return
	}
	storeCtx, cancel := context.WithTimeout(ctx, codexRadarStoreTimeout)
	defer cancel()
	data, err := provider.store.Load(storeCtx)
	if err != nil {
		common.SysLog("Codex Radar snapshot restore failed; continuing with live collection")
		return
	}
	if len(data) == 0 {
		return
	}
	var snapshot codexRadarSnapshot
	if len(data) > codexRadarMaxSnapshotBytes || common.Unmarshal(data, &snapshot) != nil || snapshot.Fetched.IsZero() || snapshot.Fetched.After(now) || now.Sub(snapshot.Fetched) > provider.staleTTL {
		common.SysLog("Codex Radar ignored an invalid or expired snapshot")
		return
	}
	overview, err := buildCodexRadarOverview(snapshot.Points)
	if err != nil {
		common.SysLog("Codex Radar ignored a snapshot with an incompatible catalog")
		return
	}
	overview.FetchedAt = snapshot.Fetched.UTC().Format(time.RFC3339)
	provider.mu.Lock()
	provider.cache = &codexRadarCacheEntry{Overview: overview, Fetched: snapshot.Fetched}
	provider.mu.Unlock()
}

func (provider *codexRadarProvider) saveSnapshot(ctx context.Context, snapshot codexRadarSnapshot) {
	if provider.store == nil {
		return
	}
	data, err := common.Marshal(snapshot)
	if err != nil || len(data) > codexRadarMaxSnapshotBytes {
		common.SysLog("Codex Radar snapshot encoding failed; keeping the in-memory result")
		return
	}
	storeCtx, cancel := context.WithTimeout(ctx, codexRadarStoreTimeout)
	defer cancel()
	if err := provider.store.Save(storeCtx, data, provider.staleTTL); err != nil {
		common.SysLog("Codex Radar snapshot persistence failed; keeping the in-memory result")
	}
}
func (provider *codexRadarProvider) cachedOverview(now time.Time, allowStale bool) (CodexRadarOverview, bool) {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if provider.cache == nil {
		return CodexRadarOverview{}, false
	}
	age := now.Sub(provider.cache.Fetched)
	if age <= provider.cacheTTL || (allowStale && age <= provider.staleTTL) {
		overview := provider.cache.Overview
		overview.Stale = age > provider.cacheTTL
		return overview, true
	}
	return CodexRadarOverview{}, false
}

func (provider *codexRadarProvider) fetch(ctx context.Context) (CodexRadarOverview, []codexRadarSourcePoint, error) {
	total := len(codexRadarModels) * len(codexRadarEfforts)
	points := make([]codexRadarSourcePoint, total)
	errors := make([]error, total)
	jobs := make(chan int, total)
	for index := range total {
		jobs <- index
	}
	close(jobs)
	var workers sync.WaitGroup
	for range codexRadarConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if ctx.Err() != nil {
					errors[index] = ctx.Err()
					continue
				}
				model := codexRadarModels[index/len(codexRadarEfforts)].Model
				effort := codexRadarEfforts[index%len(codexRadarEfforts)]
				points[index], errors[index] = provider.fetchPoint(ctx, model, effort)
			}
		}()
	}
	workers.Wait()
	// Never replace a complete cached snapshot with a silently partial one.
	var failure error
	for index, err := range errors {
		if err != nil {
			// Preserve the longest upstream cooldown when more than one point
			// fails, even if an earlier point failed with a generic timeout.
			if failure == nil || CodexRadarRetryDelay(err) > CodexRadarRetryDelay(failure) {
				failure = fmt.Errorf("codex radar %s/%s: %w", codexRadarModels[index/len(codexRadarEfforts)].Model, codexRadarEfforts[index%len(codexRadarEfforts)], err)
			}
		}
	}
	if failure != nil {
		return CodexRadarOverview{}, nil, failure
	}
	overview, err := buildCodexRadarOverview(points)
	return overview, points, err
}

func (provider *codexRadarProvider) fetchPoint(ctx context.Context, model, effort string) (codexRadarSourcePoint, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return codexRadarSourcePoint{}, err
		}
		point, err := provider.fetchPointOnce(ctx, model, effort)
		if ctx.Err() != nil {
			return codexRadarSourcePoint{}, ctx.Err()
		}
		if err == nil || attempt+1 >= codexRadarPointAttempts || !retryableCodexRadarError(err) {
			return point, err
		}
		wait := provider.pointRetryWait
		var statusErr *codexRadarStatusError
		if errors.As(err, &statusErr) && statusErr.retryAfter > wait {
			// Do not violate a long upstream Retry-After or occupy workers for an
			// unbounded delay; the whole-round backoff will handle this instead.
			if statusErr.retryAfter > 5*time.Second {
				return point, err
			}
			wait = statusErr.retryAfter
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return codexRadarSourcePoint{}, ctx.Err()
		case <-timer.C:
		}
	}
}

type codexRadarStatusError struct {
	status     int
	retryAfter time.Duration
}

// CodexRadarRetryDelay also respects a longer upstream cooldown on a failed
// round. Wrapping the point error must not turn a 120s Retry-After into 60s.
func CodexRadarRetryDelay(err error) time.Duration {
	var statusErr *codexRadarStatusError
	if errors.As(err, &statusErr) && statusErr.retryAfter > codexRadarRetryDelay {
		return statusErr.retryAfter
	}
	return codexRadarRetryDelay
}

func (err *codexRadarStatusError) Error() string {
	return fmt.Sprintf("upstream returned status %d", err.status)
}

func retryableCodexRadarError(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var statusErr *codexRadarStatusError
	if errors.As(err, &statusErr) {
		return statusErr.status == http.StatusRequestTimeout || statusErr.status == http.StatusTooManyRequests || statusErr.status >= 500 && statusErr.status <= 599
	}
	var networkErr net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary())
}

func codexRadarRetryAfter(header string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(header, 10, 32); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if deadline, err := http.ParseTime(header); err == nil && deadline.After(now) {
		return deadline.Sub(now)
	}
	return 0
}

func (provider *codexRadarProvider) fetchPointOnce(ctx context.Context, model, effort string) (codexRadarSourcePoint, error) {
	endpoint, err := url.Parse(provider.endpoint)
	if err != nil {
		return codexRadarSourcePoint{}, err
	}
	query := endpoint.Query()
	query.Set("model", model)
	query.Set("effort", effort)
	query.Set("view", "summary")
	endpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return codexRadarSourcePoint{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "new-api-cmsg codex-radar overview")
	req.Header.Set("X-Radar-Bench-Selection", "user-selected-v1")
	// Let the transport negotiate and transparently decode gzip.
	resp, err := provider.client.Do(req)
	if err != nil {
		return codexRadarSourcePoint{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return codexRadarSourcePoint{}, &codexRadarStatusError{status: resp.StatusCode, retryAfter: codexRadarRetryAfter(resp.Header.Get("Retry-After"), provider.now())}
	}
	if resp.ContentLength > codexRadarMaxBodyBytes {
		return codexRadarSourcePoint{}, fmt.Errorf("codex radar response exceeds size limit")
	}
	body, err := readCodexRadarBody(resp.Body)
	if err != nil {
		return codexRadarSourcePoint{}, err
	}
	if len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return codexRadarSourcePoint{}, fmt.Errorf("decompress codex radar response: %w", err)
		}
		defer reader.Close()
		body, err = readCodexRadarBody(reader)
		if err != nil {
			return codexRadarSourcePoint{}, err
		}
	}
	var point codexRadarSourcePoint
	if err := common.Unmarshal(body, &point); err != nil {
		return codexRadarSourcePoint{}, fmt.Errorf("decode codex radar response: %w", err)
	}
	if point.Model != model || point.Effort != effort {
		return codexRadarSourcePoint{}, fmt.Errorf("upstream returned a different model/effort selection")
	}
	return point, nil
}

func readCodexRadarBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, codexRadarMaxBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > codexRadarMaxBodyBytes {
		return nil, fmt.Errorf("codex radar response exceeds size limit")
	}
	return body, nil
}

func buildCodexRadarOverview(points []codexRadarSourcePoint) (CodexRadarOverview, error) {
	if len(points) != len(codexRadarModels)*len(codexRadarEfforts) {
		return CodexRadarOverview{}, fmt.Errorf("codex radar response contains an incomplete catalog")
	}
	overview := CodexRadarOverview{
		SchemaVersion: codexRadarScoreVersion, SourceURL: codexRadarSourceURL,
		Attribution: "数据来自 Codex 雷达 codexradar.com", Metrics: make([]CodexRadarMetric, 0, len(points)),
	}
	for index, point := range points {
		model := codexRadarModels[index/len(codexRadarEfforts)]
		effort := codexRadarEfforts[index%len(codexRadarEfforts)]
		if point.Model != model.Model || point.Effort != effort || point.ScoreVersion != codexRadarScoreVersion || point.ScoreMax != 150 || point.Benchmark == "" || point.RequiredTasks <= 0 || point.Coverage > point.RequiredTasks {
			return CodexRadarOverview{}, fmt.Errorf("invalid codex radar summary for %s/%s", model.Model, effort)
		}
		if index > 0 && (point.Benchmark != points[0].Benchmark || point.TaskSetSHA256 != points[0].TaskSetSHA256) {
			return CodexRadarOverview{}, fmt.Errorf("codex radar summaries use inconsistent benchmarks")
		}
		if point.Score != nil && (!validCodexRadarNumber(*point.Score) || *point.Score > point.ScoreMax) {
			return CodexRadarOverview{}, fmt.Errorf("invalid codex radar score for %s/%s", model.Model, effort)
		}
		if point.Coverage == 0 {
			point.Score = nil
		}
		status := point.ScoreStatus
		if point.Score == nil {
			status = "no_data"
		}
		metric := CodexRadarMetric{
			Key:   strings.NewReplacer("-", "_", ".", "").Replace(model.Model) + "_" + effort,
			Label: model.Label + " " + effort, Model: model.Model, Family: model.Model,
			ReasoningEffort: effort, Score: point.Score, ScoreMax: point.ScoreMax, Status: status,
			Coverage: int(point.Coverage), Tasks: int(point.RequiredTasks), GradedRunsCount: int(point.GradedRunsCount),
			InsufficientData:   point.Coverage < 40,
			AverageCostUSD:     codexRadarMean(point.Statistics.Metrics.APIEquivalentUSD, 1),
			AverageTaskSeconds: codexRadarMean(point.Statistics.Metrics.ElapsedMS, 1000),
		}
		overview.Metrics = append(overview.Metrics, metric)
		overview.Benchmark = point.Benchmark
		if updated, err := time.Parse(time.RFC3339, point.History.UpdatedBucket); err == nil {
			current, _ := time.Parse(time.RFC3339, overview.UpdatedAt)
			if updated.After(current) {
				overview.UpdatedAt = updated.UTC().Format(time.RFC3339)
			}
		}
	}
	return overview, nil
}
func validCodexRadarNumber(value float64) bool {
	return value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}
func codexRadarMean(stat codexRadarStatistic, divisor float64) *float64 {
	// Null means missing evidence, not a free task. Do not estimate from
	// known_total: the upstream deliberately suppresses incomplete averages.
	if stat.KnownSamples <= 0 || stat.Mean == nil || !validCodexRadarNumber(*stat.Mean) {
		return nil
	}
	value := *stat.Mean / divisor
	return &value
}
