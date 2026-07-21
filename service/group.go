package service

import (
	"strings"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func GetUserUsableGroups(userGroup string) map[string]string {
	groupsCopy := setting.GetUserUsableGroupsCopy()
	userGroup = strings.TrimSpace(userGroup)
	if userGroup != "" {
		userGroup = setting.NormalizeUserIdentityGroup(userGroup)
		for _, groupAlias := range setting.UserIdentityGroupAliases(userGroup) {
			applySpecialUsableGroups(groupsCopy, groupAlias)
			if _, ok := groupsCopy[groupAlias]; !ok {
				groupsCopy[groupAlias] = "用户分组"
			}
		}
	}
	return groupsCopy
}

func applySpecialUsableGroups(groupsCopy map[string]string, userGroup string) {
	specialSettings, ok := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
	if !ok {
		return
	}
	// 处理特殊可用分组
	for specialGroup, desc := range specialSettings {
		if strings.HasPrefix(specialGroup, "-:") {
			// 移除分组
			groupToRemove := strings.TrimPrefix(specialGroup, "-:")
			delete(groupsCopy, groupToRemove)
		} else if strings.HasPrefix(specialGroup, "+:") {
			// 添加分组
			groupToAdd := strings.TrimPrefix(specialGroup, "+:")
			groupsCopy[groupToAdd] = desc
		} else {
			// 直接添加分组
			groupsCopy[specialGroup] = desc
		}
	}
}

func GroupInUserUsableGroups(userGroup, groupName string) bool {
	_, ok := GetUserUsableGroups(userGroup)[groupName]
	return ok
}

// GetUserAutoGroup 根据用户分组获取自动分组设置
func GetUserAutoGroup(userGroup string) []string {
	groups := GetUserUsableGroups(userGroup)
	autoGroups := make([]string, 0)
	for _, group := range setting.GetAutoGroups() {
		if _, ok := groups[group]; ok {
			autoGroups = append(autoGroups, group)
		}
	}
	return autoGroups
}

// GetUserGroupRatio 获取用户使用某个分组的倍率
// userGroup 用户分组
// group 需要获取倍率的分组
func GetUserGroupRatio(userGroup, group string) float64 {
	if strings.TrimSpace(userGroup) == "" {
		return ratio_setting.GetGroupRatio(group)
	}
	for _, groupAlias := range setting.UserIdentityGroupAliases(userGroup) {
		ratio, ok := ratio_setting.GetGroupGroupRatio(groupAlias, group)
		if ok {
			return ratio
		}
	}
	return ratio_setting.GetGroupRatio(group)
}
