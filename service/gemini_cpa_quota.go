package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"golang.org/x/sync/singleflight"
)

const geminiCPAQuotaInfoKey = "gemini_cpa_quota"

var geminiCPAQuotaRefresh singleflight.Group

type GeminiCPAModelQuota struct {
	Model            string   `json:"model"`
	RemainingPercent *float64 `json:"remaining_percent"`
	ResetAt          int64    `json:"reset_at,omitempty"`
}

type GeminiCPAAccount struct {
	ID                 string                `json:"id"`
	Tier               string                `json:"tier,omitempty"`
	RuntimeUnavailable bool                  `json:"runtime_unavailable"`
	RemainingPercent   *float64              `json:"remaining_percent"`
	Models             []GeminiCPAModelQuota `json:"models"`
	Error              string                `json:"error,omitempty"`
}

// Percentages are subscription quotas, never dollar balances. Identities are
// hashed; OAuth records and raw management/upstream responses are not stored.
type GeminiCPAQuotaBalance struct {
	Source                string                `json:"source"`
	RemainingPercent      *float64              `json:"remaining_percent"`
	Models                []GeminiCPAModelQuota `json:"models"`
	Accounts              []GeminiCPAAccount    `json:"accounts"`
	AccountCount          int                   `json:"account_count"`
	CredentialCount       int                   `json:"credential_count"`
	AvailableAccountCount int                   `json:"available_account_count"`
	UpdatedAt             int64                 `json:"updated_at"`
	Partial               bool                  `json:"partial"`
	Error                 string                `json:"error,omitempty"`
}

func IsGeminiCPAChannel(channel *model.Channel) bool {
	if channel == nil || channel.Type != constant.ChannelTypeOpenAI || IsKimiCPAChannel(channel) {
		return false
	}
	parsed, err := url.Parse(channel.GetBaseURL())
	if err != nil || !strings.Contains(strings.ToLower(parsed.Hostname()), "cliproxy") {
		return false
	}
	for _, name := range geminiCPARelevantModels(channel) {
		if strings.HasPrefix(name, "gemini-") {
			return true
		}
	}
	return false
}

func geminiCPARelevantModels(channel *model.Channel) []string {
	if channel == nil {
		return nil
	}
	var mapping map[string]string
	if channel.ModelMapping != nil {
		_ = common.UnmarshalJsonStr(*channel.ModelMapping, &mapping)
	}
	seen := make(map[string]struct{})
	for _, name := range strings.Split(channel.Models, ",") {
		name = strings.TrimSpace(name)
		if target := strings.TrimSpace(mapping[name]); target != "" {
			name = target
		}
		if strings.HasPrefix(name, "gemini-") || strings.HasPrefix(name, "claude-") || strings.HasPrefix(name, "gpt-oss-") {
			seen[name] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for name := range seen {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func geminiCPAAccountID(email, authIndex string) string {
	identity := strings.ToLower(strings.TrimSpace(email))
	if identity == "" {
		identity = strings.TrimSpace(authIndex)
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity)))[:16]
}

func geminiCPAParseFloat(value any) (float64, bool) {
	var result float64
	switch typed := value.(type) {
	case float64:
		result = typed
	case string:
		var err error
		result, err = strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return result, !math.IsNaN(result) && !math.IsInf(result, 0)
}

func parseGeminiCPAModelQuotas(raw []byte, relevant []string) ([]GeminiCPAModelQuota, error) {
	var response struct {
		Models map[string]struct {
			QuotaInfo map[string]any `json:"quotaInfo"`
		} `json:"models"`
	}
	if common.Unmarshal(raw, &response) != nil || len(response.Models) == 0 {
		return nil, fmt.Errorf("Gemini CPA model quota response is invalid")
	}
	quotas := make([]GeminiCPAModelQuota, 0, len(relevant))
	known := false
	for _, name := range relevant {
		quota := GeminiCPAModelQuota{Model: name}
		upstream, exists := response.Models[name]
		if exists && upstream.QuotaInfo != nil {
			// Google protobuf JSON omits a zero scalar. A quotaInfo with no
			// remainingFraction means exhausted, not unknown or 100 percent.
			fraction := 0.0
			valid := true
			if rawFraction, present := upstream.QuotaInfo["remainingFraction"]; present {
				fraction, valid = geminiCPAParseFloat(rawFraction)
			}
			if valid && fraction >= 0 && fraction <= 1 {
				percent := fraction * 100
				quota.RemainingPercent = &percent
				known = true
			}
			if reset, ok := upstream.QuotaInfo["resetTime"].(string); ok {
				if parsed, err := time.Parse(time.RFC3339Nano, reset); err == nil {
					quota.ResetAt = parsed.Unix()
				}
			}
		}
		quotas = append(quotas, quota)
	}
	if !known {
		return nil, fmt.Errorf("Gemini CPA has no quota for this channel's models")
	}
	return quotas, nil
}

func geminiCPAAccountRemaining(quotas []GeminiCPAModelQuota) *float64 {
	var remaining *float64
	for _, quota := range quotas {
		// Claude/GPT-OSS have independent quota pools. Exhausting one must
		// not make the Gemini quota headline falsely read zero.
		if !strings.HasPrefix(quota.Model, "gemini-") || quota.RemainingPercent == nil {
			continue
		}
		if remaining == nil || *quota.RemainingPercent < *remaining {
			value := *quota.RemainingPercent
			remaining = &value
		}
	}
	return remaining
}

func geminiCPAAccountAvailable(account GeminiCPAAccount) bool {
	if account.RuntimeUnavailable || account.Error != "" {
		return false
	}
	for _, quota := range account.Models {
		if quota.RemainingPercent != nil && *quota.RemainingPercent > 0 {
			return true
		}
	}
	return false
}

// Model summaries are the average quota across observed accounts. Reset
// times remain per-account, because a pooled reset time would be misleading.
func summarizeGeminiCPAQuota(summary *GeminiCPAQuotaBalance, relevant []string) {
	summary.AccountCount = len(summary.Accounts)
	primaryTotal, primaryCount := 0.0, 0
	for _, account := range summary.Accounts {
		if geminiCPAAccountAvailable(account) {
			summary.AvailableAccountCount++
		}
		if account.Error != "" {
			summary.Partial = true
		}
		if account.RemainingPercent != nil {
			primaryTotal += *account.RemainingPercent
			primaryCount++
		}
	}
	if primaryCount > 0 {
		value := primaryTotal / float64(primaryCount)
		summary.RemainingPercent = &value
	}
	for _, name := range relevant {
		total, count := 0.0, 0
		for _, account := range summary.Accounts {
			for _, quota := range account.Models {
				if quota.Model == name && quota.RemainingPercent != nil {
					total += *quota.RemainingPercent
					count++
				}
			}
		}
		quota := GeminiCPAModelQuota{Model: name}
		if count > 0 {
			value := total / float64(count)
			quota.RemainingPercent = &value
		}
		summary.Models = append(summary.Models, quota)
	}
}

type geminiCPAManagementClient struct {
	baseURL string
	key     string
	client  *http.Client
}

func newGeminiCPAManagementClient() (*geminiCPAManagementClient, error) {
	baseURL := strings.TrimRight(firstNonEmptyEnv("GEMINI_CPA_MANAGEMENT_BASE_URL", "http://cliproxy-api:8317"), "/")
	keyPath := firstNonEmptyEnv("GEMINI_CPA_MANAGEMENT_KEY_FILE", "/data/ops/gemini-cpa-management-key")
	key, err := os.ReadFile(keyPath)
	if err != nil || len(bytes.TrimSpace(key)) == 0 {
		return nil, fmt.Errorf("Gemini CPA management key is unavailable")
	}
	return &geminiCPAManagementClient{
		baseURL: baseURL, key: strings.TrimSpace(string(key)),
		client: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (client *geminiCPAManagementClient) request(ctx context.Context, path string, payload any, result any) error {
	method := http.MethodGet
	var raw []byte
	if payload != nil {
		var err error
		raw, err = common.Marshal(payload)
		if err != nil {
			return fmt.Errorf("Gemini CPA management payload is invalid")
		}
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("Gemini CPA management URL is invalid")
	}
	req.Header.Set("X-Management-Key", client.key)
	req.Header.Set("Authorization", "Bearer "+client.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.client.Do(req)
	if err != nil {
		return fmt.Errorf("Gemini CPA management request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Gemini CPA management HTTP %d", resp.StatusCode)
	}
	if common.DecodeJson(io.LimitReader(resp.Body, 2<<20), result) != nil {
		return fmt.Errorf("Gemini CPA management response is invalid")
	}
	return nil
}

func (client *geminiCPAManagementClient) apiCall(ctx context.Context, authIndex, action string, body any) ([]byte, error) {
	raw, err := common.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("Gemini CPA quota payload is invalid")
	}
	payload := map[string]any{
		"auth_index": authIndex, "method": http.MethodPost,
		"url": "https://cloudcode-pa.googleapis.com/v1internal:" + action,
		"header": map[string]string{
			"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json",
			"User-Agent": "antigravity/cli/1.0.8 darwin/arm64",
		},
		"data": string(raw),
	}
	var response struct {
		StatusCode int    `json:"status_code"`
		Body       string `json:"body"`
	}
	if err := client.request(ctx, "/v0/management/api-call", payload, &response); err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Gemini CPA quota %s HTTP %d", action, response.StatusCode)
	}
	return []byte(response.Body), nil
}

func (client *geminiCPAManagementClient) fetchAccount(ctx context.Context, index string, relevant []string, account *GeminiCPAAccount) error {
	loadBody, err := client.apiCall(ctx, index, "loadCodeAssist", map[string]any{
		"metadata": map[string]string{"ideType": "ANTIGRAVITY"},
	})
	if err != nil {
		return err
	}
	var load struct {
		Project  any `json:"cloudaicompanionProject"`
		PaidTier struct {
			ID string `json:"id"`
		} `json:"paidTier"`
		CurrentTier struct {
			ID string `json:"id"`
		} `json:"currentTier"`
	}
	if common.Unmarshal(loadBody, &load) != nil {
		return fmt.Errorf("Gemini CPA subscription response is invalid")
	}
	project, _ := load.Project.(string)
	if object, ok := load.Project.(map[string]any); ok {
		project, _ = object["id"].(string)
	}
	if strings.TrimSpace(project) == "" {
		return fmt.Errorf("Gemini CPA subscription project is unavailable")
	}
	account.Tier = load.PaidTier.ID
	if account.Tier == "" {
		account.Tier = load.CurrentTier.ID
	}
	modelBody, err := client.apiCall(ctx, index, "fetchAvailableModels", map[string]string{"project": project})
	if err != nil {
		return err
	}
	account.Models, err = parseGeminiCPAModelQuotas(modelBody, relevant)
	if err == nil {
		account.RemainingPercent = geminiCPAAccountRemaining(account.Models)
	}
	return err
}

func (client *geminiCPAManagementClient) fetch(ctx context.Context, channel *model.Channel) (GeminiCPAQuotaBalance, error) {
	summary := GeminiCPAQuotaBalance{Source: geminiCPAQuotaInfoKey, UpdatedAt: common.GetTimestamp()}
	var inventory struct {
		Files []struct {
			Provider    string `json:"provider"`
			AuthIndex   string `json:"auth_index"`
			Email       string `json:"email"`
			Disabled    bool   `json:"disabled"`
			Status      string `json:"status"`
			Unavailable bool   `json:"unavailable"`
		} `json:"files"`
	}
	if err := client.request(ctx, "/v0/management/auth-files", nil, &inventory); err != nil {
		return summary, err
	}
	relevant := geminiCPARelevantModels(channel)
	accounts := make(map[string]GeminiCPAAccount)
	succeeded := 0
	for _, entry := range inventory.Files {
		if !strings.EqualFold(entry.Provider, "antigravity") || entry.Disabled || strings.EqualFold(entry.Status, "disabled") {
			continue
		}
		summary.CredentialCount++
		if strings.TrimSpace(entry.AuthIndex) == "" {
			summary.Partial = true
			continue
		}
		id := geminiCPAAccountID(entry.Email, entry.AuthIndex)
		if previous, exists := accounts[id]; exists && previous.Error == "" {
			continue
		}
		account := GeminiCPAAccount{ID: id, RuntimeUnavailable: entry.Unavailable}
		if err := client.fetchAccount(ctx, entry.AuthIndex, relevant, &account); err != nil {
			account.Error = err.Error()
		} else {
			succeeded++
		}
		accounts[id] = account
	}
	for _, account := range accounts {
		summary.Accounts = append(summary.Accounts, account)
	}
	sort.Slice(summary.Accounts, func(i, j int) bool { return summary.Accounts[i].ID < summary.Accounts[j].ID })
	summarizeGeminiCPAQuota(&summary, relevant)
	summary.UpdatedAt = common.GetTimestamp()
	if succeeded == 0 {
		return summary, fmt.Errorf("Gemini CPA quota is unavailable")
	}
	return summary, nil
}

func saveGeminiCPAQuotaSnapshot(channel *model.Channel, summary GeminiCPAQuotaBalance) error {
	// Reload before merging so a manual refresh using a cached channel does not
	// overwrite unrelated details added by another guard or administrator.
	info := channel.GetOtherInfo()
	if model.DB != nil {
		var latest model.Channel
		if err := model.DB.Select("id", "other_info").First(&latest, channel.Id).Error; err != nil {
			return err
		}
		info = latest.GetOtherInfo()
	}
	info[geminiCPAQuotaInfoKey] = summary
	raw, err := common.Marshal(info)
	if err != nil {
		return err
	}
	if model.DB != nil {
		if err := model.DB.Model(&model.Channel{}).Where("id = ?", channel.Id).Updates(map[string]any{
			"other_info": string(raw), "balance_updated_time": summary.UpdatedAt,
		}).Error; err != nil {
			return err
		}
	}
	// Do not convert a subscription percentage into Channel.Balance (USD).
	channel.OtherInfo = string(raw)
	channel.BalanceUpdatedTime = summary.UpdatedAt
	return nil
}

func UpdateGeminiCPAQuotaBalance(ctx context.Context, channel *model.Channel) (float64, error) {
	if !IsGeminiCPAChannel(channel) {
		return 0, fmt.Errorf("channel is not a Gemini CPA channel")
	}
	value, err, _ := geminiCPAQuotaRefresh.Do(strconv.Itoa(channel.Id), func() (any, error) {
		// CacheGetChannel may return a shared cached pointer. The refresh writes
		// its snapshot to the database without mutating that pointer concurrently.
		workingChannel := *channel
		channel = &workingChannel
		ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		summary := GeminiCPAQuotaBalance{Source: geminiCPAQuotaInfoKey, UpdatedAt: common.GetTimestamp()}
		client, err := newGeminiCPAManagementClient()
		if err == nil {
			summary, err = client.fetch(ctx, channel)
		}
		if err != nil {
			summary.Partial = true
			summary.Error = err.Error()
		}
		if saveErr := saveGeminiCPAQuotaSnapshot(channel, summary); saveErr != nil {
			return 0.0, saveErr
		}
		if summary.RemainingPercent == nil {
			if err == nil {
				err = fmt.Errorf("Gemini CPA quota is unavailable")
			}
			return 0.0, err
		}
		return *summary.RemainingPercent, err
	})
	if value == nil {
		return 0, err
	}
	return value.(float64), err
}
