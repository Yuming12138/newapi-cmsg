package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

const kimiCPASubscriptionInfoKey = "kimi_cpa_subscription"

type KimiSubscriptionWindow struct {
	Name             string  `json:"name"`
	RemainingPercent float64 `json:"remaining_percent"`
	ResetAt          int64   `json:"reset_at"`
}

type KimiSubscriptionAccount struct {
	ID               string                   `json:"id"`
	RemainingPercent float64                  `json:"remaining_percent"`
	Windows          []KimiSubscriptionWindow `json:"windows"`
}

// Subscription percentages are independent of the official Moonshot wallet.
// Account IDs are hashes; OAuth credentials never leave the management client.
type KimiSubscriptionBalance struct {
	Source                string                    `json:"source"`
	RemainingPercent      *float64                  `json:"remaining_percent"`
	Windows               []KimiSubscriptionWindow  `json:"windows"`
	Accounts              []KimiSubscriptionAccount `json:"accounts"`
	AccountCount          int                       `json:"account_count"`
	CredentialCount       int                       `json:"credential_count"`
	AvailableAccountCount int                       `json:"available_account_count"`
	ChannelCount          int                       `json:"channel_count"`
	UpdatedAt             int64                     `json:"updated_at"`
	Partial               bool                      `json:"partial"`
	Error                 string                    `json:"error,omitempty"`
}

func IsKimiCPAChannel(channel *model.Channel) bool {
	if channel == nil || channel.Type != constant.ChannelTypeOpenAI || isOfficialKimiChannel(channel) {
		return false
	}
	parsed, err := url.Parse(channel.GetBaseURL())
	if err != nil || !strings.Contains(strings.ToLower(parsed.Hostname()), "cliproxy") {
		return false
	}
	for _, name := range strings.Split(channel.Models, ",") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "kimi-") {
			return true
		}
	}
	var mapping map[string]string
	if channel.ModelMapping != nil && common.UnmarshalJsonStr(*channel.ModelMapping, &mapping) == nil {
		for _, name := range mapping {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(name)), "kimi-") {
				return true
			}
		}
	}
	return false
}

type kimiCPAManagementClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func newKimiCPAManagementClient() (*kimiCPAManagementClient, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("KIMI_CPA_MANAGEMENT_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv("CLIPROXY_CPA_BASE_URL")), "/")
	}
	if baseURL == "" {
		baseURL = "http://cliproxy-api:8317"
	}
	keyPaths := []string{
		strings.TrimSpace(os.Getenv("KIMI_CPA_MANAGEMENT_KEY_FILE")),
		strings.TrimSpace(os.Getenv("CLIPROXY_CPA_MANAGEMENT_KEY_FILE")),
		"/run/secrets/cliproxy_cpa_management_key",
		"/data/ops/kimi-cpa-management-key",
	}
	var key []byte
	for _, keyPath := range keyPaths {
		if keyPath == "" {
			continue
		}
		candidate, readErr := os.ReadFile(keyPath)
		if readErr == nil && len(bytes.TrimSpace(candidate)) > 0 {
			key = candidate
			break
		}
	}
	if len(bytes.TrimSpace(key)) == 0 {
		return nil, fmt.Errorf("Kimi CPA management key is unavailable")
	}
	return &kimiCPAManagementClient{baseURL: baseURL, key: strings.TrimSpace(string(key)), client: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (client *kimiCPAManagementClient) request(ctx context.Context, path string, payload any, result any) error {
	method := http.MethodGet
	var raw []byte
	if payload != nil {
		var err error
		raw, err = common.Marshal(payload)
		if err != nil {
			return err
		}
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("X-Management-Key", client.key)
	// CPA's management API authenticates with the bearer header. Keep the
	// legacy header as well because older CPA builds still inspect it.
	req.Header.Set("Authorization", "Bearer "+client.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.client.Do(req)
	if err != nil {
		return fmt.Errorf("Kimi CPA management request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Kimi CPA management HTTP %d", resp.StatusCode)
	}
	return common.DecodeJson(io.LimitReader(resp.Body, 2<<20), result)
}

// Decode identity only for deduplication, never as authentication. Two device
// tokens for one Kimi user share one subscription and must not double its quota.
func kimiSubscriptionAccountID(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("Kimi subscription account identity is unavailable")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("invalid Kimi subscription account identity")
	}
	var claims map[string]any
	if err := common.Unmarshal(raw, &claims); err != nil {
		return "", fmt.Errorf("invalid Kimi subscription account identity")
	}
	for _, field := range []string{"user_id", "sub"} {
		id, _ := claims[field].(string)
		if id != "" {
			return fmt.Sprintf("%x", sha256.Sum256([]byte(id)))[:16], nil
		}
	}
	return "", fmt.Errorf("Kimi subscription account identity is unavailable")
}

func parseKimiSubscriptionUsage(raw []byte) ([]KimiSubscriptionWindow, error) {
	var response struct {
		Usages map[string]struct {
			UsedRatio *float64 `json:"used_ratio"`
			ResetTime string   `json:"reset_time"`
		} `json:"usages"`
		Limits []struct {
			Window struct {
				Duration int    `json:"duration"`
				TimeUnit string `json:"timeUnit"`
			} `json:"window"`
			Detail struct {
				Limit     any    `json:"limit"`
				Remaining any    `json:"remaining"`
				ResetTime string `json:"resetTime"`
			} `json:"detail"`
		} `json:"limits"`
	}
	if err := common.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("invalid Kimi subscription usage response")
	}
	var windows []KimiSubscriptionWindow
	for _, name := range []string{"limit_5h", "limit_week", "limit_month_total", "limit_month_code"} {
		usage, exists := response.Usages[name]
		if !exists || usage.UsedRatio == nil {
			continue
		}
		ratio := *usage.UsedRatio
		reset, err := time.Parse(time.RFC3339Nano, usage.ResetTime)
		if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) || err != nil {
			return nil, fmt.Errorf("invalid Kimi subscription quota window")
		}
		windows = append(windows, KimiSubscriptionWindow{Name: strings.TrimPrefix(name, "limit_"), RemainingPercent: max(0, 1-ratio) * 100, ResetAt: reset.Unix()})
	}
	if len(windows) == 0 {
		for _, limit := range response.Limits {
			values := map[string]interface{}{"limit": limit.Detail.Limit, "remaining": limit.Detail.Remaining}
			total, okTotal := guardObjectFloat(values, "limit")
			left, okLeft := guardObjectFloat(values, "remaining")
			reset, err := time.Parse(time.RFC3339Nano, limit.Detail.ResetTime)
			if !okTotal || !okLeft || total <= 0 || left < 0 || left > total || err != nil {
				return nil, fmt.Errorf("invalid Kimi subscription quota window")
			}
			name := fmt.Sprintf("%d_%s", limit.Window.Duration, strings.ToLower(limit.Window.TimeUnit))
			if limit.Window.Duration == 300 && limit.Window.TimeUnit == "TIME_UNIT_MINUTE" {
				name = "5h"
			}
			windows = append(windows, KimiSubscriptionWindow{Name: name, RemainingPercent: left / total * 100, ResetAt: reset.Unix()})
		}
	}
	if len(windows) == 0 {
		return nil, fmt.Errorf("Kimi subscription quota windows are unavailable")
	}
	return windows, nil
}

func (client *kimiCPAManagementClient) fetch(ctx context.Context) (KimiSubscriptionBalance, error) {
	summary := KimiSubscriptionBalance{Source: kimiCPASubscriptionInfoKey, ChannelCount: 1}
	var inventory struct {
		Files []struct {
			Provider  string `json:"provider"`
			AuthIndex string `json:"auth_index"`
			Name      string `json:"name"`
			Disabled  bool   `json:"disabled"`
		} `json:"files"`
	}
	if err := client.request(ctx, "/v0/management/auth-files", nil, &inventory); err != nil {
		return summary, err
	}
	accounts := make(map[string]KimiSubscriptionAccount)
	failedAccounts := make(map[string]struct{})
	for _, credential := range inventory.Files {
		if credential.Provider != "kimi" || credential.Disabled {
			continue
		}
		summary.CredentialCount++
		var record struct {
			AccessToken string `json:"access_token"`
		}
		if err := client.request(ctx, "/v0/management/auth-files/download?name="+url.QueryEscape(credential.Name), nil, &record); err != nil {
			summary.Partial = true
			continue
		}
		accountID, err := kimiSubscriptionAccountID(record.AccessToken)
		record.AccessToken = ""
		if err != nil {
			summary.Partial = true
			continue
		}
		if _, exists := accounts[accountID]; exists {
			continue
		}
		var response struct {
			StatusCode int    `json:"status_code"`
			Body       string `json:"body"`
		}
		payload := map[string]any{
			"auth_index": credential.AuthIndex, "method": http.MethodGet,
			"url":    "https://api.kimi.com/coding/v1/usages",
			"header": map[string]string{"Authorization": "Bearer $TOKEN$", "Accept": "application/json"},
		}
		if err := client.request(ctx, "/v0/management/api-call", payload, &response); err != nil || response.StatusCode != http.StatusOK {
			failedAccounts[accountID] = struct{}{}
			continue
		}
		windows, err := parseKimiSubscriptionUsage([]byte(response.Body))
		if err != nil {
			failedAccounts[accountID] = struct{}{}
			continue
		}
		remaining := 100.0
		for _, window := range windows {
			remaining = min(remaining, window.RemainingPercent)
		}
		accounts[accountID] = KimiSubscriptionAccount{ID: accountID, Windows: windows, RemainingPercent: remaining}
		delete(failedAccounts, accountID)
	}
	if len(accounts) == 0 {
		return summary, fmt.Errorf("Kimi subscription quota is unavailable")
	}
	for _, account := range accounts {
		summary.Accounts = append(summary.Accounts, account)
	}
	sort.Slice(summary.Accounts, func(i, j int) bool { return summary.Accounts[i].ID < summary.Accounts[j].ID })
	summary.AccountCount = len(accounts) + len(failedAccounts)
	summary.Partial = summary.Partial || len(failedAccounts) > 0
	remaining := 0.0
	for _, account := range summary.Accounts {
		remaining += account.RemainingPercent
		if account.RemainingPercent > 0 {
			summary.AvailableAccountCount++
		}
	}
	remaining /= float64(len(accounts))
	if !summary.Partial {
		summary.RemainingPercent = &remaining
	}
	// Windows are account-specific; multiple accounts retain their individual
	// reset times rather than publishing one misleading pooled reset time.
	if len(summary.Accounts) == 1 {
		summary.Windows = summary.Accounts[0].Windows
	}
	summary.UpdatedAt = common.GetTimestamp()
	return summary, nil
}

func kimiSubscriptionSnapshot(channel *model.Channel) *KimiSubscriptionBalance {
	info := channel.GetOtherInfo()
	value, exists := info[kimiCPASubscriptionInfoKey]
	if !exists {
		return nil
	}
	raw, err := common.Marshal(value)
	var summary KimiSubscriptionBalance
	if err != nil || common.Unmarshal(raw, &summary) != nil {
		return nil
	}
	return &summary
}

func saveKimiSubscriptionSnapshot(channel *model.Channel, summary KimiSubscriptionBalance) error {
	info := channel.GetOtherInfo()
	if summary.UpdatedAt <= 0 {
		summary.UpdatedAt = common.GetTimestamp()
	}
	info[kimiCPASubscriptionInfoKey] = summary
	raw, err := common.Marshal(info)
	if err != nil {
		return err
	}
	channel.OtherInfo = string(raw)
	channel.BalanceUpdatedTime = summary.UpdatedAt
	if model.DB == nil {
		return nil
	}
	return model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]interface{}{
		"other_info":           string(raw),
		"balance_updated_time": summary.UpdatedAt,
	}).Error
}

func UpdateKimiCPASubscriptionBalance(ctx context.Context, channel *model.Channel) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	client, err := newKimiCPAManagementClient()
	summary := KimiSubscriptionBalance{Source: kimiCPASubscriptionInfoKey, ChannelCount: 1}
	if err == nil {
		summary, err = client.fetch(ctx)
	}
	if err != nil {
		if previous := kimiSubscriptionSnapshot(channel); previous != nil {
			summary = *previous
		}
		summary.Partial = true
		summary.Error = "Kimi subscription quota sync failed"
	}
	if summary.UpdatedAt <= 0 {
		summary.UpdatedAt = common.GetTimestamp()
	}
	if saveErr := saveKimiSubscriptionSnapshot(channel, summary); saveErr != nil {
		return 0, saveErr
	}
	if summary.RemainingPercent == nil {
		return 0, err
	}
	return *summary.RemainingPercent, err
}
