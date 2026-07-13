package auth

import (
	"context"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

const dispatchAuditLimit = 2000

type dispatchAuditContextKey struct{}

// DispatchAuditCandidate describes one credential considered for a request.
type DispatchAuditCandidate struct {
	AuthIndex   string     `json:"auth_index,omitempty"`
	Account     string     `json:"account,omitempty"`
	Provider    string     `json:"provider,omitempty"`
	State       string     `json:"state"`
	Reason      string     `json:"reason,omitempty"`
	Schedulable bool       `json:"schedulable"`
	Priority    int        `json:"priority,omitempty"`
	ResetAt     *time.Time `json:"reset_at,omitempty"`
}

// DispatchAuditAttempt describes one selected credential attempt.
type DispatchAuditAttempt struct {
	AuthIndex      string     `json:"auth_index,omitempty"`
	Account        string     `json:"account,omitempty"`
	Provider       string     `json:"provider,omitempty"`
	Model          string     `json:"model,omitempty"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	DurationMillis *int64     `json:"duration_ms,omitempty"`
	Success        *bool      `json:"success,omitempty"`
	Error          *Error     `json:"error,omitempty"`
}

// DispatchAuditAffinity describes the session binding decision without exposing the raw session identifier.
type DispatchAuditAffinity struct {
	Source            string     `json:"source,omitempty"`
	Fingerprint       string     `json:"fingerprint,omitempty"`
	Event             string     `json:"event,omitempty"`
	CachedAuthIndex   string     `json:"cached_auth_index,omitempty"`
	CachedAccount     string     `json:"cached_account,omitempty"`
	SelectedAuthIndex string     `json:"selected_auth_index,omitempty"`
	SelectedAccount   string     `json:"selected_account,omitempty"`
	BlockReason       string     `json:"block_reason,omitempty"`
	ResetAt           *time.Time `json:"reset_at,omitempty"`
}

// DispatchAuditRecord is a request-scoped routing trace for management diagnostics.
type DispatchAuditRecord struct {
	ID                 uint64                   `json:"id"`
	RequestID          string                   `json:"request_id,omitempty"`
	Operation          string                   `json:"operation"`
	Providers          []string                 `json:"providers,omitempty"`
	Model              string                   `json:"model,omitempty"`
	Stream             bool                     `json:"stream"`
	StartedAt          time.Time                `json:"started_at"`
	FinishedAt         *time.Time               `json:"finished_at,omitempty"`
	DurationMillis     *int64                   `json:"duration_ms,omitempty"`
	FirstPayloadMillis *int64                   `json:"first_payload_ms,omitempty"`
	Success            *bool                    `json:"success,omitempty"`
	Error              *Error                   `json:"error,omitempty"`
	Candidates         []DispatchAuditCandidate `json:"candidates,omitempty"`
	Attempts           []DispatchAuditAttempt   `json:"attempts,omitempty"`
	Affinity           *DispatchAuditAffinity   `json:"affinity,omitempty"`
}

type dispatchAudit struct {
	mu        sync.Mutex
	finish    sync.Once
	record    DispatchAuditRecord
	finalized bool
}

func (m *Manager) beginDispatchAudit(ctx context.Context, operation string, providers []string, model string, stream bool, opts cliproxyexecutor.Options) (context.Context, *dispatchAudit) {
	if m == nil {
		return ctx, nil
	}
	audit := &dispatchAudit{
		record: DispatchAuditRecord{
			RequestID:  logging.GetRequestID(ctx),
			Operation:  strings.TrimSpace(operation),
			Providers:  append([]string(nil), providers...),
			Model:      strings.TrimSpace(model),
			Stream:     stream,
			StartedAt:  time.Now(),
			Candidates: m.dispatchAuditCandidates(providers, model, opts),
		},
	}
	if audit.record.Operation == "" {
		audit.record.Operation = "execute"
	}
	return context.WithValue(ctx, dispatchAuditContextKey{}, audit), audit
}

func dispatchAuditFromContext(ctx context.Context) *dispatchAudit {
	if ctx == nil {
		return nil
	}
	audit, _ := ctx.Value(dispatchAuditContextKey{}).(*dispatchAudit)
	return audit
}

func recordSessionAffinityAudit(ctx context.Context, affinity DispatchAuditAffinity) {
	audit := dispatchAuditFromContext(ctx)
	if audit == nil {
		return
	}
	audit.mu.Lock()
	affinityCopy := affinity
	audit.record.Affinity = &affinityCopy
	audit.mu.Unlock()
}

func authAuditIndex(auth *Auth) string {
	if auth == nil {
		return ""
	}
	auth.EnsureIndex()
	return auth.Index
}

func (m *Manager) recordDispatchAuditSelection(ctx context.Context, auth *Auth, provider string, model string) {
	audit := dispatchAuditFromContext(ctx)
	if audit == nil || auth == nil {
		return
	}
	authCopy := auth.Clone()
	authCopy.EnsureIndex()
	attempt := DispatchAuditAttempt{
		AuthIndex: authCopy.Index,
		Account:   dispatchAuditAccountLabel(authCopy),
		Provider:  strings.TrimSpace(provider),
		Model:     strings.TrimSpace(model),
		StartedAt: time.Now(),
	}
	audit.mu.Lock()
	audit.record.Attempts = append(audit.record.Attempts, attempt)
	audit.mu.Unlock()
}

func (m *Manager) recordDispatchAuditResult(ctx context.Context, result Result) {
	audit := dispatchAuditFromContext(ctx)
	if audit == nil || result.AuthID == "" {
		return
	}
	now := time.Now()
	success := result.Success
	var duration *int64
	audit.mu.Lock()
	defer audit.mu.Unlock()
	for idx := len(audit.record.Attempts) - 1; idx >= 0; idx-- {
		attempt := &audit.record.Attempts[idx]
		if attempt.Success != nil {
			continue
		}
		if strings.TrimSpace(result.Provider) != "" && attempt.Provider != "" && attempt.Provider != strings.TrimSpace(result.Provider) {
			continue
		}
		if strings.TrimSpace(result.Model) != "" {
			attempt.Model = strings.TrimSpace(result.Model)
		}
		elapsed := now.Sub(attempt.StartedAt).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
		duration = &elapsed
		attempt.FinishedAt = &now
		attempt.DurationMillis = duration
		attempt.Success = &success
		attempt.Error = cloneError(result.Error)
		return
	}
}

func (m *Manager) recordDispatchAuditFirstPayload(ctx context.Context) {
	audit := dispatchAuditFromContext(ctx)
	if audit == nil {
		return
	}
	now := time.Now()
	audit.mu.Lock()
	defer audit.mu.Unlock()
	if audit.record.FirstPayloadMillis != nil {
		return
	}
	elapsed := now.Sub(audit.record.StartedAt).Milliseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	audit.record.FirstPayloadMillis = &elapsed
}

func (m *Manager) finishDispatchAudit(audit *dispatchAudit, success bool, err error) {
	if m == nil || audit == nil {
		return
	}
	audit.finish.Do(func() {
		now := time.Now()
		audit.mu.Lock()
		elapsed := now.Sub(audit.record.StartedAt).Milliseconds()
		if elapsed < 0 {
			elapsed = 0
		}
		audit.record.FinishedAt = &now
		audit.record.DurationMillis = &elapsed
		audit.record.Success = &success
		if err != nil {
			audit.record.Error = dispatchAuditError(err)
		}
		record := audit.record
		audit.finalized = true
		audit.mu.Unlock()

		m.dispatchAuditMu.Lock()
		m.dispatchAuditSeq++
		record.ID = m.dispatchAuditSeq
		m.dispatchAudits = append(m.dispatchAudits, record)
		if len(m.dispatchAudits) > dispatchAuditLimit {
			overflow := len(m.dispatchAudits) - dispatchAuditLimit
			for idx := 0; idx < overflow; idx++ {
				m.dispatchAudits[idx] = DispatchAuditRecord{}
			}
			m.dispatchAudits = m.dispatchAudits[overflow:]
		}
		m.dispatchAuditMu.Unlock()
	})
}

func (m *Manager) wrapDispatchAuditStreamResult(ctx context.Context, audit *dispatchAudit, result *cliproxyexecutor.StreamResult) *cliproxyexecutor.StreamResult {
	if m == nil || audit == nil || result == nil || result.Chunks == nil {
		return result
	}
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		success := true
		var finalErr error
		for chunk := range result.Chunks {
			if len(chunk.Payload) > 0 {
				m.recordDispatchAuditFirstPayload(ctx)
			}
			if chunk.Err != nil {
				success = false
				finalErr = chunk.Err
			}
			select {
			case <-ctx.Done():
				success = false
				finalErr = ctx.Err()
				m.finishDispatchAudit(audit, success, finalErr)
				return
			case out <- chunk:
			}
		}
		m.finishDispatchAudit(audit, success, finalErr)
	}()
	return &cliproxyexecutor.StreamResult{Headers: result.Headers, Chunks: out}
}

// RecentDispatchAudits returns the newest request-scoped dispatch traces first.
func (m *Manager) RecentDispatchAudits(limit int) []DispatchAuditRecord {
	if m == nil {
		return nil
	}
	if limit <= 0 || limit > dispatchAuditLimit {
		limit = dispatchAuditLimit
	}
	m.dispatchAuditMu.Lock()
	defer m.dispatchAuditMu.Unlock()
	start := len(m.dispatchAudits) - limit
	if start < 0 {
		start = 0
	}
	out := append([]DispatchAuditRecord(nil), m.dispatchAudits[start:]...)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (m *Manager) dispatchAuditCandidates(providers []string, model string, opts cliproxyexecutor.Options) []DispatchAuditCandidate {
	if m == nil {
		return nil
	}
	providerSet := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		key := strings.ToLower(strings.TrimSpace(provider))
		if key != "" {
			providerSet[key] = struct{}{}
		}
	}
	if len(providerSet) == 0 {
		return nil
	}
	disallowFree := disallowFreeAuthFromMetadata(opts.Metadata)
	now := time.Now()
	auths := m.List()
	candidates := make([]DispatchAuditCandidate, 0, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		provider := executorKeyFromAuth(auth)
		if _, ok := providerSet[provider]; !ok {
			continue
		}
		auth.EnsureIndex()
		candidate := DispatchAuditCandidate{
			AuthIndex: auth.Index,
			Account:   dispatchAuditAccountLabel(auth),
			Provider:  provider,
			Priority:  authPriority(auth),
		}
		switch {
		case auth.Disabled || auth.Status == StatusDisabled:
			candidate.State = "manual_disabled"
			candidate.Reason = "manual_disabled"
		case disallowFree && isFreeCodexAuth(auth):
			candidate.State = "skipped"
			candidate.Reason = "free_tier_blocked"
		case strings.TrimSpace(model) != "" && !m.authSupportsRouteModel(registry.GetGlobalRegistry(), auth, model):
			candidate.State = "skipped"
			candidate.Reason = "unsupported_model"
		default:
			checkModel := model
			if strings.TrimSpace(model) != "" {
				checkModel = m.selectionModelForAuth(auth, model)
			}
			blocked, reason, next := isAuthBlockedForModel(auth, checkModel, now)
			if !blocked {
				candidate.State = "available"
				candidate.Reason = "available"
				candidate.Schedulable = true
			} else {
				candidate.State, candidate.Reason = dispatchAuditBlockReason(reason)
				if !next.IsZero() {
					resetAt := next
					candidate.ResetAt = &resetAt
				}
			}
		}
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Schedulable != candidates[j].Schedulable {
			return candidates[i].Schedulable
		}
		if candidates[i].Priority != candidates[j].Priority {
			return candidates[i].Priority > candidates[j].Priority
		}
		return candidates[i].AuthIndex < candidates[j].AuthIndex
	})
	return candidates
}

func dispatchAuditBlockReason(reason blockReason) (string, string) {
	switch reason {
	case blockReasonCooldown:
		return "cooldown", "cooldown"
	case blockReasonDisabled:
		return "manual_disabled", "manual_disabled"
	default:
		return "blocked", "blocked"
	}
}

func dispatchAuditAccountLabel(auth *Auth) string {
	if auth == nil {
		return ""
	}
	for _, value := range []string{auth.Label, authEmailFromMetadata(auth)} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	if accountType, account := auth.AccountInfo(); accountType == "oauth" && strings.TrimSpace(account) != "" {
		return strings.TrimSpace(account)
	}
	if base := strings.TrimSpace(filepath.Base(auth.FileName)); base != "" && base != "." {
		return base
	}
	if auth.Index != "" {
		return "account " + suffixString(auth.Index, 6)
	}
	return ""
}

func suffixString(value string, length int) string {
	if length <= 0 || len(value) <= length {
		return value
	}
	return value[len(value)-length:]
}

func authEmailFromMetadata(auth *Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Metadata != nil {
		if value, ok := auth.Metadata["email"].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	if auth.Attributes != nil {
		if value := strings.TrimSpace(auth.Attributes["email"]); value != "" {
			return value
		}
		if value := strings.TrimSpace(auth.Attributes["account_email"]); value != "" {
			return value
		}
	}
	return ""
}

func dispatchAuditError(err error) *Error {
	if err == nil {
		return nil
	}
	if authErr, ok := err.(*Error); ok {
		return cloneError(authErr)
	}
	result := &Error{Message: err.Error()}
	if status := statusCodeFromError(err); status > 0 && status != http.StatusOK {
		result.HTTPStatus = status
	}
	return result
}
