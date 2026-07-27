package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const codexRadarTestPayload = `{
  "schema_version": "2.0",
  "api_access": {
    "requirements": {
      "attribution_text": "数据来自 Codex 雷达 codexradar.com"
    }
  },
  "model_iq": {
    "updated_at": "2026-07-27T23:06:05+08:00",
    "latest": {
      "score": 104.9,
      "status": "green",
      "passed": 78,
      "tasks": 112,
      "average_cost_usd": 8.8514,
      "average_task_seconds": 1955.125,
      "average_task_time_human": "33分钟",
      "model": "gpt-5.6-sol",
      "reasoning_effort": "max"
    },
    "comparisons": {
      "gpt_56_terra_high": {
        "label": "GPT-5.6 Terra high",
        "model": "gpt-5.6-terra",
        "reasoning_effort": "high",
        "latest": {
          "score": 69.6,
          "status": "red",
          "passed": 52,
          "tasks": 112,
          "average_cost_usd": 1.3,
          "average_task_seconds": 720,
          "average_task_time_human": "12分钟",
          "model": "gpt-5.6-terra",
          "reasoning_effort": "high"
        }
      },
      "gpt_56_luna_missing_score": {
        "label": "GPT-5.6 Luna low",
        "model": "gpt-5.6-luna",
        "reasoning_effort": "low",
        "latest": {
          "score": 0,
          "model": "gpt-5.6-luna",
          "reasoning_effort": "low"
        }
      },
      "gpt_55_high": {
        "label": "GPT-5.5 high",
        "model": "gpt-5.5",
        "reasoning_effort": "high",
        "latest": {
          "score": 83.4,
          "model": "gpt-5.5",
          "reasoning_effort": "high"
        }
      }
    }
  }
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
	if len(overview.Metrics) != 2 {
		t.Fatalf("metrics count = %d, want 2", len(overview.Metrics))
	}
	if overview.Metrics[0].Key != "gpt_56_sol_max" {
		t.Fatalf("first metric = %q, want gpt_56_sol_max", overview.Metrics[0].Key)
	}
	if overview.Metrics[1].Key != "gpt_56_terra_high" {
		t.Fatalf("second metric = %q, want gpt_56_terra_high", overview.Metrics[1].Key)
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
