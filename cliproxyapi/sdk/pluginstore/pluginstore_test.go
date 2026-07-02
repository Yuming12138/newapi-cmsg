package pluginstore

import (
	"strings"
	"testing"
)

func TestNormalizeAuthConfigsAndAuthConfigured(t *testing.T) {
	t.Setenv("SDK_STORE_TOKEN", "sdk-token")

	auth := NormalizeAuthConfigs([]AuthConfig{{
		Match:    " https://private.example/store/ ",
		ApplyTo:  []string{" Artifact ", "artifact"},
		Type:     " Bearer ",
		TokenEnv: " SDK_STORE_TOKEN ",
	}})
	if len(auth) != 1 {
		t.Fatalf("NormalizeAuthConfigs() len = %d, want 1", len(auth))
	}
	if auth[0].Match != "https://private.example/store/" || auth[0].Type != AuthTypeBearer || auth[0].TokenEnv != "SDK_STORE_TOKEN" {
		t.Fatalf("normalized auth = %#v", auth[0])
	}
	if !AuthConfigured(auth, "https://private.example/store/plugin.zip", RequestKindArtifact) {
		t.Fatal("AuthConfigured() = false, want true for matching artifact")
	}
	if AuthConfigured(auth, "https://private.example/store/registry.json", RequestKindRegistry) {
		t.Fatal("AuthConfigured() = true, want false for non-matching request kind")
	}
}

func TestManifestValidateRequiresPinnedReleaseTag(t *testing.T) {
	manifest := validTestManifest()
	manifest.ReleaseTag = ""

	errValidate := manifest.Validate()
	if errValidate == nil {
		t.Fatal("Validate() error = nil, want release-tag error")
	}
	if !strings.Contains(errValidate.Error(), "release-tag") {
		t.Fatalf("Validate() error = %v, want release-tag", errValidate)
	}
}

func TestManifestValidateRejectsReleaseTagVersionMismatch(t *testing.T) {
	manifest := validTestManifest()
	manifest.ReleaseTag = "v0.3.0"

	errValidate := manifest.Validate()
	if errValidate == nil {
		t.Fatal("Validate() error = nil, want version mismatch")
	}
	if !strings.Contains(errValidate.Error(), "resolves version") {
		t.Fatalf("Validate() error = %v, want version mismatch", errValidate)
	}
}

func TestManifestFromReleaseBuildsPinnedManifest(t *testing.T) {
	manifest, errManifest := ManifestFromRelease(
		DefaultSource(),
		Plugin{
			ID:          "sample-provider",
			Name:        "Sample Provider",
			Description: "Adds sample provider support.",
			Author:      "author-name",
			Repository:  "https://github.com/author-name/sample-provider",
		},
		Release{TagName: "v0.2.0"},
	)
	if errManifest != nil {
		t.Fatalf("ManifestFromRelease() error = %v", errManifest)
	}
	if errValidate := manifest.Validate(); errValidate != nil {
		t.Fatalf("Validate() error = %v", errValidate)
	}
	if manifest.Version != "0.2.0" || manifest.ReleaseTag != "v0.2.0" {
		t.Fatalf("manifest version fields = %q/%q, want 0.2.0/v0.2.0", manifest.Version, manifest.ReleaseTag)
	}
}

func validTestManifest() Manifest {
	return Manifest{
		ID:          "sample-provider",
		Name:        "Sample Provider",
		Description: "Adds sample provider support.",
		Author:      "author-name",
		Version:     "0.2.0",
		ReleaseTag:  "v0.2.0",
		Repository:  "https://github.com/author-name/sample-provider",
	}
}
