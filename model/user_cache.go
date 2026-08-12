package model

import (
	"context"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
)

const userCacheSchemaVersion = 1

// UserBase struct remains the same as it represents the cached data structure
type UserBase struct {
	Id       int    `json:"id"`
	Group    string `json:"group"`
	Email    string `json:"email"`
	Quota    int    `json:"quota"`
	Status   int    `json:"status"`
	Username string `json:"username"`
	Setting  string `json:"setting"`
}

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(getUserCacheKey(userId))
}

// InvalidateUserCache is the exported cache invalidation hook used after
// administrative status/quota mutations.
func InvalidateUserCache(userId int) error {
	return invalidateUserCache(userId)
}

func populateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	base := user.ToBaseUser()
	const script = `
if redis.call('EXISTS', KEYS[1]) == 1 then
  if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') == tonumber(ARGV[1])
    and tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') == tonumber(ARGV[8])
    and redis.call('HEXISTS', KEYS[1], 'Group') == 1
    and redis.call('HEXISTS', KEYS[1], 'Email') == 1
    and redis.call('HEXISTS', KEYS[1], 'Quota') == 1
    and redis.call('HEXISTS', KEYS[1], 'Status') == 1
    and redis.call('HEXISTS', KEYS[1], 'Username') == 1
    and redis.call('HEXISTS', KEYS[1], 'Setting') == 1 then
    redis.call('EXPIRE', KEYS[1], ARGV[9])
    return 2
  end
  redis.call('DEL', KEYS[1])
end
redis.call('HSET', KEYS[1],
  'Id', ARGV[1], 'Group', ARGV[2], 'Email', ARGV[3], 'Quota', ARGV[4],
  'Status', ARGV[5], 'Username', ARGV[6], 'Setting', ARGV[7],
  'CacheSchema', ARGV[8])
redis.call('EXPIRE', KEYS[1], ARGV[9])
return 1`
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		ttl = 60
	}
	_, err := common.RDB.Eval(context.Background(), script, []string{getUserCacheKey(user.Id)},
		base.Id, base.Group, base.Email, base.Quota, base.Status, base.Username, base.Setting,
		userCacheSchemaVersion, ttl,
	).Int()
	return err
}

// updateUserCache refreshes non-quota user cache fields.
// Quota is maintained by atomic quota delta paths and must not be overwritten
// by stale user snapshots from profile/settings updates.
func updateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	if err := updateUserGroupCache(user.Id, user.Group); err != nil {
		return err
	}
	if err := updateUserEmailCache(user.Id, user.Email); err != nil {
		return err
	}
	if err := updateUserStatusCache(user.Id, user.Status == common.UserStatusEnabled); err != nil {
		return err
	}
	if err := updateUserNameCache(user.Id, user.Username); err != nil {
		return err
	}
	return updateUserSettingCache(user.Id, user.Setting)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (*UserBase, error) {
	// Try getting from Redis first
	userCache, err := cacheGetUserBase(userId)
	if err == nil {
		userCache.Group = setting.NormalizeUserIdentityGroup(userCache.Group)
		return userCache, nil
	}

	// A cold cache must be hydrated synchronously: quota reservation retries
	// immediately after this return and must never fall through to a stale DB
	// balance merely because an asynchronous cache writer has not run yet.
	user, err := GetUserById(userId, false)
	if err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		if err := populateUserCache(*user); err != nil {
			common.SysLog("failed to synchronously populate user cache: " + err.Error())
		}
	}

	return &UserBase{
		Id:       user.Id,
		Group:    setting.NormalizeUserIdentityGroup(user.Group),
		Quota:    user.Quota,
		Status:   user.Status,
		Username: user.Username,
		Setting:  user.Setting,
		Email:    user.Email,
	}, nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	// Try getting from Redis first
	err := common.RedisHGetObj(getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	schema, err := common.RDB.HGet(context.Background(), getUserCacheKey(userId), "CacheSchema").Int()
	if err != nil || schema != userCacheSchemaVersion || userCache.Id != userId {
		return nil, fmt.Errorf("user cache is incomplete or stale")
	}
	return &userCache, nil
}

// Add atomic quota operations using hash fields
func cacheIncrUserQuota(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	_, err := cacheApplyUserQuotaDelta(userId, delta)
	return err
}

func cacheDecrUserQuota(userId int, delta int64) error {
	return cacheIncrUserQuota(userId, -delta)
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

func getUserQuotaCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Quota, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// New functions for individual field updates
func updateUserStatusCache(userId int, status bool) error {
	if !common.RedisEnabled {
		return nil
	}
	statusInt := common.UserStatusEnabled
	if !status {
		statusInt = common.UserStatusDisabled
	}
	return updateUserCacheFieldIfComplete(userId, "Status", statusInt)
}

func updateUserQuotaCache(userId int, quota int) error {
	if !common.RedisEnabled {
		return nil
	}
	return updateUserCacheFieldIfComplete(userId, "Quota", quota)
}

func updateUserGroupCache(userId int, group string) error {
	if !common.RedisEnabled {
		return nil
	}
	group = setting.NormalizeUserIdentityGroup(group)
	return updateUserCacheFieldIfComplete(userId, "Group", group)
}

// RefreshUserGroupCache re-reads the authoritative database value before
// updating Redis. It prevents an older subscription completion callback from
// publishing a stale group after a newer transition has already committed.
func RefreshUserGroupCache(userId int) error {
	if userId <= 0 {
		return fmt.Errorf("invalid user id")
	}
	if !common.RedisEnabled {
		return nil
	}
	var group string
	if err := DB.Model(&User{}).Where("id = ?", userId).Select(commonGroupCol).Find(&group).Error; err != nil {
		return err
	}
	return updateUserGroupCache(userId, group)
}

func UpdateUserGroupCache(userId int, group string) error {
	return updateUserGroupCache(userId, group)
}

func updateUserEmailCache(userId int, email string) error {
	return updateUserCacheFieldIfComplete(userId, "Email", email)
}

func updateUserNameCache(userId int, username string) error {
	return updateUserCacheFieldIfComplete(userId, "Username", username)
}

func updateUserSettingCache(userId int, setting string) error {
	return updateUserCacheFieldIfComplete(userId, "Setting", setting)
}

// updateUserCacheFieldIfComplete never creates a partial user hash. Missing
// hashes are left cold and will be synchronously hydrated by GetUserCache.
func updateUserCacheFieldIfComplete(userId int, field string, value interface{}) error {
	if !common.RedisEnabled {
		return nil
	}
	const script = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[1])
  or tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') ~= tonumber(ARGV[2]) then
  return 0
end
redis.call('HSET', KEYS[1], ARGV[3], ARGV[4])
return 1`
	return common.RDB.Eval(context.Background(), script, []string{getUserCacheKey(userId)},
		userId, userCacheSchemaVersion, field, fmt.Sprint(value)).Err()
}

// GetUserLanguage returns the user's language preference from cache
// Uses the existing GetUserCache mechanism for efficiency
func GetUserLanguage(userId int) string {
	userCache, err := GetUserCache(userId)
	if err != nil {
		return ""
	}
	return userCache.GetSetting().Language
}
