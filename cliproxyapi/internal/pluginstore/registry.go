package pluginstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	DefaultRegistryURL       = "https://raw.githubusercontent.com/router-for-me/CLIProxyAPI-Plugins-Store/main/registry.json"
	DefaultSourceID          = "official"
	DefaultSourceName        = "Official"
	SchemaVersion            = 1
	SchemaVersionV2          = 2
	InstallTypeGitHubRelease = "github-release"
	InstallTypeDirect        = "direct"
)

var pluginVersionPattern = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]*$`)
var pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Registry struct {
	SchemaVersion int      `json:"schema_version"`
	Plugins       []Plugin `json:"plugins"`
}

type Plugin struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Author       string      `json:"author"`
	Version      string      `json:"version"`
	Versions     []Version   `json:"versions,omitempty"`
	Repository   string      `json:"repository,omitempty"`
	Logo         string      `json:"logo,omitempty"`
	Homepage     string      `json:"homepage,omitempty"`
	License      string      `json:"license,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Install      InstallPlan `json:"install,omitempty"`
	AuthRequired bool        `json:"auth_required,omitempty"`
}

type Version struct {
	Version string      `json:"version"`
	Install InstallPlan `json:"install,omitempty"`
}

type InstallPlan struct {
	Type      string     `yaml:"type,omitempty" json:"type,omitempty"`
	Artifacts []Artifact `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
}

type Artifact struct {
	GOOS   string `yaml:"goos,omitempty" json:"goos"`
	GOARCH string `yaml:"goarch,omitempty" json:"goarch"`
	URL    string `yaml:"url,omitempty" json:"url"`
	SHA256 string `yaml:"sha256,omitempty" json:"sha256"`
	Size   int64  `yaml:"size,omitempty" json:"size,omitempty"`
}

type Platform struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

func DefaultSource() Source {
	return Source{
		ID:   DefaultSourceID,
		Name: DefaultSourceName,
		URL:  DefaultRegistryURL,
	}
}

func NormalizeSources(registryURLs []string) ([]Source, error) {
	out := []Source{DefaultSource()}
	seenIDs := map[string]string{DefaultSourceID: DefaultRegistryURL}
	seenURLs := map[string]struct{}{DefaultRegistryURL: {}}
	for _, registryURL := range registryURLs {
		registryURL = strings.TrimSpace(registryURL)
		if registryURL == "" {
			continue
		}
		if _, exists := seenURLs[registryURL]; exists {
			continue
		}
		source := Source{
			ID:   SourceID(registryURL),
			Name: SourceName(registryURL),
			URL:  registryURL,
		}
		if existingURL, exists := seenIDs[source.ID]; exists {
			return nil, fmt.Errorf("plugin store source id collision for %q and %q", existingURL, registryURL)
		}
		seenIDs[source.ID] = registryURL
		seenURLs[registryURL] = struct{}{}
		out = append(out, source)
	}
	return out, nil
}

func SourceID(registryURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(registryURL)))
	return "source-" + hex.EncodeToString(sum[:])[:12]
}

func SourceName(registryURL string) string {
	parsed, errParse := url.Parse(strings.TrimSpace(registryURL))
	if errParse != nil || strings.TrimSpace(parsed.Host) == "" {
		return strings.TrimSpace(registryURL)
	}
	return parsed.Host
}

func ParseRegistry(data []byte) (Registry, error) {
	var registry Registry
	decoder := json.NewDecoder(bytes.NewReader(data))
	if errDecode := decoder.Decode(&registry); errDecode != nil {
		return Registry{}, fmt.Errorf("decode registry: %w", errDecode)
	}
	normalizeRegistry(&registry)
	if errValidate := ValidateRegistry(registry); errValidate != nil {
		return Registry{}, errValidate
	}
	return registry, nil
}

func normalizeRegistry(registry *Registry) {
	if registry == nil {
		return
	}
	for index := range registry.Plugins {
		plugin := &registry.Plugins[index]
		plugin.ID = strings.TrimSpace(plugin.ID)
		plugin.Name = strings.TrimSpace(plugin.Name)
		plugin.Description = strings.TrimSpace(plugin.Description)
		plugin.Author = strings.TrimSpace(plugin.Author)
		plugin.Version = normalizeVersion(plugin.Version)
		for versionIndex := range plugin.Versions {
			plugin.Versions[versionIndex].Version = normalizeVersion(plugin.Versions[versionIndex].Version)
			plugin.Versions[versionIndex].Install = NormalizeInstallPlan(plugin.Versions[versionIndex].Install)
		}
		plugin.Repository = strings.TrimSpace(plugin.Repository)
		plugin.Install = NormalizeInstallPlan(plugin.Install)
		plugin.Logo = strings.TrimSpace(plugin.Logo)
		plugin.Homepage = strings.TrimSpace(plugin.Homepage)
		plugin.License = strings.TrimSpace(plugin.License)
		for tagIndex := range plugin.Tags {
			plugin.Tags[tagIndex] = strings.TrimSpace(plugin.Tags[tagIndex])
		}
	}
}

func ValidateRegistry(registry Registry) error {
	if registry.SchemaVersion != SchemaVersion && registry.SchemaVersion != SchemaVersionV2 {
		return fmt.Errorf("unsupported schema_version %d", registry.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(registry.Plugins))
	for index, plugin := range registry.Plugins {
		if registry.SchemaVersion == SchemaVersion && PluginInstallType(plugin) == InstallTypeDirect {
			return fmt.Errorf("plugins[%d]: direct install requires schema_version %d", index, SchemaVersionV2)
		}
		if errValidate := ValidatePlugin(plugin); errValidate != nil {
			return fmt.Errorf("plugins[%d]: %w", index, errValidate)
		}
		id := strings.TrimSpace(plugin.ID)
		if _, exists := seen[id]; exists {
			return fmt.Errorf("plugins[%d]: duplicate plugin id %q", index, id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

func ValidatePlugin(plugin Plugin) error {
	required := map[string]string{
		"id":          plugin.ID,
		"name":        plugin.Name,
		"description": plugin.Description,
		"author":      plugin.Author,
	}
	installType := PluginInstallType(plugin)
	if installType == InstallTypeGitHubRelease {
		required["repository"] = plugin.Repository
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("missing required field %s", field)
		}
	}
	if !validPluginID(strings.TrimSpace(plugin.ID)) {
		return fmt.Errorf("invalid plugin id %q", plugin.ID)
	}
	// The version is optional for GitHub releases since the latest release is the
	// source of truth. Direct installs require a registry version below.
	if version := strings.TrimSpace(plugin.Version); version != "" && !validPluginVersion(version) {
		return fmt.Errorf("invalid plugin version %q", plugin.Version)
	}
	switch installType {
	case InstallTypeGitHubRelease:
		if _, _, errRepository := GitHubRepositoryParts(plugin.Repository); errRepository != nil {
			return errRepository
		}
	case InstallTypeDirect:
		if strings.TrimSpace(plugin.Version) == "" {
			return fmt.Errorf("missing required field version")
		}
		if errPlan := ValidateDirectPlugin(plugin); errPlan != nil {
			return errPlan
		}
	default:
		return fmt.Errorf("unsupported install type %q", installType)
	}
	return nil
}

func PluginInstallType(plugin Plugin) string {
	installType := strings.ToLower(strings.TrimSpace(plugin.Install.Type))
	if installType == "" {
		return InstallTypeGitHubRelease
	}
	return installType
}

func NormalizeInstallPlan(plan InstallPlan) InstallPlan {
	plan.Type = strings.ToLower(strings.TrimSpace(plan.Type))
	if plan.Type == "" {
		plan.Type = InstallTypeGitHubRelease
	}
	out := InstallPlan{Type: plan.Type}
	for _, artifact := range plan.Artifacts {
		artifact.GOOS = normalizeGOOS(artifact.GOOS)
		artifact.GOARCH = normalizeGOARCH(artifact.GOARCH)
		artifact.URL = strings.TrimSpace(artifact.URL)
		artifact.SHA256 = strings.ToLower(strings.TrimSpace(artifact.SHA256))
		out.Artifacts = append(out.Artifacts, artifact)
	}
	return out
}

func ValidateInstallPlan(plan InstallPlan) error {
	plan = NormalizeInstallPlan(plan)
	if plan.Type != InstallTypeDirect && plan.Type != InstallTypeGitHubRelease {
		return fmt.Errorf("unsupported install type %q", plan.Type)
	}
	if plan.Type != InstallTypeDirect {
		return nil
	}
	if len(plan.Artifacts) == 0 {
		return fmt.Errorf("direct install requires artifacts")
	}
	seen := make(map[string]struct{}, len(plan.Artifacts))
	for index, artifact := range plan.Artifacts {
		if errValidate := ValidateArtifact(artifact); errValidate != nil {
			return fmt.Errorf("artifacts[%d]: %w", index, errValidate)
		}
		key := artifact.GOOS + "/" + artifact.GOARCH
		if _, exists := seen[key]; exists {
			return fmt.Errorf("artifacts[%d]: duplicate platform %s", index, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func ValidateArtifact(artifact Artifact) error {
	artifact.GOOS = normalizeGOOS(artifact.GOOS)
	artifact.GOARCH = normalizeGOARCH(artifact.GOARCH)
	if artifact.GOOS == "" {
		return fmt.Errorf("missing required field goos")
	}
	if artifact.GOARCH == "" {
		return fmt.Errorf("missing required field goarch")
	}
	if strings.TrimSpace(artifact.URL) == "" {
		return fmt.Errorf("missing required field url")
	}
	parsed, errParse := url.Parse(strings.TrimSpace(artifact.URL))
	if errParse != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid artifact url")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("artifact url must use http or https")
	}
	if hasSensitiveQueryParameter(parsed) {
		return fmt.Errorf("artifact url contains sensitive query parameter")
	}
	checksum := strings.TrimSpace(artifact.SHA256)
	if checksum == "" {
		return fmt.Errorf("artifact checksum missing")
	}
	if len(checksum) != sha256.Size*2 {
		return fmt.Errorf("invalid artifact checksum")
	}
	if _, errDecode := hex.DecodeString(checksum); errDecode != nil {
		return fmt.Errorf("invalid artifact checksum")
	}
	if artifact.Size < 0 {
		return fmt.Errorf("artifact size must not be negative")
	}
	return nil
}

func ValidateDirectPlugin(plugin Plugin) error {
	if errPlan := ValidateInstallPlan(plugin.Install); errPlan != nil {
		return errPlan
	}
	for index, candidate := range plugin.Versions {
		if strings.TrimSpace(candidate.Version) == "" {
			return fmt.Errorf("versions[%d]: missing required field version", index)
		}
		if !validPluginVersion(normalizeVersion(candidate.Version)) {
			return fmt.Errorf("versions[%d]: invalid plugin version %q", index, candidate.Version)
		}
		plan := NormalizeInstallPlan(candidate.Install)
		if plan.Type == "" {
			plan.Type = InstallTypeDirect
		}
		if plan.Type != InstallTypeDirect {
			return fmt.Errorf("versions[%d]: unsupported install type %q", index, plan.Type)
		}
		if errPlan := ValidateInstallPlan(plan); errPlan != nil {
			return fmt.Errorf("versions[%d]: %w", index, errPlan)
		}
	}
	return nil
}

func PluginPlatforms(plugin Plugin) []Platform {
	seen := map[string]struct{}{}
	platforms := make([]Platform, 0)
	add := func(plan InstallPlan) {
		plan = NormalizeInstallPlan(plan)
		if plan.Type != InstallTypeDirect {
			return
		}
		for _, artifact := range plan.Artifacts {
			if artifact.GOOS == "" || artifact.GOARCH == "" {
				continue
			}
			key := artifact.GOOS + "/" + artifact.GOARCH
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			platforms = append(platforms, Platform{GOOS: artifact.GOOS, GOARCH: artifact.GOARCH})
		}
	}
	add(plugin.Install)
	for _, candidate := range plugin.Versions {
		add(candidate.Install)
	}
	return platforms
}

func PluginArtifacts(plugin Plugin) []Artifact {
	artifacts := make([]Artifact, 0)
	collect := func(plan InstallPlan) {
		plan = NormalizeInstallPlan(plan)
		if plan.Type == InstallTypeDirect {
			artifacts = append(artifacts, plan.Artifacts...)
		}
	}
	collect(plugin.Install)
	for _, candidate := range plugin.Versions {
		collect(candidate.Install)
	}
	return artifacts
}

func normalizeGOOS(goos string) string {
	return strings.ToLower(strings.TrimSpace(goos))
}

func normalizeGOARCH(goarch string) string {
	return strings.ToLower(strings.TrimSpace(goarch))
}

func validPluginVersion(version string) bool {
	return version != "" && !strings.HasPrefix(version, "v") && pluginVersionPattern.MatchString(version)
}

func validPluginID(id string) bool {
	return pluginIDPattern.MatchString(id)
}

func GitHubRepositoryParts(repository string) (string, string, error) {
	repository = strings.TrimSpace(repository)
	parsed, errParse := url.Parse(repository)
	if errParse != nil {
		return "", "", fmt.Errorf("invalid repository URL: %w", errParse)
	}
	if parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", fmt.Errorf("repository must be https://github.com/{owner}/{repo}")
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
		return "", "", fmt.Errorf("repository must be https://github.com/{owner}/{repo}")
	}
	owner, errOwner := url.PathUnescape(segments[0])
	if errOwner != nil {
		return "", "", fmt.Errorf("invalid repository owner: %w", errOwner)
	}
	repo, errRepo := url.PathUnescape(segments[1])
	if errRepo != nil {
		return "", "", fmt.Errorf("invalid repository name: %w", errRepo)
	}
	if strings.HasSuffix(repo, ".git") {
		return "", "", fmt.Errorf("repository must be https://github.com/{owner}/{repo}")
	}
	return owner, repo, nil
}

func (r Registry) PluginByID(id string) (Plugin, bool) {
	id = strings.TrimSpace(id)
	for _, plugin := range r.Plugins {
		if strings.TrimSpace(plugin.ID) == id {
			return plugin, true
		}
	}
	return Plugin{}, false
}
