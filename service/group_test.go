package service

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestGetUserUsableGroupsEmptyUserGroupKeepsGlobalGroupsOnly(t *testing.T) {
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组"}`))

	groups := GetUserUsableGroups("")

	require.Equal(t, map[string]string{"default": "默认分组"}, groups)
}

func TestGetUserUsableGroupsCMSGUsesLegacyASXSSpecialRules(t *testing.T) {
	oldUsableGroups := setting.UserUsableGroups2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldUsableGroups))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"默认分组"}`))

	specialGroups := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	oldSpecialGroups := specialGroups.ReadAll()
	specialGroups.Clear()
	specialGroups.Set("asxs", map[string]string{
		"-:default":        "隐藏默认",
		"+:cliproxy-codex": "CLIProxyAPI Codex 池",
	})
	t.Cleanup(func() {
		specialGroups.Clear()
		specialGroups.AddAll(oldSpecialGroups)
	})

	groups := GetUserUsableGroups("cmsg")

	require.NotContains(t, groups, "default")
	require.Equal(t, "CLIProxyAPI Codex 池", groups["cliproxy-codex"])
	require.Equal(t, "用户分组", groups["cmsg"])
	require.Equal(t, "用户分组", groups["asxs"])
}

func TestGetUserGroupRatioCMSGUsesLegacyASXSOverride(t *testing.T) {
	oldGroupGroupRatio := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(oldGroupGroupRatio))
	})
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{"asxs":{"cliproxy-codex":0}}`))

	require.Equal(t, 0.0, GetUserGroupRatio("cmsg", "cliproxy-codex"))
}
