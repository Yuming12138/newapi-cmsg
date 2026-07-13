package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestManagerDispatchAuditRecordsSuccessfulExecution(t *testing.T) {
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.executors["codex"] = schedulerTestExecutor{}

	model := "gpt-5.4-mini"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("auth-a", "codex", []*registry.ModelInfo{{ID: model}})
	reg.RegisterClient("auth-disabled", "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() {
		reg.UnregisterClient("auth-a")
		reg.UnregisterClient("auth-disabled")
	})

	if _, errRegister := manager.Register(context.Background(), &Auth{
		ID:       "auth-a",
		Provider: "codex",
		Label:    "Pro account",
	}); errRegister != nil {
		t.Fatalf("Register(auth-a) error = %v", errRegister)
	}
	if _, errRegister := manager.Register(context.Background(), &Auth{
		ID:       "auth-disabled",
		Provider: "codex",
		Disabled: true,
		Label:    "Disabled account",
	}); errRegister != nil {
		t.Fatalf("Register(auth-disabled) error = %v", errRegister)
	}

	ctx := logging.WithRequestID(context.Background(), "req-audit-1")
	if _, errExec := manager.Execute(ctx, []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); errExec != nil {
		t.Fatalf("Execute() error = %v", errExec)
	}

	got := manager.RecentDispatchAudits(10)
	if len(got) != 1 {
		t.Fatalf("RecentDispatchAudits len = %d, want 1: %#v", len(got), got)
	}
	record := got[0]
	if record.RequestID != "req-audit-1" || record.Operation != "execute" || record.Model != model {
		t.Fatalf("record identity = %#v", record)
	}
	if record.Success == nil || *record.Success != true {
		t.Fatalf("record success = %#v", record.Success)
	}
	if len(record.Attempts) != 1 || record.Attempts[0].Account != "Pro account" {
		t.Fatalf("record attempts = %#v", record.Attempts)
	}
	if record.Attempts[0].Success == nil || *record.Attempts[0].Success != true {
		t.Fatalf("attempt success = %#v", record.Attempts[0].Success)
	}
	if len(record.Candidates) != 2 {
		t.Fatalf("record candidates len = %d, want 2: %#v", len(record.Candidates), record.Candidates)
	}
	if record.Candidates[0].State != "available" || !record.Candidates[0].Schedulable {
		t.Fatalf("first candidate = %#v", record.Candidates[0])
	}
	if record.Candidates[1].State != "manual_disabled" || record.Candidates[1].Schedulable {
		t.Fatalf("second candidate = %#v", record.Candidates[1])
	}
}

func TestManagerDispatchAuditRecordsPromptCacheAffinity(t *testing.T) {
	manager := NewManager(nil, NewSessionAffinitySelector(&RoundRobinSelector{}), nil)
	manager.executors["codex"] = schedulerTestExecutor{}

	model := "gpt-5.6"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("auth-affinity", "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient("auth-affinity") })
	if _, errRegister := manager.Register(context.Background(), &Auth{ID: "auth-affinity", Provider: "codex", Label: "Affinity account"}); errRegister != nil {
		t.Fatalf("Register(auth-affinity) error = %v", errRegister)
	}

	payload := []byte(`{"prompt_cache_key":"thread-audit-sensitive"}`)
	if _, errExec := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{OriginalRequest: payload}); errExec != nil {
		t.Fatalf("Execute() error = %v", errExec)
	}

	got := manager.RecentDispatchAudits(1)
	if len(got) != 1 || got[0].Affinity == nil {
		t.Fatalf("affinity audit = %#v", got)
	}
	affinity := got[0].Affinity
	if affinity.Source != "prompt-cache" || affinity.Event != "new_binding" {
		t.Fatalf("affinity identity = %#v", affinity)
	}
	if len(affinity.Fingerprint) != 12 || strings.Contains(affinity.Fingerprint, "thread-audit-sensitive") {
		t.Fatalf("affinity fingerprint = %q", affinity.Fingerprint)
	}
	if affinity.SelectedAccount != "Affinity account" || affinity.SelectedAuthIndex == "" {
		t.Fatalf("affinity selected auth = %#v", affinity)
	}
}

func TestManagerDispatchAuditRecordsQuotaReselectionReason(t *testing.T) {
	manager := NewManager(nil, NewSessionAffinitySelector(&RoundRobinSelector{}), nil)
	manager.executors["codex"] = schedulerTestExecutor{}

	model := "gpt-5.6"
	reg := registry.GetGlobalRegistry()
	for _, authID := range []string{"auth-a", "auth-b"} {
		reg.RegisterClient(authID, "codex", []*registry.ModelInfo{{ID: model}})
	}
	t.Cleanup(func() {
		reg.UnregisterClient("auth-a")
		reg.UnregisterClient("auth-b")
	})
	for _, auth := range []*Auth{
		{ID: "auth-a", Provider: "codex", Label: "Exhausted account"},
		{ID: "auth-b", Provider: "codex", Label: "Replacement account"},
	} {
		if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("Register(%s) error = %v", auth.ID, errRegister)
		}
	}

	payload := []byte(`{"prompt_cache_key":"thread-quota-reselection"}`)
	opts := cliproxyexecutor.Options{OriginalRequest: payload}
	if _, errExec := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, opts); errExec != nil {
		t.Fatalf("first Execute() error = %v", errExec)
	}
	retryAfter := time.Hour
	manager.MarkResult(context.Background(), Result{
		AuthID:     "auth-a",
		Provider:   "codex",
		Model:      model,
		Success:    false,
		Error:      &Error{HTTPStatus: 429, Message: "quota exceeded"},
		RetryAfter: &retryAfter,
	})

	if _, errExec := manager.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, opts); errExec != nil {
		t.Fatalf("second Execute() error = %v", errExec)
	}

	got := manager.RecentDispatchAudits(1)
	if len(got) != 1 || got[0].Affinity == nil {
		t.Fatalf("reselection audit = %#v", got)
	}
	affinity := got[0].Affinity
	if affinity.Event != "reselected" || affinity.BlockReason != "quota" || affinity.ResetAt == nil {
		t.Fatalf("reselection affinity = %#v", affinity)
	}
	if affinity.CachedAccount != "Exhausted account" || affinity.SelectedAccount != "Replacement account" {
		t.Fatalf("reselection accounts = %#v", affinity)
	}
}

func TestManagerDispatchAuditRetentionLimit(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	for i := 0; i < dispatchAuditLimit+5; i++ {
		audit := &dispatchAudit{record: DispatchAuditRecord{Operation: "execute"}}
		manager.finishDispatchAudit(audit, true, nil)
	}

	got := manager.RecentDispatchAudits(0)
	if len(got) != dispatchAuditLimit {
		t.Fatalf("RecentDispatchAudits() len = %d, want %d", len(got), dispatchAuditLimit)
	}
	if got[0].ID != uint64(dispatchAuditLimit+5) {
		t.Fatalf("newest audit ID = %d, want %d", got[0].ID, dispatchAuditLimit+5)
	}
	if got[len(got)-1].ID != 6 {
		t.Fatalf("oldest retained audit ID = %d, want 6", got[len(got)-1].ID)
	}
}
