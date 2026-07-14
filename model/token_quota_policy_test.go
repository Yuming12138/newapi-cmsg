package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func withModelQuotaPolicy(t *testing.T, chargedGroups, defaultAction string, rules []operation_setting.QuotaPolicyRule) {
	t.Helper()
	cfg := operation_setting.GetQuotaPolicySetting()
	old := *cfg
	cfg.ChargedGroups = chargedGroups
	cfg.DefaultAction = defaultAction
	cfg.Rules = rules
	t.Cleanup(func() {
		*cfg = old
	})
}

func internalMeteredOnlyRules() []operation_setting.QuotaPolicyRule {
	return []operation_setting.QuotaPolicyRule{
		{
			UserGroups:  []string{"asxs"},
			UsingGroups: []string{"cliproxy-codex", "deepseek-codex", "deepseek-claude"},
			Action:      "metered_only",
		},
	}
}

func insertUserForQuotaPolicy(t *testing.T, id int, group string) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:       id,
		Username: "quota_policy_user",
		Status:   common.UserStatusEnabled,
		Group:    group,
	}).Error)
}

func insertTokenForQuotaPolicy(t *testing.T, token *Token) {
	t.Helper()
	initCol()
	if token.ExpiredTime == 0 {
		token.ExpiredTime = -1
	}
	require.NoError(t, DB.Create(token).Error)
}

func TestValidateUserToken_AllowsInternalExhaustedTokenForMeteredOnlyGroup(t *testing.T) {
	truncateTables(t)
	withModelQuotaPolicy(t, "asxs,default", "charged", internalMeteredOnlyRules())
	insertUserForQuotaPolicy(t, 201, "asxs")

	insertTokenForQuotaPolicy(t, &Token{
		Id:          201,
		UserId:      201,
		Key:         "metered-exhausted",
		Name:        "metered",
		Status:      common.TokenStatusExhausted,
		RemainQuota: 0,
		Group:       "cliproxy-codex",
	})

	token, err := ValidateUserToken("metered-exhausted")
	require.NoError(t, err)
	require.NotNil(t, token)
	require.Equal(t, common.TokenStatusExhausted, token.Status)
}

func TestValidateUserToken_RejectsExternalExhaustedTokenForCPAGroup(t *testing.T) {
	truncateTables(t)
	withModelQuotaPolicy(t, "asxs,default", "charged", internalMeteredOnlyRules())
	insertUserForQuotaPolicy(t, 202, "default")

	insertTokenForQuotaPolicy(t, &Token{
		Id:          202,
		UserId:      202,
		Key:         "external-cpa-exhausted",
		Name:        "external-cpa",
		Status:      common.TokenStatusExhausted,
		RemainQuota: 0,
		Group:       "cliproxy-codex",
	})

	token, err := ValidateUserToken("external-cpa-exhausted")
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.NotNil(t, token)
}

func TestValidateUserToken_RejectsInternalExhaustedTokenForASXSGroup(t *testing.T) {
	truncateTables(t)
	withModelQuotaPolicy(t, "asxs,default", "charged", internalMeteredOnlyRules())
	insertUserForQuotaPolicy(t, 204, "asxs")

	insertTokenForQuotaPolicy(t, &Token{
		Id:          204,
		UserId:      204,
		Key:         "internal-asxs-exhausted",
		Name:        "internal-asxs",
		Status:      common.TokenStatusExhausted,
		RemainQuota: 0,
		Group:       "asxs",
	})

	token, err := ValidateUserToken("internal-asxs-exhausted")
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.NotNil(t, token)
}

func TestValidateUserToken_StillRejectsDisabledTokenForMeteredOnlyGroup(t *testing.T) {
	truncateTables(t)
	withModelQuotaPolicy(t, "asxs,default", "charged", internalMeteredOnlyRules())
	insertUserForQuotaPolicy(t, 203, "asxs")

	insertTokenForQuotaPolicy(t, &Token{
		Id:          203,
		UserId:      203,
		Key:         "metered-disabled",
		Name:        "disabled",
		Status:      common.TokenStatusDisabled,
		RemainQuota: 0,
		Group:       "cliproxy-codex",
	})

	token, err := ValidateUserToken("metered-disabled")
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.NotNil(t, token)
}
