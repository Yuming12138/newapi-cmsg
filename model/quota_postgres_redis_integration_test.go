package model

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestQuotaReservePostgresRedisIntegration is intentionally opt-in. It is run
// against disposable PostgreSQL and Redis instances so the production and
// standby data planes are never needed for concurrency validation.
func TestQuotaReservePostgresRedisIntegration(t *testing.T) {
	postgresDSN := os.Getenv("CMSG_TEST_POSTGRES_DSN")
	redisURL := os.Getenv("CMSG_TEST_REDIS_URL")
	if postgresDSN == "" || redisURL == "" {
		t.Skip("disposable PostgreSQL/Redis integration environment is not configured")
	}

	testDB, err := gorm.Open(postgres.New(postgres.Config{
		DSN:                  postgresDSN,
		PreferSimpleProtocol: true,
	}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := testDB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(40)
	sqlDB.SetMaxIdleConns(20)
	t.Cleanup(func() { _ = sqlDB.Close() })

	redisOptions, err := redis.ParseURL(redisURL)
	require.NoError(t, err)
	testRedis := redis.NewClient(redisOptions)
	require.NoError(t, testRedis.Ping(context.Background()).Err())
	t.Cleanup(func() { _ = testRedis.Close() })

	oldDB, oldLogDB := DB, LOG_DB
	oldRDB := common.RDB
	oldRedisEnabled := common.RedisEnabled
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	oldUsingPostgreSQL := common.UsingPostgreSQL
	oldUsingSQLite := common.UsingSQLite
	oldUsingMySQL := common.UsingMySQL
	DB, LOG_DB = testDB, testDB
	common.RDB = testRedis
	common.RedisEnabled = true
	common.MemoryCacheEnabled = false
	common.BatchUpdateEnabled = false
	common.UsingPostgreSQL = true
	common.UsingSQLite = false
	common.UsingMySQL = false
	initCol()
	t.Cleanup(func() {
		DB, LOG_DB = oldDB, oldLogDB
		common.RDB = oldRDB
		common.RedisEnabled = oldRedisEnabled
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
		common.UsingPostgreSQL = oldUsingPostgreSQL
		common.UsingSQLite = oldUsingSQLite
		common.UsingMySQL = oldUsingMySQL
		initCol()
	})

	require.NoError(t, DB.AutoMigrate(&User{}, &Token{}, &Channel{}, &Ability{}))
	require.NoError(t, common.RDB.FlushDB(context.Background()).Err())

	user := User{
		Username: "pg-reserve-user-" + common.GetRandomString(8),
		Password: "unused-password-hash",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		Quota:    1000,
		AffCode:  "pg-reserve-aff-" + common.GetRandomString(10),
	}
	require.NoError(t, DB.Create(&user).Error)
	token := Token{
		UserId:      user.Id,
		Key:         "pg-reserve-token-" + common.GetRandomString(12),
		Name:        "postgres-redis-integration",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: 1000,
	}
	require.NoError(t, token.Insert())
	_, err = GetUserCache(user.Id)
	require.NoError(t, err)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)

	const attempts = 80
	const amount = 15
	userSuccess := runConcurrentReserves(t, attempts, func() (bool, error) {
		return TryReserveUserQuota(user.Id, amount)
	})
	tokenSuccess := runConcurrentReserves(t, attempts, func() (bool, error) {
		return TryReserveTokenQuota(token.Id, token.Key, amount, false)
	})
	assert.Equal(t, int64(66), userSuccess)
	assert.Equal(t, int64(66), tokenSuccess)
	assert.Equal(t, 10, readIntegrationUserQuota(t, user.Id))
	storedToken := readIntegrationToken(t, token.Id)
	assert.Equal(t, 10, storedToken.RemainQuota)
	assert.Equal(t, 990, storedToken.UsedQuota)
	cachedUser, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 10, cachedUser.Quota)
	cachedToken, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 10, cachedToken.RemainQuota)
	assert.Equal(t, 990, cachedToken.UsedQuota)

	runConcurrentRefunds(t, int(userSuccess), func() error {
		return IncreaseUserQuota(user.Id, amount, false)
	})
	runConcurrentRefunds(t, int(tokenSuccess), func() error {
		return IncreaseTokenQuota(token.Id, token.Key, amount)
	})
	require.Eventually(t, func() bool {
		userCache, userErr := cacheGetUserBase(user.Id)
		tokenCache, tokenErr := cacheGetTokenByKey(token.Key)
		return userErr == nil && tokenErr == nil && userCache.Quota == 1000 &&
			tokenCache.RemainQuota == 1000 && tokenCache.UsedQuota == 0
	}, 10*time.Second, 20*time.Millisecond)
	assert.Equal(t, 1000, readIntegrationUserQuota(t, user.Id))
	storedToken = readIntegrationToken(t, token.Id)
	assert.Equal(t, 1000, storedToken.RemainQuota)
	assert.Zero(t, storedToken.UsedQuota)

	staleToken := storedToken
	staleToken.Key = token.Key
	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -100)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)
	code, err := cacheInitToken(staleToken)
	require.NoError(t, err)
	assert.Equal(t, 2, code, "a live atomic quota must not be overwritten by a DB snapshot")
	cachedToken, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 900, cachedToken.RemainQuota)

	result, err = cacheApplyUserQuotaDelta(user.Id, -100)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)
	user.Email = "metadata-refresh@example.invalid"
	require.NoError(t, updateUserCache(user))
	cachedUser, err = cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 900, cachedUser.Quota, "metadata refresh must not overwrite atomic quota")

	channel := Channel{
		Name:          "pg-channel-status-" + common.GetRandomString(6),
		Key:           "key-a\nkey-b",
		Status:        common.ChannelStatusEnabled,
		OtherSettings: "{}",
		ChannelInfo: ChannelInfo{
			IsMultiKey:           true,
			MultiKeySize:         2,
			MultiKeyMode:         constant.MultiKeyModePolling,
			MultiKeyPollingIndex: 1,
		},
	}
	require.NoError(t, DB.Create(&channel).Error)
	var channelWG sync.WaitGroup
	for _, key := range []string{"key-a", "key-b"} {
		key := key
		channelWG.Add(1)
		go func() {
			defer channelWG.Done()
			assert.True(t, UpdateChannelStatus(channel.Id, key, common.ChannelStatusAutoDisabled, "integration test"))
		}()
	}
	channelWG.Wait()
	var storedChannel Channel
	require.NoError(t, DB.First(&storedChannel, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusAutoDisabled, storedChannel.Status)
	assert.Equal(t, common.ChannelStatusAutoDisabled, storedChannel.ChannelInfo.MultiKeyStatusList[0])
	assert.Equal(t, common.ChannelStatusAutoDisabled, storedChannel.ChannelInfo.MultiKeyStatusList[1])
}

func runConcurrentReserves(t *testing.T, attempts int, reserve func() (bool, error)) int64 {
	t.Helper()
	var successes atomic.Int64
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reserved, err := reserve()
			assert.NoError(t, err)
			if reserved {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	return successes.Load()
}

func runConcurrentRefunds(t *testing.T, count int, refund func() error) {
	t.Helper()
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, refund())
		}()
	}
	wg.Wait()
}

func readIntegrationUserQuota(t *testing.T, userID int) int {
	t.Helper()
	var quota int
	require.NoError(t, DB.Model(&User{}).Where("id = ?", userID).Select("quota").Find(&quota).Error)
	return quota
}

func readIntegrationToken(t *testing.T, tokenID int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, tokenID).Error)
	return token
}
