package auth

import (
	"context"
	"testing"

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
