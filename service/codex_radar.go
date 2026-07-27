package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/sync/singleflight"
)

const (
	codexRadarPublicEndpoint = "https://codexradar.com/data/intelligence-efficiency.json"
	codexRadarSourceURL      = "https://codexradar.com/"
	codexRadarCacheTTL       = 10 * time.Minute
	codexRadarStaleTTL       = 24 * time.Hour
	codexRadarHTTPTimeout    = 5 * time.Second
	codexRadarMaxBodyBytes   = 2 << 20
)

type CodexRadarMetric struct {
	Key                  string  `json:"key"`
	Label                string  `json:"label"`
	Model                string  `json:"model"`
	Family               string  `json:"family"`
	ReasoningEffort      string  `json:"reasoning_effort"`
	Score                float64 `json:"score"`
	Status               string  `json:"status"`
	Passed               int     `json:"passed"`
	Tasks                int     `json:"tasks"`
	AverageCostUSD       float64 `json:"average_cost_usd"`
	AverageTaskSeconds   float64 `json:"average_task_seconds"`
	AverageTaskTimeHuman string  `json:"average_task_time_human"`
}

type CodexRadarOverview struct {
	SchemaVersion string             `json:"schema_version"`
	UpdatedAt     string             `json:"updated_at"`
	FetchedAt     string             `json:"fetched_at"`
	SourceURL     string             `json:"source_url"`
	Attribution   string             `json:"attribution"`
	Stale         bool               `json:"stale"`
	Metrics       []CodexRadarMetric `json:"metrics"`
}

type codexRadarSourcePoint struct {
	Model           string  `json:"model"`
	Effort          string  `json:"effort"`
	IQ              float64 `json:"iq"`
	Passed          int     `json:"passed"`
	ValidTasks      int     `json:"valid_tasks"`
	AveragePriceUSD float64 `json:"average_price_usd"`
	AverageMinutes  float64 `json:"average_minutes"`
}

type codexRadarSource struct {
	Schema          int                     `json:"schema"`
	SourceUpdatedAt string                  `json:"source_updated_at"`
	Points          []codexRadarSourcePoint `json:"points"`
}

type codexRadarCacheEntry struct {
	Overview CodexRadarOverview
	ETag     string
	Fetched  time.Time
}

type codexRadarProvider struct {
	endpoint string
	client   *http.Client
	cacheTTL time.Duration
	staleTTL time.Duration
	now      func() time.Time

	mu    sync.RWMutex
	cache *codexRadarCacheEntry
	group singleflight.Group
}

func newCodexRadarProvider(endpoint string, client *http.Client) *codexRadarProvider {
	return &codexRadarProvider{
		endpoint: endpoint,
		client:   client,
		cacheTTL: codexRadarCacheTTL,
		staleTTL: codexRadarStaleTTL,
		now:      time.Now,
	}
}

var defaultCodexRadarProvider = newCodexRadarProvider(
	codexRadarPublicEndpoint,
	&http.Client{Timeout: codexRadarHTTPTimeout},
)

func GetCodexRadarOverview(ctx context.Context) (CodexRadarOverview, error) {
	return defaultCodexRadarProvider.Get(ctx)
}

func (provider *codexRadarProvider) Get(ctx context.Context) (CodexRadarOverview, error) {
	if overview, ok := provider.cachedOverview(provider.now(), false); ok {
		return overview, nil
	}

	value, err, _ := provider.group.Do("overview", func() (interface{}, error) {
		now := provider.now()
		if overview, ok := provider.cachedOverview(now, false); ok {
			return overview, nil
		}

		overview, etag, notModified, fetchErr := provider.fetch(ctx)
		if fetchErr != nil {
			if stale, ok := provider.cachedOverview(now, true); ok {
				return stale, nil
			}
			return CodexRadarOverview{}, fetchErr
		}

		if notModified {
			if refreshed, ok := provider.refreshCachedOverview(now); ok {
				return refreshed, nil
			}
			return CodexRadarOverview{}, fmt.Errorf("codex radar returned 304 without cached data")
		}

		overview.FetchedAt = now.UTC().Format(time.RFC3339)
		provider.store(overview, etag, now)
		return overview, nil
	})
	if err != nil {
		return CodexRadarOverview{}, err
	}

	overview, ok := value.(CodexRadarOverview)
	if !ok {
		return CodexRadarOverview{}, fmt.Errorf("unexpected codex radar cache result")
	}
	return overview, nil
}

func (provider *codexRadarProvider) cachedOverview(now time.Time, allowStale bool) (CodexRadarOverview, bool) {
	provider.mu.RLock()
	defer provider.mu.RUnlock()

	if provider.cache == nil {
		return CodexRadarOverview{}, false
	}

	age := now.Sub(provider.cache.Fetched)
	if age < 0 {
		age = 0
	}
	if age <= provider.cacheTTL {
		overview := provider.cache.Overview
		overview.Stale = false
		return overview, true
	}
	if allowStale && age <= provider.staleTTL {
		overview := provider.cache.Overview
		overview.Stale = true
		return overview, true
	}
	return CodexRadarOverview{}, false
}

func (provider *codexRadarProvider) refreshCachedOverview(now time.Time) (CodexRadarOverview, bool) {
	provider.mu.Lock()
	defer provider.mu.Unlock()

	if provider.cache == nil {
		return CodexRadarOverview{}, false
	}
	provider.cache.Fetched = now
	provider.cache.Overview.FetchedAt = now.UTC().Format(time.RFC3339)
	provider.cache.Overview.Stale = false
	return provider.cache.Overview, true
}

func (provider *codexRadarProvider) store(overview CodexRadarOverview, etag string, fetched time.Time) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.cache = &codexRadarCacheEntry{
		Overview: overview,
		ETag:     etag,
		Fetched:  fetched,
	}
}

func (provider *codexRadarProvider) cachedETag() string {
	provider.mu.RLock()
	defer provider.mu.RUnlock()
	if provider.cache == nil {
		return ""
	}
	return provider.cache.ETag
}

func (provider *codexRadarProvider) fetch(ctx context.Context) (CodexRadarOverview, string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.endpoint, nil)
	if err != nil {
		return CodexRadarOverview{}, "", false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "new-api-cmsg codex-radar overview")
	if etag := provider.cachedETag(); etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := provider.client.Do(req)
	if err != nil {
		return CodexRadarOverview{}, "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return CodexRadarOverview{}, resp.Header.Get("ETag"), true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return CodexRadarOverview{}, "", false, fmt.Errorf("codex radar returned status %d", resp.StatusCode)
	}
	if resp.ContentLength > codexRadarMaxBodyBytes {
		return CodexRadarOverview{}, "", false, fmt.Errorf("codex radar response exceeds size limit")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, codexRadarMaxBodyBytes+1))
	if err != nil {
		return CodexRadarOverview{}, "", false, err
	}
	if len(body) > codexRadarMaxBodyBytes {
		return CodexRadarOverview{}, "", false, fmt.Errorf("codex radar response exceeds size limit")
	}

	var source codexRadarSource
	if err := common.Unmarshal(body, &source); err != nil {
		return CodexRadarOverview{}, "", false, fmt.Errorf("decode codex radar response: %w", err)
	}
	overview, err := buildCodexRadarOverview(source)
	if err != nil {
		return CodexRadarOverview{}, "", false, err
	}
	return overview, resp.Header.Get("ETag"), false, nil
}

func buildCodexRadarOverview(source codexRadarSource) (CodexRadarOverview, error) {
	metrics := make([]CodexRadarMetric, 0, len(source.Points))
	seenSlots := make(map[string]struct{}, len(source.Points))
	for _, point := range source.Points {
		if !isCodexRadarPoint(point) {
			continue
		}
		family := codexRadarFamily(point.Model)
		effort := strings.ToLower(strings.TrimSpace(point.Effort))
		slot := family + "\x00" + effort
		if _, exists := seenSlots[slot]; exists {
			continue
		}
		seenSlots[slot] = struct{}{}
		metrics = append(metrics, makeCodexRadarMetric(point, family, effort))
	}

	if len(metrics) == 0 {
		return CodexRadarOverview{}, fmt.Errorf("codex radar response contains no supported metrics")
	}
	sortCodexRadarMetrics(metrics)

	return CodexRadarOverview{
		SchemaVersion: strconv.Itoa(source.Schema),
		UpdatedAt:     source.SourceUpdatedAt,
		SourceURL:     codexRadarSourceURL,
		Attribution:   "数据来自 Codex 雷达 codexradar.com",
		Metrics:       metrics,
	}, nil
}

func isCodexRadarPoint(point codexRadarSourcePoint) bool {
	model := strings.ToLower(strings.TrimSpace(point.Model))
	return (strings.HasPrefix(model, "gpt-5.6-") || model == "gpt-5.5") && point.IQ > 0
}

func makeCodexRadarMetric(point codexRadarSourcePoint, family string, effort string) CodexRadarMetric {
	label := fmt.Sprintf("GPT-5.6 %s %s", familyLabel(family), effort)
	key := fmt.Sprintf("gpt_56_%s_%s", family, effort)
	if family == "gpt-5.5" {
		label = fmt.Sprintf("GPT-5.5 %s", effort)
		key = "gpt_55_" + effort
	}
	return CodexRadarMetric{
		Key:                  key,
		Label:                label,
		Model:                point.Model,
		Family:               family,
		ReasoningEffort:      effort,
		Score:                point.IQ,
		Status:               codexRadarStatus(point.IQ),
		Passed:               point.Passed,
		Tasks:                point.ValidTasks,
		AverageCostUSD:       point.AveragePriceUSD,
		AverageTaskSeconds:   point.AverageMinutes * 60,
		AverageTaskTimeHuman: fmt.Sprintf("%.0f分钟", point.AverageMinutes),
	}
}

func codexRadarStatus(score float64) string {
	if score >= 90 {
		return "green"
	}
	if score >= 75 {
		return "yellow"
	}
	return "red"
}

func codexRadarFamily(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "gpt-5.5" {
		return normalized
	}
	return strings.TrimPrefix(normalized, "gpt-5.6-")
}

func familyLabel(family string) string {
	switch family {
	case "sol":
		return "Sol"
	case "terra":
		return "Terra"
	case "luna":
		return "Luna"
	default:
		return family
	}
}

func sortCodexRadarMetrics(metrics []CodexRadarMetric) {
	familyOrder := map[string]int{"sol": 0, "terra": 1, "luna": 2, "gpt-5.5": 3}
	effortOrder := map[string]int{"ultra": 0, "max": 1, "xhigh": 2, "high": 3, "medium": 4, "low": 5}
	sort.SliceStable(metrics, func(i int, j int) bool {
		leftFamily, leftOK := familyOrder[metrics[i].Family]
		rightFamily, rightOK := familyOrder[metrics[j].Family]
		if !leftOK {
			leftFamily = len(familyOrder)
		}
		if !rightOK {
			rightFamily = len(familyOrder)
		}
		if leftFamily != rightFamily {
			return leftFamily < rightFamily
		}

		leftEffort, leftOK := effortOrder[metrics[i].ReasoningEffort]
		rightEffort, rightOK := effortOrder[metrics[j].ReasoningEffort]
		if !leftOK {
			leftEffort = len(effortOrder)
		}
		if !rightOK {
			rightEffort = len(effortOrder)
		}
		if leftEffort != rightEffort {
			return leftEffort < rightEffort
		}
		return metrics[i].Key < metrics[j].Key
	})
}
