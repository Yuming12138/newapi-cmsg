package setting

import "strings"

const (
	DefaultUserIdentityGroup = "default"
	CMSGUserIdentityGroup    = "cmsg"
	LegacyASXSUserGroup      = "asxs"
)

var userIdentityGroups = map[string]string{
	DefaultUserIdentityGroup: "默认外售用户",
	CMSGUserIdentityGroup:    "CMSG 组内用户",
}

// NormalizeUserIdentityGroup canonicalizes user identity groups. Channel and
// token routing groups intentionally keep their original names.
func NormalizeUserIdentityGroup(group string) string {
	group = strings.TrimSpace(group)
	switch strings.ToLower(group) {
	case "":
		return DefaultUserIdentityGroup
	case LegacyASXSUserGroup:
		return CMSGUserIdentityGroup
	default:
		return group
	}
}

func UserIdentityGroupAliases(group string) []string {
	group = NormalizeUserIdentityGroup(group)
	if group == CMSGUserIdentityGroup {
		return []string{CMSGUserIdentityGroup, LegacyASXSUserGroup}
	}
	return []string{group}
}

func GetUserIdentityGroupsCopy() map[string]string {
	groups := make(map[string]string, len(userIdentityGroups))
	for group, desc := range userIdentityGroups {
		groups[group] = desc
	}
	return groups
}

func GetUserIdentityGroupNames() []string {
	return []string{DefaultUserIdentityGroup, CMSGUserIdentityGroup}
}
