package operation_setting

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func TestDefaultCodexAffinityRuleContainsCurrentHeaders(t *testing.T) {
	setting := GetChannelAffinitySetting()
	require.NotNil(t, setting)

	rule := findCodexAffinityRuleForTest(t, setting.Rules)
	headers, _ := findPassHeadersOperationForTest(t, rule)
	for _, expected := range codexCliPassThroughHeaders {
		requireHeaderCount(t, headers, expected, 1)
	}
}

func TestPersistedCodexAffinityRuleNormalizationPreservesCustomizations(t *testing.T) {
	customRule := ChannelAffinityRule{
		Name:       "custom tenant affinity",
		ModelRegex: []string{"^tenant-model$"},
		PathRegex:  []string{"/v1/responses"},
		KeySources: []ChannelAffinityKeySource{{Type: "request_header", Key: "X-Tenant"}},
		ParamOverrideTemplate: map[string]interface{}{
			"operations": []map[string]interface{}{{
				"mode":  "set",
				"path":  "metadata.tenant",
				"value": "keep-me",
			}},
		},
	}
	legacyCodexRule := ChannelAffinityRule{
		Name:       codexCliAffinityRuleName,
		ModelRegex: []string{"^gpt-.*$"},
		PathRegex:  []string{"/v1/responses"},
		KeySources: []ChannelAffinityKeySource{{Type: "gjson", Path: "prompt_cache_key"}},
		ParamOverrideTemplate: map[string]interface{}{
			"custom_template_key": "keep-template-value",
			"operations": []map[string]interface{}{
				{
					"mode":        "pass_headers",
					"value":       []string{"Originator", "Session_id", "X-Custom-Persisted"},
					"keep_origin": false,
				},
				{
					"mode":  "set",
					"path":  "metadata.persisted",
					"value": "keep-operation",
				},
			},
		},
	}

	rulesJSON, err := common.Marshal([]ChannelAffinityRule{legacyCodexRule, customRule})
	require.NoError(t, err)
	customRuleJSON, err := common.Marshal(customRule)
	require.NoError(t, err)

	var setting ChannelAffinitySetting
	manager := config.NewConfigManager()
	manager.Register("channel_affinity_setting", &setting)
	require.NoError(t, manager.LoadFromDB(map[string]string{
		"channel_affinity_setting.rules": string(rulesJSON),
	}))
	require.Len(t, setting.Rules, 2)

	normalizedRule := findCodexAffinityRuleForTest(t, setting.Rules)
	headers, operation := findPassHeadersOperationForTest(t, normalizedRule)
	require.Equal(t, []string{"Originator", "Session_id", "X-Custom-Persisted"}, headers[:3])
	for _, expected := range codexCliPassThroughHeaders {
		requireHeaderCount(t, headers, expected, 1)
	}
	require.Equal(t, false, operation["keep_origin"])
	require.Equal(t, "keep-template-value", normalizedRule.ParamOverrideTemplate["custom_template_key"])

	operations, valid := affinityOperations(normalizedRule.ParamOverrideTemplate["operations"])
	require.True(t, valid)
	require.Len(t, operations, 2)
	require.Equal(t, "set", operations[1].(map[string]interface{})["mode"])
	require.Equal(t, "keep-operation", operations[1].(map[string]interface{})["value"])

	actualCustomRuleJSON, err := common.Marshal(setting.Rules[1])
	require.NoError(t, err)
	require.JSONEq(t, string(customRuleJSON), string(actualCustomRuleJSON))

	firstNormalization, err := common.Marshal(setting)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(&setting, map[string]string{}))
	secondNormalization, err := common.Marshal(setting)
	require.NoError(t, err)
	require.JSONEq(t, string(firstNormalization), string(secondNormalization))
}

func TestChannelAffinityRuntimeSnapshotConcurrentReload(t *testing.T) {
	rawSetting := config.GlobalConfig.Get("channel_affinity_setting")
	require.NotNil(t, rawSetting)
	saved, err := config.ConfigToMap(rawSetting)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(rawSetting, map[string]string{"rules": saved["rules"]}))
	})

	legacyRule := ChannelAffinityRule{
		Name:       codexCliAffinityRuleName,
		ModelRegex: []string{"^gpt-.*$"},
		PathRegex:  []string{"/v1/responses"},
		KeySources: []ChannelAffinityKeySource{{Type: "gjson", Path: "prompt_cache_key"}},
		ParamOverrideTemplate: map[string]interface{}{
			"operations": []map[string]interface{}{{
				"mode":        "pass_headers",
				"value":       []string{"Originator", "Session_id"},
				"keep_origin": true,
			}},
		},
	}
	legacyRulesJSON, err := common.Marshal([]ChannelAffinityRule{legacyRule})
	require.NoError(t, err)

	const iterations = 50
	errCh := make(chan error, iterations*2)
	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := config.UpdateConfigFromMap(rawSetting, map[string]string{"rules": string(legacyRulesJSON)}); err != nil {
				errCh <- err
			}
		}()
		go func() {
			defer wg.Done()
			snapshot := GetChannelAffinitySetting()
			if snapshot == nil {
				errCh <- fmt.Errorf("nil channel affinity snapshot")
				return
			}
			rule := findCodexAffinityRule(snapshot.Rules)
			if rule == nil {
				errCh <- fmt.Errorf("missing Codex affinity rule")
				return
			}
			headers, ok := passHeadersFromRule(*rule)
			if !ok {
				errCh <- fmt.Errorf("missing Codex pass_headers operation")
				return
			}
			for _, expected := range codexCliPassThroughHeaders {
				if headerCount(headers, expected) != 1 {
					errCh <- fmt.Errorf("header %q was not normalized exactly once", expected)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

func findCodexAffinityRuleForTest(t *testing.T, rules []ChannelAffinityRule) ChannelAffinityRule {
	t.Helper()
	rule := findCodexAffinityRule(rules)
	require.NotNil(t, rule)
	return *rule
}

func findCodexAffinityRule(rules []ChannelAffinityRule) *ChannelAffinityRule {
	for i := range rules {
		if strings.EqualFold(strings.TrimSpace(rules[i].Name), codexCliAffinityRuleName) {
			return &rules[i]
		}
	}
	return nil
}

func findPassHeadersOperationForTest(t *testing.T, rule ChannelAffinityRule) ([]string, map[string]interface{}) {
	t.Helper()
	headers, operation, ok := passHeadersOperation(rule)
	require.True(t, ok)
	return headers, operation
}

func passHeadersFromRule(rule ChannelAffinityRule) ([]string, bool) {
	headers, _, ok := passHeadersOperation(rule)
	return headers, ok
}

func passHeadersOperation(rule ChannelAffinityRule) ([]string, map[string]interface{}, bool) {
	operations, valid := affinityOperations(rule.ParamOverrideTemplate["operations"])
	if !valid {
		return nil, nil, false
	}
	for _, operationAny := range operations {
		operation, ok := operationAny.(map[string]interface{})
		if !ok || !strings.EqualFold(common.Interface2String(operation["mode"]), "pass_headers") {
			continue
		}
		headers, ok := affinityHeaderList(operation["value"])
		return headers, operation, ok
	}
	return nil, nil, false
}

func requireHeaderCount(t *testing.T, headers []string, expected string, count int) {
	t.Helper()
	require.Equal(t, count, headerCount(headers, expected), "header %q", expected)
}

func headerCount(headers []string, expected string) int {
	count := 0
	for _, header := range headers {
		if strings.EqualFold(strings.TrimSpace(header), expected) {
			count++
		}
	}
	return count
}
