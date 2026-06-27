package model

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestExtractOpenWebUIRequestInfoFromSignedJWT(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "test-open-webui-forward-secret"
	t.Setenv(openWebUIJWTSecretEnv, secret)
	t.Setenv(openWebUIJWTHeaderEnv, "")

	ctx := newOpenWebUITestContext(t, signOpenWebUITestJWT(t, secret, jwt.MapClaims{
		"sub":   "user-123",
		"email": "gmchen@example.com",
		"name":  "gmchen",
		"role":  "user",
		"iss":   "open-webui",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Minute).Unix(),
	}))

	info := extractOpenWebUIRequestInfo(ctx)
	if info["source"] != "open-webui" || info["verified"] != true {
		t.Fatalf("unexpected Open WebUI source info: %#v", info)
	}
	if info["user_id"] != "user-123" {
		t.Fatalf("unexpected user_id: %#v", info["user_id"])
	}
	if info["email"] != "gmchen@example.com" {
		t.Fatalf("unexpected email: %#v", info["email"])
	}
	if info["name"] != "gmchen" {
		t.Fatalf("unexpected name: %#v", info["name"])
	}
	if info["role"] != "user" {
		t.Fatalf("unexpected role: %#v", info["role"])
	}
	if info["chat_id"] != "chat-456" {
		t.Fatalf("unexpected chat_id: %#v", info["chat_id"])
	}
	if info["message_id"] != "message-789" {
		t.Fatalf("unexpected message_id: %#v", info["message_id"])
	}
}

func TestExtractOpenWebUIRequestInfoRejectsBadSignature(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv(openWebUIJWTSecretEnv, "expected-secret")
	ctx := newOpenWebUITestContext(t, signOpenWebUITestJWT(t, "wrong-secret", jwt.MapClaims{
		"sub": "user-123",
		"iss": "open-webui",
		"exp": time.Now().Add(time.Minute).Unix(),
	}))

	if info := extractOpenWebUIRequestInfo(ctx); info != nil {
		t.Fatalf("expected invalid Open WebUI JWT to be ignored, got %#v", info)
	}
}

func TestAttachOpenWebUIAdminInfoPreservesExistingAdminInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "test-open-webui-forward-secret"
	t.Setenv(openWebUIJWTSecretEnv, secret)
	ctx := newOpenWebUITestContext(t, signOpenWebUITestJWT(t, secret, jwt.MapClaims{
		"sub": "user-123",
		"iss": "open-webui",
		"exp": time.Now().Add(time.Minute).Unix(),
	}))

	other := attachOpenWebUIAdminInfo(ctx, map[string]interface{}{
		"admin_info": map[string]interface{}{
			"use_channel": []string{"cliproxy"},
		},
	})
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("admin_info missing or wrong type: %#v", other["admin_info"])
	}
	if _, ok := adminInfo["use_channel"]; !ok {
		t.Fatalf("existing admin_info was not preserved: %#v", adminInfo)
	}
	openWebUI, ok := adminInfo["open_webui"].(map[string]interface{})
	if !ok {
		t.Fatalf("open_webui admin info missing: %#v", adminInfo)
	}
	if openWebUI["user_id"] != "user-123" {
		t.Fatalf("unexpected open_webui user_id: %#v", openWebUI["user_id"])
	}
}

func newOpenWebUITestContext(t *testing.T, token string) *gin.Context {
	t.Helper()
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set(defaultOpenWebUIJWTHeader, token)
	req.Header.Set(openWebUIChatIDHeader, "chat-456")
	req.Header.Set(openWebUIMessageIDHeader, "message-789")
	ctx.Request = req
	return ctx
}

func signOpenWebUITestJWT(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to sign test JWT: %v", err)
	}
	return token
}
