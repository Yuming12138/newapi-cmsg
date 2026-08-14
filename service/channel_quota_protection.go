package service

import (
	"context"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

const (
	channelDailyProtectedBudgetExhaustedCode = "channel_daily_protected_budget_exhausted"
	channelProtectedReserveReachedCode       = "channel_protected_reserve_reached"
)

type ChannelQuotaProtectionBlock struct {
	ChannelID         int
	Code              string
	Reason            string
	Kind              string
	RetryAt           int64
	RetryAfterSeconds int64
	Timezone          string
	UpdatedAt         int64
	AllowedModels     []string
}

func (block *ChannelQuotaProtectionBlock) RecoveryTime() string {
	if block == nil || block.RetryAt <= 0 {
		return ""
	}
	location := time.Local
	if name := strings.TrimSpace(block.Timezone); name != "" {
		if loaded, err := time.LoadLocation(name); err == nil {
			location = loaded
		}
	}
	return time.Unix(block.RetryAt, 0).In(location).Format("2006-01-02 15:04:05 -07:00")
}

func GetChannelQuotaProtectionBlock(channel *model.Channel) *ChannelQuotaProtectionBlock {
	return GetChannelQuotaProtectionBlockForModel(channel, "")
}

func GetChannelQuotaProtectionBlockForModel(channel *model.Channel, modelName string) *ChannelQuotaProtectionBlock {
	if channel == nil || (channel.Status != common.ChannelStatusAutoDisabled && channel.Status != common.ChannelStatusEnabled) {
		return nil
	}
	otherInfo := parseGuardObject(channel.OtherInfo)
	quotaSource, ok := otherInfo[channelQuotaSourceInfoKey].(map[string]interface{})
	if !ok {
		return nil
	}
	blockInfo, ok := quotaSource["block"].(map[string]interface{})
	if !ok {
		return nil
	}
	allowedModels := quotaProtectionStringList(blockInfo, "allowed_models")
	if channel.Status == common.ChannelStatusEnabled && len(allowedModels) == 0 {
		return nil
	}
	if quotaProtectionModelAllowed(modelName, allowedModels) {
		return nil
	}
	if spendable, exists := guardObjectBool(quotaSource, "spendable"); exists && spendable && len(allowedModels) == 0 {
		return nil
	}
	httpStatus, ok := guardObjectInt64(blockInfo, "http_status")
	if !ok || httpStatus != 429 {
		return nil
	}
	code := quotaProtectionString(blockInfo, "code")
	if code != channelDailyProtectedBudgetExhaustedCode && code != channelProtectedReserveReachedCode {
		return nil
	}
	retryAt, ok := guardObjectInt64(blockInfo, "retry_at")
	if !ok || retryAt <= 0 {
		return nil
	}
	retryAfter, _ := guardObjectInt64(blockInfo, "retry_after_seconds")
	updatedAt, _ := guardObjectInt64(quotaSource, "updated_at")
	if value, exists := guardObjectInt64(blockInfo, "updated_at"); exists && value > updatedAt {
		updatedAt = value
	}
	return &ChannelQuotaProtectionBlock{
		ChannelID:         channel.Id,
		Code:              code,
		Reason:            quotaProtectionString(blockInfo, "reason"),
		Kind:              quotaProtectionString(blockInfo, "kind"),
		RetryAt:           retryAt,
		RetryAfterSeconds: retryAfter,
		Timezone:          quotaProtectionString(blockInfo, "timezone"),
		UpdatedAt:         updatedAt,
		AllowedModels:     allowedModels,
	}
}

func FindChannelQuotaProtectionBlock(ctx context.Context, groups []string, modelName string, requestPath string) (*ChannelQuotaProtectionBlock, error) {
	if model.DB == nil {
		return nil, nil
	}
	groupSet := make(map[string]struct{}, len(groups))
	for _, group := range groups {
		if normalized := strings.TrimSpace(group); normalized != "" {
			groupSet[normalized] = struct{}{}
		}
	}
	if len(groupSet) == 0 {
		return nil, nil
	}
	models := []string{modelName}
	if normalized := ratio_setting.FormatMatchingModelName(modelName); normalized != "" && normalized != modelName {
		models = append(models, normalized)
	}

	var abilities []model.Ability
	if err := model.DB.WithContext(ctx).Where("model IN ?", models).Find(&abilities).Error; err != nil {
		return nil, err
	}
	channelIDs := make([]int, 0, len(abilities))
	seenChannelIDs := make(map[int]struct{}, len(abilities))
	for _, ability := range abilities {
		if _, ok := groupSet[ability.Group]; !ok {
			continue
		}
		if _, seen := seenChannelIDs[ability.ChannelId]; seen {
			continue
		}
		seenChannelIDs[ability.ChannelId] = struct{}{}
		channelIDs = append(channelIDs, ability.ChannelId)
	}
	if len(channelIDs) == 0 {
		return nil, nil
	}

	var channels []*model.Channel
	if err := model.DB.WithContext(ctx).
		Select("id", "type", "status", "other_info", "settings").
		Where("id IN ?", channelIDs).
		Find(&channels).Error; err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	var selected *ChannelQuotaProtectionBlock
	for _, channel := range channels {
		if !quotaProtectionChannelSupportsRequestPath(channel, requestPath) {
			continue
		}
		block := GetChannelQuotaProtectionBlockForModel(channel, modelName)
		if block == nil {
			continue
		}
		normalizeChannelQuotaProtectionRetry(block, now)
		if selected == nil || block.RetryAt < selected.RetryAt || (block.RetryAt == selected.RetryAt && block.UpdatedAt > selected.UpdatedAt) {
			selected = block
		}
	}
	return selected, nil
}

func normalizeChannelQuotaProtectionRetry(block *ChannelQuotaProtectionBlock, now int64) {
	if block == nil {
		return
	}
	if block.RetryAt <= now {
		block.RetryAt = now + 60
	}
	block.RetryAfterSeconds = max(int64(1), block.RetryAt-now)
}

func quotaProtectionChannelSupportsRequestPath(channel *model.Channel, requestPath string) bool {
	if channel == nil || requestPath == "" || channel.Type != constant.ChannelTypeAdvancedCustom {
		return channel != nil
	}
	config := channel.GetOtherSettings().AdvancedCustom
	return config != nil && config.SupportsPath(requestPath)
}

func quotaProtectionString(values map[string]interface{}, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func quotaProtectionStringList(values map[string]interface{}, key string) []string {
	raw, ok := values[key].([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		value, ok := item.(string)
		value = strings.TrimSpace(value)
		if !ok || value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func quotaProtectionModelAllowed(modelName string, allowedModels []string) bool {
	if strings.TrimSpace(modelName) == "" || len(allowedModels) == 0 {
		return false
	}
	normalizedModel := ratio_setting.FormatMatchingModelName(modelName)
	for _, allowed := range allowedModels {
		if modelName == allowed || normalizedModel == allowed {
			return true
		}
	}
	return false
}
