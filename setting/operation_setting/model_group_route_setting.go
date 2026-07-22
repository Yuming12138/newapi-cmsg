package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type ModelGroupRouteSetting struct {
	Enabled        bool     `json:"enabled"`
	UserGroups     []string `json:"user_groups"`
	SourceGroups   []string `json:"source_groups"`
	ModelPrefixes  []string `json:"model_prefixes"`
	PreferredGroup string   `json:"preferred_group"`
	FallbackGroup  string   `json:"fallback_group"`
}

var modelGroupRouteSetting = ModelGroupRouteSetting{
	Enabled:        false,
	UserGroups:     []string{"cmsg"},
	SourceGroups:   []string{"asxs", "cmsg"},
	ModelPrefixes:  []string{"gpt-5.6", "gpt-image"},
	PreferredGroup: "cliproxy-codex",
	FallbackGroup:  "asxs",
}

func init() {
	config.GlobalConfig.Register("model_group_route_setting", &modelGroupRouteSetting)
}

func GetModelGroupRouteSetting() *ModelGroupRouteSetting {
	return &modelGroupRouteSetting
}
