package service

import (
	"encoding/base64"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

func TestParseKimiSubscriptionUsage(t *testing.T) {
	raw := []byte(`{
		"usages": {
			"limit_5h": {"used_ratio": 0.784292, "reset_time": "2026-10-02T08:59:40Z"},
			"limit_month_total": {"used_ratio": 0.0728, "reset_time": "2026-11-01T00:00:00Z"},
			"limit_month_code": {"used_ratio": 0.0728, "reset_time": "2026-11-01T00:00:00Z"}
		}
	}`)
	windows, err := parseKimiSubscriptionUsage(raw)
	if err != nil {
		t.Fatalf("parseKimiSubscriptionUsage() error = %v", err)
	}
	if len(windows) != 3 {
		t.Fatalf("parseKimiSubscriptionUsage() windows = %+v", windows)
	}
	if windows[0].Name != "5h" || windows[0].RemainingPercent < 21.5707 || windows[0].RemainingPercent > 21.5709 {
		t.Fatalf("5h window = %+v", windows[0])
	}
	if windows[1].Name != "month_total" || windows[1].RemainingPercent != 92.72 {
		t.Fatalf("monthly window = %+v", windows[1])
	}
}

func TestParseKimiSubscriptionUsageLimitsFallback(t *testing.T) {
	raw := []byte(`{
		"limits": [{
			"window": {"duration": 300, "timeUnit": "TIME_UNIT_MINUTE"},
			"detail": {"limit": "100", "remaining": "22", "resetTime": "2026-10-02T08:59:40.244313Z"}
		}]
	}`)
	windows, err := parseKimiSubscriptionUsage(raw)
	if err != nil {
		t.Fatalf("parseKimiSubscriptionUsage() error = %v", err)
	}
	if len(windows) != 1 || windows[0].Name != "5h" || windows[0].RemainingPercent != 22 {
		t.Fatalf("fallback window = %+v", windows)
	}
}

func TestKimiSubscriptionAccountIDDeduplicatesSameUser(t *testing.T) {
	first := testKimiJWT(t, "user-123")
	second := testKimiJWT(t, "user-123")
	third := testKimiJWT(t, "user-456")
	firstID, err := kimiSubscriptionAccountID(first)
	if err != nil {
		t.Fatalf("first account id error = %v", err)
	}
	secondID, err := kimiSubscriptionAccountID(second)
	if err != nil {
		t.Fatalf("second account id error = %v", err)
	}
	thirdID, err := kimiSubscriptionAccountID(third)
	if err != nil {
		t.Fatalf("third account id error = %v", err)
	}
	if firstID != secondID || firstID == thirdID {
		t.Fatalf("account ids = %q, %q, %q", firstID, secondID, thirdID)
	}
}

func TestIsKimiCPAChannelUsesModelMapping(t *testing.T) {
	mapping := `{"gpt-5.6-sol":"kimi-k3"}`
	baseURL := "http://cliproxy-api:8317"
	channel := &model.Channel{
		Type:         constant.ChannelTypeOpenAI,
		BaseURL:      &baseURL,
		ModelMapping: &mapping,
	}
	if !IsKimiCPAChannel(channel) {
		t.Fatal("IsKimiCPAChannel() = false, want true")
	}
}

func testKimiJWT(t *testing.T, userID string) string {
	t.Helper()
	encode := func(value any) string {
		raw, err := common.Marshal(value)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	return encode(map[string]string{"alg": "none"}) + "." + encode(map[string]string{"user_id": userID}) + ".signature"
}
