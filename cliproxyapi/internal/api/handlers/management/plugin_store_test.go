package management

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginstore"
	"gopkg.in/yaml.v3"
)

func TestListPluginStoreMergesInstalledStatus(t *testing.T) {
	t.Parallel()

	pluginsDir := writeManagementPluginFile(t, "sample-provider")
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     pluginsDir,
				Configs: map[string]config.PluginInstanceConfig{
					"sample-provider": pluginConfigFromYAML(t, "enabled: true\nmode: fast\n"),
				},
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": registryJSON(t),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body pluginStoreListResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if !body.PluginsEnabled {
		t.Fatal("plugins_enabled = false, want true")
	}
	if len(body.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(body.Plugins))
	}
	entry := body.Plugins[0]
	if !entry.Installed || !entry.Configured || !entry.Enabled {
		t.Fatalf("store entry status = %#v, want installed configured enabled", entry)
	}
	if entry.Registered || entry.EffectiveEnabled {
		t.Fatalf("runtime status = registered %v effective %v, want false false", entry.Registered, entry.EffectiveEnabled)
	}
	if entry.InstalledVersion != "" {
		t.Fatalf("installed_version = %q, want empty for unregistered plugin", entry.InstalledVersion)
	}
	if entry.UpdateAvailable {
		t.Fatal("update_available = true, want false when installed version is unknown")
	}
	if entry.Path == "" {
		t.Fatal("path is empty")
	}
}

func TestListPluginStoreEscapesRegistryStrings(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     t.TempDir(),
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": []byte(`{
				"schema_version": 1,
				"plugins": [{
					"id": "sample-provider",
					"name": "<script>alert(1)</script>",
					"description": "<img src=x onerror=alert(1)>",
					"author": "\"attacker\"",
					"version": "0.1.0",
					"repository": "https://github.com/author-name/cliproxy-sample-provider-plugin",
					"logo": "<svg onload=alert(1)>",
					"homepage": "https://example.com/?q=<x>",
					"license": "<b>MIT</b>",
					"tags": ["<provider>", "safe & sound"]
				}]
			}`),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body pluginStoreListResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if len(body.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(body.Plugins))
	}
	entry := body.Plugins[0]
	if entry.Name != html.EscapeString("<script>alert(1)</script>") ||
		entry.Description != html.EscapeString("<img src=x onerror=alert(1)>") ||
		entry.Author != html.EscapeString(`"attacker"`) ||
		entry.Version != "0.1.0" ||
		entry.Repository != "https://github.com/author-name/cliproxy-sample-provider-plugin" ||
		entry.Logo != html.EscapeString("<svg onload=alert(1)>") ||
		entry.Homepage != html.EscapeString("https://example.com/?q=<x>") ||
		entry.License != html.EscapeString("<b>MIT</b>") {
		t.Fatalf("store entry = %#v, want escaped strings", entry)
	}
	if len(entry.Tags) != 2 ||
		entry.Tags[0] != html.EscapeString("<provider>") ||
		entry.Tags[1] != html.EscapeString("safe & sound") {
		t.Fatalf("tags = %#v, want escaped strings", entry.Tags)
	}
}

func TestListPluginStoreShowsLatestReleaseVersionAndCaches(t *testing.T) {
	t.Parallel()

	httpClient := &countingPluginStoreHTTPClient{responses: fakePluginStoreHTTPClient{
		"https://registry.example/registry.json": registryJSON(t),
		"https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/latest": []byte(`{
			"tag_name": "v0.2.0",
			"assets": []
		}`),
	}}
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     t.TempDir(),
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient:  httpClient,
	}

	listOnce := func() pluginStoreListResponse {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)
		h.ListPluginStore(c)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var body pluginStoreListResponse
		if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
			t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
		}
		return body
	}

	for call := 0; call < 2; call++ {
		body := listOnce()
		if len(body.Plugins) != 1 {
			t.Fatalf("plugins len = %d, want 1", len(body.Plugins))
		}
		if body.Plugins[0].Version != "0.2.0" {
			t.Fatalf("version = %q, want 0.2.0 from latest release tag", body.Plugins[0].Version)
		}
	}
	releaseCalls := httpClient.count("https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/latest")
	if releaseCalls != 1 {
		t.Fatalf("latest release fetched %d times, want 1 (cached)", releaseCalls)
	}
}

func TestListPluginStoreFallsBackToRegistryVersion(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     t.TempDir(),
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": registryJSON(t),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body pluginStoreListResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if len(body.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(body.Plugins))
	}
	if body.Plugins[0].Version != "0.1.0" {
		t.Fatalf("version = %q, want registry fallback 0.1.0", body.Plugins[0].Version)
	}
}

func TestListPluginStoreIncludesThirdPartySources(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled:      true,
				Dir:          t.TempDir(),
				StoreSources: []string{"https://community.example/registry.json"},
			},
		},
		configFilePath: writeTestConfigFile(t),
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			pluginstore.DefaultRegistryURL: registryJSON(t),
			"https://community.example/registry.json": []byte(`{
				"schema_version": 1,
				"plugins": [{
					"id": "third-provider",
					"name": "Third Provider",
					"description": "Adds third-party provider support.",
					"author": "community",
					"version": "0.3.0",
					"repository": "https://github.com/community/cliproxy-third-provider-plugin"
				}]
			}`),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body pluginStoreListResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if len(body.Sources) != 2 {
		t.Fatalf("sources len = %d, want 2: %#v", len(body.Sources), body.Sources)
	}
	if len(body.Plugins) != 2 {
		t.Fatalf("plugins len = %d, want 2: %#v", len(body.Plugins), body.Plugins)
	}
	byID := map[string]pluginStoreListEntry{}
	for _, entry := range body.Plugins {
		byID[entry.ID] = entry
	}
	if byID["sample-provider"].SourceID != pluginstore.DefaultSourceID {
		t.Fatalf("official source id = %q, want %q", byID["sample-provider"].SourceID, pluginstore.DefaultSourceID)
	}
	third := byID["third-provider"]
	communitySourceID := pluginstore.SourceID("https://community.example/registry.json")
	if third.StoreID != communitySourceID+"/third-provider" || third.SourceID != communitySourceID || third.SourceName != "community.example" || third.SourceURL != "https://community.example/registry.json" {
		t.Fatalf("third-party source fields = %#v", third)
	}
}

func TestListPluginStoreIncludesDirectMetadataAndAuth(t *testing.T) {
	t.Setenv("PLUGIN_STORE_TOKEN", "secret-token")

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     t.TempDir(),
				StoreAuth: []pluginstore.AuthConfig{{
					Match:    "https://registry.example/",
					ApplyTo:  []string{pluginstore.RequestKindRegistry},
					Type:     pluginstore.AuthTypeBearer,
					TokenEnv: "PLUGIN_STORE_TOKEN",
				}},
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": directRegistryJSON("https://downloads.example/sample-provider.zip", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body pluginStoreListResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if len(body.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(body.Plugins))
	}
	entry := body.Plugins[0]
	if entry.InstallType != pluginstore.InstallTypeDirect || !entry.AuthRequired || !entry.AuthConfigured {
		t.Fatalf("direct metadata = %#v, want direct auth metadata", entry)
	}
	if !pluginStorePlatformsContain(entry.Platforms, "linux", "amd64") {
		t.Fatalf("platforms = %#v, want linux/amd64", entry.Platforms)
	}
}

func TestListPluginStoreReportsVersionArtifactAuth(t *testing.T) {
	t.Setenv("PLUGIN_STORE_TOKEN", "secret-token")

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     t.TempDir(),
				StoreAuth: []pluginstore.AuthConfig{{
					Match:    "https://versioned.example/",
					ApplyTo:  []string{pluginstore.RequestKindArtifact},
					Type:     pluginstore.AuthTypeBearer,
					TokenEnv: "PLUGIN_STORE_TOKEN",
				}},
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": directRegistryJSONWithVersionArtifact(
				"https://downloads.example/sample-provider.zip",
				"https://versioned.example/sample-provider-0.3.0.zip",
				"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body pluginStoreListResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if len(body.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(body.Plugins))
	}
	if !body.Plugins[0].AuthConfigured {
		t.Fatalf("auth_configured = false, want true for version artifact auth")
	}
}

func TestListPluginStoreReportsGitHubMetadataAuth(t *testing.T) {
	t.Setenv("PLUGIN_STORE_TOKEN", "secret-token")

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     t.TempDir(),
				StoreAuth: []pluginstore.AuthConfig{{
					Match:    "https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/",
					ApplyTo:  []string{pluginstore.RequestKindMetadata},
					Type:     pluginstore.AuthTypeBearer,
					TokenEnv: "PLUGIN_STORE_TOKEN",
				}},
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": registryJSON(t),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body pluginStoreListResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if len(body.Plugins) != 1 {
		t.Fatalf("plugins len = %d, want 1", len(body.Plugins))
	}
	if !body.Plugins[0].AuthConfigured {
		t.Fatalf("auth_configured = false, want true for GitHub metadata auth")
	}
}

func TestListPluginStoreSendsConfiguredAuth(t *testing.T) {
	t.Setenv("STORE_REGISTRY_TOKEN", "private-token")
	registryURL := "https://private.example/registry.json"
	httpClient := &authRecordingPluginStoreHTTPClient{responses: fakePluginStoreHTTPClient{
		registryURL: registryJSON(t),
	}}
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     t.TempDir(),
				StoreAuth: []pluginstore.AuthConfig{{
					Match:    "https://private.example/",
					ApplyTo:  []string{pluginstore.RequestKindRegistry},
					Type:     pluginstore.AuthTypeBearer,
					TokenEnv: "STORE_REGISTRY_TOKEN",
				}},
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: registryURL,
		pluginStoreHTTPClient:  httpClient,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v0/management/plugin-store", nil)

	h.ListPluginStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := httpClient.header(registryURL, "Authorization"); got != "Bearer private-token" {
		t.Fatalf("registry Authorization = %q, want bearer token", got)
	}
}

func TestInstallPluginFromStoreWritesFileAndEnablesConfig(t *testing.T) {
	t.Parallel()

	pluginsDir := t.TempDir()
	archiveData := makeManagementPluginStoreZip(t, "sample-provider"+managementPluginExtension(runtime.GOOS), "library-data")
	archiveName := "sample-provider_0.1.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
	checksum := sha256.Sum256(archiveData)
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: false,
				Dir:     pluginsDir,
				Configs: map[string]config.PluginInstanceConfig{
					"sample-provider": pluginConfigFromYAML(t, "enabled: false\nmode: fast\n"),
				},
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": registryJSON(t),
			"https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/latest": []byte(`{
				"tag_name": "v0.1.0",
				"assets": [
					{"name": "` + archiveName + `", "browser_download_url": "https://downloads.example/` + archiveName + `"},
					{"name": "checksums.txt", "browser_download_url": "https://downloads.example/checksums.txt"}
				]
			}`),
			"https://downloads.example/" + archiveName: archiveData,
			"https://downloads.example/checksums.txt":  []byte(hex.EncodeToString(checksum[:]) + "  " + archiveName + "\n"),
		},
	}
	reloads, reloadDone := captureConfigReload(h)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "sample-provider"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/plugin-store/sample-provider/install", nil)

	h.InstallPluginFromStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	cfgSnapshot := waitForAsyncReload(t, reloads)
	waitForReloadDone(t, reloadDone)
	if cfgSnapshot == h.cfg {
		t.Fatalf("reload config = handler config %p, want independent snapshot", h.cfg)
	}
	var body pluginInstallResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if body.Status != "installed" || body.ID != "sample-provider" || body.Version != "0.1.0" {
		t.Fatalf("install response = %#v", body)
	}
	if body.PluginsEnabled {
		t.Fatal("plugins_enabled = true, want false")
	}
	if body.RestartRequired {
		t.Fatal("restart_required = true, want false")
	}
	targetPath := filepath.Join(pluginsDir, runtime.GOOS, runtime.GOARCH, "sample-provider-v0.1.0"+managementPluginExtension(runtime.GOOS))
	data, errRead := os.ReadFile(targetPath)
	if errRead != nil {
		t.Fatalf("ReadFile(%s) error = %v", targetPath, errRead)
	}
	if string(data) != "library-data" {
		t.Fatalf("installed file = %q, want library-data", data)
	}
	item := h.cfg.Plugins.Configs["sample-provider"]
	if item.Enabled == nil || !*item.Enabled {
		t.Fatalf("plugin enabled = %#v, want true", item.Enabled)
	}
	snapshotItem := cfgSnapshot.Plugins.Configs["sample-provider"]
	if snapshotItem.Enabled == nil || !*snapshotItem.Enabled {
		t.Fatalf("snapshot plugin enabled = %#v, want true", snapshotItem.Enabled)
	}
	if h.cfg.Plugins.Enabled {
		t.Fatal("global plugins.enabled changed to true")
	}
	if cfgSnapshot.Plugins.Enabled {
		t.Fatal("snapshot global plugins.enabled changed to true")
	}
	raw := marshalPluginRaw(t, item)
	if !strings.Contains(raw, "mode: fast") {
		t.Fatalf("plugin raw config lost custom field:\n%s", raw)
	}
	if raw := marshalPluginRaw(t, snapshotItem); !strings.Contains(raw, "mode: fast") {
		t.Fatalf("snapshot plugin raw config lost custom field:\n%s", raw)
	}
	if raw := marshalPluginRaw(t, item); !strings.Contains(raw, "store:") || !strings.Contains(raw, "release-tag: v0.1.0") {
		t.Fatalf("plugin raw config missing store manifest:\n%s", raw)
	}
}

func TestInstallPluginFromStoreInstallsDirectArtifact(t *testing.T) {
	t.Parallel()

	pluginsDir := t.TempDir()
	archiveData := makeManagementPluginStoreZip(t, "sample-provider"+managementPluginExtension(runtime.GOOS), "direct-library-data")
	checksum := sha256.Sum256(archiveData)
	artifactURL := "https://downloads.example/sample-provider.zip"
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: false,
				Dir:     pluginsDir,
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": directRegistryJSON(artifactURL, hex.EncodeToString(checksum[:])),
			artifactURL:                              archiveData,
		},
	}
	reloads, reloadDone := captureConfigReload(h)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "sample-provider"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/plugin-store/sample-provider/install", nil)

	h.InstallPluginFromStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	waitForAsyncReload(t, reloads)
	waitForReloadDone(t, reloadDone)
	var body pluginInstallResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if body.InstallType != pluginstore.InstallTypeDirect || body.Version != "0.4.0" {
		t.Fatalf("install response = %#v, want direct 0.4.0", body)
	}
	targetPath := filepath.Join(pluginsDir, runtime.GOOS, runtime.GOARCH, "sample-provider-v0.4.0"+managementPluginExtension(runtime.GOOS))
	data, errRead := os.ReadFile(targetPath)
	if errRead != nil {
		t.Fatalf("ReadFile(%s) error = %v", targetPath, errRead)
	}
	if string(data) != "direct-library-data" {
		t.Fatalf("installed file = %q, want direct-library-data", data)
	}
	manifest := pluginStoreManifestFromConfig(t, h.cfg.Plugins.Configs["sample-provider"])
	if manifest.SchemaVersion != pluginstore.SchemaVersionV2 || manifest.InstallType() != pluginstore.InstallTypeDirect || manifest.Version != "0.4.0" {
		t.Fatalf("store manifest = %#v, want direct schema v2 0.4.0", manifest)
	}
	if manifest.SourceURL != "https://registry.example/registry.json" || len(manifest.Install.Artifacts) != 0 {
		t.Fatalf("store manifest source/artifacts = %q/%d, want source URL without artifacts", manifest.SourceURL, len(manifest.Install.Artifacts))
	}
	if raw := marshalPluginRaw(t, h.cfg.Plugins.Configs["sample-provider"]); strings.Contains(raw, "artifacts:") {
		t.Fatalf("direct store manifest should not persist artifacts:\n%s", raw)
	}
}

func TestInstallPluginFromStoreHonorsDirectQueryVersion(t *testing.T) {
	t.Parallel()

	pluginsDir := t.TempDir()
	archiveData := makeManagementPluginStoreZip(t, "sample-provider"+managementPluginExtension(runtime.GOOS), "direct-history-data")
	checksum := sha256.Sum256(archiveData)
	topArtifactURL := "https://downloads.example/sample-provider-0.4.0.zip"
	versionArtifactURL := "https://downloads.example/sample-provider-0.3.0.zip"
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: false,
				Dir:     pluginsDir,
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": directRegistryJSONWithVersionArtifact(topArtifactURL, versionArtifactURL, hex.EncodeToString(checksum[:])),
			versionArtifactURL:                       archiveData,
		},
	}
	reloads, reloadDone := captureConfigReload(h)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "sample-provider"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/plugin-store/sample-provider/install?version=0.3.0", nil)

	h.InstallPluginFromStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	waitForAsyncReload(t, reloads)
	waitForReloadDone(t, reloadDone)
	var body pluginInstallResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if body.InstallType != pluginstore.InstallTypeDirect || body.Version != "0.3.0" {
		t.Fatalf("install response = %#v, want direct 0.3.0", body)
	}
	targetPath := filepath.Join(pluginsDir, runtime.GOOS, runtime.GOARCH, "sample-provider-v0.3.0"+managementPluginExtension(runtime.GOOS))
	data, errRead := os.ReadFile(targetPath)
	if errRead != nil {
		t.Fatalf("ReadFile(%s) error = %v", targetPath, errRead)
	}
	if string(data) != "direct-history-data" {
		t.Fatalf("installed file = %q, want direct-history-data", data)
	}
	manifest := pluginStoreManifestFromConfig(t, h.cfg.Plugins.Configs["sample-provider"])
	if manifest.Version != "0.3.0" || manifest.InstallType() != pluginstore.InstallTypeDirect || len(manifest.Install.Artifacts) != 0 {
		t.Fatalf("store manifest = %#v, want source-backed direct 0.3.0", manifest)
	}
}

func TestInstallPluginFromStoreUsesRequestedThirdPartySource(t *testing.T) {
	t.Parallel()

	pluginsDir := t.TempDir()
	archiveData := makeManagementPluginStoreZip(t, "sample-provider"+managementPluginExtension(runtime.GOOS), "third-party-library-data")
	archiveName := "sample-provider_0.3.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
	checksum := sha256.Sum256(archiveData)
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled:      false,
				Dir:          pluginsDir,
				StoreSources: []string{"https://community.example/registry.json"},
			},
		},
		configFilePath: writeTestConfigFile(t),
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			pluginstore.DefaultRegistryURL:            registryJSON(t),
			"https://community.example/registry.json": thirdPartySampleRegistryJSON(t),
			"https://api.github.com/repos/community/cliproxy-sample-provider-plugin/releases/latest": []byte(`{
				"tag_name": "v0.3.0",
				"assets": [
					{"name": "` + archiveName + `", "browser_download_url": "https://downloads.example/` + archiveName + `"},
					{"name": "checksums.txt", "browser_download_url": "https://downloads.example/checksums.txt"}
				]
			}`),
			"https://downloads.example/" + archiveName: archiveData,
			"https://downloads.example/checksums.txt":  []byte(hex.EncodeToString(checksum[:]) + "  " + archiveName + "\n"),
		},
	}
	reloads, reloadDone := captureConfigReload(h)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "sample-provider"}}
	communitySourceID := pluginstore.SourceID("https://community.example/registry.json")
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/plugin-store/sample-provider/install?source="+communitySourceID, nil)

	h.InstallPluginFromStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	cfgSnapshot := waitForAsyncReload(t, reloads)
	waitForReloadDone(t, reloadDone)
	if cfgSnapshot == h.cfg {
		t.Fatalf("reload config = handler config %p, want independent snapshot", h.cfg)
	}
	var body pluginInstallResponse
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("Unmarshal() error = %v; body=%s", errDecode, rec.Body.String())
	}
	if body.SourceID != communitySourceID || body.Version != "0.3.0" {
		t.Fatalf("install response = %#v, want community source version 0.3.0", body)
	}
	targetPath := filepath.Join(pluginsDir, runtime.GOOS, runtime.GOARCH, "sample-provider-v0.3.0"+managementPluginExtension(runtime.GOOS))
	data, errRead := os.ReadFile(targetPath)
	if errRead != nil {
		t.Fatalf("ReadFile(%s) error = %v", targetPath, errRead)
	}
	if string(data) != "third-party-library-data" {
		t.Fatalf("installed file = %q, want third-party-library-data", data)
	}
	snapshotItem := cfgSnapshot.Plugins.Configs["sample-provider"]
	if snapshotItem.Enabled == nil || !*snapshotItem.Enabled {
		t.Fatalf("snapshot plugin enabled = %#v, want true", snapshotItem.Enabled)
	}
}

func TestInstallPluginFromStoreTimesOutStalledDownload(t *testing.T) {
	t.Parallel()

	archiveName := "sample-provider_0.1.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
	downloadURL := "https://downloads.example/" + archiveName
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: false,
				Dir:     t.TempDir(),
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: blockingPluginStoreHTTPClient{
			responses: fakePluginStoreHTTPClient{
				"https://registry.example/registry.json": registryJSON(t),
				"https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/latest": []byte(`{
					"tag_name": "v0.1.0",
					"assets": [
						{"name": "` + archiveName + `", "browser_download_url": "` + downloadURL + `"},
						{"name": "checksums.txt", "browser_download_url": "https://downloads.example/checksums.txt"}
					]
				}`),
				"https://downloads.example/checksums.txt": []byte("deadbeef  " + archiveName + "\n"),
			},
			blockURL: downloadURL,
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "sample-provider"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/plugin-store/sample-provider/install", nil)

	h.installPluginFromStoreWithTimeout(c, runtime.GOOS, runtime.GOARCH, time.Nanosecond)

	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusGatewayTimeout, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plugin_install_timeout") {
		t.Fatalf("body = %s, want plugin_install_timeout", rec.Body.String())
	}
}

func TestInstallPluginFromStoreRequiresSourceForDuplicateIDs(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled:      false,
				Dir:          t.TempDir(),
				StoreSources: []string{"https://community.example/registry.json"},
			},
		},
		configFilePath: writeTestConfigFile(t),
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			pluginstore.DefaultRegistryURL:            registryJSON(t),
			"https://community.example/registry.json": thirdPartySampleRegistryJSON(t),
		},
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "sample-provider"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/plugin-store/sample-provider/install", nil)

	h.InstallPluginFromStore(c)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plugin_store_source_required") {
		t.Fatalf("body = %s, want source required error", rec.Body.String())
	}
}

func TestInstallPluginFromStoreOverwritesFilePreservesConfigAndReloads(t *testing.T) {
	t.Parallel()

	pluginsDir := t.TempDir()
	existingPath := filepath.Join(pluginsDir, runtime.GOOS, runtime.GOARCH, "sample-provider-v0.1.0"+managementPluginExtension(runtime.GOOS))
	if errMkdir := os.MkdirAll(filepath.Dir(existingPath), 0o755); errMkdir != nil {
		t.Fatalf("MkdirAll(%s) error = %v", filepath.Dir(existingPath), errMkdir)
	}
	if errWrite := os.WriteFile(existingPath, []byte("old-library-data"), 0o644); errWrite != nil {
		t.Fatalf("WriteFile(%s) error = %v", existingPath, errWrite)
	}
	archiveData := makeManagementPluginStoreZip(t, "sample-provider"+managementPluginExtension(runtime.GOOS), "new-library-data")
	archiveName := "sample-provider_0.1.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".zip"
	checksum := sha256.Sum256(archiveData)
	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: true,
				Dir:     pluginsDir,
				Configs: map[string]config.PluginInstanceConfig{
					"sample-provider": pluginConfigFromYAML(t, "enabled: false\npriority: 5\nmode: fast\nextra: keep\n"),
				},
			},
		},
		configFilePath:         writeTestConfigFile(t),
		pluginStoreRegistryURL: "https://registry.example/registry.json",
		pluginStoreHTTPClient: fakePluginStoreHTTPClient{
			"https://registry.example/registry.json": registryJSON(t),
			"https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/latest": []byte(`{
				"tag_name": "v0.1.0",
				"assets": [
					{"name": "` + archiveName + `", "browser_download_url": "https://downloads.example/` + archiveName + `"},
					{"name": "checksums.txt", "browser_download_url": "https://downloads.example/checksums.txt"}
				]
			}`),
			"https://downloads.example/" + archiveName: archiveData,
			"https://downloads.example/checksums.txt":  []byte(hex.EncodeToString(checksum[:]) + "  " + archiveName + "\n"),
		},
	}
	reloads, reloadDone := captureConfigReload(h)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "sample-provider"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/v0/management/plugin-store/sample-provider/install", nil)

	h.InstallPluginFromStore(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	cfgSnapshot := waitForAsyncReload(t, reloads)
	waitForReloadDone(t, reloadDone)
	if cfgSnapshot == h.cfg {
		t.Fatalf("reload config = handler config %p, want independent snapshot", h.cfg)
	}
	data, errRead := os.ReadFile(existingPath)
	if errRead != nil {
		t.Fatalf("ReadFile(%s) error = %v", existingPath, errRead)
	}
	if string(data) != "new-library-data" {
		t.Fatalf("installed file = %q, want new-library-data", data)
	}
	item := h.cfg.Plugins.Configs["sample-provider"]
	if item.Enabled == nil || !*item.Enabled {
		t.Fatalf("plugin enabled = %#v, want true", item.Enabled)
	}
	snapshotItem := cfgSnapshot.Plugins.Configs["sample-provider"]
	if snapshotItem.Enabled == nil || !*snapshotItem.Enabled {
		t.Fatalf("snapshot plugin enabled = %#v, want true", snapshotItem.Enabled)
	}
	if item.Priority != 5 {
		t.Fatalf("plugin priority = %d, want 5", item.Priority)
	}
	if snapshotItem.Priority != 5 {
		t.Fatalf("snapshot plugin priority = %d, want 5", snapshotItem.Priority)
	}
	raw := marshalPluginRaw(t, item)
	if !strings.Contains(raw, "mode: fast") || !strings.Contains(raw, "extra: keep") {
		t.Fatalf("plugin raw config lost custom fields:\n%s", raw)
	}
	if raw := marshalPluginRaw(t, snapshotItem); !strings.Contains(raw, "mode: fast") || !strings.Contains(raw, "extra: keep") {
		t.Fatalf("snapshot plugin raw config lost custom fields:\n%s", raw)
	}
}

func TestEnablePluginConfigLockedPreservesExistingFields(t *testing.T) {
	t.Parallel()

	h := &Handler{
		cfg: &config.Config{
			Plugins: config.PluginsConfig{
				Enabled: false,
				Configs: map[string]config.PluginInstanceConfig{
					"sample-provider": pluginConfigFromYAML(t, "enabled: false\npriority: 5\nmode: fast\n"),
				},
			},
		},
	}

	if errEnable := h.enablePluginConfigLocked("sample-provider"); errEnable != nil {
		t.Fatalf("enablePluginConfigLocked() error = %v", errEnable)
	}
	if h.cfg.Plugins.Enabled {
		t.Fatal("global Plugins.Enabled changed to true")
	}
	item := h.cfg.Plugins.Configs["sample-provider"]
	if item.Enabled == nil || !*item.Enabled {
		t.Fatalf("plugin enabled = %#v, want true", item.Enabled)
	}
	if item.Priority != 5 {
		t.Fatalf("plugin priority = %d, want 5", item.Priority)
	}
	raw := marshalPluginRaw(t, item)
	if !strings.Contains(raw, "mode: fast") {
		t.Fatalf("plugin raw config lost custom field:\n%s", raw)
	}
}

func TestEnablePluginConfigLockedCreatesMissingConfig(t *testing.T) {
	t.Parallel()

	h := &Handler{cfg: &config.Config{}}
	if errEnable := h.enablePluginConfigLocked("sample-provider"); errEnable != nil {
		t.Fatalf("enablePluginConfigLocked() error = %v", errEnable)
	}
	item := h.cfg.Plugins.Configs["sample-provider"]
	if item.Enabled == nil || !*item.Enabled {
		t.Fatalf("plugin enabled = %#v, want true", item.Enabled)
	}
}

type authRecordingPluginStoreHTTPClient struct {
	responses fakePluginStoreHTTPClient
	mu        sync.Mutex
	headers   map[string]http.Header
}

func (c *authRecordingPluginStoreHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	if c.headers == nil {
		c.headers = make(map[string]http.Header)
	}
	c.headers[req.URL.String()] = req.Header.Clone()
	c.mu.Unlock()
	return c.responses.Do(req)
}

func (c *authRecordingPluginStoreHTTPClient) header(url string, name string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.headers[url].Get(name)
}

type fakePluginStoreHTTPClient map[string][]byte

func (c fakePluginStoreHTTPClient) Do(req *http.Request) (*http.Response, error) {
	body, ok := c[req.URL.String()]
	if !ok {
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader("not found")),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

type blockingPluginStoreHTTPClient struct {
	responses fakePluginStoreHTTPClient
	blockURL  string
}

func (c blockingPluginStoreHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if req.URL.String() == c.blockURL {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return c.responses.Do(req)
}

type countingPluginStoreHTTPClient struct {
	responses fakePluginStoreHTTPClient
	mu        sync.Mutex
	counts    map[string]int
}

func (c *countingPluginStoreHTTPClient) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	if c.counts == nil {
		c.counts = make(map[string]int)
	}
	c.counts[req.URL.String()]++
	c.mu.Unlock()
	return c.responses.Do(req)
}

func (c *countingPluginStoreHTTPClient) count(url string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[url]
}

func registryJSON(t *testing.T) []byte {
	t.Helper()

	return []byte(`{
		"schema_version": 1,
		"plugins": [{
			"id": "sample-provider",
			"name": "Sample Provider",
			"description": "Adds sample provider support.",
			"author": "author-name",
			"version": "0.1.0",
			"repository": "https://github.com/author-name/cliproxy-sample-provider-plugin",
			"tags": ["provider"]
		}]
	}`)
}

func thirdPartySampleRegistryJSON(t *testing.T) []byte {
	t.Helper()

	return []byte(`{
		"schema_version": 1,
		"plugins": [{
			"id": "sample-provider",
			"name": "Sample Provider Community Build",
			"description": "Adds sample provider support from a third-party source.",
			"author": "community",
			"version": "0.3.0",
			"repository": "https://github.com/community/cliproxy-sample-provider-plugin"
		}]
	}`)
}

func pluginStoreManifestFromConfig(t *testing.T, item config.PluginInstanceConfig) pluginstore.Manifest {
	t.Helper()
	raw := marshalPluginRaw(t, item)
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v; raw=\n%s", err, raw)
	}
	mapping := root.Content[0]
	var storeNode *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "store" {
			storeNode = mapping.Content[i+1]
			break
		}
	}
	if storeNode == nil {
		t.Fatalf("store node missing in raw config:\n%s", raw)
	}
	var manifest pluginstore.Manifest
	if err := storeNode.Decode(&manifest); err != nil {
		t.Fatalf("store Decode() error = %v; raw=\n%s", err, raw)
	}
	return manifest
}

func directRegistryJSON(artifactURL, checksum string) []byte {
	return []byte(`{
		"schema_version": 2,
		"plugins": [{
			"id": "sample-provider",
			"name": "Sample Provider",
			"description": "Adds sample provider support.",
			"author": "author-name",
			"version": "0.4.0",
			"install": {
				"type": "direct",
				"artifacts": [{
					"goos": "linux",
					"goarch": "amd64",
					"url": "` + artifactURL + `",
					"sha256": "` + checksum + `"
				}, {
					"goos": "darwin",
					"goarch": "arm64",
					"url": "https://downloads.example/sample-provider-darwin-arm64.zip",
					"sha256": "` + checksum + `"
				}]
			},
			"auth_required": true
		}]
	}`)
}

func directRegistryJSONWithVersionArtifact(topArtifactURL, versionArtifactURL, checksum string) []byte {
	return []byte(`{
		"schema_version": 2,
		"plugins": [{
			"id": "sample-provider",
			"name": "Sample Provider",
			"description": "Adds sample provider support.",
			"author": "author-name",
			"version": "0.4.0",
			"install": {
				"type": "direct",
				"artifacts": [{
					"goos": "linux",
					"goarch": "amd64",
					"url": "` + topArtifactURL + `",
					"sha256": "` + checksum + `"
				}]
			},
			"versions": [{
				"version": "0.3.0",
				"install": {
					"type": "direct",
					"artifacts": [{
						"goos": "` + runtime.GOOS + `",
						"goarch": "` + runtime.GOARCH + `",
						"url": "` + versionArtifactURL + `",
						"sha256": "` + checksum + `"
					}]
				}
			}]
		}]
	}`)
}

func pluginStorePlatformsContain(platforms []pluginStorePlatform, goos, goarch string) bool {
	for _, platform := range platforms {
		if platform.GOOS == goos && platform.GOARCH == goarch {
			return true
		}
	}
	return false
}

func makeManagementPluginStoreZip(t *testing.T, name string, content string) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	file, errCreate := writer.Create(name)
	if errCreate != nil {
		t.Fatalf("Create(%s) error = %v", name, errCreate)
	}
	if _, errWrite := file.Write([]byte(content)); errWrite != nil {
		t.Fatalf("Write(%s) error = %v", name, errWrite)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("Close() error = %v", errClose)
	}
	return buffer.Bytes()
}
