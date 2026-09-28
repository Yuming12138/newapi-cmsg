package service

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

const (
	kimiBalanceEndpoint     = "https://api.moonshot.cn/v1/users/me/balance"
	kimiBalanceTickInterval = 5 * time.Minute
	kimiBalanceTimeout      = 15 * time.Second
)

type kimiBalanceResponse struct {
	Code   int  `json:"code"`
	Status bool `json:"status"`
	Data   *struct {
		AvailableBalance float64 `json:"available_balance"`
	} `json:"data"`
}

var (
	kimiBalanceSyncOnce    sync.Once
	kimiBalanceSyncRunning atomic.Bool
)

func StartKimiBalanceSyncTask() {
	kimiBalanceSyncOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		gopool.Go(func() {
			runKimiBalanceSyncOnce()
			for {
				time.Sleep(kimiBalanceTickInterval)
				runKimiBalanceSyncOnce()
			}
		})
	})
}

func UpdateKimiBalance(channel *model.Channel) (float64, error) {
	if channel == nil || !isOfficialKimiChannel(channel) {
		return 0, fmt.Errorf("channel is not an official Kimi channel")
	}
	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), kimiBalanceTimeout)
	defer cancel()
	balanceCNY, err := fetchKimiBalance(ctx, client, channel.Key)
	if err != nil {
		return 0, err
	}
	balanceUSD := balanceCNY / ratio_setting.USD2RMB
	updatedAt := common.GetTimestamp()
	if err := model.DB.Model(channel).Select("balance", "balance_updated_time").Updates(model.Channel{
		Balance:            balanceUSD,
		BalanceUpdatedTime: updatedAt,
	}).Error; err != nil {
		return 0, err
	}
	channel.Balance = balanceUSD
	channel.BalanceUpdatedTime = updatedAt
	return balanceUSD, nil
}

func fetchKimiBalance(ctx context.Context, client *http.Client, key string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, kimiBalanceEndpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("Kimi balance HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return 0, err
	}
	return parseKimiBalanceResponse(raw)
}

func parseKimiBalanceResponse(raw []byte) (float64, error) {
	var payload kimiBalanceResponse
	if err := common.Unmarshal(raw, &payload); err != nil {
		return 0, err
	}
	if !payload.Status || payload.Code != 0 || payload.Data == nil {
		return 0, fmt.Errorf("Kimi balance is unavailable (code %d)", payload.Code)
	}
	balance := payload.Data.AvailableBalance
	if balance < 0 || math.IsNaN(balance) || math.IsInf(balance, 0) {
		return 0, fmt.Errorf("Kimi balance is invalid")
	}
	return balance, nil
}

func runKimiBalanceSyncOnce() {
	if !kimiBalanceSyncRunning.CompareAndSwap(false, true) {
		return
	}
	defer kimiBalanceSyncRunning.Store(false)

	ctx := context.Background()
	var channels []*model.Channel
	if err := model.DB.Select("id", "name", "key", "type", "base_url", "setting", "status").
		Where("status = ?", common.ChannelStatusEnabled).
		Find(&channels).Error; err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Kimi balance sync query failed: %v", err))
		return
	}
	for _, channel := range channels {
		if !isOfficialKimiChannel(channel) || strings.TrimSpace(channel.Key) == "" {
			continue
		}
		if _, err := UpdateKimiBalance(channel); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("Kimi balance sync: channel_id=%d failed: %v", channel.Id, err))
		}
	}
}
