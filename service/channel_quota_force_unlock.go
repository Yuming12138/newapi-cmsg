package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const (
	CliproxyCPAQuotaForceUnlockChannelID = 12
	cliproxyCPAQuotaForceUnlockOptionKey = "cliproxy_cpa_quota_guard.force_unlock"
	cliproxyCPAQuotaForceUnlockMaxWindow = 8 * 24 * time.Hour
)

type ChannelQuotaForceUnlockResult struct {
	ChannelID            int   `json:"channel_id"`
	Active               bool  `json:"active"`
	Until                int64 `json:"until"`
	ChannelEnabled       bool  `json:"channel_enabled"`
	ProtectionRestoredOn int64 `json:"protection_restored_on,omitempty"`
}

func ForceUnlockCliproxyCPAQuotaGuard(channelID int, requestedBy int, now int64) (*ChannelQuotaForceUnlockResult, error) {
	channel, guard, health, err := getCliproxyCPAQuotaGuardChannel(channelID)
	if err != nil {
		return nil, err
	}
	if channel.Status == common.ChannelStatusManuallyDisabled {
		return nil, fmt.Errorf("channel 12 is manually disabled; enable it before forcing a quota guard unlock")
	}
	if health == nil {
		return nil, fmt.Errorf("channel 12 quota health is unavailable; wait for the next guard probe")
	}
	if !cliproxyCPAQuotaForceUnlockEligible(health) {
		return nil, fmt.Errorf("channel 12 has no schedulable upstream quota; force unlock cannot bypass actual CPA exhaustion or account unavailability")
	}

	until, cycleSignature, err := cliproxyCPAQuotaForceUnlockBoundary(health, now)
	if err != nil {
		return nil, err
	}
	override := map[string]interface{}{
		"active":          true,
		"until":           until,
		"requested_at":    now,
		"requested_by":    requestedBy,
		"cycle_signature": cycleSignature,
		"scope":           "dynamic_daily_budget_and_protected_reserve",
	}
	if err := saveCliproxyCPAQuotaForceUnlockOption(override); err != nil {
		return nil, err
	}

	guard["manual_force_unlock"] = override
	otherInfo := channel.GetOtherInfo()
	otherInfo["cliproxy_cpa_quota_guard"] = guard
	otherInfoJSON, err := common.Marshal(otherInfo)
	if err != nil {
		_ = saveCliproxyCPAQuotaForceUnlockOption(map[string]interface{}{})
		return nil, fmt.Errorf("encode channel quota guard metadata: %w", err)
	}

	err = model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Channel{}).Where("id = ?", channelID).Updates(map[string]interface{}{
			"other_info": string(otherInfoJSON),
			"status":     common.ChannelStatusEnabled,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&model.Ability{}).Where("channel_id = ?", channelID).Update("enabled", true).Error
	})
	if err != nil {
		_ = saveCliproxyCPAQuotaForceUnlockOption(map[string]interface{}{})
		return nil, fmt.Errorf("enable channel 12 after quota guard unlock: %w", err)
	}
	model.InitChannelCache()

	return &ChannelQuotaForceUnlockResult{
		ChannelID:      channelID,
		Active:         true,
		Until:          until,
		ChannelEnabled: true,
	}, nil
}

func CancelCliproxyCPAQuotaGuardForceUnlock(channelID int, requestedBy int, now int64) (*ChannelQuotaForceUnlockResult, error) {
	channel, guard, _, err := getCliproxyCPAQuotaGuardChannel(channelID)
	if err != nil {
		return nil, err
	}
	override := map[string]interface{}{
		"active":       false,
		"until":        0,
		"cancelled_at": now,
		"cancelled_by": requestedBy,
	}
	if err := saveCliproxyCPAQuotaForceUnlockOption(override); err != nil {
		return nil, err
	}

	guard["manual_force_unlock"] = override
	otherInfo := channel.GetOtherInfo()
	otherInfo["cliproxy_cpa_quota_guard"] = guard
	otherInfoJSON, err := common.Marshal(otherInfo)
	if err != nil {
		return nil, fmt.Errorf("encode channel quota guard metadata: %w", err)
	}
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("other_info", string(otherInfoJSON)).Error; err != nil {
		return nil, fmt.Errorf("restore channel 12 quota protection metadata: %w", err)
	}
	model.InitChannelCache()

	return &ChannelQuotaForceUnlockResult{
		ChannelID:            channelID,
		Active:               false,
		ChannelEnabled:       channel.Status == common.ChannelStatusEnabled,
		ProtectionRestoredOn: now + 60,
	}, nil
}

func getCliproxyCPAQuotaGuardChannel(channelID int) (*model.Channel, map[string]interface{}, map[string]interface{}, error) {
	if channelID != CliproxyCPAQuotaForceUnlockChannelID {
		return nil, nil, nil, fmt.Errorf("force unlock is restricted to channel 12")
	}
	channel, err := model.GetChannelById(channelID, false)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load channel 12: %w", err)
	}
	otherInfo := channel.GetOtherInfo()
	guard, ok := otherInfo["cliproxy_cpa_quota_guard"].(map[string]interface{})
	if !ok {
		return nil, nil, nil, fmt.Errorf("channel 12 is not managed by the CPA quota guard")
	}
	managed, _ := guardObjectBool(guard, "managed")
	if !managed {
		return nil, nil, nil, fmt.Errorf("channel 12 CPA quota guard is not active")
	}
	health, _ := guard["health"].(map[string]interface{})
	return channel, guard, health, nil
}

func cliproxyCPAQuotaForceUnlockEligible(health map[string]interface{}) bool {
	if health == nil {
		return false
	}
	ok, exists := guardObjectBool(health, "ok")
	if !exists || !ok {
		return false
	}
	dynamic, _ := health["dynamic_daily_budget"].(map[string]interface{})
	applied, _ := guardObjectBool(dynamic, "applied")
	if !applied {
		return false
	}
	availableAccounts, _ := guardObjectInt64(health, "available_account_count")
	totalBalance, _ := guardObjectFloat(health, "total_balance_units")
	if availableAccounts > 0 && totalBalance > 0 {
		return true
	}
	accounts, _ := health["accounts"].([]interface{})
	for _, raw := range accounts {
		account, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		accountOK, _ := guardObjectBool(account, "ok")
		schedulable, _ := guardObjectBool(account, "schedulable")
		remaining, _ := guardObjectFloat(account, "raw_remaining_percent")
		if accountOK && schedulable && remaining > 0 {
			return true
		}
	}
	return false
}

func cliproxyCPAQuotaForceUnlockBoundary(health map[string]interface{}, now int64) (int64, string, error) {
	dynamic, _ := health["dynamic_daily_budget"].(map[string]interface{})
	cycleSignature := strings.TrimSpace(quotaProtectionString(dynamic, "planning_signature"))
	if cycleSignature == "" {
		return 0, "", fmt.Errorf("channel 12 quota cycle signature is unavailable; wait for the next guard probe")
	}
	candidates := make([]int64, 0, 4)
	for _, key := range []string{"next_daily_budget_reset_at", "weekly_reset_at", "effective_reset_at"} {
		if value, ok := guardObjectInt64(dynamic, key); ok {
			candidates = append(candidates, value)
		}
	}
	if windows, ok := health["windows"].(map[string]interface{}); ok {
		if weekly, ok := windows["7d"].(map[string]interface{}); ok {
			if value, ok := guardObjectInt64(weekly, "reset_at"); ok {
				candidates = append(candidates, value)
			}
		}
	}

	maxUntil := now + int64(cliproxyCPAQuotaForceUnlockMaxWindow/time.Second)
	until := int64(0)
	for _, candidate := range candidates {
		if candidate > now && candidate <= maxUntil && (until == 0 || candidate < until) {
			until = candidate
		}
	}
	if until > 0 {
		return until, cycleSignature, nil
	}
	return 0, "", fmt.Errorf("channel 12 official reset time is unavailable or stale; wait for the next guard probe")
}

func saveCliproxyCPAQuotaForceUnlockOption(value map[string]interface{}) error {
	raw, err := common.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode channel 12 force unlock option: %w", err)
	}
	if err := model.UpdateOption(cliproxyCPAQuotaForceUnlockOptionKey, string(raw)); err != nil {
		return fmt.Errorf("save channel 12 force unlock option: %w", err)
	}
	return nil
}
