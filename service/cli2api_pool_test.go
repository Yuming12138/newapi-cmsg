package service

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

func seedCli2APIReferencePrice(t *testing.T) {
	t.Helper()
	// Production reads these from options; seed the same values explicitly.
	if err := ratio_setting.UpdateModelRatioByJSONString(`{"deepseek-flash":0.0684931506849315}`); err != nil {
		t.Fatalf("seed model ratio: %v", err)
	}
	if err := ratio_setting.UpdateCompletionRatioByJSONString(`{"deepseek-flash":4}`); err != nil {
		t.Fatalf("seed completion ratio: %v", err)
	}
}

func TestCli2APICreditUSDPerCreditUsesReferenceModelPrice(t *testing.T) {
	seedCli2APIReferencePrice(t)
	cfg := &operation_setting.C2ACreditPoolSetting{
		Enabled:             true,
		ReferenceModel:      "deepseek-flash",
		CreditsPerKiloToken: 0.03,
		CompletionShare:     0.1,
	}
	perCredit := cli2APICreditUSDPerCredit(cfg)
	if perCredit <= 0 {
		t.Fatalf("expected a positive USD per credit, got %v", perCredit)
	}
	// All-input floor: tokens_per_credit(33333.3) * $0.136986/1M tokens
	floor := (1000.0 / 0.03) * 0.0684931506849315 * 0.002 / 1000.0
	if perCredit < floor {
		t.Fatalf("blended %v must not drop below the all-input rate %v", perCredit, floor)
	}
	// completion_ratio=4 with 10%% output share => 1.3x the input rate
	if got, want := perCredit, floor*1.3; math.Abs(got-want) > want*0.01 {
		t.Fatalf("expected ~%v (1.3x input), got %v", want, got)
	}
}

func TestCli2APIDailyQuotaPoolGroupFoldsAdvancedCustomChannels(t *testing.T) {
	seedCli2APIReferencePrice(t)
	var enabled int = common.ChannelStatusEnabled
	channels := []*model.Channel{
		{Id: 36, Type: constant.ChannelTypeAdvancedCustom, Status: enabled, Balance: 13271, BalanceUpdatedTime: 1793091518},
		{Id: 35, Type: constant.ChannelTypeOpenAI, Status: enabled, Balance: 36.619, BalanceUpdatedTime: 1793091518},
	}
	group, handled := cli2apiDailyQuotaPoolGroup(channels)
	if !handled {
		t.Fatalf("expected the cli2api group to be handled")
	}
	if group.ChannelCount != 1 {
		t.Fatalf("expected exactly one Advanced Custom channel, got %d", group.ChannelCount)
	}
	if group.Source != "cli2api_credits" || group.Group != "c2a" {
		t.Fatalf("unexpected source/group: %s/%s", group.Source, group.Group)
	}
	if !group.Estimated {
		t.Fatalf("the group is an estimate and must be flagged as such")
	}
	if group.RemainingUSD <= 0 || group.RemainingUSD > 13271 {
		// credits must be converted into dollars, never left as credits
		t.Fatalf("implausible remaining USD %v for 13271 credits", group.RemainingUSD)
	}
	if !group.Available {
		t.Fatalf("credits above zero must mark the group available")
	}
}
