package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

const userQuotaGuardStateOptionKey = "user_quota_guard_state"

type userQuotaGuardState struct {
	Version int                                `json:"version"`
	Users   map[string]userQuotaGuardUserState `json:"users"`
}

type userQuotaGuardUserState struct {
	Date                        string  `json:"date"`
	Phase                       string  `json:"phase"`
	AppliedRestrictedGrantQuota int     `json:"applied_restricted_grant_quota"`
	AppliedExtraUSD             float64 `json:"applied_extra_usd"`
	AppliedUnlockedQuota        int     `json:"applied_unlocked_quota,omitempty"`
	AppliedUnlockedUSD          float64 `json:"applied_unlocked_usd,omitempty"`
	UnlockedQuotaSource         string  `json:"unlocked_quota_source,omitempty"`
}

type userQuotaPhase struct {
	DateKey string
	Phase   string
}

var (
	userQuotaGuardOnce    sync.Once
	userQuotaGuardRunning atomic.Bool
)

func StartUserQuotaGuardTask() {
	userQuotaGuardOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			cfg := operation_setting.GetUserQuotaGuardSetting()
			logger.LogInfo(context.Background(), fmt.Sprintf("user quota guard task started: enabled=%t tick=%s restricted=%s-%s", cfg.Enabled, userQuotaGuardInterval(cfg), cfg.RestrictedStart, cfg.RestrictedEnd))
			runUserQuotaGuardOnce()
			for {
				time.Sleep(userQuotaGuardInterval(operation_setting.GetUserQuotaGuardSetting()))
				runUserQuotaGuardOnce()
			}
		})
	})
}

func userQuotaGuardInterval(cfg *operation_setting.UserQuotaGuardSetting) time.Duration {
	if cfg == nil || cfg.TickIntervalMinutes < 1 {
		return time.Minute
	}
	return time.Duration(cfg.TickIntervalMinutes) * time.Minute
}

func userQuotaGuardQuotaPerUSD(cfg *operation_setting.UserQuotaGuardSetting) int {
	if cfg == nil || cfg.QuotaPerUSD <= 0 {
		return 500000
	}
	return cfg.QuotaPerUSD
}

func runUserQuotaGuardOnce() {
	if !userQuotaGuardRunning.CompareAndSwap(false, true) {
		return
	}
	defer userQuotaGuardRunning.Store(false)

	ctx := context.Background()
	cfg := operation_setting.GetUserQuotaGuardSetting()
	if cfg == nil || !cfg.Enabled {
		return
	}

	users, err := fetchUserQuotaGuardUsers()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("user quota guard: query users failed: %v", err))
		return
	}
	managedUsers := filterUserQuotaGuardUsers(cfg, users)
	if len(managedUsers) == 0 {
		if common.DebugEnabled {
			logger.LogDebug(ctx, "user quota guard: no managed users")
		}
		return
	}

	state, hadExistingState := loadUserQuotaGuardState()
	phase, err := currentUserQuotaPhase(cfg)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("user quota guard: invalid schedule: %v", err))
		return
	}
	quotaPerUSD := userQuotaGuardQuotaPerUSD(cfg)
	unlockedQuotaUSD := cfg.UnlockedQuotaUSD
	unlockedQuotaSource := "static"
	if phase.Phase == "unlocked" {
		resolvedUSD, resolvedSource, err := resolveUserQuotaGuardUnlockedQuota(ctx, cfg)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("user quota guard: resolve unlocked quota failed, fallback to static $%g: %v", cfg.UnlockedQuotaUSD, err))
		} else {
			unlockedQuotaUSD = resolvedUSD
			unlockedQuotaSource = resolvedSource
		}
	}

	stateChanged := false
	updated := 0
	failed := 0
	initialized := 0
	for _, user := range managedUsers {
		if user == nil {
			continue
		}
		userIDKey := strconv.Itoa(user.Id)
		userState, exists := state.Users[userIDKey]
		if !hadExistingState {
			state.Users[userIDKey] = initialUserQuotaGuardState(cfg, phase, user.Id, quotaPerUSD, unlockedQuotaUSD, unlockedQuotaSource)
			stateChanged = true
			initialized++
			continue
		}

		newState, changed, err := applyUserQuotaGuardPolicy(cfg, user, userState, exists, phase, quotaPerUSD, unlockedQuotaUSD, unlockedQuotaSource)
		if err != nil {
			failed++
			logger.LogWarn(ctx, fmt.Sprintf("user quota guard: user_id=%d username=%s failed: %v", user.Id, user.Username, err))
			continue
		}
		if newState != userState || !exists {
			state.Users[userIDKey] = newState
			stateChanged = true
		}
		if changed {
			updated++
		}
	}

	if stateChanged {
		if err := saveJSONOption(userQuotaGuardStateOptionKey, state); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("user quota guard: save state failed: %v", err))
		}
	}
	if common.DebugEnabled || updated > 0 || failed > 0 || initialized > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("user quota guard: phase=%s date=%s managed=%d initialized=%d updated=%d failed=%d", phase.Phase, phase.DateKey, len(managedUsers), initialized, updated, failed))
	}
}

func fetchUserQuotaGuardUsers() ([]*model.User, error) {
	var users []*model.User
	err := model.DB.
		Select("id", "username", "display_name", "role", "status", "quota", "used_quota", "group", "remark").
		Where("status = ?", common.UserStatusEnabled).
		Order("id asc").
		Find(&users).Error
	return users, err
}

func filterUserQuotaGuardUsers(cfg *operation_setting.UserQuotaGuardSetting, users []*model.User) []*model.User {
	includeIDs := intSet(cfg.IncludeUserIDs)
	excludeIDs := intSet(cfg.ExcludeUserIDs)
	includeRoles := intSet(cfg.IncludeRoles)
	includeGroups := normalizedStringSet(cfg.IncludeGroups)
	result := make([]*model.User, 0, len(users))

	for _, user := range users {
		if user == nil {
			continue
		}
		if _, excluded := excludeIDs[user.Id]; excluded {
			continue
		}
		_, explicitlyIncluded := includeIDs[user.Id]
		managed := false
		if cfg.AutoManage {
			roleOK := len(includeRoles) == 0 || includeRoles[user.Role]
			groupOK := len(includeGroups) == 0 || guardGroupContainsAny(user.Group, includeGroups)
			managed = roleOK && groupOK
			managed = managed || explicitlyIncluded
		} else {
			managed = explicitlyIncluded
		}
		if managed {
			result = append(result, user)
		}
	}
	return result
}

func currentUserQuotaPhase(cfg *operation_setting.UserQuotaGuardSetting) (userQuotaPhase, error) {
	now := guardNow(cfg.Timezone)
	start, err := parseGuardHHMM(defaultString(cfg.RestrictedStart, "09:00"))
	if err != nil {
		return userQuotaPhase{}, err
	}
	end, err := parseGuardHHMM(defaultString(cfg.RestrictedEnd, "18:00"))
	if err != nil {
		return userQuotaPhase{}, err
	}
	phase := "unlocked"
	if inGuardMinuteWindow(now.Hour()*60+now.Minute(), start, end) {
		phase = "restricted"
	}
	return userQuotaPhase{DateKey: now.Format("2006-01-02"), Phase: phase}, nil
}

func applyUserQuotaGuardPolicy(cfg *operation_setting.UserQuotaGuardSetting, user *model.User, state userQuotaGuardUserState, exists bool, phase userQuotaPhase, quotaPerUSD int, unlockedQuotaUSD float64, unlockedQuotaSource string) (userQuotaGuardUserState, bool, error) {
	if phase.Phase == "restricted" {
		baseUSD, extraUSD, targetUSD := userQuotaRestrictedUSD(cfg, phase.DateKey, user.Id)
		targetQuota := quotaFromUSD(targetUSD, quotaPerUSD)
		shouldEnter := !exists || state.Phase != "restricted" || state.Date != phase.DateKey
		remark := fmt.Sprintf("个人限额时段额度 $%g（基础 $%g，追加 $%g）；%s 后解锁", targetUSD, baseUSD, extraUSD, defaultString(cfg.RestrictedEnd, "18:00"))
		if shouldEnter {
			if err := updateGuardedUserQuota(user, targetQuota, remark); err != nil {
				return state, false, err
			}
			return userQuotaGuardUserState{Date: phase.DateKey, Phase: "restricted", AppliedRestrictedGrantQuota: targetQuota, AppliedExtraUSD: extraUSD}, true, nil
		}
		if targetQuota != state.AppliedRestrictedGrantQuota {
			delta := targetQuota - state.AppliedRestrictedGrantQuota
			newQuota := user.Quota + delta
			if newQuota < 0 {
				newQuota = 0
			}
			if err := updateGuardedUserQuota(user, newQuota, remark); err != nil {
				return state, false, err
			}
			state.AppliedRestrictedGrantQuota = targetQuota
			state.AppliedExtraUSD = extraUSD
			return state, true, nil
		}
		return state, false, nil
	}

	targetQuota := quotaFromUSD(unlockedQuotaUSD, quotaPerUSD)
	shouldUnlock := !exists || state.Phase != "unlocked" || state.Date != phase.DateKey
	shouldSync := shouldUnlock || user.Quota != targetQuota || state.AppliedUnlockedQuota != targetQuota || state.UnlockedQuotaSource != unlockedQuotaSource
	if shouldSync {
		remark := fmt.Sprintf("非个人限额时段已解锁；当前渠道池可用 $%g", unlockedQuotaUSD)
		if unlockedQuotaSource == "daily_quota_pool" {
			remark = fmt.Sprintf("非个人限额时段已解锁；当前今日总额度池可用 $%g", unlockedQuotaUSD)
		}
		if unlockedQuotaSource == "static" {
			remark = "非个人限额时段已解锁；总额度由渠道池控制"
		}
		if err := updateGuardedUserQuota(user, targetQuota, remark); err != nil {
			return state, false, err
		}
		return userQuotaGuardUserState{
			Date:                        phase.DateKey,
			Phase:                       "unlocked",
			AppliedRestrictedGrantQuota: 0,
			AppliedExtraUSD:             0,
			AppliedUnlockedQuota:        targetQuota,
			AppliedUnlockedUSD:          unlockedQuotaUSD,
			UnlockedQuotaSource:         unlockedQuotaSource,
		}, true, nil
	}
	return state, false, nil
}

func resolveUserQuotaGuardUnlockedQuota(ctx context.Context, cfg *operation_setting.UserQuotaGuardSetting) (float64, string, error) {
	source := strings.ToLower(strings.TrimSpace(cfg.UnlockedQuotaSource))
	if source == "" || source == "static" {
		return cfg.UnlockedQuotaUSD, "static", nil
	}
	switch source {
	case "daily_quota_pool", "daily_pool":
		summary, handled, err := GetDailyQuotaPoolSnapshot(ctx)
		if err != nil {
			return cfg.UnlockedQuotaUSD, "static", err
		}
		if !handled || summary.ChannelCount == 0 {
			return cfg.UnlockedQuotaUSD, "static", nil
		}
		return summary.RemainingUSD, "daily_quota_pool", nil
	case "asxs_channel_pool", "asxs_pool", "channel_pool", "quota_source_pool", "unified_quota_source_pool":
		summary, handled, err := RefreshASXSChannelBudgetPoolSummary(ctx)
		if err != nil {
			return cfg.UnlockedQuotaUSD, "static", err
		}
		if !handled || summary.ChannelCount == 0 {
			return cfg.UnlockedQuotaUSD, "static", nil
		}
		if source == "quota_source_pool" || source == "unified_quota_source_pool" {
			return summary.RemainingUSD, "quota_source_pool", nil
		}
		return summary.RemainingUSD, "asxs_channel_pool", nil
	default:
		return cfg.UnlockedQuotaUSD, "static", fmt.Errorf("unsupported unlocked_quota_source %q", cfg.UnlockedQuotaSource)
	}
}

func initialUserQuotaGuardState(cfg *operation_setting.UserQuotaGuardSetting, phase userQuotaPhase, userID int, quotaPerUSD int, unlockedQuotaUSD float64, unlockedQuotaSource string) userQuotaGuardUserState {
	if phase.Phase == "restricted" {
		_, extraUSD, targetUSD := userQuotaRestrictedUSD(cfg, phase.DateKey, userID)
		return userQuotaGuardUserState{
			Date:                        phase.DateKey,
			Phase:                       "restricted",
			AppliedRestrictedGrantQuota: quotaFromUSD(targetUSD, quotaPerUSD),
			AppliedExtraUSD:             extraUSD,
		}
	}
	return userQuotaGuardUserState{
		Date:                 phase.DateKey,
		Phase:                "unlocked",
		AppliedUnlockedQuota: quotaFromUSD(unlockedQuotaUSD, quotaPerUSD),
		AppliedUnlockedUSD:   unlockedQuotaUSD,
		UnlockedQuotaSource:  unlockedQuotaSource,
	}
}

func updateGuardedUserQuota(user *model.User, quota int, remark string) error {
	if user.Quota == quota && user.Remark == remark {
		return nil
	}
	if err := model.DB.Model(&model.User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":  quota,
		"remark": remark,
	}).Error; err != nil {
		return err
	}
	user.Quota = quota
	user.Remark = remark
	return model.InvalidateUserCache(user.Id)
}

func userQuotaExtraUSD(cfg *operation_setting.UserQuotaGuardSetting, dateKey string, userID int) float64 {
	key := strconv.Itoa(userID)
	extra := 0.0
	if cfg.PerUserExtraUSD != nil {
		extra += cfg.PerUserExtraUSD[key]
	}
	if cfg.DailyApprovals != nil {
		if day, ok := cfg.DailyApprovals[dateKey]; ok && day != nil {
			extra += day[key].ExtraUSD
		}
	}
	return extra
}

func userQuotaRestrictedUSD(cfg *operation_setting.UserQuotaGuardSetting, dateKey string, userID int) (baseUSD float64, extraUSD float64, targetUSD float64) {
	if cfg == nil {
		return 0, 0, 0
	}
	baseUSD = math.Max(0, cfg.DaytimeBaseUSD)
	if cfg.PerUserBaseUSD != nil {
		if override, ok := cfg.PerUserBaseUSD[strconv.Itoa(userID)]; ok {
			baseUSD = math.Max(0, override)
		}
	}
	extraUSD = userQuotaExtraUSD(cfg, dateKey, userID)
	targetUSD = math.Max(0, baseUSD+extraUSD)
	return baseUSD, extraUSD, targetUSD
}

func loadUserQuotaGuardState() (userQuotaGuardState, bool) {
	state := userQuotaGuardState{Version: 1, Users: map[string]userQuotaGuardUserState{}}
	if loadJSONOption(userQuotaGuardStateOptionKey, &state) && state.Users != nil {
		state.Version = 1
		return state, len(state.Users) > 0
	}
	return state, false
}

func quotaFromUSD(usd float64, quotaPerUSD int) int {
	return int(math.Round(usd * float64(quotaPerUSD)))
}

func parseGuardHHMM(value string) (int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid HH:MM value %q", value)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("invalid HH:MM value %q", value)
	}
	return hour*60 + minute, nil
}

func inGuardMinuteWindow(current int, start int, end int) bool {
	if start < end {
		return current >= start && current < end
	}
	return current >= start || current < end
}

func intSet(values []int) map[int]bool {
	result := make(map[int]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func normalizedStringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed != "" {
			result[trimmed] = true
		}
	}
	return result
}

func guardGroupContainsAny(groupValue string, targets map[string]bool) bool {
	if len(targets) == 0 {
		return true
	}
	for _, group := range strings.Split(groupValue, ",") {
		if targets[strings.ToLower(strings.TrimSpace(group))] {
			return true
		}
	}
	return false
}
