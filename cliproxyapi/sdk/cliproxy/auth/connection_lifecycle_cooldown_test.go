package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestManager_MarkResult_ConnectionLifecycleDoesNotCooldown(t *testing.T) {
	previous := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(previous) })

	prevTransient := transientErrorCooldownSeconds.Load()
	SetTransientErrorCooldownSeconds(5)
	t.Cleanup(func() { transientErrorCooldownSeconds.Store(prevTransient) })

	cases := []struct {
		name string
		err  *Error
	}{
		{name: "websocket 1000", err: &Error{Message: "websocket: close 1000 (normal)"}},
		{name: "websocket 1001", err: &Error{Message: "websocket: close 1001 (going away)"}},
		{name: "websocket 1006", err: &Error{Message: "websocket: close 1006 (abnormal closure): unexpected EOF"}},
		{name: "context canceled", err: &Error{Message: "context canceled"}},
		{name: "context deadline exceeded", err: &Error{Message: "context deadline exceeded"}},
		{name: "unexpected EOF", err: &Error{Message: "unexpected EOF"}},
		{name: "plain EOF", err: &Error{Message: "EOF"}},
		{name: "wrapped unexpected EOF", err: &Error{Message: "read tcp 127.0.0.1:1->127.0.0.1:2: unexpected EOF"}},
		{name: "typed canceled", err: resultErrorFromError(context.Canceled)},
		{name: "typed deadline", err: resultErrorFromError(context.DeadlineExceeded)},
		{name: "url canceled", err: resultErrorFromError(&url.Error{Op: "Post", URL: "https://example.com", Err: context.Canceled})},
		{name: "url deadline", err: resultErrorFromError(&url.Error{Op: "Post", URL: "https://example.com", Err: context.DeadlineExceeded})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(nil, nil, nil)
			auth := &Auth{ID: "auth-lifecycle-" + tc.name, Provider: "codex"}
			if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
				t.Fatalf("register auth: %v", errRegister)
			}

			model := "gpt-5.6-sol"
			m.MarkResult(context.Background(), Result{
				AuthID:   auth.ID,
				Provider: auth.Provider,
				Model:    model,
				Success:  false,
				Error:    tc.err,
			})

			assertNoCooldown(t, m, auth.ID, model)
		})
	}
}

func TestManager_MarkResult_ConnectionLifecycleAuthLevelDoesNotCooldown(t *testing.T) {
	m := NewManager(nil, nil, nil)
	auth := &Auth{ID: "auth-lifecycle-auth-level", Provider: "codex"}
	if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	m.MarkResult(context.Background(), Result{
		AuthID:   auth.ID,
		Provider: auth.Provider,
		Success:  false,
		Error:    &Error{Message: "websocket: close 1006 (abnormal closure): unexpected EOF"},
	})

	updated, ok := m.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if updated.Unavailable || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("auth-level lifecycle error created cooldown: %#v", updated)
	}
}

func TestManager_MarkResult_HTTPStatusWithLifecycleTextStillCooldowns(t *testing.T) {
	previous := quotaCooldownDisabled.Load()
	quotaCooldownDisabled.Store(false)
	t.Cleanup(func() { quotaCooldownDisabled.Store(previous) })

	prevTransient := transientErrorCooldownSeconds.Load()
	SetTransientErrorCooldownSeconds(5)
	t.Cleanup(func() { transientErrorCooldownSeconds.Store(prevTransient) })

	for _, tc := range []struct {
		name       string
		httpStatus int
		message    string
	}{
		{name: "401 unexpected EOF", httpStatus: http.StatusUnauthorized, message: "unexpected EOF"},
		{name: "429 context canceled", httpStatus: http.StatusTooManyRequests, message: "context canceled"},
		{name: "500 unexpected EOF", httpStatus: http.StatusInternalServerError, message: "unexpected EOF"},
		{name: "500 websocket 1006 text", httpStatus: http.StatusInternalServerError, message: "websocket: close 1006 (abnormal closure): unexpected EOF"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(nil, nil, nil)
			auth := &Auth{ID: "auth-status-" + tc.name, Provider: "codex"}
			if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
				t.Fatalf("register auth: %v", errRegister)
			}

			model := "gpt-5.6-sol"
			before := time.Now()
			m.MarkResult(context.Background(), Result{
				AuthID:   auth.ID,
				Provider: auth.Provider,
				Model:    model,
				Success:  false,
				Error:    &Error{HTTPStatus: tc.httpStatus, Message: tc.message},
			})

			updated, _ := m.GetByID(auth.ID)
			state := updated.ModelStates[model]
			if state == nil || state.NextRetryAfter.IsZero() {
				t.Fatalf("HTTP status %d with lifecycle text did not cool", tc.httpStatus)
			}
			if tc.httpStatus == http.StatusInternalServerError && state.NextRetryAfter.Before(before.Add(4*time.Second)) {
				t.Fatalf("expected about 5s transient cooldown, got %v", state.NextRetryAfter)
			}
		})
	}
}

func TestResultErrorFromError_ConnectionLifecycleClassification(t *testing.T) {
	cases := []error{
		context.Canceled,
		context.DeadlineExceeded,
		io.EOF,
		io.ErrUnexpectedEOF,
		&url.Error{Op: "Post", URL: "https://example.com", Err: context.Canceled},
		&websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "normal"},
		&websocket.CloseError{Code: websocket.CloseGoingAway, Text: "bye"},
		&websocket.CloseError{Code: websocket.CloseAbnormalClosure, Text: "unexpected EOF"},
		fmt.Errorf("wrap: %w", io.ErrUnexpectedEOF),
		errors.New("websocket: close 1006 (abnormal closure): unexpected EOF"),
	}
	for _, err := range cases {
		got := resultErrorFromError(err)
		if got == nil || got.Code != connectionLifecycleErrorCode {
			t.Fatalf("resultErrorFromError(%v) = %#v, want lifecycle code", err, got)
		}
		if got.IsRequestScoped() || !shouldSkipCredentialCooldown(got) {
			t.Fatalf("lifecycle classification is incorrect: %#v", got)
		}
		if isRequestInvalidError(err) {
			t.Fatalf("lifecycle error must remain eligible for credential fallback: %v", err)
		}
	}
}

func TestIsConnectionLifecycleError_StatusBearingErrorsStayCoolable(t *testing.T) {
	for _, err := range []error{
		&statusBearingError{status: http.StatusUnauthorized, msg: "unexpected EOF"},
		&statusBearingError{status: http.StatusTooManyRequests, msg: "context canceled"},
		&statusBearingError{status: http.StatusInternalServerError, msg: "unexpected EOF"},
	} {
		if isConnectionLifecycleError(err) {
			t.Fatalf("status-bearing error was misclassified: %v", err)
		}
		if shouldSkipCredentialCooldown(resultErrorFromError(err)) {
			t.Fatalf("status-bearing error skipped cooldown: %v", err)
		}
	}
}

type statusBearingError struct {
	status int
	msg    string
}

func (e *statusBearingError) Error() string   { return e.msg }
func (e *statusBearingError) StatusCode() int { return e.status }

func assertNoCooldown(t *testing.T, m *Manager, authID, model string) {
	t.Helper()
	updated, ok := m.GetByID(authID)
	if !ok || updated == nil {
		t.Fatal("expected auth to be present")
	}
	if updated.Unavailable || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("connection lifecycle error created auth cooldown: %#v", updated)
	}
	if state := updated.ModelStates[model]; state != nil {
		if state.Unavailable || !state.NextRetryAfter.IsZero() {
			t.Fatalf("connection lifecycle error created model cooldown: %#v", state)
		}
	}
}
