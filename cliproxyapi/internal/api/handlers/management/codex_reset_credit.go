package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	codexauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const codexResetCreditUserAgent = "codex_cli_rs/0.76.0 (Debian 13.0.0; x86_64) WindowsTerminal"

var codexResetCreditsConsumeURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"

// ConsumeCodexResetCredit spends one upstream Codex reset credit for one auth index.
func (h *Handler) ConsumeCodexResetCredit(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	var req struct {
		AuthIndex       string `json:"auth_index"`
		RedeemRequestID string `json:"redeem_request_id"`
	}
	if errBindJSON := c.ShouldBindJSON(&req); errBindJSON != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
		return
	}

	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth provider must be codex"})
		return
	}

	redeemID, errRedeemID := codexResetRedeemRequestID(req.RedeemRequestID)
	if errRedeemID != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errRedeemID.Error()})
		return
	}
	token, errToken := h.codexAccessTokenForReset(c.Request.Context(), auth)
	if errToken != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": errToken.Error()})
		return
	}

	errReset := h.postCodexResetCredit(c.Request.Context(), auth, token, redeemID)
	if errReset == errCodexResetUnauthorized {
		token, errToken = h.refreshCodexResetAccessToken(c.Request.Context(), auth)
		if errToken != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": fmt.Sprintf("failed to refresh codex token: %v", errToken)})
			return
		}
		errReset = h.postCodexResetCredit(c.Request.Context(), auth, token, redeemID)
	}
	if errReset != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": errReset.Error()})
		return
	}

	updated, models, errLocalReset := h.authManager.ResetQuota(c.Request.Context(), auth.ID)
	if errLocalReset != nil {
		c.JSON(http.StatusOK, gin.H{
			"status":            "ok",
			"auth_index":        authIndex,
			"local_reset_error": fmt.Sprintf("failed to reset local quota: %v", errLocalReset),
		})
		return
	}
	if updated != nil {
		updated.EnsureIndex()
		authIndex = updated.Index
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"auth_index": authIndex,
		"models":     models,
	})
}

func codexResetRedeemRequestID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.NewString(), nil
	}
	parsed, errParse := uuid.Parse(value)
	if errParse != nil {
		return "", fmt.Errorf("redeem_request_id must be a valid UUID")
	}
	return parsed.String(), nil
}

var errCodexResetUnauthorized = fmt.Errorf("codex reset unauthorized")

func (h *Handler) codexAccessTokenForReset(ctx context.Context, auth *coreauth.Auth) (string, error) {
	if auth == nil {
		return "", fmt.Errorf("auth is required")
	}
	token := strings.TrimSpace(stringValue(auth.Metadata, "access_token"))
	if token == "" {
		return h.refreshCodexResetAccessToken(ctx, auth)
	}
	if codexAccessTokenNeedsRefresh(auth, token, time.Now()) {
		if refreshed, errRefresh := h.refreshCodexResetAccessToken(ctx, auth); errRefresh == nil && strings.TrimSpace(refreshed) != "" {
			return refreshed, nil
		}
	}
	return token, nil
}

func (h *Handler) refreshCodexResetAccessToken(ctx context.Context, auth *coreauth.Auth) (string, error) {
	if auth == nil {
		return "", fmt.Errorf("auth is required")
	}
	refreshToken := strings.TrimSpace(stringValue(auth.Metadata, "refresh_token"))
	if refreshToken == "" {
		return "", fmt.Errorf("codex refresh_token missing")
	}
	svc := codexauth.NewCodexAuthWithProxyURL(h.cfg, auth.ProxyURL)
	tokenData, errRefresh := svc.RefreshTokensWithRetry(ctx, refreshToken, 3)
	if errRefresh != nil {
		return "", errRefresh
	}
	if strings.TrimSpace(tokenData.AccessToken) == "" {
		return "", fmt.Errorf("codex token refresh returned empty access_token")
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["id_token"] = tokenData.IDToken
	auth.Metadata["access_token"] = tokenData.AccessToken
	if strings.TrimSpace(tokenData.RefreshToken) != "" {
		auth.Metadata["refresh_token"] = tokenData.RefreshToken
	}
	if strings.TrimSpace(tokenData.AccountID) != "" {
		auth.Metadata["account_id"] = tokenData.AccountID
	}
	if strings.TrimSpace(tokenData.Email) != "" {
		auth.Metadata["email"] = tokenData.Email
	}
	auth.Metadata["expired"] = tokenData.Expire
	auth.Metadata["type"] = "codex"
	auth.Metadata["last_refresh"] = time.Now().Format(time.RFC3339)
	auth.LastRefreshedAt = time.Now()
	auth.UpdatedAt = auth.LastRefreshedAt
	if h != nil && h.authManager != nil {
		if _, errUpdate := h.authManager.Update(ctx, auth); errUpdate != nil {
			log.WithError(errUpdate).Warn("failed to persist refreshed codex token before reset credit consume")
		}
	}
	return strings.TrimSpace(tokenData.AccessToken), nil
}

func (h *Handler) postCodexResetCredit(ctx context.Context, auth *coreauth.Auth, token string, redeemID string) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return fmt.Errorf("codex access_token missing")
	}
	redeemID = strings.TrimSpace(redeemID)
	if redeemID == "" {
		redeemID = uuid.NewString()
	}
	body, errMarshal := json.Marshal(map[string]string{"redeem_request_id": redeemID})
	if errMarshal != nil {
		return errMarshal
	}
	req, errReq := http.NewRequestWithContext(ctx, http.MethodPost, codexResetCreditsConsumeURL, bytes.NewReader(body))
	if errReq != nil {
		return errReq
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", codexResetCreditUserAgent)
	if accountID := codexAccountID(auth); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}

	httpClient := &http.Client{Transport: h.apiCallTransport(auth)}
	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return fmt.Errorf("codex reset credit request failed: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("codex reset credit response body close error: %v", errClose)
		}
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errCodexResetUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		snippet := strings.TrimSpace(string(data))
		if len([]rune(snippet)) > 200 {
			snippet = string([]rune(snippet)[:200])
		}
		if snippet == "" {
			return fmt.Errorf("codex reset credit failed: status %d", resp.StatusCode)
		}
		return fmt.Errorf("codex reset credit failed: status %d: %s", resp.StatusCode, snippet)
	}
	return nil
}

func codexAccessTokenNeedsRefresh(auth *coreauth.Auth, token string, now time.Time) bool {
	if auth != nil && auth.Metadata != nil {
		if expiredRaw := strings.TrimSpace(stringValue(auth.Metadata, "expired")); expiredRaw != "" {
			if expiredAt, errParse := time.Parse(time.RFC3339, expiredRaw); errParse == nil {
				return !expiredAt.After(now.Add(time.Minute))
			}
		}
	}
	if claims, errParse := codexauth.ParseJWTToken(token); errParse == nil && claims != nil && claims.Exp > 0 {
		return !time.Unix(int64(claims.Exp), 0).After(now.Add(time.Minute))
	}
	return false
}

func codexAccountID(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if accountID := strings.TrimSpace(stringValue(auth.Metadata, "account_id")); accountID != "" {
		return accountID
	}
	if idToken := strings.TrimSpace(stringValue(auth.Metadata, "id_token")); idToken != "" {
		if claims, errParse := codexauth.ParseJWTToken(idToken); errParse == nil && claims != nil {
			return strings.TrimSpace(claims.GetAccountID())
		}
	}
	return ""
}
