package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type dispatchAuditTestExecutor struct{}

func (dispatchAuditTestExecutor) Identifier() string { return "codex" }

func (dispatchAuditTestExecutor) Execute(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (dispatchAuditTestExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}

func (dispatchAuditTestExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}

func (dispatchAuditTestExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (dispatchAuditTestExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestGetDispatchAudits(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(dispatchAuditTestExecutor{})
	model := "gpt-5.4-mini"
	reg := registry.GetGlobalRegistry()
	reg.RegisterClient("auth-a", "codex", []*registry.ModelInfo{{ID: model}})
	t.Cleanup(func() { reg.UnregisterClient("auth-a") })

	if _, errRegister := manager.Register(context.Background(), &coreauth.Auth{ID: "auth-a", Provider: "codex"}); errRegister != nil {
		t.Fatalf("Register(auth-a) error = %v", errRegister)
	}
	ctx := logging.WithRequestID(context.Background(), "req-management-audit")
	if _, errExec := manager.Execute(ctx, []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); errExec != nil {
		t.Fatalf("Execute() error = %v", errExec)
	}
	ctxOther := logging.WithRequestID(context.Background(), "req-management-other")
	if _, errExec := manager.Execute(ctxOther, []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{}); errExec != nil {
		t.Fatalf("Execute(other) error = %v", errExec)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/dispatch-audits?request_id=req-management-audit&limit=1", nil)

	h.GetDispatchAudits(ginCtx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("decode payload: %v", errUnmarshal)
	}
	dispatches, ok := payload["dispatches"].([]any)
	if !ok || len(dispatches) != 1 {
		t.Fatalf("dispatches = %#v", payload["dispatches"])
	}
	entry, ok := dispatches[0].(map[string]any)
	if !ok {
		t.Fatalf("dispatch entry = %#v", dispatches[0])
	}
	if entry["request_id"] != "req-management-audit" {
		t.Fatalf("request_id = %#v", entry["request_id"])
	}
}
