package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

const cmsgHomeRefreshProvider = "cmsg-home-refresh-test"

type cmsgHomeRefreshDispatcher struct {
	calls atomic.Int32
}

func (*cmsgHomeRefreshDispatcher) HeartbeatOK() bool { return true }

func (d *cmsgHomeRefreshDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	d.calls.Add(1)
	return json.Marshal(homeAuthDispatchResponse{Auth: Auth{
		ID:       "cmsg-home-auth",
		Index:    "cmsg-home-index",
		Provider: cmsgHomeRefreshProvider,
		Status:   StatusActive,
		Attributes: map[string]string{
			AttributeAuthKind: AuthKindOAuth,
		},
		Metadata: map[string]any{"access_token": "stale-token"},
	}})
}

type cmsgHomeRefreshExecutor struct {
	streamMode      string
	keepStale       bool
	requirePrepared bool
	refreshStarted  chan struct{}
	refreshRelease  chan struct{}
	executeCalls    atomic.Int32
	countCalls      atomic.Int32
	streamCalls     atomic.Int32
	refreshCalls    atomic.Int32
	prepareCalls    atomic.Int32
}

func (*cmsgHomeRefreshExecutor) Identifier() string { return cmsgHomeRefreshProvider }

func (e *cmsgHomeRefreshExecutor) Execute(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.executeCalls.Add(1)
	if cmsgHomeAccessToken(auth) == "stale-token" {
		return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusUnauthorized, Message: "expired access token"}
	}
	if e.requirePrepared && auth.Metadata["project_id"] != "prepared-project" {
		return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusBadRequest, Message: "missing prepared auth"}
	}
	return cliproxyexecutor.Response{Payload: []byte("ok")}, nil
}

func (e *cmsgHomeRefreshExecutor) ExecuteStream(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	e.streamCalls.Add(1)
	if cmsgHomeAccessToken(auth) == "stale-token" {
		if e.streamMode == "bootstrap" {
			chunks := make(chan cliproxyexecutor.StreamChunk, 1)
			chunks <- cliproxyexecutor.StreamChunk{Err: &Error{HTTPStatus: http.StatusUnauthorized, Message: "expired access token"}}
			close(chunks)
			return &cliproxyexecutor.StreamResult{Chunks: chunks}, nil
		}
		return nil, &Error{HTTPStatus: http.StatusUnauthorized, Message: "expired access token"}
	}
	if e.requirePrepared && auth.Metadata["project_id"] != "prepared-project" {
		return nil, &Error{HTTPStatus: http.StatusBadRequest, Message: "missing prepared auth"}
	}
	chunks := make(chan cliproxyexecutor.StreamChunk, 1)
	chunks <- cliproxyexecutor.StreamChunk{Payload: []byte("ok")}
	close(chunks)
	return &cliproxyexecutor.StreamResult{Chunks: chunks}, nil
}

func (e *cmsgHomeRefreshExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	if e.refreshCalls.Add(1) == 1 && e.refreshStarted != nil {
		close(e.refreshStarted)
	}
	if e.refreshRelease != nil {
		<-e.refreshRelease
	}
	updated := auth.Clone()
	if !e.keepStale {
		updated.Metadata["access_token"] = "fresh-token"
	}
	if e.requirePrepared {
		delete(updated.Metadata, "project_id")
	}
	return updated, nil
}

func (e *cmsgHomeRefreshExecutor) CountTokens(_ context.Context, auth *Auth, _ cliproxyexecutor.Request, _ cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	e.countCalls.Add(1)
	if cmsgHomeAccessToken(auth) == "stale-token" {
		return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusUnauthorized, Message: "expired access token"}
	}
	if e.requirePrepared && auth.Metadata["project_id"] != "prepared-project" {
		return cliproxyexecutor.Response{}, &Error{HTTPStatus: http.StatusBadRequest, Message: "missing prepared auth"}
	}
	return cliproxyexecutor.Response{Payload: []byte("ok")}, nil
}

func (*cmsgHomeRefreshExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func (e *cmsgHomeRefreshExecutor) ShouldPrepareRequestAuth(auth *Auth) bool {
	return e.requirePrepared && auth != nil && auth.Metadata["project_id"] != "prepared-project"
}

func (e *cmsgHomeRefreshExecutor) PrepareRequestAuth(_ context.Context, auth *Auth) (*Auth, error) {
	e.prepareCalls.Add(1)
	updated := auth.Clone()
	updated.Metadata["project_id"] = "prepared-project"
	return updated, nil
}

func cmsgHomeAccessToken(auth *Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	value, _ := auth.Metadata["access_token"].(string)
	return value
}

func newCMSGHomeRefreshManager(t *testing.T, dispatcher *cmsgHomeRefreshDispatcher, executor *cmsgHomeRefreshExecutor) *Manager {
	t.Helper()
	oldDispatcher := currentHomeDispatcher
	currentHomeDispatcher = func() homeAuthDispatcher { return dispatcher }
	t.Cleanup(func() { currentHomeDispatcher = oldDispatcher })
	manager := NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})
	manager.RegisterExecutor(executor)
	return manager
}

func TestCMSGHomeUnauthorizedRefreshesSameAuthAndReprepares(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Manager) error
	}{
		{name: "execute", run: func(m *Manager) error {
			_, err := m.Execute(context.Background(), []string{cmsgHomeRefreshProvider}, cliproxyexecutor.Request{Model: "model-a"}, cliproxyexecutor.Options{})
			return err
		}},
		{name: "count", run: func(m *Manager) error {
			_, err := m.ExecuteCount(context.Background(), []string{cmsgHomeRefreshProvider}, cliproxyexecutor.Request{Model: "model-a"}, cliproxyexecutor.Options{})
			return err
		}},
		{name: "stream", run: func(m *Manager) error {
			result, err := m.ExecuteStream(context.Background(), []string{cmsgHomeRefreshProvider}, cliproxyexecutor.Request{Model: "model-a"}, cliproxyexecutor.Options{Stream: true})
			if err != nil {
				return err
			}
			for chunk := range result.Chunks {
				if chunk.Err != nil {
					return chunk.Err
				}
			}
			return nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dispatcher := &cmsgHomeRefreshDispatcher{}
			executor := &cmsgHomeRefreshExecutor{requirePrepared: true}
			manager := newCMSGHomeRefreshManager(t, dispatcher, executor)
			if err := test.run(manager); err != nil {
				t.Fatalf("execution error = %v", err)
			}
			if dispatcher.calls.Load() != 1 || executor.refreshCalls.Load() != 1 {
				t.Fatalf("dispatch calls = %d, refresh calls = %d, want 1 and 1", dispatcher.calls.Load(), executor.refreshCalls.Load())
			}
			if executor.prepareCalls.Load() != 2 {
				t.Fatalf("prepare calls = %d, want initial and refreshed preparation", executor.prepareCalls.Load())
			}
		})
	}
}

func TestCMSGHomeUnauthorizedStreamBootstrapRefreshes(t *testing.T) {
	dispatcher := &cmsgHomeRefreshDispatcher{}
	executor := &cmsgHomeRefreshExecutor{streamMode: "bootstrap"}
	manager := newCMSGHomeRefreshManager(t, dispatcher, executor)
	result, err := manager.ExecuteStream(context.Background(), []string{cmsgHomeRefreshProvider}, cliproxyexecutor.Request{Model: "model-a"}, cliproxyexecutor.Options{Stream: true})
	if err != nil {
		t.Fatalf("ExecuteStream error = %v", err)
	}
	var payload string
	for chunk := range result.Chunks {
		if chunk.Err != nil {
			t.Fatalf("stream chunk error = %v", chunk.Err)
		}
		payload += string(chunk.Payload)
	}
	if payload != "ok" || executor.streamCalls.Load() != 2 || executor.refreshCalls.Load() != 1 {
		t.Fatalf("payload = %q, stream calls = %d, refresh calls = %d", payload, executor.streamCalls.Load(), executor.refreshCalls.Load())
	}
}

func TestCMSGHomeUnauthorizedRefreshIsAttemptedAtMostOnce(t *testing.T) {
	dispatcher := &cmsgHomeRefreshDispatcher{}
	executor := &cmsgHomeRefreshExecutor{keepStale: true}
	manager := newCMSGHomeRefreshManager(t, dispatcher, executor)
	_, err := manager.Execute(context.Background(), []string{cmsgHomeRefreshProvider}, cliproxyexecutor.Request{Model: "model-a"}, cliproxyexecutor.Options{})
	if statusCodeFromError(err) != http.StatusUnauthorized {
		t.Fatalf("Execute error = %v, want 401", err)
	}
	if executor.refreshCalls.Load() != 1 || executor.executeCalls.Load() != 2 {
		t.Fatalf("refresh calls = %d, execute calls = %d, want 1 and 2", executor.refreshCalls.Load(), executor.executeCalls.Load())
	}
}

func TestCMSGHomeConcurrentUnauthorizedReusesRefreshedToken(t *testing.T) {
	dispatcher := &cmsgHomeRefreshDispatcher{}
	executor := &cmsgHomeRefreshExecutor{refreshStarted: make(chan struct{}), refreshRelease: make(chan struct{})}
	manager := newCMSGHomeRefreshManager(t, dispatcher, executor)
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	run := func() {
		defer wait.Done()
		_, err := manager.Execute(context.Background(), []string{cmsgHomeRefreshProvider}, cliproxyexecutor.Request{Model: "model-a"}, cliproxyexecutor.Options{})
		errs <- err
	}
	wait.Add(1)
	go run()
	select {
	case <-executor.refreshStarted:
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}
	wait.Add(1)
	go run()
	close(executor.refreshRelease)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent execution error = %v", err)
		}
	}
	if executor.refreshCalls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want one CAS refresh", executor.refreshCalls.Load())
	}
}
