package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func withQuotaPolicy(t *testing.T, chargedGroups, defaultAction string, rules []operation_setting.QuotaPolicyRule) {
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

func internalMeteredOnlyBillingRules() []operation_setting.QuotaPolicyRule {
	return []operation_setting.QuotaPolicyRule{
		{
			UserGroups:  []string{"asxs"},
			UsingGroups: []string{"cliproxy-codex", "deepseek-codex", "deepseek-claude"},
			Action:      "metered_only",
		},
	}
}

func seedUserInGroup(t *testing.T, id int, quota int, group string) {
	t.Helper()
	seedUser(t, id, quota)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", id).Update("group", group).Error)
}

func newBillingTestContext() *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	return c
}

func TestPreConsumeBilling_InternalMeteredOnlyGroupSkipsUserAndTokenQuota(t *testing.T) {
	truncate(t)
	withQuotaPolicy(t, "asxs,default", "charged", internalMeteredOnlyBillingRules())

	const userID = 101
	const tokenID = 101
	const tokenRemain = 5000

	seedUserInGroup(t, userID, 0, "asxs")
	seedToken(t, tokenID, userID, "metered-token", tokenRemain)

	c := newBillingTestContext()
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "metered-token",
		UserGroup:       "asxs",
		UsingGroup:      "cliproxy-codex",
		OriginModelName: "gpt-5.5",
		RequestId:       "metered-only-request",
	}

	apiErr := PreConsumeBilling(c, 3000, info)
	require.Nil(t, apiErr)
	require.NotNil(t, info.Billing)
	require.Equal(t, BillingSourceMeteredOnly, info.BillingSource)
	require.Equal(t, 0, info.FinalPreConsumedQuota)
	require.Equal(t, 0, info.Billing.GetPreConsumedQuota())

	require.NoError(t, info.Billing.Reserve(6000))
	require.NoError(t, SettleBilling(c, info, 2500))

	require.Equal(t, 0, getUserQuota(t, userID))
	require.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	require.Equal(t, 0, getTokenUsedQuota(t, tokenID))
}

func TestPreConsumeBilling_ExternalCPAGroupStillRequiresUserQuota(t *testing.T) {
	truncate(t)
	withQuotaPolicy(t, "asxs,default", "charged", internalMeteredOnlyBillingRules())

	const userID = 102
	const tokenID = 102

	seedUserInGroup(t, userID, 0, "default")
	seedToken(t, tokenID, userID, "charged-token", 5000)

	c := newBillingTestContext()
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "charged-token",
		UserGroup:       "default",
		UsingGroup:      "cliproxy-codex",
		OriginModelName: "gpt-5.5",
		RequestId:       "charged-request",
	}

	apiErr := PreConsumeBilling(c, 3000, info)
	require.NotNil(t, apiErr)
	require.Nil(t, info.Billing)
	require.Equal(t, "", info.BillingSource)
	require.Equal(t, 0, getUserQuota(t, userID))
}

func TestPreConsumeBilling_InternalASXSGroupStillRequiresUserQuota(t *testing.T) {
	truncate(t)
	withQuotaPolicy(t, "asxs,default", "charged", internalMeteredOnlyBillingRules())

	const userID = 104
	const tokenID = 104

	seedUserInGroup(t, userID, 0, "asxs")
	seedToken(t, tokenID, userID, "internal-asxs-token", 5000)

	c := newBillingTestContext()
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "internal-asxs-token",
		UserGroup:       "asxs",
		UsingGroup:      "asxs",
		OriginModelName: "gpt-5.5",
		RequestId:       "internal-asxs-request",
	}

	apiErr := PreConsumeBilling(c, 3000, info)
	require.NotNil(t, apiErr)
	require.Nil(t, info.Billing)
	require.Equal(t, "", info.BillingSource)
	require.Equal(t, 0, getUserQuota(t, userID))
}

func TestPreWssConsumeQuota_MeteredOnlySkipsQuotaLookups(t *testing.T) {
	truncate(t)

	info := &relaycommon.RelayInfo{
		UserId:        9999,
		TokenKey:      "missing-token",
		BillingSource: BillingSourceMeteredOnly,
	}

	require.NoError(t, PreWssConsumeQuota(newBillingTestContext(), info, &dto.RealtimeUsage{}))
}

func TestRecalculateTaskQuota_MeteredOnlySkipsFundingAndTokenQuota(t *testing.T) {
	truncate(t)

	const userID = 103
	const tokenID = 103
	const channelID = 103
	const tokenRemain = 5000
	const preConsumed = 1000
	const actualQuota = 2500

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "metered-task-token", tokenRemain)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceMeteredOnly, 0)

	RecalculateTaskQuota(context.Background(), task, actualQuota, "metered adjustment")

	require.Equal(t, 0, getUserQuota(t, userID))
	require.Equal(t, tokenRemain, getTokenRemainQuota(t, tokenID))
	require.Equal(t, 0, getTokenUsedQuota(t, tokenID))
	require.Equal(t, actualQuota, task.Quota)

	log := getLastLog(t)
	require.NotNil(t, log)
	require.Equal(t, model.LogTypeConsume, log.Type)
	require.Equal(t, actualQuota-preConsumed, log.Quota)
}
