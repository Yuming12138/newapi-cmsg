package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/go-redis/redis/v8"
)

const (
	kimiCPAHomeDefaultAddr       = "172.17.0.1:8327"
	kimiCPAHomeDefaultCertFile   = "/run/secrets/cliproxy_home/client-crt.pem"
	kimiCPAHomeDefaultKeyFile    = "/run/secrets/cliproxy_home/client-key.pem"
	kimiCPAHomeDefaultCAFile     = "/run/secrets/cliproxy_home/home-ca-crt.pem"
	kimiCPAHomeDefaultModel      = "kimi-k3"
	kimiCPAHomeRedisDialTimeout  = 5 * time.Second
	kimiCPAHomeRedisReadTimeout  = 15 * time.Second
	kimiCPAHomeRedisWriteTimeout = 15 * time.Second
)

type kimiCPAHomeClient struct {
	redis *redis.Client
}

func newKimiCPAHomeClient() (*kimiCPAHomeClient, error) {
	addr := firstNonEmptyEnv("KIMI_CPA_HOME_ADDR", kimiCPAHomeDefaultAddr)
	certFile := firstNonEmptyEnv("KIMI_CPA_HOME_CERT_FILE", kimiCPAHomeDefaultCertFile)
	keyFile := firstNonEmptyEnv("KIMI_CPA_HOME_KEY_FILE", kimiCPAHomeDefaultKeyFile)
	caFile := firstNonEmptyEnv("KIMI_CPA_HOME_CA_FILE", kimiCPAHomeDefaultCAFile)
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("Kimi CPA Home client certificate unavailable")
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("Kimi CPA Home CA certificate unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("Kimi CPA Home CA certificate is invalid")
	}
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		TLSConfig:    &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, RootCAs: pool, InsecureSkipVerify: true},
		DialTimeout:  kimiCPAHomeRedisDialTimeout,
		ReadTimeout:  kimiCPAHomeRedisReadTimeout,
		WriteTimeout: kimiCPAHomeRedisWriteTimeout,
		MaxRetries:   1,
	})
	return &kimiCPAHomeClient{redis: client}, nil
}

func firstNonEmptyEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func (client *kimiCPAHomeClient) Close() {
	if client != nil && client.redis != nil {
		_ = client.redis.Close()
	}
}

func (client *kimiCPAHomeClient) fetch(ctx context.Context, channel *model.Channel) (KimiSubscriptionBalance, error) {
	summary := KimiSubscriptionBalance{Source: kimiCPASubscriptionInfoKey, ChannelCount: 1}
	if client == nil || client.redis == nil {
		return summary, fmt.Errorf("Kimi CPA Home client is unavailable")
	}
	apiKey := strings.TrimSpace(channel.Key)
	if apiKey == "" {
		return summary, fmt.Errorf("Kimi CPA channel key is unavailable")
	}
	request := map[string]any{
		"type": "auth", "model": kimiCPAHomeDefaultModel, "count": 1,
		"concurrency_protocol": 1, "session_id": fmt.Sprintf("new-api-kimi-balance-%d", time.Now().UnixNano()),
		"headers": map[string]string{"accept": "application/json", "x-goog-api-key": apiKey},
	}
	requestKey, err := json.Marshal(request)
	if err != nil {
		return summary, err
	}
	raw, err := client.redis.WithContext(ctx).RPop(ctx, string(requestKey)).Bytes()
	if err != nil {
		return summary, fmt.Errorf("Kimi CPA Home auth dispatch failed")
	}
	var dispatch struct {
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Auth struct {
			Provider string         `json:"provider"`
			Metadata map[string]any `json:"metadata"`
		} `json:"auth"`
	}
	if err := common.Unmarshal(raw, &dispatch); err != nil {
		return summary, fmt.Errorf("invalid Kimi CPA Home auth response")
	}
	if dispatch.Error != nil {
		return summary, fmt.Errorf("Kimi CPA Home auth dispatch unavailable")
	}
	if !strings.EqualFold(strings.TrimSpace(dispatch.Auth.Provider), "kimi") {
		return summary, fmt.Errorf("Kimi CPA Home returned a non-Kimi credential")
	}
	accessToken, _ := dispatch.Auth.Metadata["access_token"].(string)
	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return summary, fmt.Errorf("Kimi CPA Home returned no access token")
	}
	accountID, err := kimiSubscriptionAccountID(accessToken)
	if err != nil {
		return summary, err
	}
	proxy := ""
	if channel != nil {
		proxy = channel.GetSetting().Proxy
	}
	httpClient, err := NewProxyHttpClient(proxy)
	if err != nil {
		return summary, err
	}
	usageBody, err := fetchKimiCPAUsage(ctx, httpClient, accessToken)
	if err != nil {
		return summary, err
	}
	windows, err := parseKimiSubscriptionUsage(usageBody)
	if err != nil {
		return summary, err
	}
	remaining := 100.0
	for _, window := range windows {
		remaining = min(remaining, window.RemainingPercent)
	}
	summary.CredentialCount = 1
	summary.AccountCount = 1
	if remaining > 0 {
		summary.AvailableAccountCount = 1
	}
	summary.RemainingPercent = &remaining
	summary.Windows = windows
	summary.Accounts = []KimiSubscriptionAccount{{ID: accountID, RemainingPercent: remaining, Windows: windows}}
	summary.UpdatedAt = common.GetTimestamp()
	return summary, nil
}

func fetchKimiCPAUsage(ctx context.Context, client *http.Client, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.kimi.com/coding/v1/usages", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Kimi subscription usage request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Kimi subscription usage HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	return body, nil
}
