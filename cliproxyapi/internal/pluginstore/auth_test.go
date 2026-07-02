package pluginstore

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestNormalizeAuthConfigsTrimsAndDedupes(t *testing.T) {
	t.Parallel()

	auth := NormalizeAuthConfigs([]AuthConfig{
		{},
		{
			Match:          " https://private.example/store/ ",
			ApplyTo:        []string{" Registry ", "", "registry", "ARTIFACT"},
			Type:           " Bearer ",
			TokenEnv:       " TOKEN_ENV ",
			UsernameEnv:    " USER_ENV ",
			PasswordEnv:    " PASS_ENV ",
			HeaderName:     " X-Store-Token ",
			HeaderValueEnv: " HEADER_ENV ",
		},
	})

	if len(auth) != 1 {
		t.Fatalf("NormalizeAuthConfigs() len = %d, want 1", len(auth))
	}
	got := auth[0]
	if got.Match != "https://private.example/store/" || got.Type != AuthTypeBearer || got.TokenEnv != "TOKEN_ENV" || got.UsernameEnv != "USER_ENV" || got.PasswordEnv != "PASS_ENV" || got.HeaderName != "X-Store-Token" || got.HeaderValueEnv != "HEADER_ENV" {
		t.Fatalf("normalized auth = %#v", got)
	}
	if len(got.ApplyTo) != 2 || got.ApplyTo[0] != RequestKindRegistry || got.ApplyTo[1] != RequestKindArtifact {
		t.Fatalf("ApplyTo = %#v, want registry/artifact", got.ApplyTo)
	}
}

func TestClientAppliesStoreAuth(t *testing.T) {
	t.Setenv("STORE_BEARER_TOKEN", "registry-token")
	t.Setenv("STORE_BASIC_USER", "store-user")
	t.Setenv("STORE_BASIC_PASS", "store-pass")
	t.Setenv("STORE_HEADER_VALUE", "custom-token")

	checks := map[string]func(*testing.T, *http.Request){
		"https://registry.example/registry.json": func(t *testing.T, req *http.Request) {
			t.Helper()
			if got := req.Header.Get("Authorization"); got != "Bearer registry-token" {
				t.Fatalf("registry Authorization = %q", got)
			}
		},
		"https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/latest": func(t *testing.T, req *http.Request) {
			t.Helper()
			want := "Basic " + base64.StdEncoding.EncodeToString([]byte("store-user:store-pass"))
			if got := req.Header.Get("Authorization"); got != want {
				t.Fatalf("metadata Authorization = %q, want %q", got, want)
			}
		},
		"https://downloads.example/sample.zip": func(t *testing.T, req *http.Request) {
			t.Helper()
			if got := req.Header.Get("X-Store-Token"); got != "custom-token" {
				t.Fatalf("artifact X-Store-Token = %q", got)
			}
		},
	}

	client := Client{
		RegistryURL: "https://registry.example/registry.json",
		HTTPClient: checkingHTTPDoer{t: t, checks: checks, responses: map[string]string{
			"https://registry.example/registry.json":                                                   `{"schema_version":1,"plugins":[]}`,
			"https://api.github.com/repos/author-name/cliproxy-sample-provider-plugin/releases/latest": `{"tag_name":"v0.1.0","assets":[]}`,
			"https://downloads.example/sample.zip":                                                     "zip-data",
		}},
		Auth: []AuthConfig{
			{Match: "https://registry.example/", ApplyTo: []string{RequestKindRegistry}, Type: AuthTypeBearer, TokenEnv: "STORE_BEARER_TOKEN"},
			{Match: "https://api.github.com/repos/author-name/", ApplyTo: []string{RequestKindMetadata}, Type: AuthTypeBasic, UsernameEnv: "STORE_BASIC_USER", PasswordEnv: "STORE_BASIC_PASS"},
			{Match: "https://downloads.example/", ApplyTo: []string{RequestKindArtifact}, Type: AuthTypeHeader, HeaderName: "X-Store-Token", HeaderValueEnv: "STORE_HEADER_VALUE"},
		},
	}

	if _, errRegistry := client.FetchRegistry(context.Background()); errRegistry != nil {
		t.Fatalf("FetchRegistry() error = %v", errRegistry)
	}
	if _, errRelease := client.FetchLatestRelease(context.Background(), testPlugin()); errRelease != nil {
		t.Fatalf("FetchLatestRelease() error = %v", errRelease)
	}
	data, errDownload := client.DownloadAsset(context.Background(), ReleaseAsset{Name: "sample.zip", BrowserDownloadURL: "https://downloads.example/sample.zip"})
	if errDownload != nil {
		t.Fatalf("DownloadAsset() error = %v", errDownload)
	}
	if string(data) != "zip-data" {
		t.Fatalf("DownloadAsset() data = %q", data)
	}
}

func TestClientRejectsUnsafeStoreURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		requestURL string
		auth       []AuthConfig
		wantErr    string
	}{
		{
			name:       "http without allow insecure",
			requestURL: "http://registry.example/registry.json",
			wantErr:    "insecure plugin store url",
		},
		{
			name:       "sensitive query",
			requestURL: "https://registry.example/registry.json?token=secret",
			wantErr:    "sensitive query parameter",
		},
		{
			name:       "http with allow insecure",
			requestURL: "http://registry.example/registry.json",
			auth:       []AuthConfig{{Match: "http://registry.example/", AllowInsecure: true}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := Client{
				RegistryURL: tt.requestURL,
				HTTPClient:  checkingHTTPDoer{t: t, responses: map[string]string{tt.requestURL: `{"schema_version":1,"plugins":[]}`}},
				Auth:        tt.auth,
			}
			_, errRegistry := client.FetchRegistry(context.Background())
			if tt.wantErr == "" {
				if errRegistry != nil {
					t.Fatalf("FetchRegistry() error = %v", errRegistry)
				}
				return
			}
			if errRegistry == nil || !strings.Contains(errRegistry.Error(), tt.wantErr) {
				t.Fatalf("FetchRegistry() error = %v, want %q", errRegistry, tt.wantErr)
			}
		})
	}
}

func TestDownloadAssetPrefersAuthenticatedAPIURL(t *testing.T) {
	t.Setenv("STORE_GITHUB_TOKEN", "github-token")
	apiURL := "https://api.github.com/repos/owner/repo/releases/assets/1"
	browserURL := "https://github.com/owner/repo/releases/download/v0.1.0/sample.zip"
	client := Client{
		HTTPClient: checkingHTTPDoer{t: t, checks: map[string]func(*testing.T, *http.Request){
			apiURL: func(t *testing.T, req *http.Request) {
				t.Helper()
				if got := req.Header.Get("Authorization"); got != "Bearer github-token" {
					t.Fatalf("Authorization = %q", got)
				}
			},
		}, responses: map[string]string{
			apiURL: "api-asset-data",
		}},
		Auth: []AuthConfig{{Match: "https://api.github.com/repos/owner/repo/", ApplyTo: []string{RequestKindArtifact}, Type: AuthTypeGitHubToken, TokenEnv: "STORE_GITHUB_TOKEN"}},
	}

	data, errDownload := client.DownloadAsset(context.Background(), ReleaseAsset{Name: "sample.zip", APIURL: apiURL, BrowserDownloadURL: browserURL})
	if errDownload != nil {
		t.Fatalf("DownloadAsset() error = %v", errDownload)
	}
	if string(data) != "api-asset-data" {
		t.Fatalf("DownloadAsset() data = %q", data)
	}
}

func TestDownloadAssetPrefersBrowserURLWithoutAPIAuth(t *testing.T) {
	t.Parallel()

	apiURL := "https://api.github.com/repos/owner/repo/releases/assets/1"
	browserURL := "https://github.com/owner/repo/releases/download/v0.1.0/sample.zip"
	client := Client{
		HTTPClient: checkingHTTPDoer{t: t, checks: map[string]func(*testing.T, *http.Request){
			browserURL: func(t *testing.T, req *http.Request) {
				t.Helper()
				if req.URL.String() != browserURL {
					t.Fatalf("request URL = %q, want browser URL", req.URL.String())
				}
			},
		}, responses: map[string]string{
			browserURL: "browser-asset-data",
		}},
	}

	data, errDownload := client.DownloadAsset(context.Background(), ReleaseAsset{Name: "sample.zip", APIURL: apiURL, BrowserDownloadURL: browserURL})
	if errDownload != nil {
		t.Fatalf("DownloadAsset() error = %v", errDownload)
	}
	if string(data) != "browser-asset-data" {
		t.Fatalf("DownloadAsset() data = %q", data)
	}
}

type checkingHTTPDoer struct {
	t         *testing.T
	checks    map[string]func(*testing.T, *http.Request)
	responses map[string]string
}

func (c checkingHTTPDoer) Do(req *http.Request) (*http.Response, error) {
	c.t.Helper()
	if check := c.checks[req.URL.String()]; check != nil {
		check(c.t, req)
	}
	body, ok := c.responses[req.URL.String()]
	if !ok {
		c.t.Fatalf("unexpected request URL %q", req.URL.String())
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}
