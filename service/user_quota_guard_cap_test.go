package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/alicebob/miniredis/v2"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func quotaCapTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "quota-cap.db")), &gorm.Config{})
	require.NoError(t, err)
	originalDB, originalRedis := model.DB, common.RedisEnabled
	model.DB, common.RedisEnabled = db, false
	t.Cleanup(func() {
		model.DB, common.RedisEnabled = originalDB, originalRedis
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	require.NoError(t, db.AutoMigrate(&model.User{}))
	return db
}

func TestUserQuotaGuardCapReclaimsManualTopUpWithoutRefilling(t *testing.T) {
	db := quotaCapTestDB(t)
	server := miniredis.RunT(t)
	originalRDB := common.RDB
	common.RedisEnabled = true
	common.RDB = redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = common.RDB.Close(); common.RDB = originalRDB })
	user := &model.User{Id: 6, Username: "capped-user", AffCode: "cap-test", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: 500 * 500000, UsedQuota: 1200}
	require.NoError(t, db.Create(user).Error)
	server.HSet("user:6", "Quota", "250000000")
	cfg := &operation_setting.UserQuotaGuardSetting{DaytimeBaseUSD: 40, RestrictedBalanceCapUSD: 40}
	phase := userQuotaPhase{DateKey: "2026-10-27", Phase: "restricted"}
	state := initialUserQuotaGuardState(cfg, phase, user.Id, 500000, 0, "static")
	next, changed, err := applyUserQuotaGuardPolicy(cfg, user, state, true, phase, 500000, 0, "static")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, state, next)
	require.False(t, server.Exists("user:6"), "the oversized cached balance must be invalidated")
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 40*500000, stored.Quota)
	require.Equal(t, 1200, stored.UsedQuota)

	// A later tick must not replenish the amount already consumed.
	require.NoError(t, db.Model(&stored).Update("quota", 12*500000).Error)
	user.Quota = 500 * 500000 // stale scan, before a concurrent consumption
	server.HSet("user:6", "Quota", "6000000")
	_, changed, err = applyUserQuotaGuardPolicy(cfg, user, next, true, phase, 500000, 0, "static")
	require.NoError(t, err)
	require.False(t, changed)
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 12*500000, stored.Quota)
	require.Equal(t, "6000000", server.HGet("user:6", "Quota"))

	// Cache invalidation is safe to retry and does not create a partial hash.
	require.NoError(t, model.InvalidateUserCache(user.Id))
	require.Equal(t, int64(0), common.RDB.Exists(context.Background(), "user:6").Val())
}

func TestUserQuotaGuardCapTransitionsAndExemptions(t *testing.T) {
	for _, tt := range []struct {
		name        string
		role        int
		storedRole  int
		phase       string
		oldGrant    int
		balance     int
		want        int
		wantChanged bool
	}{
		{name: "ordinary top-up", role: 1, storedRole: 1, phase: "restricted", oldGrant: 40, balance: 300, want: 40, wantChanged: true},
		{name: "already consumed", role: 1, storedRole: 1, phase: "restricted", oldGrant: 40, balance: 8, want: 8},
		{name: "old approval reduced", role: 1, storedRole: 1, phase: "restricted", oldGrant: 60, balance: 35, want: 15, wantChanged: true},
		{name: "old approval plus top-up", role: 1, storedRole: 1, phase: "restricted", oldGrant: 60, balance: 300, want: 40, wantChanged: true},
		{name: "old approval exhausted", role: 1, storedRole: 1, phase: "restricted", oldGrant: 60, balance: 8, want: 0, wantChanged: true},
		{name: "demoted user", role: 1, storedRole: 1, phase: "restricted", oldGrant: 40, balance: 300, want: 40, wantChanged: true},
		{name: "admin exempt", role: 10, storedRole: 10, phase: "restricted", oldGrant: 40, balance: 300, want: 300},
		{name: "promotion after scan", role: 1, storedRole: 10, phase: "restricted", oldGrant: 40, balance: 300, want: 300},
		{name: "root exempt", role: 100, storedRole: 100, phase: "restricted", oldGrant: 40, balance: 300, want: 300},
		{name: "evening pool intact", role: 1, storedRole: 1, phase: "unlocked", oldGrant: 40, balance: 300, want: 125, wantChanged: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db := quotaCapTestDB(t)
			user := &model.User{Id: 6, Username: "transition-user", AffCode: "transition-test", Role: tt.storedRole,
				Status: common.UserStatusEnabled, Quota: tt.balance * 500000}
			require.NoError(t, db.Create(user).Error)
			user.Role = tt.role
			cfg := &operation_setting.UserQuotaGuardSetting{DaytimeBaseUSD: 40, RestrictedBalanceCapUSD: 40}
			phase := userQuotaPhase{DateKey: "2026-10-27", Phase: tt.phase}
			state := userQuotaGuardUserState{Date: phase.DateKey, Phase: "restricted", AppliedRestrictedGrantQuota: tt.oldGrant * 500000}
			_, changed, err := applyUserQuotaGuardPolicy(cfg, user, state, true, phase, 500000, 125, "daily_quota_pool")
			require.NoError(t, err)
			require.Equal(t, tt.wantChanged, changed)
			var stored model.User
			require.NoError(t, db.First(&stored, user.Id).Error)
			require.Equal(t, tt.want*500000, stored.Quota)
		})
	}
}

func TestUserQuotaGuardCapAppliesToGrantsAndRequests(t *testing.T) {
	cfg := &operation_setting.UserQuotaGuardSetting{Enabled: true, DaytimeBaseUSD: 40, RestrictedBalanceCapUSD: 40,
		PerUserBaseUSD: map[string]float64{"13": 10},
		DailyApprovals: map[string]map[string]operation_setting.UserQuotaGuardApproval{
			"2026-10-27": {"6": {ExtraUSD: 20}, "13": {ExtraUSD: 20}},
		}}
	base, extra, target := userQuotaRestrictedUSD(cfg, "2026-10-27", 6)
	require.Equal(t, 40.0, base)
	require.Zero(t, extra)
	require.Equal(t, 40.0, target)
	status := buildUserQuotaRequestStatus(cfg, "2026-10-27", 6, 500000, 0, nil)
	require.False(t, status.CanRequest)
	require.Contains(t, status.Reason, "cap")
	require.Zero(t, status.RequestAmountUSD)
	require.Error(t, addUserQuotaGuardDailyApproval(cfg, "2026-10-27", 6, 20, "should not apply", 0))
	require.Equal(t, 20.0, cfg.DailyApprovals["2026-10-27"]["6"].ExtraUSD)

	// Lower per-user grants remain lower, and a partial approval stops at $40.
	base, extra, target = userQuotaRestrictedUSD(cfg, "2026-10-27", 13)
	require.Equal(t, 10.0, base)
	require.Equal(t, 20.0, extra)
	require.Equal(t, 30.0, target)
	status = buildUserQuotaRequestStatus(cfg, "2026-10-27", 13, 500000, 0, nil)
	require.True(t, status.CanRequest)
	require.Equal(t, 10.0, status.RequestAmountUSD)
	require.Equal(t, 5000000, status.RequestAmountQuota)

	cfg.RestrictedBalanceCapUSD = 0
	_, _, target = userQuotaRestrictedUSD(cfg, "2026-10-27", 6)
	require.Equal(t, 60.0, target, "zero cap preserves legacy approvals")
	status = buildUserQuotaRequestStatus(cfg, "2026-10-27", 6, 500000, 0, nil)
	require.True(t, status.CanRequest)
	require.Equal(t, 20.0, status.RequestAmountUSD)
}

func TestUserQuotaGuardAdjustmentUsesLiveBalance(t *testing.T) {
	db := quotaCapTestDB(t)
	user := &model.User{Id: 6, Username: "adjust-user", AffCode: "adjust-test", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: 5 * 500000}
	require.NoError(t, db.Create(user).Error)
	user.Quota = 35 * 500000
	require.NoError(t, adjustGuardedUserQuota(user, -20*500000, 40*500000, true, "capped"))
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Zero(t, stored.Quota, "a stale scan must not replenish a concurrently consumed balance")
}

func TestUserQuotaGuardTwentyFiveDollarRollout(t *testing.T) {
	db := quotaCapTestDB(t)
	cfg := &operation_setting.UserQuotaGuardSetting{Enabled: true, DaytimeBaseUSD: 25, RestrictedBalanceCapUSD: 25,
		PerUserBaseUSD: map[string]float64{"13": 10}}
	phase := userQuotaPhase{DateKey: "2026-10-27", Phase: "restricted"}
	user := &model.User{Id: 6, Username: "rollout-user", AffCode: "rollout-test", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Quota: 30 * 500000}
	require.NoError(t, db.Create(user).Error)
	state := userQuotaGuardUserState{Date: phase.DateKey, Phase: "restricted", AppliedRestrictedGrantQuota: 40 * 500000}
	state, changed, err := applyUserQuotaGuardPolicy(cfg, user, state, true, phase, 500000, 0, "static")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 15*500000, user.Quota, "lower today's grant without resetting today's consumed amount")
	require.Equal(t, 25*500000, state.AppliedRestrictedGrantQuota)

	require.NoError(t, db.Model(user).Update("quota", 500*500000).Error)
	_, changed, err = applyUserQuotaGuardPolicy(cfg, user, state, true, phase, 500000, 0, "static")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 25*500000, user.Quota)
	_, changed, err = applyUserQuotaGuardPolicy(cfg, user, state, true, phase, 500000, 0, "static")
	require.NoError(t, err)
	require.False(t, changed, "subsequent guard ticks are idempotent")
	_, _, target := userQuotaRestrictedUSD(cfg, phase.DateKey, 13)
	require.Equal(t, 10.0, target)

	status := buildUserQuotaRequestStatus(cfg, phase.DateKey, 13, 500000, 0, nil)
	require.Equal(t, 15.0, status.RequestAmountUSD, "$10 users may only request the remaining $15")
}

func TestUserQuotaGuardFixedDailyKeepsTodaysConsumption(t *testing.T) {
	db := quotaCapTestDB(t)
	const rate = 500000
	cfg := &operation_setting.UserQuotaGuardSetting{
		Enabled: true, RestrictedBalanceCapUSD: 65,
		PerUserFixedDailyUSD: map[string]float64{"13": 10},
	}
	user := &model.User{Id: 13, Username: "fixed-daily-user", AffCode: "fixed-daily",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 8 * rate}
	require.NoError(t, db.Create(user).Error)
	require.Len(t, filterUserQuotaGuardUsers(cfg, []*model.User{user}), 1)

	apply := func(state userQuotaGuardUserState, date, phase string, wantBalance int) userQuotaGuardUserState {
		t.Helper()
		next, _, err := applyUserQuotaGuardPolicy(cfg, user, state, true, userQuotaPhase{DateKey: date, Phase: phase}, rate, 125, "daily_quota_pool")
		require.NoError(t, err)
		require.NoError(t, db.First(user, user.Id).Error)
		require.Equal(t, wantBalance*rate, user.Quota)
		require.Equal(t, userQuotaPhaseFixedDaily, next.Phase)
		require.Equal(t, date, next.Date)
		require.Equal(t, 10*rate, next.AppliedRestrictedGrantQuota)
		return next
	}

	const today = "2026-11-25"
	old := userQuotaGuardUserState{Date: today, Phase: "restricted", AppliedRestrictedGrantQuota: 15 * rate}
	state := apply(old, today, "restricted", 3) // $7 spent on the day of activation
	state = apply(state, today, "unlocked", 3)  // shared pool becomes available at 17:00
	require.NoError(t, db.Model(user).Update("quota", 0).Error)
	user.Quota = 0
	state = apply(state, today, "unlocked", 0)

	state = apply(state, "2026-11-26", "restricted", 10)
	require.NoError(t, db.Model(user).Update("quota", 6*rate).Error)
	user.Quota = 6 * rate
	state = apply(state, "2026-11-26", "restricted", 6)
	apply(state, "2026-11-27", "restricted", 10) // unused quota does not accumulate

	status := buildUserQuotaRequestStatus(cfg, today, user.Id, rate, 0, nil)
	require.False(t, status.Enabled)
	require.False(t, status.CanRequest)
	require.Zero(t, status.RequestAmountUSD)
	require.ErrorContains(t, addUserQuotaGuardDailyApproval(cfg, today, user.Id, 20, "", 0), "cannot be topped up")
	require.True(t, buildUserQuotaRequestStatus(cfg, today, 14, rate, 0, nil).CanRequest)
}

func TestUserQuotaGuardFixedDailyActivationAfterUnlock(t *testing.T) {
	db := quotaCapTestDB(t)
	oldLogDB := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = oldLogDB })
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	const rate = 500000
	const date = "2026-11-25"
	user := &model.User{Id: 13, Username: "fixed-evening-user", AffCode: "fixed-evening",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 125 * rate}
	require.NoError(t, db.Create(user).Error)
	usageTime := time.Date(2026, 11, 24, 17, 5, 0, 0, time.UTC).Unix() // 01:05 on the 25th in Shanghai
	require.NoError(t, db.Create(&model.Log{UserId: user.Id, Type: model.LogTypeConsume, Quota: 6 * rate, CreatedAt: usageTime}).Error)
	require.NoError(t, db.Create(&model.Log{UserId: user.Id, Type: model.LogTypeConsume, Quota: 9 * rate, CreatedAt: usageTime - 2*3600}).Error)
	cfg := &operation_setting.UserQuotaGuardSetting{
		Enabled: true, Timezone: "Asia/Shanghai",
		PerUserFixedDailyUSD: map[string]float64{"13": 10},
	}
	old := userQuotaGuardUserState{Date: date, Phase: "unlocked"}
	phase := userQuotaPhase{DateKey: date, Phase: "unlocked"}
	state, changed, err := applyUserQuotaGuardPolicy(cfg, user, old, true, phase, rate, 125, "daily_quota_pool")
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, userQuotaPhaseFixedDaily, state.Phase)
	require.Equal(t, 4*rate, user.Quota)
}
