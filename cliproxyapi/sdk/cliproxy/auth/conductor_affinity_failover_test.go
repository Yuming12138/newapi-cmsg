package auth

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type affinityFailoverExecutor struct {
	authFallbackExecutor
	failures      map[string]error
	bootstrap     bool
	lateError     bool
	models        []string
	failoverFlags []bool
}

func (e *affinityFailoverExecutor) ExecuteStream(ctx context.Context, auth *Auth, req cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	e.mu.Lock()
	e.streamCalls = append(e.streamCalls, auth.ID)
	e.models = append(e.models, req.Model)
	e.failoverFlags = append(e.failoverFlags, cliproxyexecutor.RateLimitFailoverEnabled(ctx))
	err := e.failures[auth.ID]
	e.mu.Unlock()
	if err != nil && !e.bootstrap && !e.lateError {
		return nil, err
	}
	ch := make(chan cliproxyexecutor.StreamChunk, 2)
	if err == nil || e.lateError {
		ch <- cliproxyexecutor.StreamChunk{Payload: []byte(auth.ID)}
	}
	if err != nil {
		ch <- cliproxyexecutor.StreamChunk{Err: err}
	}
	close(ch)
	return &cliproxyexecutor.StreamResult{Headers: http.Header{"X-Auth": {auth.ID}}, Chunks: ch}, nil
}

func newAffinityFailoverManager(t *testing.T, model string, count int) (*Manager, *SessionAffinitySelector, *affinityFailoverExecutor) {
	t.Helper()
	selector := NewSessionAffinitySelector(&FillFirstSelector{})
	t.Cleanup(selector.Stop)
	m := NewManager(nil, selector, nil)
	m.SetRetryConfig(0, 0, 0)
	exec := &affinityFailoverExecutor{authFallbackExecutor: authFallbackExecutor{id: "antigravity"}, failures: make(map[string]error)}
	m.RegisterExecutor(exec)
	reg := registry.GetGlobalRegistry()
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("affinity-auth-%d", i)
		if _, err := m.Register(context.Background(), &Auth{ID: id, Provider: "antigravity", Status: StatusActive}); err != nil {
			t.Fatalf("register auth: %v", err)
		}
		reg.RegisterClient(id, "antigravity", []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func() { reg.UnregisterClient(id) })
	}
	return m, selector, exec
}

func TestManagerExecuteStream_Affinity429FailsOverAndRebinds(t *testing.T) {
	for _, bootstrap := range []bool{false, true} {
		for _, eager := range []bool{false, true} {
			t.Run(fmt.Sprintf("bootstrap=%t/eager=%t", bootstrap, eager), func(t *testing.T) {
				model := "gemini-3-flash"
				m, selector, exec := newAffinityFailoverManager(t, model, 3)
				cfg := &internalconfig.Config{}
				cfg.Streaming.EagerHeaders = eager
				m.SetConfig(cfg)
				exec.bootstrap = bootstrap
				for _, id := range []string{"affinity-auth-0", "affinity-auth-1"} {
					exec.failures[id] = fmt.Errorf("upstream: %w", &Error{
						HTTPStatus: http.StatusTooManyRequests,
						Message:    `{"error":{"status":"RESOURCE_EXHAUSTED","message":"Resource has been exhausted"}}`,
					})
				}
				opts := cliproxyexecutor.Options{Headers: http.Header{"X-Session-Id": {"affinity-session"}}}
				key := "mixed::" + ExtractSessionID(opts.Headers, nil, nil) + "::" + model
				selector.cache.Set(key, "affinity-auth-0")
				selector.cache.Set("other-session", "affinity-auth-0")
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				for i := 0; i < 2; i++ {
					result, err := m.ExecuteStream(ctx, []string{"antigravity"}, cliproxyexecutor.Request{Model: model}, opts)
					if err != nil {
						t.Fatalf("ExecuteStream: %v", err)
					}
					if got := readOpenAICompatStreamPayload(t, result); got != "affinity-auth-2" {
						t.Fatalf("stream payload = %q, want healthy auth", got)
					}
					if got := result.Headers.Get("X-Auth"); got != "affinity-auth-2" {
						t.Fatalf("upstream header = %q, want healthy auth", got)
					}
				}
				if got, want := exec.StreamCalls(), []string{"affinity-auth-0", "affinity-auth-1", "affinity-auth-2", "affinity-auth-2"}; !reflect.DeepEqual(got, want) {
					t.Fatalf("stream attempts = %v, want %v", got, want)
				}
				if got, ok := selector.cache.Get(key); !ok || got != "affinity-auth-2" {
					t.Fatalf("affinity binding = %q, exists=%t, want healthy auth", got, ok)
				}
				if _, ok := selector.cache.Get("other-session"); ok {
					t.Fatal("429 should invalidate other bindings to the failed auth")
				}
			})
		}
	}
}

func TestManagerExecuteStream_RateLimitFailoverRespectsLimits(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		limit int
		pin   bool
	}{
		{name: "single auth", count: 1},
		{name: "credential retry limit", count: 2, limit: 1},
		{name: "pinned auth", count: 2, pin: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := "gemini-3-flash"
			m, selector, exec := newAffinityFailoverManager(t, model, tc.count)
			m.SetRetryConfig(0, 0, tc.limit)
			exec.failures["affinity-auth-0"] = &Error{HTTPStatus: http.StatusTooManyRequests, Message: "RESOURCE_EXHAUSTED"}
			selector.cache.Set("failed-session", "affinity-auth-0")
			opts := cliproxyexecutor.Options{}
			if tc.pin {
				opts.Metadata = map[string]any{cliproxyexecutor.PinnedAuthMetadataKey: "affinity-auth-0"}
			}
			_, err := m.ExecuteStream(context.Background(), []string{"antigravity"}, cliproxyexecutor.Request{Model: model}, opts)
			if statusCodeFromError(err) != http.StatusTooManyRequests {
				t.Fatalf("error = %v, want 429", err)
			}
			if got := exec.StreamCalls(); !reflect.DeepEqual(got, []string{"affinity-auth-0"}) {
				t.Fatalf("stream attempts = %v, want only original auth", got)
			}
			if !reflect.DeepEqual(exec.failoverFlags, []bool{false}) {
				t.Fatalf("failover flags = %v, want disabled", exec.failoverFlags)
			}
			if _, exists := selector.cache.Get("failed-session"); exists {
				t.Fatal("429 should clear binding even without an alternative")
			}
		})
	}
}

func TestManagerExecuteStream_ExhaustedPoolDoesNotRetrySameCredential(t *testing.T) {
	model := "gemini-3-flash"
	m, _, exec := newAffinityFailoverManager(t, model, 2)
	exec.failures["affinity-auth-0"] = &Error{HTTPStatus: http.StatusTooManyRequests, Message: "RESOURCE_EXHAUSTED"}
	exec.failures["affinity-auth-1"] = &Error{HTTPStatus: http.StatusTooManyRequests, Message: "RESOURCE_EXHAUSTED"}
	_, err := m.ExecuteStream(context.Background(), []string{"antigravity"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if statusCodeFromError(err) != http.StatusTooManyRequests {
		t.Fatalf("error = %v, want 429", err)
	}
	if got, want := exec.StreamCalls(), []string{"affinity-auth-0", "affinity-auth-1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stream attempts = %v, want %v", got, want)
	}
	if got, want := exec.failoverFlags, []bool{true, true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("failover flags = %v, want %v", got, want)
	}
}

func TestManagerExecuteStream_ModelPool429PrefersAnotherAuth(t *testing.T) {
	for _, bootstrap := range []bool{false, true} {
		t.Run(fmt.Sprintf("bootstrap=%t", bootstrap), func(t *testing.T) {
			const provider = "openai-compatible-affinity-pool"
			const alias = "pool-alias"
			cfg := &internalconfig.Config{OpenAICompatibility: []internalconfig.OpenAICompatibility{{
				Name: provider,
				Models: []internalconfig.OpenAICompatibilityModel{
					{Name: "upstream-1", Alias: alias},
					{Name: "upstream-2", Alias: alias},
				},
			}}}
			selector := NewSessionAffinitySelector(&FillFirstSelector{})
			defer selector.Stop()
			m := NewManager(nil, selector, nil)
			m.SetConfig(cfg)
			m.SetRetryConfig(0, 0, 0)
			exec := &affinityFailoverExecutor{
				authFallbackExecutor: authFallbackExecutor{id: provider}, bootstrap: bootstrap,
				failures: map[string]error{"pool-auth-0": &Error{HTTPStatus: http.StatusTooManyRequests, Message: "rate limited"}},
			}
			m.RegisterExecutor(exec)
			reg := registry.GetGlobalRegistry()
			for i := 0; i < 2; i++ {
				id := fmt.Sprintf("pool-auth-%d", i)
				auth := &Auth{ID: id, Provider: provider, Status: StatusActive, Attributes: map[string]string{
					"api_key": "test-key", "compat_name": provider, "provider_key": provider,
				}}
				if _, err := m.Register(context.Background(), auth); err != nil {
					t.Fatalf("register auth: %v", err)
				}
				reg.RegisterClient(id, provider, []*registry.ModelInfo{{ID: alias}})
				t.Cleanup(func() { reg.UnregisterClient(id) })
			}
			result, err := m.ExecuteStream(context.Background(), []string{provider}, cliproxyexecutor.Request{Model: alias}, cliproxyexecutor.Options{})
			if err != nil {
				t.Fatalf("ExecuteStream: %v", err)
			}
			if got := readOpenAICompatStreamPayload(t, result); got != "pool-auth-1" {
				t.Fatalf("payload = %q, want healthy account", got)
			}
			if got, want := exec.StreamCalls(), []string{"pool-auth-0", "pool-auth-1"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("stream attempts = %v, want %v", got, want)
			}
			if want := []string{"upstream-1", "upstream-1"}; !reflect.DeepEqual(exec.models, want) {
				t.Fatalf("model attempts = %v, want %v", exec.models, want)
			}
		})
	}
}

func TestManagerExecuteStream_Late429InvalidatesWithoutReplayingPayload(t *testing.T) {
	model := "gemini-3-flash"
	m, selector, exec := newAffinityFailoverManager(t, model, 2)
	exec.lateError = true
	exec.failures["affinity-auth-0"] = &Error{HTTPStatus: http.StatusTooManyRequests, Message: "RESOURCE_EXHAUSTED"}
	selector.cache.Set("failed-session", "affinity-auth-0")
	result, err := m.ExecuteStream(context.Background(), []string{"antigravity"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	var chunks []cliproxyexecutor.StreamChunk
	for chunk := range result.Chunks {
		chunks = append(chunks, chunk)
	}
	if len(chunks) != 2 || string(chunks[0].Payload) != "affinity-auth-0" || statusCodeFromError(chunks[1].Err) != http.StatusTooManyRequests {
		t.Fatalf("unexpected committed stream: %v", chunks)
	}
	if got := exec.StreamCalls(); !reflect.DeepEqual(got, []string{"affinity-auth-0"}) {
		t.Fatalf("committed stream should not replay, attempts=%v", got)
	}
	if _, exists := selector.cache.Get("failed-session"); exists {
		t.Fatal("late 429 did not invalidate affinity")
	}
}
