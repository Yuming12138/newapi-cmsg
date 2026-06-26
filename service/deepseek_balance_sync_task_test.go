package service

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

func TestNormalizeDeepSeekBalanceURL(t *testing.T) {
	got := normalizeDeepSeekBalanceURL("  HTTPS://API.DEEPSEEK.COM/ANTHROPIC/  ")
	want := "https://api.deepseek.com/anthropic"
	if got != want {
		t.Fatalf("normalizeDeepSeekBalanceURL() = %q, want %q", got, want)
	}
}

func TestChannelHasGroup(t *testing.T) {
	if !channelHasGroup("default, deepseek, test", "deepseek") {
		t.Fatalf("channelHasGroup() should match deepseek token")
	}
	if channelHasGroup("default, test", "deepseek") {
		t.Fatalf("channelHasGroup() should not match absent token")
	}
}

func TestIsDeepSeekBalanceChannel(t *testing.T) {
	baseURL := "https://api.deepseek.com/anthropic/"
	channel := &model.Channel{
		Type:    constant.ChannelTypeAnthropic,
		BaseURL: &baseURL,
		Group:   "default, deepseek-claude",
	}
	cfg := deepSeekBalanceRuntimeSetting{
		ChannelType: constant.ChannelTypeAnthropic,
		BaseURL:     deepSeekBalanceDefaultBaseURL,
		Group:       deepSeekBalanceDefaultGroup,
	}
	if !isDeepSeekBalanceChannel(channel, cfg) {
		t.Fatalf("isDeepSeekBalanceChannel() should match deepseek anthropic channel")
	}
}

func TestParseDeepSeekBalanceResponse(t *testing.T) {
	raw := []byte(`{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"1.5"},{"currency":"CNY","total_balance":"193.7"}]}`)
	got, err := parseDeepSeekBalanceResponse(raw)
	if err != nil {
		t.Fatalf("parseDeepSeekBalanceResponse() error = %v", err)
	}
	if got != 193.7 {
		t.Fatalf("parseDeepSeekBalanceResponse() = %v, want 193.7", got)
	}
}

func TestParseDeepSeekBalanceResponseUnavailable(t *testing.T) {
	raw := []byte(`{"is_available":false,"balance_infos":[{"currency":"CNY","total_balance":"193.7"}]}`)
	if _, err := parseDeepSeekBalanceResponse(raw); err == nil {
		t.Fatalf("parseDeepSeekBalanceResponse() expected unavailable error")
	}
}
