package pluginhost

import (
	"context"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestHostPluginAuthProviderMethodsNilSafe(t *testing.T) {
	var h *Host
	ctx := context.Background()

	if h.HasAuthProvider("provider") {
		t.Fatal("HasAuthProvider() = true for nil host, want false")
	}
	if plugins := h.RegisteredPlugins(); plugins != nil {
		t.Fatalf("RegisteredPlugins() = %#v, want nil", plugins)
	}
	if resp, handled, err := h.StartLogin(ctx, "provider", "http://localhost"); handled || err != nil || resp.URL != "" {
		t.Fatalf("StartLogin() = %#v, %t, %v; want zero response, false, nil", resp, handled, err)
	}
	if resp, handled, err := h.PollLogin(ctx, "provider", "state"); handled || err != nil || resp.Auth.Provider != "" {
		t.Fatalf("PollLogin() = %#v, %t, %v; want zero response, false, nil", resp, handled, err)
	}
	if auth := h.AuthDataToCoreAuth(pluginapi.AuthData{Provider: "provider"}, "", ""); auth != nil {
		t.Fatalf("AuthDataToCoreAuth() = %#v, want nil", auth)
	}
}

func TestRegisteredPluginPublicAliasesCompile(t *testing.T) {
	info := RegisteredPluginInfo{
		ID:    "plugin-a",
		Menus: []RegisteredPluginMenu{{Path: "/plugin-a", Menu: "Menu"}},
	}
	if info.ID != "plugin-a" || len(info.Menus) != 1 || info.Menus[0].Path != "/plugin-a" || info.Menus[0].Menu != "Menu" {
		t.Fatalf("registered plugin aliases = %#v", info)
	}
}

func TestHostAuthDataToCoreAuthWrapperCompiles(t *testing.T) {
	h := &Host{}
	var auth *coreauth.Auth = h.AuthDataToCoreAuth(pluginapi.AuthData{Provider: "provider"}, "", "")
	if auth != nil {
		t.Fatalf("AuthDataToCoreAuth() = %#v, want nil for host without inner", auth)
	}
}
