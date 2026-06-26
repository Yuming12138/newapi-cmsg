package service

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	ginKeyChannelSchedulerExcludedIDs = "channel_scheduler_excluded_ids"
	ginKeyChannelSchedulerLogInfo     = "channel_scheduler_log_info"
)

const (
	channelTempUnschedTTLRateLimit = 3 * time.Minute
	channelTempUnschedTTLTransport = 90 * time.Second
	channelTempUnschedTTLQuota     = 10 * time.Minute
	channelTempUnschedTTLChannel   = 10 * time.Minute
)

func ExcludeChannelForRequest(c *gin.Context, channelID int, reason string) bool {
	if c == nil || channelID <= 0 {
		return false
	}

	excluded := getOrCreateRequestExcludedChannelIDs(c)
	if _, exists := excluded[channelID]; exists {
		return false
	}
	excluded[channelID] = struct{}{}
	c.Set(ginKeyChannelSchedulerExcludedIDs, excluded)

	appendChannelSchedulerLogInfo(c, map[string]interface{}{
		"excluded_channel_id": channelID,
		"reason":              reason,
	})
	return true
}

func GetExcludedChannelIDsForRequest(c *gin.Context) map[int]struct{} {
	if c == nil {
		return nil
	}
	anyExcluded, ok := c.Get(ginKeyChannelSchedulerExcludedIDs)
	if !ok {
		return nil
	}
	excluded, ok := anyExcluded.(map[int]struct{})
	if !ok || len(excluded) == 0 {
		return nil
	}
	cloned := make(map[int]struct{}, len(excluded))
	for channelID := range excluded {
		cloned[channelID] = struct{}{}
	}
	return cloned
}

func AppendChannelSchedulerAdminInfo(c *gin.Context, adminInfo map[string]interface{}) {
	if c == nil || adminInfo == nil {
		return
	}
	anyInfo, ok := c.Get(ginKeyChannelSchedulerLogInfo)
	if !ok {
		return
	}
	info, ok := anyInfo.(map[string]interface{})
	if !ok || len(info) == 0 {
		return
	}
	adminInfo["channel_scheduler"] = info
}

func HandleChannelFailure(c *gin.Context, channelError types.ChannelError, err *types.NewAPIError) {
	if c == nil || err == nil || channelError.ChannelId <= 0 {
		return
	}

	reason := buildChannelFailureReason(err)
	ExcludeChannelForRequest(c, channelError.ChannelId, reason)

	if ShouldClearChannelAffinityAfterError(err) {
		ClearChannelAffinityForRequest(c, fmt.Sprintf("channel #%d relay failure: %s", channelError.ChannelId, reason))
	}

	if channelError.AutoBan && ShouldDisableChannel(err) {
		return
	}

	ttl, tempReason, ok := temporaryUnschedulableDecision(err)
	if !ok || ttl <= 0 {
		return
	}

	state := model.ChannelTemporaryUnschedulable{
		UntilUnix:  time.Now().Add(ttl).Unix(),
		Reason:     tempReason,
		StatusCode: err.StatusCode,
		ErrorCode:  string(err.GetErrorCode()),
	}
	if cacheErr := model.MarkChannelTemporarilyUnschedulable(channelError.ChannelId, ttl, state); cacheErr != nil {
		common.SysError(fmt.Sprintf("mark channel #%d temporarily unschedulable failed: %v", channelError.ChannelId, cacheErr))
		return
	}

	appendChannelSchedulerLogInfo(c, map[string]interface{}{
		"temporary_unschedulable": map[string]interface{}{
			"channel_id":   channelError.ChannelId,
			"ttl_seconds":  int(ttl.Seconds()),
			"reason":       tempReason,
			"status_code":  err.StatusCode,
			"error_code":   err.GetErrorCode(),
			"channel_name": channelError.ChannelName,
		},
	})
}

func ShouldClearChannelAffinityAfterError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if ShouldDisableChannel(err) {
		return true
	}
	if _, _, ok := temporaryUnschedulableDecision(err); ok {
		return true
	}
	if err.GetErrorCode() == types.ErrorCodeDoRequestFailed ||
		err.GetErrorCode() == types.ErrorCodeBadResponseStatusCode ||
		err.GetErrorCode() == types.ErrorCodeBadResponse ||
		err.GetErrorCode() == types.ErrorCodeBadResponseBody {
		return true
	}
	if err.StatusCode > 0 && operation_setting.ShouldRetryByStatusCode(err.StatusCode) {
		return true
	}
	return false
}

func temporaryUnschedulableDecision(err *types.NewAPIError) (time.Duration, string, bool) {
	if err == nil {
		return 0, "", false
	}

	if types.IsChannelError(err) {
		return channelTempUnschedTTLChannel, "channel_error", true
	}

	messageLower := strings.ToLower(err.Error())
	switch err.StatusCode {
	case http.StatusTooManyRequests:
		return channelTempUnschedTTLRateLimit, "rate_limit", true
	case http.StatusForbidden:
		if containsAnyKeyword(messageLower,
			"quota", "credit", "balance", "insufficient", "exhaust",
			"额度", "余额", "不足", "用完", "耗尽",
			"disabled", "suspend", "forbidden", "denied") {
			return channelTempUnschedTTLQuota, "quota_or_disabled", true
		}
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusInternalServerError, http.StatusGatewayTimeout:
		return channelTempUnschedTTLTransport, "upstream_unavailable", true
	}

	if err.StatusCode >= 500 && err.StatusCode <= 599 {
		return channelTempUnschedTTLTransport, "upstream_unavailable", true
	}

	if err.GetErrorCode() == types.ErrorCodeDoRequestFailed ||
		err.GetErrorCode() == types.ErrorCodeBadResponseStatusCode ||
		err.GetErrorCode() == types.ErrorCodeBadResponse ||
		err.GetErrorCode() == types.ErrorCodeBadResponseBody {
		return channelTempUnschedTTLTransport, "transport_error", true
	}

	return 0, "", false
}

func buildChannelFailureReason(err *types.NewAPIError) string {
	if err == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if err.StatusCode > 0 {
		parts = append(parts, fmt.Sprintf("status_code=%d", err.StatusCode))
	}
	if err.GetErrorCode() != "" {
		parts = append(parts, fmt.Sprintf("error_code=%s", err.GetErrorCode()))
	}
	if len(parts) == 0 {
		return common.MaskSensitiveInfo(err.Error())
	}
	return strings.Join(parts, ", ")
}

func appendChannelSchedulerLogInfo(c *gin.Context, entry map[string]interface{}) {
	if c == nil || len(entry) == 0 {
		return
	}
	info := make(map[string]interface{})
	if anyInfo, ok := c.Get(ginKeyChannelSchedulerLogInfo); ok {
		if existing, ok := anyInfo.(map[string]interface{}); ok {
			for k, v := range existing {
				info[k] = v
			}
		}
	}

	if existingEntries, ok := info["events"].([]map[string]interface{}); ok {
		info["events"] = append(existingEntries, entry)
	} else {
		info["events"] = []map[string]interface{}{entry}
	}
	c.Set(ginKeyChannelSchedulerLogInfo, info)
}

func getOrCreateRequestExcludedChannelIDs(c *gin.Context) map[int]struct{} {
	if c == nil {
		return nil
	}
	anyExcluded, ok := c.Get(ginKeyChannelSchedulerExcludedIDs)
	if ok {
		if excluded, ok := anyExcluded.(map[int]struct{}); ok && excluded != nil {
			return excluded
		}
	}
	excluded := make(map[int]struct{})
	c.Set(ginKeyChannelSchedulerExcludedIDs, excluded)
	return excluded
}

func containsAnyKeyword(text string, keywords ...string) bool {
	for _, keyword := range keywords {
		if keyword == "" {
			continue
		}
		if strings.Contains(text, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
