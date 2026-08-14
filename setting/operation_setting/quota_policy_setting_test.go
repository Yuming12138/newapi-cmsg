package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/stretchr/testify/require"
)

func withQuotaPolicySetting(t *testing.T, chargedGroups, defaultAction string, rules []QuotaPolicyRule) {
	t.Helper()
	old := *GetQuotaPolicySetting()
	cfg := GetQuotaPolicySetting()
	cfg.ChargedGroups = chargedGroups
	cfg.DefaultAction = defaultAction
	cfg.Rules = rules
	t.Cleanup(func() {
		*cfg = old
	})
}

func TestIsQuotaChargedForUser_LegacyChargedGroupsCompatibility(t *testing.T) {
	withQuotaPolicySetting(t, "asxs", "", nil)

	require.True(t, IsQuotaChargedForUser("default", "asxs"))
	require.False(t, IsQuotaChargedForUser("default", "cliproxy-codex"))
}

func TestIsQuotaChargedForUser_UserAndUsingGroupMatrix(t *testing.T) {
	withQuotaPolicySetting(t, "asxs,default", "charged", []QuotaPolicyRule{
		{
			UserGroups:  []string{"asxs"},
			UsingGroups: []string{"cliproxy-codex", "deepseek-codex", "deepseek-claude"},
			Action:      "metered_only",
		},
	})

	require.True(t, IsQuotaChargedForUser("default", "cliproxy-codex"))
	require.True(t, IsQuotaChargedForUser("default", "deepseek-codex"))
	require.False(t, IsQuotaChargedForUser("asxs", "cliproxy-codex"))
	require.False(t, IsQuotaChargedForUser("asxs", "deepseek-codex"))
	require.False(t, IsQuotaChargedForUser("asxs", "deepseek-claude"))
	require.True(t, IsQuotaChargedForUser("asxs", "asxs"))
	require.False(t, IsQuotaChargedForUser("cmsg", "cliproxy-codex"))
	require.False(t, IsQuotaChargedForUser("cmsg", "deepseek-codex"))
	require.True(t, IsQuotaChargedForUser("cmsg", "asxs"))
}

func TestIsQuotaChargedForUser_FirstMatchingValidRuleWins(t *testing.T) {
	withQuotaPolicySetting(t, "asxs", "charged", []QuotaPolicyRule{
		{UserGroups: []string{"asxs"}, UsingGroups: []string{"cliproxy-codex"}, Action: "metered_only"},
		{UserGroups: []string{"asxs"}, UsingGroups: []string{"cliproxy-codex"}, Action: "charged"},
	})

	require.False(t, IsQuotaChargedForUser("asxs", "cliproxy-codex"))
}

func TestIsQuotaChargedForUser_InvalidActionsFailClosed(t *testing.T) {
	withQuotaPolicySetting(t, "none", "invalid", []QuotaPolicyRule{
		{UserGroups: []string{"*"}, UsingGroups: []string{"all"}, Action: "free"},
	})

	require.True(t, IsQuotaChargedForUser("asxs", "cliproxy-codex"))
	require.True(t, IsQuotaChargedForUser("", "cliproxy-codex"))
}

func TestIsQuotaChargedForUser_DefaultChargedWithoutExceptions(t *testing.T) {
	withQuotaPolicySetting(t, "*", "charged", nil)

	for _, usingGroup := range []string{
		"asxs",
		"asxs-gpt56",
		"cliproxy-codex",
		"deepseek",
		"asxs-grok",
		"cliproxy-claude",
		"asxs-gpt56-direct",
	} {
		require.True(t, IsQuotaChargedForUser("asxs", usingGroup), usingGroup)
		require.True(t, IsQuotaChargedForUser("cmsg", usingGroup), usingGroup)
	}
}

func TestIsQuotaChargedForUser_Wildcards(t *testing.T) {
	withQuotaPolicySetting(t, "all", "charged", []QuotaPolicyRule{
		{UserGroups: []string{"all"}, UsingGroups: []string{"*"}, Action: "metered_only"},
	})

	require.False(t, IsQuotaChargedForUser("asxs", "cliproxy-codex"))
}

func TestQuotaPolicySetting_ConfigRoundTripUsesStableKeys(t *testing.T) {
	rules := []QuotaPolicyRule{
		{UserGroups: []string{"asxs"}, UsingGroups: []string{"cliproxy-codex"}, Action: "metered_only"},
	}
	rulesJSON, err := common.Marshal(rules)
	require.NoError(t, err)

	var loaded QuotaPolicySetting
	manager := config.NewConfigManager()
	manager.Register("quota_policy_setting", &loaded)
	require.NoError(t, manager.LoadFromDB(map[string]string{
		"quota_policy_setting.charged_groups": "asxs,default",
		"quota_policy_setting.default_action": "charged",
		"quota_policy_setting.rules":          string(rulesJSON),
	}))

	require.Equal(t, "asxs,default", loaded.ChargedGroups)
	require.Equal(t, "charged", loaded.DefaultAction)
	require.Equal(t, rules, loaded.Rules)

	configMap, err := config.ConfigToMap(&loaded)
	require.NoError(t, err)
	require.Equal(t, "charged", configMap["default_action"])
	require.Contains(t, configMap, "rules")
	require.NotContains(t, configMap, "default_action,omitempty")
	require.NotContains(t, configMap, "rules,omitempty")
}

func TestQuotaPolicySetting_GlobalReloadPublishesSnapshot(t *testing.T) {
	rawSetting := config.GlobalConfig.Get("quota_policy_setting")
	require.NotNil(t, rawSetting)
	saved, err := config.ConfigToMap(rawSetting)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, config.UpdateConfigFromMap(rawSetting, saved))
	})

	rules := []QuotaPolicyRule{
		{UserGroups: []string{"asxs"}, UsingGroups: []string{"cliproxy-codex"}, Action: "metered_only"},
	}
	rulesJSON, err := common.Marshal(rules)
	require.NoError(t, err)
	require.NoError(t, config.UpdateConfigFromMap(rawSetting, map[string]string{
		"charged_groups": "asxs,default",
		"default_action": "charged",
		"rules":          string(rulesJSON),
	}))

	snapshot := GetQuotaPolicySetting()
	require.Equal(t, "charged", snapshot.DefaultAction)
	require.Equal(t, rules, snapshot.Rules)
	require.False(t, IsQuotaChargedForUser("asxs", "cliproxy-codex"))
	require.True(t, IsQuotaChargedForUser("default", "cliproxy-codex"))
}
