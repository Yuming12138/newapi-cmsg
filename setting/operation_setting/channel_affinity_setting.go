package operation_setting

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type ChannelAffinityKeySource struct {
	Type string `json:"type"` // context_int, context_string, request_header, gjson
	Key  string `json:"key,omitempty"`
	Path string `json:"path,omitempty"`
}

type ChannelAffinityRule struct {
	Name             string                     `json:"name"`
	ModelRegex       []string                   `json:"model_regex"`
	PathRegex        []string                   `json:"path_regex"`
	UserAgentInclude []string                   `json:"user_agent_include,omitempty"`
	KeySources       []ChannelAffinityKeySource `json:"key_sources"`

	ValueRegex string `json:"value_regex"`
	TTLSeconds int    `json:"ttl_seconds"`

	ParamOverrideTemplate map[string]interface{} `json:"param_override_template,omitempty"`

	SkipRetryOnFailure bool `json:"skip_retry_on_failure"`

	IncludeUsingGroup bool `json:"include_using_group"`
	IncludeModelName  bool `json:"include_model_name"`
	IncludeRuleName   bool `json:"include_rule_name"`
}

type ChannelAffinitySetting struct {
	Enabled               bool                  `json:"enabled"`
	SwitchOnSuccess       bool                  `json:"switch_on_success"`
	KeepOnChannelDisabled bool                  `json:"keep_on_channel_disabled"`
	MaxEntries            int                   `json:"max_entries"`
	DefaultTTLSeconds     int                   `json:"default_ttl_seconds"`
	Rules                 []ChannelAffinityRule `json:"rules"`
}

const codexCliAffinityRuleName = "codex cli trace"

var codexCliPassThroughHeaders = []string{
	"Originator",
	"Session_id",
	"Thread_id",
	"Session-Id",
	"Thread-Id",
	"X-Client-Request-Id",
	"User-Agent",
	"X-Codex-Beta-Features",
	"X-Codex-Turn-State",
	"X-Codex-Turn-Metadata",
	"X-Codex-Window-Id",
	"X-Codex-Parent-Thread-Id",
	"X-OpenAI-Subagent",
	"X-OpenAI-Memgen-Request",
	"X-ResponsesAPI-Include-Timing-Metrics",
	"X-OpenAI-Internal-Codex-Responses-Lite",
}

var claudeCliPassThroughHeaders = []string{
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-Os",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
	"User-Agent",
	"X-App",
	"Anthropic-Beta",
	"Anthropic-Dangerous-Direct-Browser-Access",
	"Anthropic-Version",
}

func buildPassHeaderTemplate(headers []string) map[string]interface{} {
	clonedHeaders := make([]string, 0, len(headers))
	clonedHeaders = append(clonedHeaders, headers...)
	return map[string]interface{}{
		"operations": []map[string]interface{}{
			{
				"mode":        "pass_headers",
				"value":       clonedHeaders,
				"keep_origin": true,
			},
		},
	}
}

var channelAffinitySetting = ChannelAffinitySetting{
	Enabled:               true,
	SwitchOnSuccess:       true,
	KeepOnChannelDisabled: false,
	MaxEntries:            100_000,
	DefaultTTLSeconds:     3600,
	Rules: []ChannelAffinityRule{
		{
			Name:       "codex cli trace",
			ModelRegex: []string{"^gpt-.*$"},
			PathRegex:  []string{"/v1/responses"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "prompt_cache_key"},
			},
			ValueRegex:            "",
			TTLSeconds:            0,
			ParamOverrideTemplate: buildPassHeaderTemplate(codexCliPassThroughHeaders),
			SkipRetryOnFailure:    true,
			IncludeUsingGroup:     true,
			IncludeRuleName:       true,
			UserAgentInclude:      nil,
		},
		{
			Name:       "claude cli trace",
			ModelRegex: []string{"^claude-.*$"},
			PathRegex:  []string{"/v1/messages"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"},
			},
			ValueRegex:            "",
			TTLSeconds:            0,
			ParamOverrideTemplate: buildPassHeaderTemplate(claudeCliPassThroughHeaders),
			SkipRetryOnFailure:    true,
			IncludeUsingGroup:     true,
			IncludeRuleName:       true,
			UserAgentInclude:      nil,
		},
	},
}

var (
	channelAffinitySettingUpdateMu sync.Mutex
	channelAffinitySettingSnapshot atomic.Pointer[ChannelAffinitySetting]
)

type channelAffinitySettingConfig ChannelAffinitySetting

// UpdateConfigFromMap serializes config reloads, normalizes persisted built-in
// rules, and publishes a fresh immutable snapshot for request-time readers.
func (s *ChannelAffinitySetting) UpdateConfigFromMap(configMap map[string]string) error {
	channelAffinitySettingUpdateMu.Lock()
	defer channelAffinitySettingUpdateMu.Unlock()

	if err := config.UpdateConfigFromMap((*channelAffinitySettingConfig)(s), configMap); err != nil {
		return err
	}
	normalizeChannelAffinitySetting(s)
	if s == &channelAffinitySetting {
		return publishChannelAffinitySettingSnapshot(s)
	}
	return nil
}

func publishChannelAffinitySettingSnapshot(setting *ChannelAffinitySetting) error {
	data, err := common.Marshal(setting)
	if err != nil {
		return err
	}
	var snapshot ChannelAffinitySetting
	if err := common.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	channelAffinitySettingSnapshot.Store(&snapshot)
	return nil
}

func normalizeChannelAffinitySetting(setting *ChannelAffinitySetting) {
	if setting == nil {
		return
	}
	for i := range setting.Rules {
		rule := &setting.Rules[i]
		if isBuiltInCodexAffinityRule(*rule) {
			normalizeCodexPassThroughHeaders(rule)
		}
	}
}

func isBuiltInCodexAffinityRule(rule ChannelAffinityRule) bool {
	if !strings.EqualFold(strings.TrimSpace(rule.Name), codexCliAffinityRuleName) ||
		!containsExactString(rule.ModelRegex, "^gpt-.*$") ||
		!containsExactString(rule.PathRegex, "/v1/responses") {
		return false
	}
	for _, source := range rule.KeySources {
		if strings.EqualFold(strings.TrimSpace(source.Type), "gjson") &&
			strings.TrimSpace(source.Path) == "prompt_cache_key" {
			return true
		}
	}
	return false
}

func containsExactString(values []string, expected string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expected {
			return true
		}
	}
	return false
}

func normalizeCodexPassThroughHeaders(rule *ChannelAffinityRule) {
	if rule.ParamOverrideTemplate == nil {
		rule.ParamOverrideTemplate = make(map[string]interface{})
	}

	operations, valid := affinityOperations(rule.ParamOverrideTemplate["operations"])
	if !valid {
		return
	}

	for _, operationAny := range operations {
		operation, ok := operationAny.(map[string]interface{})
		if !ok || !strings.EqualFold(strings.TrimSpace(common.Interface2String(operation["mode"])), "pass_headers") {
			continue
		}
		headers, ok := affinityHeaderList(operation["value"])
		if !ok {
			continue
		}
		operation["value"] = appendMissingHeaders(headers, codexCliPassThroughHeaders)
		rule.ParamOverrideTemplate["operations"] = operations
		return
	}

	operations = append(operations, map[string]interface{}{
		"mode":        "pass_headers",
		"value":       append([]string(nil), codexCliPassThroughHeaders...),
		"keep_origin": true,
	})
	rule.ParamOverrideTemplate["operations"] = operations
}

func affinityOperations(value interface{}) ([]interface{}, bool) {
	switch operations := value.(type) {
	case nil:
		return make([]interface{}, 0, 1), true
	case []interface{}:
		return append([]interface{}(nil), operations...), true
	case []map[string]interface{}:
		out := make([]interface{}, 0, len(operations))
		for _, operation := range operations {
			out = append(out, operation)
		}
		return out, true
	default:
		return nil, false
	}
}

func affinityHeaderList(value interface{}) ([]string, bool) {
	switch headers := value.(type) {
	case nil:
		return nil, true
	case string:
		return []string{headers}, true
	case []string:
		return append([]string(nil), headers...), true
	case []interface{}:
		out := make([]string, 0, len(headers))
		for _, header := range headers {
			name, ok := header.(string)
			if !ok {
				return nil, false
			}
			out = append(out, name)
		}
		return out, true
	default:
		return nil, false
	}
}

func appendMissingHeaders(existing, required []string) []string {
	out := append([]string(nil), existing...)
	seen := make(map[string]struct{}, len(existing)+len(required))
	for _, header := range existing {
		seen[strings.ToLower(strings.TrimSpace(header))] = struct{}{}
	}
	for _, header := range required {
		key := strings.ToLower(strings.TrimSpace(header))
		if _, ok := seen[key]; ok {
			continue
		}
		out = append(out, header)
		seen[key] = struct{}{}
	}
	return out
}

func init() {
	normalizeChannelAffinitySetting(&channelAffinitySetting)
	if err := publishChannelAffinitySettingSnapshot(&channelAffinitySetting); err != nil {
		panic(err)
	}
	config.GlobalConfig.Register("channel_affinity_setting", &channelAffinitySetting)
}

// GetChannelAffinitySetting returns an immutable request-time snapshot.
func GetChannelAffinitySetting() *ChannelAffinitySetting {
	return channelAffinitySettingSnapshot.Load()
}
