package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"github.com/tidwall/gjson"
)

func TestAntigravityExecuteStream_Returns429ImmediatelyForFailover(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		retryAfter time.Duration
	}{
		{name: "resource exhausted", body: `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`},
		{name: "plain rate limit", body: `rate limit exceeded`},
		{name: "instant retry hint", body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"0.1s"}]}}`, retryAfter: 100 * time.Millisecond},
		{name: "quota exhausted", body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"QUOTA_EXHAUSTED"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			exec := NewAntigravityExecutor(&config.Config{RequestRetry: 5})
			auth := antigravityStreamTestAuth("ag-immediate-"+tc.name, server.URL, "test-token")
			ctx, cancel := context.WithTimeout(cliproxyexecutor.WithRateLimitFailover(context.Background()), time.Second)
			defer cancel()
			_, err := exec.ExecuteStream(ctx, auth, antigravityStreamTestRequest(), cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatAntigravity})
			var status statusErr
			if !errors.As(err, &status) || status.StatusCode() != http.StatusTooManyRequests {
				t.Fatalf("ExecuteStream error = %v, want upstream 429", err)
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("upstream requests = %d, want one per credential", got)
			}
			if tc.retryAfter > 0 && (status.retryAfter == nil || *status.retryAfter != tc.retryAfter) {
				t.Fatalf("retry hint = %v, want %v", status.retryAfter, tc.retryAfter)
			}
		})
	}
}

func TestAntigravityExecuteStream_SingleAuthStillRetriesTransient429(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"status":"RESOURCE_EXHAUSTED","message":"Resource has been exhausted"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}]}}\n\n")
	}))
	defer server.Close()
	exec := NewAntigravityExecutor(&config.Config{RequestRetry: 1})
	result, err := exec.ExecuteStream(context.Background(), antigravityStreamTestAuth("ag-single-stream", server.URL, "test-token"), antigravityStreamTestRequest(), cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatAntigravity})
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	var payload []byte
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		payload = append(payload, chunk.Payload...)
	}
	if !strings.Contains(string(payload), `"ok"`) || calls.Load() != 2 {
		t.Fatalf("single-auth retry: requests=%d payload=%s", calls.Load(), payload)
	}
}

func TestAntigravityExecuteStream_ManagerRotatesHTTPAccounts(t *testing.T) {
	for _, allExhausted := range []bool{false, true} {
		t.Run(fmt.Sprintf("all_exhausted=%t", allExhausted), func(t *testing.T) {
			var mu sync.Mutex
			var attempts []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, errRead := io.ReadAll(r.Body)
				if errRead != nil {
					t.Errorf("read body: %v", errRead)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if requestType := gjson.GetBytes(body, "requestType"); requestType.Exists() {
					t.Errorf("ordinary flash request included requestType=%s", requestType.Raw)
				}
				token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
				mu.Lock()
				attempts = append(attempts, token)
				mu.Unlock()
				if allExhausted || token != "test-token-2" {
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"Resource has been exhausted"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"response\":{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}]}}\n\n")
			}))
			defer server.Close()
			selector := cliproxyauth.NewSessionAffinitySelector(&cliproxyauth.FillFirstSelector{})
			defer selector.Stop()
			manager := cliproxyauth.NewManager(nil, selector, nil)
			manager.SetRetryConfig(0, 0, 0)
			manager.RegisterExecutor(NewAntigravityExecutor(&config.Config{RequestRetry: 5}))
			reg := registry.GetGlobalRegistry()
			for i := 0; i < 3; i++ {
				id := fmt.Sprintf("ag-http-failover-%d", i)
				auth := antigravityStreamTestAuth(id, server.URL, fmt.Sprintf("test-token-%d", i))
				if _, err := manager.Register(context.Background(), auth); err != nil {
					t.Fatalf("register auth: %v", err)
				}
				reg.RegisterClient(id, "antigravity", []*registry.ModelInfo{{ID: "gemini-3-flash"}})
				t.Cleanup(func() { reg.UnregisterClient(id) })
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			opts := cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatAntigravity, Headers: http.Header{"X-Session-Id": {"http-failover-session"}}}
			result, err := manager.ExecuteStream(ctx, []string{"antigravity"}, antigravityStreamTestRequest(), opts)
			if allExhausted {
				var status statusErr
				if !errors.As(err, &status) || status.StatusCode() != http.StatusTooManyRequests {
					t.Fatalf("all exhausted error = %v, want 429", err)
				}
			} else {
				if err != nil {
					t.Fatalf("ExecuteStream: %v", err)
				}
				for chunk := range result.Chunks {
					if chunk.Err != nil {
						t.Fatalf("stream error: %v", chunk.Err)
					}
				}
			}
			mu.Lock()
			got := append([]string(nil), attempts...)
			mu.Unlock()
			if want := []string{"test-token-0", "test-token-1", "test-token-2"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("HTTP attempts = %v, want %v", got, want)
			}
		})
	}
}

func antigravityStreamTestAuth(id, baseURL, token string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		ID: id, Provider: "antigravity", Status: cliproxyauth.StatusActive,
		Attributes: map[string]string{"base_url": baseURL},
		Metadata: map[string]any{
			"access_token": token, "project_id": "test-project",
			"expired": time.Now().Add(time.Hour).Format(time.RFC3339),
		},
	}
}

func antigravityStreamTestRequest() cliproxyexecutor.Request {
	return cliproxyexecutor.Request{Model: "gemini-3-flash", Payload: []byte(`{"request":{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}}`)}
}
