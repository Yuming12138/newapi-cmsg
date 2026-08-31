package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type homeDispatchErrorTestDispatcher struct {
	raw []byte
}

func (d homeDispatchErrorTestDispatcher) HeartbeatOK() bool {
	return true
}

func (d homeDispatchErrorTestDispatcher) RPopAuth(context.Context, string, string, http.Header, int) ([]byte, error) {
	return append([]byte(nil), d.raw...), nil
}

func withHomeDispatchErrorTestDispatcher(t *testing.T, raw string) {
	t.Helper()
	previous := currentHomeDispatcher
	currentHomeDispatcher = func() homeAuthDispatcher {
		return homeDispatchErrorTestDispatcher{raw: []byte(raw)}
	}
	t.Cleanup(func() {
		currentHomeDispatcher = previous
	})
}

func TestPickNextViaHomeMapsDirectModelCooldownToRateLimit(t *testing.T) {
	withHomeDispatchErrorTestDispatcher(t, `{"error":{"code":"model_cooldown","message":"all credentials are cooling","model":"gpt-5.6-sol","provider":"codex","reset_seconds":7,"reset_time":"7s"}}`)

	manager := NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})

	_, _, _, errPick := manager.pickNextViaHome(context.Background(), "gpt-5.6-sol", cliproxyexecutor.Options{}, nil)
	var cooldownErr *modelCooldownError
	if !errors.As(errPick, &cooldownErr) {
		t.Fatalf("pickNextViaHome() error = %T %v, want modelCooldownError", errPick, errPick)
	}
	if got := cooldownErr.StatusCode(); got != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", got, http.StatusTooManyRequests)
	}
	if got := cooldownErr.Headers().Get("Retry-After"); got != "7" {
		t.Fatalf("Retry-After = %q, want 7", got)
	}
}

func TestPickNextViaHomeUnwrapsLegacyNestedModelCooldown(t *testing.T) {
	raw := `{"error":{"type":"error","message":"{\"error\":{\"code\":\"model_cooldown\",\"message\":\"all credentials are cooling\",\"model\":\"gpt-5.6-sol\",\"provider\":\"codex\",\"reset_seconds\":4}}"}}`
	withHomeDispatchErrorTestDispatcher(t, raw)

	manager := NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})

	_, _, _, errPick := manager.pickNextViaHome(context.Background(), "requested-model", cliproxyexecutor.Options{}, nil)
	var cooldownErr *modelCooldownError
	if !errors.As(errPick, &cooldownErr) {
		t.Fatalf("pickNextViaHome() error = %T %v, want modelCooldownError", errPick, errPick)
	}
	if got := cooldownErr.Headers().Get("Retry-After"); got != "4" {
		t.Fatalf("Retry-After = %q, want 4", got)
	}
	var body struct {
		Error struct {
			Code         string `json:"code"`
			Model        string `json:"model"`
			ResetSeconds int    `json:"reset_seconds"`
		} `json:"error"`
	}
	if errUnmarshal := json.Unmarshal([]byte(cooldownErr.Error()), &body); errUnmarshal != nil {
		t.Fatalf("cooldown body is invalid JSON: %v", errUnmarshal)
	}
	if body.Error.Code != "model_cooldown" || body.Error.Model != "gpt-5.6-sol" || body.Error.ResetSeconds != 4 {
		t.Fatalf("cooldown body = %s", cooldownErr.Error())
	}
}

func TestPickNextViaHomeMapsRateLimitCodeToRateLimit(t *testing.T) {
	withHomeDispatchErrorTestDispatcher(t, `{"error":{"type":"rate_limit","message":"try later"}}`)

	manager := NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{Home: internalconfig.HomeConfig{Enabled: true}})

	_, _, _, errPick := manager.pickNextViaHome(context.Background(), "gpt-5.6-sol", cliproxyexecutor.Options{}, nil)
	var authErr *Error
	if !errors.As(errPick, &authErr) {
		t.Fatalf("pickNextViaHome() error = %T %v, want *Error", errPick, errPick)
	}
	if authErr.Code != "rate_limit" || authErr.StatusCode() != http.StatusTooManyRequests || !authErr.Retryable {
		t.Fatalf("rate limit error = %+v", authErr)
	}
}
