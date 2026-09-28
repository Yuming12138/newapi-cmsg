package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func TestSummarizeDashboardProviderBalances(t *testing.T) {
	kimiURL := "https://api.moonshot.cn"
	deepSeekURL := "https://api.deepseek.com"
	asxsURL := "https://api.itygk.sbs"
	channels := []*model.Channel{
		{Id: 1, Type: constant.ChannelTypeOpenAI, Key: "asxs-key", BaseURL: &asxsURL, Group: "asxs", Status: common.ChannelStatusEnabled, Balance: 50, BalanceUpdatedTime: 150},
		{Id: 33, Type: constant.ChannelTypeOpenAI, Key: "kimi-key", BaseURL: &kimiURL, Group: "kimi,asxs", Status: common.ChannelStatusEnabled, Balance: 24.98 / ratio_setting.USD2RMB, BalanceUpdatedTime: 200},
		{Id: 34, Type: constant.ChannelTypeOpenAI, Key: "kimi-key", BaseURL: &kimiURL, Group: "kimi", Status: common.ChannelStatusEnabled, Balance: 20 / ratio_setting.USD2RMB, BalanceUpdatedTime: 100},
		{Id: 11, Type: constant.ChannelTypeDeepSeek, Key: "deepseek-key", BaseURL: &deepSeekURL, Group: "deepseek", Status: common.ChannelStatusEnabled, Balance: 6.10 / ratio_setting.USD2RMB, BalanceUpdatedTime: 210},
	}

	got := summarizeDashboardProviderBalances(channels)
	if got.Kimi.Balance == nil || math.Abs(*got.Kimi.Balance-24.98) > 0.001 || got.Kimi.ChannelCount != 2 || got.Kimi.AccountCount != 1 || got.Kimi.UpdatedAt != 200 {
		t.Fatalf("Kimi balance summary = %+v, want one distinct account with CNY 24.98", got.Kimi)
	}
	if got.DeepSeek.Balance == nil || math.Abs(*got.DeepSeek.Balance-6.10) > 0.001 || got.DeepSeek.AccountCount != 1 {
		t.Fatalf("DeepSeek balance summary = %+v, want CNY 6.10", got.DeepSeek)
	}
}

func TestSummarizeProviderAccountBalanceDistinguishesMissingFromZero(t *testing.T) {
	kimiURL := "https://api.moonshot.cn"
	channel := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "kimi-key", BaseURL: &kimiURL, Status: common.ChannelStatusEnabled}
	missing := summarizeProviderAccountBalance([]*model.Channel{channel}, isOfficialKimiChannel)
	if missing.Balance != nil || !missing.Partial {
		t.Fatalf("missing balance = %+v, want unavailable and partial", missing)
	}
	channel.BalanceUpdatedTime = 100
	zero := summarizeProviderAccountBalance([]*model.Channel{channel}, isOfficialKimiChannel)
	if zero.Balance == nil || *zero.Balance != 0 || zero.Partial {
		t.Fatalf("zero balance = %+v, want available CNY zero", zero)
	}
	second := &model.Channel{Type: constant.ChannelTypeOpenAI, Key: "other-kimi-key", BaseURL: &kimiURL, Status: common.ChannelStatusEnabled}
	partial := summarizeProviderAccountBalance([]*model.Channel{channel, second}, isOfficialKimiChannel)
	if partial.Balance != nil || !partial.Partial || partial.AccountCount != 2 {
		t.Fatalf("partial balance = %+v, want no misleading total", partial)
	}
}
