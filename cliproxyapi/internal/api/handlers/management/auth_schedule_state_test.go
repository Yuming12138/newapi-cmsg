package management

import (
	"net/http"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestDeriveAuthScheduleStateManualDisabled(t *testing.T) {
	got := deriveAuthScheduleState(&coreauth.Auth{Disabled: true, Status: coreauth.StatusActive}, time.Now())
	if got.State != authScheduleStateManualDisabled || got.Schedulable {
		t.Fatalf("state = %+v", got)
	}
}

func TestDeriveAuthScheduleStateAuthInvalid(t *testing.T) {
	got := deriveAuthScheduleState(&coreauth.Auth{
		Status:    coreauth.StatusError,
		LastError: &coreauth.Error{HTTPStatus: http.StatusUnauthorized, Message: "unauthorized"},
	}, time.Now())
	if got.State != authScheduleStateAuthInvalid || got.Retryable || got.Schedulable {
		t.Fatalf("state = %+v", got)
	}
}

func TestDeriveAuthScheduleStateQuotaWindows(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		reason string
		want   string
	}{
		{name: "five hour", reason: "quota_5h_exhausted", want: authScheduleStateQuota5hExhausted},
		{name: "weekly", reason: "quota_7d_exhausted", want: authScheduleStateQuota7dExhausted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveAuthScheduleState(&coreauth.Auth{
				Status:         coreauth.StatusActive,
				NextRetryAfter: now.Add(time.Hour),
				Quota: coreauth.QuotaState{
					Exceeded:      true,
					Reason:        tt.reason,
					NextRecoverAt: now.Add(time.Hour),
				},
			}, now)
			if got.State != tt.want || !got.Retryable || got.Schedulable || !got.ResetAt.After(now) {
				t.Fatalf("state = %+v", got)
			}
		})
	}
}

func TestDeriveAuthScheduleStateCooldown(t *testing.T) {
	now := time.Now()
	got := deriveAuthScheduleState(&coreauth.Auth{
		Status:         coreauth.StatusActive,
		Unavailable:    true,
		NextRetryAfter: now.Add(10 * time.Minute),
	}, now)
	if got.State != authScheduleStateCooldown || !got.Retryable || got.Schedulable {
		t.Fatalf("state = %+v", got)
	}
}

func TestDeriveAuthScheduleStateProtectedReserve(t *testing.T) {
	got := deriveAuthScheduleState(&coreauth.Auth{
		Status: coreauth.StatusActive,
		Quota: coreauth.QuotaState{
			Exceeded: true,
			Reason:   "protected_reserve_reached",
		},
	}, time.Now())
	if got.State != authScheduleStateProtectedReserve || got.Retryable || got.Schedulable {
		t.Fatalf("state = %+v", got)
	}
}

func TestDeriveAuthScheduleStateAvailable(t *testing.T) {
	got := deriveAuthScheduleState(&coreauth.Auth{Status: coreauth.StatusActive}, time.Now())
	if got.State != authScheduleStateAvailable || !got.Schedulable {
		t.Fatalf("state = %+v", got)
	}
}
