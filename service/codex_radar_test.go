package service

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
)

func codexRadarTestPoint(model, effort string) codexRadarSourcePoint {
	score, usd, elapsed := 112.61757399688433, 2.3726981694915255, 617070.3898305085
	point := codexRadarSourcePoint{
		Schema:    "radar-bench-public-summary-v5-150-daily-graded-count",
		Benchmark: "radar-bench-final64-20261004", ScoreVersion: codexRadarScoreVersion,
		TaskSetSHA256: "test-task-set", Model: model, Effort: effort,
		Score: &score, ScoreMax: 150, Coverage: 59, RequiredTasks: 64,
		ScoreStatus: "provisional", GradedRunsCount: 71,
	}
	point.Statistics.Metrics.APIEquivalentUSD = codexRadarStatistic{Mean: &usd, KnownSamples: 59}
	point.Statistics.Metrics.ElapsedMS = codexRadarStatistic{Mean: &elapsed, KnownSamples: 59}
	point.History.UpdatedBucket = "2026-10-13T00:00:00.000Z"
	return point
}

func codexRadarTestPoints() []codexRadarSourcePoint {
	points := make([]codexRadarSourcePoint, 0, 24)
	for _, model := range codexRadarModels {
		for _, effort := range codexRadarEfforts {
			points = append(points, codexRadarTestPoint(model.Model, effort))
		}
	}
	return points
}

func writeCodexRadarTestPoint(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("User-Agent"); got != "new-api-cmsg codex-radar overview" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := r.Header.Get("X-Radar-Bench-Selection"); got != "user-selected-v1" {
		t.Errorf("selection header = %q", got)
	}
	if got := r.URL.Query().Get("view"); got != "summary" {
		t.Errorf("view = %q", got)
	}
	point := codexRadarTestPoint(r.URL.Query().Get("model"), r.URL.Query().Get("effort"))
	body, err := common.Marshal(point)
	if err != nil {
		t.Errorf("marshal fixture: %v", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func TestCodexRadarProviderFetchesCompleteCatalogAndCaches(t *testing.T) {
	var requests atomic.Int32
	var slots sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		slot := r.URL.Query().Get("model") + ":" + r.URL.Query().Get("effort")
		if _, exists := slots.LoadOrStore(slot, true); exists {
			t.Errorf("duplicate slot %s", slot)
		}
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	now := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	provider.now = func() time.Time { return now }
	overview, err := provider.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Metrics) != 24 {
		t.Fatalf("metrics = %d, want 24", len(overview.Metrics))
	}
	if overview.SchemaVersion != codexRadarScoreVersion || overview.Stale {
		t.Fatalf("overview = %#v", overview)
	}
	if overview.UpdatedAt != "2026-10-13T00:00:00Z" || overview.FetchedAt != now.Format(time.RFC3339) {
		t.Fatalf("dates = %s / %s", overview.UpdatedAt, overview.FetchedAt)
	}
	for index, metric := range overview.Metrics {
		want := codexRadarTestPoints()[index]
		if metric.Model != want.Model || metric.Family != want.Model || metric.ReasoningEffort != want.Effort {
			t.Errorf("wrong order: %#v", metric)
		}
		if metric.ScoreMax != 150 || metric.Coverage != 59 || metric.Tasks != 64 || metric.GradedRunsCount != 71 || metric.InsufficientData {
			t.Errorf("counts = %#v", metric)
		}
		if metric.Score == nil || math.Abs(*metric.Score-*want.Score) > 1e-9 || metric.AverageCostUSD == nil || math.Abs(*metric.AverageCostUSD-2.3726981694915255) > 1e-9 || metric.AverageTaskSeconds == nil || math.Abs(*metric.AverageTaskSeconds-617.0703898305085) > 1e-9 {
			t.Errorf("statistics = %#v", metric)
		}
		if strings.Contains(metric.Model, "luna") || strings.Contains(metric.Model, "terra") {
			t.Errorf("unwanted model %s", metric.Model)
		}
	}
	if _, err := provider.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 24 {
		t.Fatalf("requests = %d, want 24", requests.Load())
	}
}

func TestCodexRadarProviderDeduplicatesAndBoundsConcurrency(t *testing.T) {
	var requests, active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	var callers sync.WaitGroup
	for range 12 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			if _, err := provider.Get(context.Background()); err != nil {
				t.Errorf("Get: %v", err)
			}
		}()
	}
	callers.Wait()
	if requests.Load() != 24 || peak.Load() > codexRadarConcurrency || peak.Load() < 2 {
		t.Fatalf("requests=%d peak=%d", requests.Load(), peak.Load())
	}
}

func TestCodexRadarProviderStaleFallbackRetryDelayAndExpiry(t *testing.T) {
	var requests atomic.Int32
	var broken atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if broken.Load() && r.URL.Query().Get("effort") == "low" {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.pointRetryWait = 0
	now := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	provider.now = func() time.Time { return now }
	first, err := provider.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	broken.Store(true)
	now = now.Add(11 * time.Minute)
	stale, err := provider.Get(context.Background())
	if err != nil || !stale.Stale || len(stale.Metrics) != 24 || stale.FetchedAt != first.FetchedAt {
		t.Fatalf("stale=%#v err=%v", stale, err)
	}
	if _, err := provider.Get(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 52 {
		t.Fatalf("retry storm: requests=%d", requests.Load())
	}
	now = now.Add(25 * time.Hour)
	if _, err := provider.Get(context.Background()); err == nil {
		t.Fatal("expired data accepted")
	}
	if requests.Load() != 80 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestCodexRadarProviderDoesNotCachePartialColdResponse(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("model") == "gpt-6-sol" && r.URL.Query().Get("effort") == "high" {
			http.Error(w, "bad gateway", 502)
			return
		}
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.pointRetryWait = 0
	for range 2 {
		if _, err := provider.Get(context.Background()); err == nil || !strings.Contains(err.Error(), "gpt-6-sol/high") {
			t.Fatalf("err=%v", err)
		}
	}
	if provider.cache != nil || requests.Load() != 25 {
		t.Fatalf("partial response cached or refresh repeated: %d", requests.Load())
	}
}

func TestCodexRadarProviderRetriesOnlyTheFailedPoint(t *testing.T) {
	var requests, failedSlot, active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		if r.URL.Query().Get("model") == "gpt-6-sol" && r.URL.Query().Get("effort") == "ultra" && failedSlot.Add(1) == 1 {
			http.Error(w, "temporary gateway error", http.StatusBadGateway)
			return
		}
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.pointRetryWait = 0
	overview, err := provider.Get(context.Background())
	if err != nil || len(overview.Metrics) != 24 || requests.Load() != 25 || failedSlot.Load() != 2 || peak.Load() > 4 {
		t.Fatalf("metrics=%d requests=%d failedSlot=%d peak=%d err=%v", len(overview.Metrics), requests.Load(), failedSlot.Load(), peak.Load(), err)
	}
}

type codexRadarTestTransport func(*http.Request) (*http.Response, error)

func (transport codexRadarTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestCodexRadarProviderRetriesRequestTimeout(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	base := server.Client().Transport
	client := &http.Client{Transport: codexRadarTestTransport(func(r *http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			return nil, context.DeadlineExceeded
		}
		return base.RoundTrip(r)
	})}
	provider := newCodexRadarProvider(server.URL, client)
	provider.pointRetryWait = 0
	if _, err := provider.fetchPoint(context.Background(), "gpt-6-sol", "ultra"); err != nil || attempts.Load() != 2 {
		t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
	}
}

func TestCodexRadarProviderDoesNotRetryPermanentOrInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name, body, retryAfter string
		status                 int
	}{
		{name: "forbidden", status: 403},
		{name: "not found", status: 404},
		{name: "malformed json", status: 200, body: "not-json"},
		{name: "wrong selection", status: 200, body: `{"model":"gpt-6-luna","effort":"ultra"}`},
		{name: "long rate limit", status: 429, retryAfter: "120"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempts.Add(1)
				w.Header().Set("Retry-After", test.retryAfter)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			t.Cleanup(server.Close)
			provider := newCodexRadarProvider(server.URL, server.Client())
			provider.pointRetryWait = 0
			if _, err := provider.fetchPoint(context.Background(), "gpt-6-sol", "ultra"); err == nil || attempts.Load() != 1 {
				t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
			}
		})
	}
}

func TestCodexRadarPointRetryBackoffHonorsCancellation(t *testing.T) {
	var attempts atomic.Int32
	first := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			close(first)
		}
		http.Error(w, "unavailable", 503)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.pointRetryWait = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := provider.fetchPoint(ctx, "gpt-6-sol", "ultra"); result <- err }()
	<-first
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || attempts.Load() != 1 {
			t.Fatalf("attempts=%d err=%v", attempts.Load(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry wait ignored cancellation")
	}
}

func TestCodexRadarRetryPolicyHonorsTransientErrorsAndLongCooldowns(t *testing.T) {
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, syscall.EPIPE, context.DeadlineExceeded} {
		if !retryableCodexRadarError(err) {
			t.Fatalf("transient error not retried: %v", err)
		}
	}
	if retryableCodexRadarError(context.Canceled) || retryableCodexRadarError(errors.New("invalid json")) {
		t.Fatal("permanent error retried")
	}
	wrapped := fmt.Errorf("point failed: %w", &codexRadarStatusError{status: 429, retryAfter: 120 * time.Second})
	if CodexRadarRetryDelay(wrapped) != 120*time.Second || CodexRadarRetryDelay(nil) != time.Minute {
		t.Fatal("upstream cooldown lost")
	}
}

func useCodexRadarMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()
	server := miniredis.RunT(t)
	oldEnabled, oldClient := common.RedisEnabled, common.RDB
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	common.RedisEnabled, common.RDB = true, client
	t.Cleanup(func() {
		_ = client.Close()
		common.RedisEnabled, common.RDB = oldEnabled, oldClient
	})
	return server
}

func TestCodexRadarSnapshotSurvivesProviderRestartAndPreservesAge(t *testing.T) {
	redisServer := useCodexRadarMiniRedis(t)
	var requests atomic.Int32
	var broken atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if broken.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	now := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	makeProvider := func() *codexRadarProvider {
		provider := newCodexRadarProvider(server.URL, server.Client())
		provider.store = codexRadarRedisStore{}
		provider.now = func() time.Time { return now }
		provider.pointRetryWait = 0
		return provider
	}
	first, err := makeProvider().Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := redisServer.Get(codexRadarSnapshotKey)
	if err != nil || redisServer.TTL(codexRadarSnapshotKey) != codexRadarStaleTTL {
		t.Fatalf("persisted=%t ttl=%v err=%v", err == nil, redisServer.TTL(codexRadarSnapshotKey), err)
	}
	now = now.Add(time.Minute)
	redisServer.FastForward(time.Minute)
	broken.Store(true)
	fresh, err := makeProvider().Get(context.Background())
	if err != nil || fresh.Stale || fresh.FetchedAt != first.FetchedAt || requests.Load() != 24 {
		t.Fatalf("fresh=%#v requests=%d err=%v", fresh, requests.Load(), err)
	}
	now = now.Add(10 * time.Minute)
	redisServer.FastForward(10 * time.Minute)
	staleProvider := makeProvider()
	stale, err := staleProvider.Get(context.Background())
	if err != nil || !stale.Stale || len(stale.Metrics) != 24 || stale.FetchedAt != first.FetchedAt || requests.Load() != 72 {
		t.Fatalf("stale=%#v requests=%d err=%v", stale, requests.Load(), err)
	}
	after, _ := redisServer.Get(codexRadarSnapshotKey)
	if after != saved || redisServer.TTL(codexRadarSnapshotKey) != codexRadarStaleTTL-11*time.Minute {
		t.Fatal("failed refresh changed snapshot or extended its TTL")
	}
	if _, err := staleProvider.Get(context.Background()); err != nil || requests.Load() != 72 {
		t.Fatalf("backoff requests=%d err=%v", requests.Load(), err)
	}
	now = now.Add(25 * time.Hour)
	redisServer.FastForward(25 * time.Hour)
	if _, err := makeProvider().Get(context.Background()); err == nil || redisServer.Exists(codexRadarSnapshotKey) {
		t.Fatal("expired persisted snapshot accepted")
	}
}

func TestCodexRadarSnapshotRejectsCorruptionPartialAndIncompatibleData(t *testing.T) {
	redisServer := useCodexRadarMiniRedis(t)
	now := time.Date(2026, 10, 14, 1, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		modify func(*codexRadarSnapshot) []byte
	}{
		{"malformed", func(*codexRadarSnapshot) []byte { return []byte("not-json") }},
		{"oversized", func(*codexRadarSnapshot) []byte { return []byte(strings.Repeat("x", codexRadarMaxSnapshotBytes+1)) }},
		{"partial", func(s *codexRadarSnapshot) []byte { s.Points = s.Points[:23]; return nil }},
		{"wrong version", func(s *codexRadarSnapshot) []byte { s.Points[0].ScoreVersion = "old"; return nil }},
		{"wrong model", func(s *codexRadarSnapshot) []byte { s.Points[0].Model = "gpt-6-luna"; return nil }},
		{"mixed benchmark", func(s *codexRadarSnapshot) []byte { s.Points[1].Benchmark = "old"; return nil }},
		{"future date", func(s *codexRadarSnapshot) []byte { s.Fetched = now.Add(time.Minute); return nil }},
		{"old date", func(s *codexRadarSnapshot) []byte { s.Fetched = now.Add(-25 * time.Hour); return nil }},
		{"missing date", func(s *codexRadarSnapshot) []byte { s.Fetched = time.Time{}; return nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := codexRadarSnapshot{Fetched: now, Points: codexRadarTestPoints()}
			data := test.modify(&snapshot)
			if data == nil {
				var err error
				data, err = common.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
			}
			redisServer.Set(codexRadarSnapshotKey, string(data))
			provider := newCodexRadarProvider("unused", nil)
			provider.store = codexRadarRedisStore{}
			provider.restoreSnapshot(context.Background(), now)
			if provider.cache != nil {
				t.Fatal("invalid snapshot cached")
			}
		})
	}
}

type codexRadarFailingStore struct{}

func (codexRadarFailingStore) Load(context.Context) ([]byte, error) {
	return nil, errors.New("store offline")
}
func (codexRadarFailingStore) Save(context.Context, []byte, time.Duration) error {
	return errors.New("store offline")
}

func TestCodexRadarPersistenceFailureDoesNotDiscardLiveData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	provider.store = codexRadarFailingStore{}
	if overview, err := provider.Get(context.Background()); err != nil || len(overview.Metrics) != 24 || provider.cache == nil {
		t.Fatalf("metrics=%d err=%v", len(overview.Metrics), err)
	}
}

func TestCodexRadarProviderCancellationDoesNotPoisonSharedRefresh(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		once.Do(func() { close(started) })
		<-release
		writeCodexRadarTestPoint(t, w, r)
	}))
	t.Cleanup(server.Close)
	provider := newCodexRadarProvider(server.URL, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := provider.Get(ctx); result <- err }()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller did not return")
	}
	close(release)
	overview, err := provider.Get(context.Background())
	if err != nil || len(overview.Metrics) != 24 || requests.Load() != 24 {
		t.Fatalf("metrics=%d requests=%d err=%v", len(overview.Metrics), requests.Load(), err)
	}
}

func TestCodexRadarProviderGzipAndBodyLimits(t *testing.T) {
	for _, test := range []struct {
		name    string
		size    int
		gzip    bool
		wantErr bool
	}{
		{"gzip", 0, true, false},
		{"oversized", codexRadarMaxBodyBytes + 1, false, true},
		{"gzip expansion", codexRadarMaxBodyBytes + 1, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body []byte
				if test.size > 0 {
					body = []byte(strings.Repeat("x", test.size))
				} else {
					var err error
					body, err = common.Marshal(codexRadarTestPoint(r.URL.Query().Get("model"), r.URL.Query().Get("effort")))
					if err != nil {
						t.Error(err)
						return
					}
				}
				if test.gzip {
					w.Header().Set("Content-Encoding", "gzip")
					writer := gzip.NewWriter(w)
					_, _ = writer.Write(body)
					_ = writer.Close()
				} else {
					_, _ = w.Write(body)
				}
			}))
			t.Cleanup(server.Close)
			transport := &http.Transport{DisableCompression: true}
			defer transport.CloseIdleConnections()
			provider := newCodexRadarProvider(server.URL, &http.Client{Transport: transport})
			_, err := provider.fetchPoint(context.Background(), "gpt-6-astra", "high")
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, test.wantErr)
			}
		})
	}
}

func TestCodexRadarOverviewHandlesMissingEvidenceAndZeroScores(t *testing.T) {
	points := codexRadarTestPoints()
	points[0].Statistics.Metrics.APIEquivalentUSD.Mean = nil // Official null with known samples > 0.
	points[0].Coverage = 39
	points[1].Statistics.Metrics.ElapsedMS.KnownSamples = 0
	points[1].Coverage = 40
	zero := 0.0
	points[2].Score = &zero
	points[3].Coverage = 0
	points[4].Score = nil
	overview, err := buildCodexRadarOverview(points)
	if err != nil {
		t.Fatal(err)
	}
	if overview.Metrics[0].AverageCostUSD != nil || !overview.Metrics[0].InsufficientData {
		t.Fatal("missing evidence treated as free or low coverage ignored")
	}
	if overview.Metrics[1].AverageTaskSeconds != nil || overview.Metrics[1].InsufficientData {
		t.Fatal("missing time or coverage threshold mishandled")
	}
	if overview.Metrics[2].Score == nil || *overview.Metrics[2].Score != 0 {
		t.Fatal("legitimate zero score dropped")
	}
	for _, index := range []int{3, 4} {
		if overview.Metrics[index].Score != nil || overview.Metrics[index].Status != "no_data" {
			t.Fatal("missing score not represented")
		}
	}
}

func TestCodexRadarOverviewRejectsWrongContracts(t *testing.T) {
	for _, test := range []struct {
		name   string
		modify func([]codexRadarSourcePoint) []codexRadarSourcePoint
	}{
		{"partial", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { return p[:23] }},
		{"legacy scale", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { p[0].ScoreMax = 100; return p }},
		{"legacy version", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { p[0].ScoreVersion = "v1"; return p }},
		{"wrong model", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { p[0].Model = "gpt-6-luna"; return p }},
		{"mixed benchmark", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { p[1].Benchmark = "old"; return p }},
		{"mixed task set", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { p[1].TaskSetSHA256 = "old"; return p }},
		{"coverage overflow", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { p[0].Coverage = 65; return p }},
		{"score overflow", func(p []codexRadarSourcePoint) []codexRadarSourcePoint { value := 151.0; p[0].Score = &value; return p }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := buildCodexRadarOverview(test.modify(codexRadarTestPoints())); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
}

func TestCodexRadarCountRejectsFractionalValues(t *testing.T) {
	var count codexRadarCount
	if err := count.UnmarshalJSON([]byte("209.0")); err != nil || count != 209 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	for _, raw := range []string{"209.5", "-1", "null", "NaN", "1e99"} {
		if err := count.UnmarshalJSON([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
