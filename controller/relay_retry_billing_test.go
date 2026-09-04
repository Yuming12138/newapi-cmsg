package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type retryBillingRecorder struct {
	preConsumedQuota int
	reserveTargets   []int
}

func (*retryBillingRecorder) Settle(int) error           { return nil }
func (*retryBillingRecorder) Refund(*gin.Context)        {}
func (*retryBillingRecorder) NeedsRefund() bool          { return false }
func (b *retryBillingRecorder) GetPreConsumedQuota() int { return b.preConsumedQuota }

func (b *retryBillingRecorder) Reserve(targetQuota int) error {
	if targetQuota > b.preConsumedQuota {
		b.reserveTargets = append(b.reserveTargets, targetQuota)
		b.preConsumedQuota = targetQuota
	}
	return nil
}

func newRetryBillingContext(t *testing.T, mapping string, billingByMappedModel bool) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-5.6-sol")
	common.SetContextKey(c, constant.ContextKeyChannelType, 1)
	common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		BillingByMappedModelEnabled: billingByMappedModel,
	})
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, mapping)
	return c
}

func initialRetryPrice(t *testing.T, c *gin.Context, info *relaycommon.RelayInfo, modelName string, promptTokens int, meta *types.TokenCountMeta) types.PriceData {
	t.Helper()
	info.BillingModelName = modelName
	priceData, err := helper.ModelPriceHelper(c, info, promptTokens, meta)
	require.NoError(t, err)
	return priceData
}

func TestPrepareBillingForSelectedChannelRepricesSolFallbackToLuna(t *testing.T) {
	ratio_setting.InitRatioSettings()
	c := newRetryBillingContext(t, `{"gpt-5.6-sol":"gpt-5.6-luna"}`, true)
	meta := &types.TokenCountMeta{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-sol",
		UserGroup:       "default",
		UsingGroup:      "default",
		UserId:          10001,
	}
	initial := initialRetryPrice(t, c, info, "gpt-5.6-sol", 1000, meta)
	billing := &retryBillingRecorder{preConsumedQuota: initial.QuotaToPreConsume}
	info.Billing = billing

	require.Nil(t, prepareBillingForSelectedChannel(c, info, 1000, meta))

	require.Equal(t, "gpt-5.6-luna", info.GetBillingModelName())
	require.Equal(t, 0.1, info.PriceData.ModelRatio)
	require.Equal(t, ratio_setting.GetCompletionRatio("gpt-5.6-luna"), info.PriceData.CompletionRatio)
	require.Less(t, info.PriceData.QuotaToPreConsume, initial.QuotaToPreConsume)
	require.Empty(t, billing.reserveTargets, "a cheaper fallback must not reserve again")
}

func TestPrepareBillingForSelectedChannelReservesMoreForExpensiveMapping(t *testing.T) {
	ratio_setting.InitRatioSettings()
	c := newRetryBillingContext(t, `{"gpt-5.6-sol":"gpt-5.6-sol"}`, true)
	meta := &types.TokenCountMeta{}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5.6-sol",
		UserGroup:       "default",
		UsingGroup:      "default",
		UserId:          10001,
	}
	cheap := initialRetryPrice(t, c, info, "gpt-5.6-luna", 1000, meta)
	billing := &retryBillingRecorder{preConsumedQuota: cheap.QuotaToPreConsume}
	info.Billing = billing

	require.Nil(t, prepareBillingForSelectedChannel(c, info, 1000, meta))

	require.Equal(t, "gpt-5.6-sol", info.GetBillingModelName())
	require.Equal(t, 2.0, info.PriceData.ModelRatio)
	require.Equal(t, []int{info.PriceData.QuotaToPreConsume}, billing.reserveTargets)
	require.Equal(t, info.PriceData.QuotaToPreConsume, info.FinalPreConsumedQuota)
}

func TestPrepareBillingForSelectedChannelClearsPreviousMappedBillingModel(t *testing.T) {
	ratio_setting.InitRatioSettings()
	c := newRetryBillingContext(t, "{}", true)
	meta := &types.TokenCountMeta{}
	info := &relaycommon.RelayInfo{
		OriginModelName:  "gpt-5.6-sol",
		BillingModelName: "gpt-5.6-luna",
		UserGroup:        "default",
		UsingGroup:       "default",
		UserId:           10001,
	}
	billing := &retryBillingRecorder{preConsumedQuota: 100000}
	info.Billing = billing

	require.Nil(t, prepareBillingForSelectedChannel(c, info, 1000, meta))

	require.Equal(t, "gpt-5.6-sol", info.GetBillingModelName())
	require.Equal(t, 2.0, info.PriceData.ModelRatio)
}
