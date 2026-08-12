package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
)

const homeResultExecutorType = "home-result"

func (m *Manager) reportHomeUnauthorized(ctx context.Context, auth *Auth, provider, model string) {
	if m == nil || auth == nil {
		return
	}
	authIndex := strings.TrimSpace(auth.Index)
	if authIndex == "" {
		authIndex = strings.TrimSpace(auth.EnsureIndex())
	}
	fingerprint := AccessTokenSHA256(auth)
	if authIndex == "" || fingerprint == "" {
		return
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = strings.TrimSpace(auth.Provider)
	}
	model = strings.TrimSpace(model)
	alias := strings.TrimSpace(coreusage.RequestedModelAliasFromContext(ctx))
	if alias == "" {
		alias = model
	}
	coreusage.PublishRecord(ctx, coreusage.Record{
		Provider:          provider,
		ExecutorType:      homeResultExecutorType,
		Model:             model,
		Alias:             alias,
		AuthID:            auth.ID,
		AuthIndex:         authIndex,
		AccessTokenSHA256: fingerprint,
		AuthType:          auth.AuthKind(),
		Source:            auth.AuthSourceKind(),
		ReasoningEffort:   coreusage.ReasoningEffortFromContext(ctx),
		ServiceTier:       coreusage.ServiceTierFromContext(ctx),
		RequestedAt:       time.Now(),
		Failed:            true,
		Fail: coreusage.Failure{
			StatusCode: http.StatusUnauthorized,
			Body:       "upstream unauthorized",
		},
	})
}
