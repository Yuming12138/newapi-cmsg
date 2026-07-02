package pluginstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/httpfetch"
)

const userAgent = "CLIProxyAPI"

// HTTPDoer abstracts the HTTP client used to execute requests.
type HTTPDoer = httpfetch.Doer

type Client struct {
	HTTPClient  HTTPDoer
	RegistryURL string
	UserAgent   string
	Auth        []AuthConfig
}

type Release struct {
	TagName string         `json:"tag_name"`
	Assets  []ReleaseAsset `json:"assets"`
}

type ReleaseAsset struct {
	APIURL             string `json:"url"`
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func (c Client) FetchRegistry(ctx context.Context) (Registry, error) {
	registryURL := strings.TrimSpace(c.RegistryURL)
	if registryURL == "" {
		registryURL = DefaultRegistryURL
	}
	data, errDownload := c.get(ctx, registryURL, "application/json", RequestKindRegistry)
	if errDownload != nil {
		return Registry{}, errDownload
	}
	registry, errParse := ParseRegistry(data)
	if errParse != nil {
		return Registry{}, errParse
	}
	return registry, nil
}

// FetchLatestRelease returns the latest published release of the plugin's
// GitHub repository, mirroring the WebUI panel update check.
func (c Client) FetchLatestRelease(ctx context.Context, plugin Plugin) (Release, error) {
	owner, repo, errRepository := GitHubRepositoryParts(plugin.Repository)
	if errRepository != nil {
		return Release{}, errRepository
	}
	releaseURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/releases/latest",
		url.PathEscape(owner),
		url.PathEscape(repo),
	)
	data, errDownload := c.get(ctx, releaseURL, "application/vnd.github+json", RequestKindMetadata)
	if errDownload != nil {
		return Release{}, errDownload
	}
	var release Release
	if errDecode := json.Unmarshal(data, &release); errDecode != nil {
		return Release{}, fmt.Errorf("decode release: %w", errDecode)
	}
	return release, nil
}

// FetchReleaseByTag returns a published release by its exact GitHub tag.
func (c Client) FetchReleaseByTag(ctx context.Context, plugin Plugin, tag string) (Release, error) {
	owner, repo, errRepository := GitHubRepositoryParts(plugin.Repository)
	if errRepository != nil {
		return Release{}, errRepository
	}
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return Release{}, fmt.Errorf("release tag is required")
	}
	releaseURL := fmt.Sprintf(
		"https://api.github.com/repos/%s/%s/releases/tags/%s",
		url.PathEscape(owner),
		url.PathEscape(repo),
		url.PathEscape(tag),
	)
	data, errDownload := c.get(ctx, releaseURL, "application/vnd.github+json", RequestKindMetadata)
	if errDownload != nil {
		return Release{}, errDownload
	}
	var release Release
	if errDecode := json.Unmarshal(data, &release); errDecode != nil {
		return Release{}, fmt.Errorf("decode release: %w", errDecode)
	}
	return release, nil
}

// ReleaseVersion derives the plugin version from the release tag, stripping a
// leading "v"/"V" and validating the result.
func ReleaseVersion(release Release) (string, error) {
	version := normalizeVersion(release.TagName)
	if !validPluginVersion(version) {
		return "", fmt.Errorf("invalid release tag %q", release.TagName)
	}
	return version, nil
}

func (c Client) DownloadAsset(ctx context.Context, asset ReleaseAsset) ([]byte, error) {
	downloadURL := strings.TrimSpace(asset.BrowserDownloadURL)
	apiURL := strings.TrimSpace(asset.APIURL)
	if downloadURL == "" || c.releaseAssetAPIAuthenticated(apiURL) {
		if apiURL != "" {
			downloadURL = apiURL
		}
	}
	if downloadURL == "" {
		return nil, fmt.Errorf("asset %q missing download url", asset.Name)
	}
	return c.get(ctx, downloadURL, "application/octet-stream", RequestKindArtifact)
}

func (c Client) releaseAssetAPIAuthenticated(apiURL string) bool {
	apiURL = strings.TrimSpace(apiURL)
	if apiURL == "" {
		return false
	}
	return AuthConfigured(c.Auth, apiURL, RequestKindArtifact)
}

func (c Client) get(ctx context.Context, requestURL string, accept string, kind string) ([]byte, error) {
	if errURL := validatePluginStoreRequestURL(c.Auth, requestURL, kind); errURL != nil {
		return nil, errURL
	}
	headers := http.Header{
		"Accept":     []string{accept},
		"User-Agent": []string{c.userAgent()},
	}
	if errAuth := applyPluginStoreAuth(headers, c.Auth, requestURL, kind); errAuth != nil {
		return nil, errAuth
	}
	if headers.Get("Authorization") == "" {
		if token := gitHubAPIToken(requestURL); token != "" {
			headers.Set("Authorization", "Bearer "+token)
		}
	}
	return httpfetch.GetBytes(ctx, c.httpClient(), requestURL, headerValues(headers), 0)
}

func headerValues(headers http.Header) map[string]string {
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	return out
}

// gitHubAPIToken returns the optional GitHub token for GitHub API requests to
// raise the unauthenticated rate limit, mirroring the management asset updater.
func gitHubAPIToken(requestURL string) string {
	parsed, errParse := url.Parse(requestURL)
	if errParse != nil || !strings.EqualFold(parsed.Host, "api.github.com") {
		return ""
	}
	gitURL := strings.ToLower(strings.TrimSpace(os.Getenv("GITSTORE_GIT_URL")))
	if !strings.Contains(gitURL, "github.com") {
		return ""
	}
	return strings.TrimSpace(os.Getenv("GITSTORE_GIT_TOKEN"))
}

func (c Client) httpClient() HTTPDoer {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c Client) userAgent() string {
	if strings.TrimSpace(c.UserAgent) != "" {
		return strings.TrimSpace(c.UserAgent)
	}
	return userAgent
}

func SelectReleaseAssets(release Release, id, version, goos, goarch string) (ReleaseAsset, ReleaseAsset, error) {
	archiveName := ArchiveName(id, version, goos, goarch)
	var archiveAsset ReleaseAsset
	var checksumAsset ReleaseAsset
	for _, asset := range release.Assets {
		switch strings.TrimSpace(asset.Name) {
		case archiveName:
			archiveAsset = asset
		case "checksums.txt":
			checksumAsset = asset
		}
	}
	if strings.TrimSpace(archiveAsset.Name) == "" {
		return ReleaseAsset{}, ReleaseAsset{}, fmt.Errorf("release asset %s not found", archiveName)
	}
	if strings.TrimSpace(checksumAsset.Name) == "" {
		return ReleaseAsset{}, ReleaseAsset{}, fmt.Errorf("release asset checksums.txt not found")
	}
	return archiveAsset, checksumAsset, nil
}

func ArchiveName(id, version, goos, goarch string) string {
	return fmt.Sprintf(
		"%s_%s_%s_%s.zip",
		strings.TrimSpace(id),
		strings.TrimSpace(version),
		strings.TrimSpace(goos),
		strings.TrimSpace(goarch),
	)
}
