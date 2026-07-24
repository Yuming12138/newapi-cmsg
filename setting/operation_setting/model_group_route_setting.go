package operation_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

type ModelGroupRouteSetting struct {
	Enabled        bool     `json:"enabled"`
	UserGroups     []string `json:"user_groups"`
	SourceGroups   []string `json:"source_groups"`
	ModelPrefixes  []string `json:"model_prefixes"`
	PreferredGroup string   `json:"preferred_group"`
	FallbackGroup  string   `json:"fallback_group"`
	FallbackGroups []string `json:"fallback_groups"`
}

var modelGroupRouteSetting = ModelGroupRouteSetting{
	Enabled:        false,
	UserGroups:     []string{"cmsg"},
	SourceGroups:   []string{"asxs", "cmsg"},
	ModelPrefixes:  []string{"gpt-5.6-sol", "gpt-image"},
	PreferredGroup: "cliproxy-codex",
	FallbackGroup:  "asxs",
	FallbackGroups: []string{"asxs", "asxs-grok"},
}

func init() {
	config.GlobalConfig.Register("model_group_route_setting", &modelGroupRouteSetting)
}

func GetModelGroupRouteSetting() *ModelGroupRouteSetting {
	return &modelGroupRouteSetting
}

func (s *ModelGroupRouteSetting) GetFallbackGroups() []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(s.FallbackGroups)+1)
	appendGroup := func(group string) {
		group = strings.TrimSpace(group)
		if group == "" {
			return
		}
		key := strings.ToLower(group)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, group)
	}
	for _, group := range s.FallbackGroups {
		appendGroup(group)
	}
	appendGroup(s.FallbackGroup)
	return out
}

func (s *ModelGroupRouteSetting) PrimaryFallbackGroup() string {
	groups := s.GetFallbackGroups()
	if len(groups) == 0 {
		return ""
	}
	return groups[0]
}
