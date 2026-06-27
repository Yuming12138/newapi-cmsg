package model

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

func cacheSetToken(token Token) error {
	key := common.GenerateHMAC(token.Key)
	token.Clean()
	err := common.RedisHSetObj(fmt.Sprintf("token:%s", key), &token, time.Duration(common.RedisKeyCacheSeconds())*time.Second)
	if err != nil {
		return err
	}
	return nil
}

func cacheDeleteToken(key string) error {
	key = common.GenerateHMAC(key)
	err := common.RedisDelKey(fmt.Sprintf("token:%s", key))
	if err != nil {
		return err
	}
	return nil
}

func cacheIncrTokenQuota(key string, increment int64) error {
	return cacheAdjustTokenQuota(key, increment)
}

func cacheAdjustTokenQuota(key string, quotaDelta int64) error {
	redisKey := fmt.Sprintf("token:%s", common.GenerateHMAC(key))
	ctx := context.Background()

	ttl, err := common.RDB.TTL(ctx, redisKey).Result()
	if err != nil {
		return err
	}
	if ttl <= 0 {
		return nil
	}

	unlimitedRaw, err := common.RDB.HGet(ctx, redisKey, "UnlimitedQuota").Result()
	if err != nil {
		return err
	}
	unlimited, err := strconv.ParseBool(unlimitedRaw)
	if err != nil {
		return err
	}

	txn := common.RDB.TxPipeline()
	if unlimited {
		txn.HSet(ctx, redisKey, constant.TokenFiledRemainQuota, 0)
	} else {
		txn.HIncrBy(ctx, redisKey, constant.TokenFiledRemainQuota, quotaDelta)
	}
	txn.HIncrBy(ctx, redisKey, constant.TokenFieldUsedQuota, -quotaDelta)
	txn.Expire(ctx, redisKey, ttl)

	_, err = txn.Exec(ctx)
	return err
}

func cacheDecrTokenQuota(key string, decrement int64) error {
	return cacheIncrTokenQuota(key, -decrement)
}

func cacheSetTokenField(key string, field string, value string) error {
	key = common.GenerateHMAC(key)
	err := common.RedisHSetField(fmt.Sprintf("token:%s", key), field, value)
	if err != nil {
		return err
	}
	return nil
}

// CacheGetTokenByKey 从缓存中获取 token，如果缓存中不存在，则从数据库中获取
func cacheGetTokenByKey(key string) (*Token, error) {
	hmacKey := common.GenerateHMAC(key)
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	err := common.RedisHGetObj(fmt.Sprintf("token:%s", hmacKey), &token)
	if err != nil {
		return nil, err
	}
	token.Key = key
	return &token, nil
}
