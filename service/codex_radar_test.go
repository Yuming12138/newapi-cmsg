package service

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const codexRadarTestPayload = `{
  "schema": 2,
  "source_updated_at": "2026-07-28T01:49:07+08:00",
  "points": [
    {
      "model": "gpt-5.6-sol",
      "effort": "max",
      "iq": 103.125,
      "passed": 77.0,
      "valid_tasks": 112.0,
      "average_price_usd": 8.911578,
      "average_minutes": 31.8137
    },
    {
      "model": "gpt-5.6-terra",
      "effort": "high",
      "iq": 69.6429,
      "passed": 52,
      "valid_tasks": 112,
      "average_price_usd": 1.254286,
      "average_minutes": 11.5278
    },
    {
      "model": "gpt-5.5",
      "effort": "high",
      "iq": 80.3571,
      "passed": 60,
      "valid_tasks": 112,
      "average_price_usd": 3.683317,
      "average_minutes": 16.4511
    },
    {
      "model": "deepseek-v4-flash",
      "effort": "max",
      "iq": 86.61,
      "passed": 194.0,
      "valid_tasks": 336.0,
      "average_price_usd": 0.099226,
      "average_minutes": 31.89
    },
    {
      "model": "deepseek-v4-pro",
      "effort": "high",
      "iq": 87.5,
      "passed": 14.0,
      "valid_tasks": 24.0,
      "average_price_usd": 0.174324,
      "average_minutes": 22.69
    },
    {
      "model": "gpt-5.6-luna",
      "effort": "low",
      "iq": 0
    },
    {
      "model": "gpt-5.4",
      "effort": "high",
      "iq": 88.0
    },
    {
      "model": "deepseek-v4-unknown",
      "effort": "high",
      "iq": 99.0
    }
  ]
}`

func TestCodexRadarProviderFiltersAndCachesMetrics(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"test-v1"`)
		_, _ = w.Write([]byte(codexRadarTestPayload))
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.now = func() time.Time { return now }

	overview, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if len(overview.Metrics) != 5 {
		t.Fatalf("metrics count = %d, want 5", len(overview.Metrics))
	}
	if overview.Metrics[0].Key != "gpt_56_sol_max" {
		t.Fatalf("first metric = %q, want gpt_56_sol_max", overview.Metrics[0].Key)
	}
	if overview.Metrics[1].Key != "gpt_56_terra_high" {
		t.Fatalf("second metric = %q, want gpt_56_terra_high", overview.Metrics[1].Key)
	}
	if overview.Metrics[2].Key != "gpt_55_high" || overview.Metrics[2].Family != "gpt-5.5" {
		t.Fatalf("third metric = %#v, want GPT-5.5 high", overview.Metrics[2])
	}
	if overview.Metrics[3].Key != "deepseek_v4_pro_high" || overview.Metrics[3].Family != "deepseek-v4-pro" {
		t.Fatalf("fourth metric = %#v, want DeepSeek V4 Pro high", overview.Metrics[3])
	}
	if overview.Metrics[4].Key != "deepseek_v4_flash_max" || overview.Metrics[4].Family != "deepseek-v4-flash" {
		t.Fatalf("fifth metric = %#v, want DeepSeek V4 Flash max", overview.Metrics[4])
	}
	if overview.Metrics[0].Passed != 77 || overview.Metrics[0].Tasks != 112 {
		t.Fatalf("decimal counts were not preserved as integers: %#v", overview.Metrics[0])
	}
	if math.Abs(overview.Metrics[0].AverageCostUSD-8.911578) > 1e-9 ||
		math.Abs(overview.Metrics[0].AverageTaskSeconds-31.8137*60) > 1e-9 {
		t.Fatalf("first metric averages = %#v", overview.Metrics[0])
	}
	if overview.Attribution != "数据来自 Codex 雷达 codexradar.com" {
		t.Fatalf("attribution = %q", overview.Attribution)
	}
	if overview.Stale {
		t.Fatal("fresh response marked stale")
	}

	if _, err := provider.Get(context.Background()); err != nil {
		t.Fatalf("cached Get() error = %v", err)
	}
	if got := requestCount.Load(); got != 1 {
		t.Fatalf("upstream requests = %d, want 1", got)
	}
}

func TestBuildCodexRadarOverviewIncludesCompletePublishedCatalog(t *testing.T) {
	source := codexRadarSource{
		Schema:          2,
		SourceUpdatedAt: "2026-07-28T01:49:07+08:00",
	}
	catalog := []struct {
		model   string
		efforts []string
	}{
		{model: "gpt-5.6-sol", efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{model: "gpt-5.6-terra", efforts: []string{"low", "medium", "high", "xhigh", "max", "ultra"}},
		{model: "gpt-5.6-luna", efforts: []string{"low", "medium", "high", "xhigh", "max"}},
		{model: "gpt-5.5", efforts: []string{"high", "xhigh"}},
		{model: "deepseek-v4-pro", efforts: []string{"low", "high", "max"}},
		{model: "deepseek-v4-flash", efforts: []string{"high", "max"}},
	}
	for _, family := range catalog {
		for _, effort := range family.efforts {
			source.Points = append(source.Points, codexRadarSourcePoint{
				Model:           family.model,
				Effort:          effort,
				IQ:              80,
				AveragePriceUSD: 1,
				AverageMinutes:  10,
			})
		}
	}

	overview, err := buildCodexRadarOverview(source)
	if err != nil {
		t.Fatalf("buildCodexRadarOverview() error = %v", err)
	}
	if len(overview.Metrics) != 24 {
		t.Fatalf("metrics count = %d, want 24", len(overview.Metrics))
	}
	wantKeys := []string{
		"gpt_56_sol_ultra", "gpt_56_sol_max", "gpt_56_sol_xhigh", "gpt_56_sol_high", "gpt_56_sol_medium", "gpt_56_sol_low",
		"gpt_56_terra_ultra", "gpt_56_terra_max", "gpt_56_terra_xhigh", "gpt_56_terra_high", "gpt_56_terra_medium", "gpt_56_terra_low",
		"gpt_56_luna_max", "gpt_56_luna_xhigh", "gpt_56_luna_high", "gpt_56_luna_medium", "gpt_56_luna_low",
		"gpt_55_xhigh", "gpt_55_high",
		"deepseek_v4_pro_max", "deepseek_v4_pro_high", "deepseek_v4_pro_low",
		"deepseek_v4_flash_max", "deepseek_v4_flash_high",
	}
	for index, want := range wantKeys {
		if got := overview.Metrics[index].Key; got != want {
			t.Fatalf("metric %d key = %q, want %q", index, got, want)
		}
	}
}

func TestCodexRadarCountRejectsFractionalValues(t *testing.T) {
	var count codexRadarCount
	if err := count.UnmarshalJSON([]byte("209.0")); err != nil {
		t.Fatalf("integer-valued decimal rejected: %v", err)
	}
	if count != 209 {
		t.Fatalf("count = %d, want 209", count)
	}
	if err := count.UnmarshalJSON([]byte("209.5")); err == nil {
		t.Fatal("fractional count was silently accepted")
	}
}

func TestCodexRadarProviderFallsBackToStaleCache(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requestCount.Add(1) > 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(codexRadarTestPayload))
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.cacheTTL = time.Minute
	provider.staleTTL = time.Hour
	provider.now = func() time.Time { return now }

	if _, err := provider.Get(context.Background()); err != nil {
		t.Fatalf("initial Get() error = %v", err)
	}
	now = now.Add(2 * time.Minute)

	overview, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("stale Get() error = %v", err)
	}
	if !overview.Stale {
		t.Fatal("fallback response was not marked stale")
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("upstream requests = %d, want 2", got)
	}
}

func TestCodexRadarProviderRevalidatesWithETag(t *testing.T) {
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestCount.Add(1) > 1 {
			if got := r.Header.Get("If-None-Match"); got != `"test-v1"` {
				t.Errorf("If-None-Match = %q, want test-v1 ETag", got)
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"test-v1"`)
		_, _ = w.Write([]byte(codexRadarTestPayload))
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, 7, 28, 1, 0, 0, 0, time.UTC)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.cacheTTL = time.Minute
	provider.now = func() time.Time { return now }

	first, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("initial Get() error = %v", err)
	}
	now = now.Add(2 * time.Minute)

	second, err := provider.Get(context.Background())
	if err != nil {
		t.Fatalf("revalidated Get() error = %v", err)
	}
	if second.Stale {
		t.Fatal("304 revalidation marked response stale")
	}
	if second.FetchedAt == first.FetchedAt {
		t.Fatalf("FetchedAt was not refreshed: %q", second.FetchedAt)
	}
	if got := requestCount.Load(); got != 2 {
		t.Fatalf("upstream requests = %d, want 2", got)
	}
}
