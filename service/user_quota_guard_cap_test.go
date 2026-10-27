package service

import (
	"context"
	"path/filepath"
	"testing"

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
