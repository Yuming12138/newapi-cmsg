package helps

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestStatusFromHomeErrorCodeMapsAuthenticationErrorToUnauthorized(t *testing.T) {
	if got := statusFromHomeErrorCode("authentication_error"); got != http.StatusUnauthorized {
		t.Fatalf("statusFromHomeErrorCode(authentication_error) = %d, want %d", got, http.StatusUnauthorized)
	}
	if got := statusFromHomeErrorCode("unauthorized"); got != http.StatusUnauthorized {
		t.Fatalf("statusFromHomeErrorCode(unauthorized) = %d, want %d", got, http.StatusUnauthorized)
	}
}

type fakeHomeRefreshClient struct {
	calls             atomic.Int32
	authIndex         string
	accessTokenSHA256 string
	raw               []byte
	err               error
}

func (c *fakeHomeRefreshClient) HeartbeatOK() bool {
	return true
}

func (c *fakeHomeRefreshClient) GetRefreshAuth(_ context.Context, authIndex string, accessTokenSHA256 string) ([]byte, error) {
	c.calls.Add(1)
	c.authIndex = authIndex
	c.accessTokenSHA256 = accessTokenSHA256
	return c.raw, c.err
}

func TestRefreshAuthViaHomeMapsTemporaryFailuresSafely(t *testing.T) {
	client := &fakeHomeRefreshClient{err: errors.New("redis endpoint details must not escape")}
	oldCurrentHomeRefreshClient := currentHomeRefreshClient
	currentHomeRefreshClient = func() homeRefreshClient { return client }
	t.Cleanup(func() { currentHomeRefreshClient = oldCurrentHomeRefreshClient })

	_, handled, err := RefreshAuthViaHome(context.Background(), &config.Config{Home: config.HomeConfig{Enabled: true}}, &cliproxyauth.Auth{
		ID: "home-auth-1", Index: "home-index-1", Metadata: map[string]any{"access_token": "old-access-token"},
	})
	if !handled || err == nil {
		t.Fatalf("RefreshAuthViaHome() = handled %v, err %v", handled, err)
	}
	if statusErr, ok := err.(interface{ StatusCode() int }); !ok || statusErr.StatusCode() != http.StatusServiceUnavailable {
		t.Fatalf("refresh error = %v, want 503", err)
	}
	if err.Error() != "home refresh temporarily unavailable" {
		t.Fatalf("refresh error = %q, want redacted temporary message", err.Error())
	}
}

func TestRefreshAuthViaHomeRejectsDisabledCredential(t *testing.T) {
	raw, errMarshal := json.Marshal(cliproxyauth.Auth{ID: "home-auth-1", Disabled: true})
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	client := &fakeHomeRefreshClient{raw: raw}
	oldCurrentHomeRefreshClient := currentHomeRefreshClient
	currentHomeRefreshClient = func() homeRefreshClient { return client }
	t.Cleanup(func() { currentHomeRefreshClient = oldCurrentHomeRefreshClient })

	_, _, err := RefreshAuthViaHome(context.Background(), &config.Config{Home: config.HomeConfig{Enabled: true}}, &cliproxyauth.Auth{
		ID: "home-auth-1", Index: "home-index-1", Metadata: map[string]any{"access_token": "old-access-token"},
	})
	if statusErr, ok := err.(interface{ StatusCode() int }); !ok || statusErr.StatusCode() != http.StatusUnauthorized {
		t.Fatalf("disabled refresh error = %v, want 401", err)
	}
}

func TestRefreshAuthViaHomeAcceptsAuthEnvelope(t *testing.T) {
	raw, errMarshal := json.Marshal(struct {
		Auth      cliproxyauth.Auth `json:"auth"`
		AuthIndex string            `json:"auth_index"`
	}{
		Auth: cliproxyauth.Auth{
			ID:       "home-auth-1",
			Provider: "antigravity",
			Metadata: map[string]any{
				"access_token": "new-access-token",
			},
		},
		AuthIndex: "home-index-1",
	})
	if errMarshal != nil {
		t.Fatalf("marshal home envelope: %v", errMarshal)
	}

	client := &fakeHomeRefreshClient{raw: raw}
	oldCurrentHomeRefreshClient := currentHomeRefreshClient
	currentHomeRefreshClient = func() homeRefreshClient {
		return client
	}
	t.Cleanup(func() {
		currentHomeRefreshClient = oldCurrentHomeRefreshClient
	})

	cfg := &config.Config{Home: config.HomeConfig{Enabled: true}}
	auth := &cliproxyauth.Auth{
		ID:       "home-auth-1",
		Provider: "antigravity",
		Index:    "home-index-1",
		Metadata: map[string]any{
			"access_token":  "old-access-token",
			"refresh_token": "refresh-token",
		},
	}

	updated, handled, err := RefreshAuthViaHome(context.Background(), cfg, auth)
	if err != nil {
		t.Fatalf("RefreshAuthViaHome error: %v", err)
	}
	if !handled {
		t.Fatal("RefreshAuthViaHome handled = false, want true")
	}
	if got := client.calls.Load(); got != 1 {
		t.Fatalf("home refresh calls = %d, want 1", got)
	}
	if client.authIndex != "home-index-1" {
		t.Fatalf("home refresh auth_index = %q, want home-index-1", client.authIndex)
	}
	if want := cliproxyauth.AccessTokenSHA256(auth); client.accessTokenSHA256 != want {
		t.Fatalf("home refresh access_token_sha256 = %q, want %q", client.accessTokenSHA256, want)
	}
	if updated == nil {
		t.Fatal("updated auth = nil")
	}
	if got := updated.Metadata["access_token"]; got != "new-access-token" {
		t.Fatalf("updated access_token = %q, want new-access-token", got)
	}
	if updated.Index != "home-index-1" {
		t.Fatalf("updated auth_index = %q, want home-index-1", updated.Index)
	}
}
