package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const asxsAccountTokenFileDefault = "/data/ops/asxs-account-token"

// ASXSAccountDailyBalance is the account-wide subscription remainder. Channel
// balances from /api/usage remain separate because they describe a single
// routing pool and are also used to protect individual channels.
type ASXSAccountDailyBalance struct {
	Balance           *float64 `json:"balance"`
	Currency          string   `json:"currency"`
	UpdatedAt         int64    `json:"updated_at"`
	SubscriptionCount int      `json:"subscription_count"`
	Partial           bool     `json:"partial"`
}

type asxsBillingWindow struct {
	LimitType   string `json:"limitType"`
	LimitMicros *int64 `json:"limitMicros"`
	LeftMicros  *int64 `json:"leftMicros"`
}

type asxsBillingSubscription struct {
	ID        string              `json:"id"`
	ExpiresAt string              `json:"expiresAt"`
	Limits    []asxsBillingWindow `json:"limits"`
}

type asxsSubscriptionWindows struct {
	SubscriptionID string              `json:"subscriptionId"`
	Windows        []asxsBillingWindow `json:"windows"`
}

type asxsBillingState struct {
	BalanceMicros       *int64                    `json:"balanceMicros"`
	Subscription        *asxsBillingSubscription  `json:"subscription"`
	Subscriptions       []asxsBillingSubscription `json:"subscriptions"`
	Windows             []asxsBillingWindow       `json:"windows"`
	SubscriptionWindows []asxsSubscriptionWindows `json:"subscriptionWindows"`
}

var asxsAccountBalanceMu sync.Mutex

func unavailableASXSAccountDailyBalance() ASXSAccountDailyBalance {
	return ASXSAccountDailyBalance{Currency: "USD", Partial: true}
}

func fetchASXSAccountDailyBalance(ctx context.Context) (ASXSAccountDailyBalance, error) {
	result := unavailableASXSAccountDailyBalance()
	tokenPath := strings.TrimSpace(os.Getenv("ASXS_ACCOUNT_TOKEN_FILE"))
	if tokenPath == "" {
		tokenPath = asxsAccountTokenFileDefault
	}

	// A successful response can rotate the login token. Serialize reads, requests,
	// and replacement so concurrent dashboard loads do not restore an older token.
	asxsAccountBalanceMu.Lock()
	defer asxsAccountBalanceMu.Unlock()
	token, err := readASXSAccountToken(tokenPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	channel, err := asxsAccountProxyChannel()
	if err != nil || channel == nil {
		return result, err
	}
	client, err := NewProxyHttpClient(channel.GetSetting().Proxy)
	if err != nil {
		return result, err
	}
	requestClient := *client
	requestClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "https://api.asxs.top/api/me/billing/state", nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "new-api-asxs-account-balance/1.0")
	resp, err := requestClient.Do(req)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return result, fmt.Errorf("ASXS account balance returned HTTP %d", resp.StatusCode)
	}
	if replacement := strings.TrimSpace(resp.Header.Get("X-New-Token")); replacement != "" && replacement != token {
		if err := replaceASXSAccountToken(tokenPath, replacement); err != nil {
			return result, err
		}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return result, err
	}
	return parseASXSAccountDailyBalance(raw, time.Now())
}

func asxsAccountProxyChannel() (*model.Channel, error) {
	cfg := operation_setting.GetChannelBudgetGuardSetting()
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	channels, err := fetchChannelBudgetGuardChannels()
	if err != nil {
		return nil, err
	}
	var first *model.Channel
	for _, item := range filterASXSChannelBudgetGuardChannels(resolveChannelBudgetGuardChannels(cfg, channels)) {
		if item.channel == nil {
			continue
		}
		if first == nil {
			first = item.channel
		}
		if strings.TrimSpace(item.channel.GetSetting().Proxy) != "" {
			return item.channel, nil
		}
	}
	return first, nil
}

func readASXSAccountToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", fmt.Errorf("ASXS account token file must be regular and mode 0600")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(raw))
	if len(token) < 20 || strings.ContainsAny(token, "\r\n\x00") {
		return "", fmt.Errorf("ASXS account token file is invalid")
	}
	return token, nil
}

func replaceASXSAccountToken(path, token string) error {
	if len(token) < 20 || strings.ContainsAny(token, "\r\n\x00") {
		return fmt.Errorf("ASXS returned an invalid replacement token")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".asxs-account-token-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := temporary.Chmod(0600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(token + "\n"); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func parseASXSAccountDailyBalance(raw []byte, now time.Time) (ASXSAccountDailyBalance, error) {
	result := ASXSAccountDailyBalance{Currency: "USD", UpdatedAt: now.Unix()}
	var state asxsBillingState
	if err := common.Unmarshal(raw, &state); err != nil {
		return unavailableASXSAccountDailyBalance(), err
	}
	if state.BalanceMicros == nil {
		return unavailableASXSAccountDailyBalance(), fmt.Errorf("ASXS account billing state is incomplete")
	}
	subscriptions := state.Subscriptions
	if state.Subscription != nil {
		found := false
		for _, subscription := range subscriptions {
			if subscription.ID == state.Subscription.ID {
				found = true
				break
			}
		}
		if !found {
			subscriptions = append([]asxsBillingSubscription{*state.Subscription}, subscriptions...)
		}
	}
	windowsByID := make(map[string][]asxsBillingWindow, len(state.SubscriptionWindows))
	for _, item := range state.SubscriptionWindows {
		windowsByID[item.SubscriptionID] = item.Windows
	}
	seen := make(map[string]bool, len(subscriptions))
	var leftMicros int64
	for _, subscription := range subscriptions {
		if subscription.ID == "" || seen[subscription.ID] {
			result.Partial = true
			continue
		}
		seen[subscription.ID] = true
		if subscription.ExpiresAt != "" {
			expiresAt, err := time.Parse(time.RFC3339Nano, subscription.ExpiresAt)
			if err != nil {
				result.Partial = true
				continue
			}
			if !expiresAt.After(now) {
				continue
			}
		}
		result.SubscriptionCount++
		windows := windowsByID[subscription.ID]
		if len(windows) == 0 && state.Subscription != nil && subscription.ID == state.Subscription.ID {
			windows = state.Windows
		}
		window, runtime := asxsDailyWindow(windows)
		if !runtime {
			window, _ = asxsDailyWindow(subscription.Limits)
			if window == nil {
				continue
			}
			result.Partial = true
		}
		if window.LeftMicros == nil || window.LimitMicros == nil {
			return unavailableASXSAccountDailyBalance(), fmt.Errorf("ASXS account daily quota is incomplete")
		}
		left := *window.LeftMicros
		if left < 0 {
			left = 0
		}
		if left > (1<<63-1)-leftMicros {
			return unavailableASXSAccountDailyBalance(), fmt.Errorf("ASXS account balance exceeds int64")
		}
		leftMicros += left
	}
	// ASXS billing-state micros use 5,000,000 units per USD.
	balance := math.Round(float64(leftMicros)/5e4) / 100
	result.Balance = &balance
	return result, nil
}

func asxsDailyWindow(windows []asxsBillingWindow) (*asxsBillingWindow, bool) {
	for index := range windows {
		if windows[index].LimitType == "daily" {
			return &windows[index], true
		}
	}
	return nil, false
}
