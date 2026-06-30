package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func withModelQuotaChargedGroups(t *testing.T, chargedGroups string) {
	t.Helper()
	cfg := operation_setting.GetQuotaPolicySetting()
	old := cfg.ChargedGroups
	cfg.ChargedGroups = chargedGroups
	t.Cleanup(func() {
		cfg.ChargedGroups = old
	})
}

func insertTokenForQuotaPolicy(t *testing.T, token *Token) {
	t.Helper()
	initCol()
	if token.ExpiredTime == 0 {
		token.ExpiredTime = -1
	}
	require.NoError(t, DB.Create(token).Error)
}

func TestValidateUserToken_AllowsExhaustedTokenForMeteredOnlyGroup(t *testing.T) {
	truncateTables(t)
	withModelQuotaChargedGroups(t, "asxs")

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

func TestValidateUserToken_RejectsExhaustedTokenForChargedGroup(t *testing.T) {
	truncateTables(t)
	withModelQuotaChargedGroups(t, "asxs")

	insertTokenForQuotaPolicy(t, &Token{
		Id:          202,
		UserId:      202,
		Key:         "charged-exhausted",
		Name:        "charged",
		Status:      common.TokenStatusExhausted,
		RemainQuota: 0,
		Group:       "asxs",
	})

	token, err := ValidateUserToken("charged-exhausted")
	require.ErrorIs(t, err, ErrTokenInvalid)
	require.NotNil(t, token)
}

func TestValidateUserToken_StillRejectsDisabledTokenForMeteredOnlyGroup(t *testing.T) {
	truncateTables(t)
	withModelQuotaChargedGroups(t, "asxs")

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
