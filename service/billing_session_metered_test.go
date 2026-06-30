package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func withQuotaChargedGroups(t *testing.T, chargedGroups string) {
	t.Helper()
	cfg := operation_setting.GetQuotaPolicySetting()
	old := cfg.ChargedGroups
	cfg.ChargedGroups = chargedGroups
	t.Cleanup(func() {
		cfg.ChargedGroups = old
	})
}

func newBillingTestContext() *gin.Context {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	return c
}

func TestPreConsumeBilling_MeteredOnlyGroupSkipsUserAndTokenQuota(t *testing.T) {
	truncate(t)
	withQuotaChargedGroups(t, "asxs")

	const userID = 101
	const tokenID = 101
	const tokenRemain = 5000

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "metered-token", tokenRemain)

	c := newBillingTestContext()
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "metered-token",
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

func TestPreConsumeBilling_ChargedGroupStillRequiresUserQuota(t *testing.T) {
	truncate(t)
	withQuotaChargedGroups(t, "asxs")

	const userID = 102
	const tokenID = 102

	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "charged-token", 5000)

	c := newBillingTestContext()
	info := &relaycommon.RelayInfo{
		UserId:          userID,
		TokenId:         tokenID,
		TokenKey:        "charged-token",
		UsingGroup:      "asxs",
		OriginModelName: "gpt-5.5",
		RequestId:       "charged-request",
	}

	apiErr := PreConsumeBilling(c, 3000, info)
	require.NotNil(t, apiErr)
	require.Nil(t, info.Billing)
	require.Equal(t, "", info.BillingSource)
	require.Equal(t, 0, getUserQuota(t, userID))
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
