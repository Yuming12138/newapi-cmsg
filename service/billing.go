package service

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	BillingSourceWallet       = "wallet"
	BillingSourceSubscription = "subscription"
	BillingSourceMeteredOnly  = "metered_only"
)

// ensureMeteredOnlyFunding applies the request-scoped administrator billing
// policy to RelayInfo and reports whether the request must be metered without
// changing any wallet, subscription, or token balance.  It also recognizes
// the existing group-level metered-only source, so legacy billing paths share
// the same guard as BillingSession.
func ensureMeteredOnlyFunding(c *gin.Context, relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo == nil {
		return false
	}
	if relayInfo.BillingSource == BillingSourceMeteredOnly {
		relayInfo.FinalPreConsumedQuota = 0
		return true
	}
	if c != nil && common.GetContextKeyBool(c, constant.ContextKeyAdminAPIUnlimited) {
		relayInfo.BillingSource = BillingSourceMeteredOnly
		relayInfo.FinalPreConsumedQuota = 0
		return true
	}
	return false
}

func meteredTokenKey(relayInfo *relaycommon.RelayInfo) string {
	if relayInfo == nil {
		return ""
	}
	tokenKey := strings.TrimPrefix(strings.TrimSpace(relayInfo.TokenKey), "sk-")
	if tokenKey == "" {
		if token, err := model.GetTokenById(relayInfo.TokenId); err == nil && token != nil {
			tokenKey = token.Key
		}
	}
	return tokenKey
}

// recordMeteredTokenUsageDelta appends a signed usage delta for legacy billing
// callers whose quota argument is already a delta (for example a violation
// fee). It deliberately bypasses the absolute per-request tracker.
func recordMeteredTokenUsageDelta(relayInfo *relaycommon.RelayInfo, delta int) {
	if relayInfo == nil || relayInfo.BillingSource != BillingSourceMeteredOnly ||
		relayInfo.IsPlayground || relayInfo.TokenId <= 0 || delta == 0 {
		return
	}
	if err := model.AdjustTokenUsedQuota(relayInfo.TokenId, meteredTokenKey(relayInfo), delta); err != nil {
		common.SysLog(fmt.Sprintf("failed to record metered token usage delta (tokenId=%d, delta=%d): %v", relayInfo.TokenId, delta, err))
	}
}

// recordMeteredTokenUsage keeps the token's cumulative usage observable for
// administrator requests while deliberately leaving remain_quota untouched.
// Wallet/subscription usage and channel/user counters are handled by their
// existing paths; this helper only fills the token-level usage column.
func recordMeteredTokenUsage(relayInfo *relaycommon.RelayInfo, quota int) {
	if relayInfo == nil || relayInfo.BillingSource != BillingSourceMeteredOnly ||
		relayInfo.IsPlayground || relayInfo.TokenId <= 0 {
		return
	}

	tokenKey := meteredTokenKey(relayInfo)
	if err := relayInfo.ApplyMeteredUsage(quota, func(delta int) error {
		return model.AdjustTokenUsedQuota(relayInfo.TokenId, tokenKey, delta)
	}); err != nil {
		// Metering must not turn an otherwise successful upstream response into
		// a failed API response.  Keep the error visible for operators.
		common.SysLog(fmt.Sprintf("failed to record metered token usage (tokenId=%d, quota=%d): %v", relayInfo.TokenId, quota, err))
	}
}

// PreConsumeBilling 根据用户计费偏好创建 BillingSession 并执行预扣费。
// 会话存储在 relayInfo.Billing 上，供后续 Settle / Refund 使用。
func PreConsumeBilling(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		return apiErr
	}
	relayInfo.Billing = session
	return nil
}

// ---------------------------------------------------------------------------
// SettleBilling — 后结算辅助函数
// ---------------------------------------------------------------------------

// SettleBilling 执行计费结算。如果 RelayInfo 上有 BillingSession 则通过 session 结算，
// 否则回退到旧的 PostConsumeQuota 路径（兼容按次计费等场景）。
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo == nil {
		return fmt.Errorf("relayInfo is nil")
	}
	ensureMeteredOnlyFunding(ctx, relayInfo)
	if relayInfo.Billing != nil {
		preConsumed := relayInfo.Billing.GetPreConsumedQuota()
		delta := actualQuota - preConsumed

		if relayInfo.BillingSource == BillingSourceMeteredOnly {
			logger.LogInfo(ctx, fmt.Sprintf("仅计量不扣费：%s（用户分组：%s，使用分组：%s）",
				logger.FormatQuota(actualQuota),
				relayInfo.UserGroup,
				relayInfo.UsingGroup,
			))
		} else if delta > 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else if delta < 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(-delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费与实际消耗一致，无需调整：%s（按次计费）",
				logger.FormatQuota(actualQuota),
			))
		}

		if err := relayInfo.Billing.Settle(actualQuota); err != nil {
			return err
		}
		if relayInfo.BillingSource == BillingSourceMeteredOnly {
			recordMeteredTokenUsage(relayInfo, actualQuota)
		}

		// 发送额度通知（订阅计费使用订阅剩余额度）
		if actualQuota != 0 && relayInfo.BillingSource != BillingSourceMeteredOnly {
			if relayInfo.BillingSource == BillingSourceSubscription {
				checkAndSendSubscriptionQuotaNotify(relayInfo)
			} else {
				checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
			}
		}
		return nil
	}

	// 回退：无 BillingSession 时使用旧路径
	quotaDelta := actualQuota - relayInfo.FinalPreConsumedQuota
	if quotaDelta != 0 {
		return PostConsumeQuota(relayInfo, quotaDelta, relayInfo.FinalPreConsumedQuota, true)
	}
	if relayInfo.BillingSource == BillingSourceMeteredOnly {
		recordMeteredTokenUsage(relayInfo, actualQuota)
	}
	return nil
}
