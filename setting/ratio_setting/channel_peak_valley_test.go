package ratio_setting

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

const peakValleyTestConfig = `{
  "27": [
    {"from": "23:00", "to": "08:00", "ratio": 0.25},
    {"from": "08:00", "to": "14:00", "ratio": 0.55},
    {"from": "14:00", "to": "18:00", "ratio": 0.50},
    {"from": "18:00", "to": "21:00", "ratio": 1.00},
    {"from": "21:00", "to": "23:00", "ratio": 0.55}
  ]
}`

func withPeakValleyOption(t *testing.T, value string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previous, existed := common.OptionMap[ChannelPeakValleyOptionKey]
	common.OptionMap[ChannelPeakValleyOptionKey] = value
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[ChannelPeakValleyOptionKey] = previous
		} else {
			delete(common.OptionMap, ChannelPeakValleyOptionKey)
		}
	})
}

func TestChannelPeakValleyRatioSchedule(t *testing.T) {
	withPeakValleyOption(t, peakValleyTestConfig)

	// Times are authored in Beijing time; the helper converts `now` internally.
	tests := []struct {
		beijing string
		want    float64
	}{
		{"2026-11-04T00:00:00", 0.25},
		{"2026-11-04T07:59:00", 0.25},
		{"2026-11-04T08:00:00", 0.55},
		{"2026-11-04T13:59:00", 0.55},
		{"2026-11-04T14:00:00", 0.50},
		{"2026-11-04T17:59:00", 0.50},
		{"2026-11-04T18:00:00", 1.00},
		{"2026-11-04T20:59:00", 1.00},
		{"2026-11-04T21:00:00", 0.55},
		{"2026-11-04T22:59:00", 0.55},
		{"2026-11-04T23:00:00", 0.25},
		{"2026-11-04T23:59:00", 0.25},
	}
	for _, tc := range tests {
		local, err := time.ParseInLocation("2006-01-02T15:04:05", tc.beijing, beijingZone)
		if err != nil {
			t.Fatalf("parse %s: %v", tc.beijing, err)
		}
		if got := GetChannelPeakValleyRatio(27, local); got != tc.want {
			t.Errorf("beijing %s: got %v, want %v", tc.beijing, got, tc.want)
		}
	}
}

func TestChannelPeakValleyRatioDefaultsToOne(t *testing.T) {
	withPeakValleyOption(t, peakValleyTestConfig)

	now := time.Date(2026, 11, 4, 10, 43, 0, 0, time.UTC)
	if got := GetChannelPeakValleyRatio(999, now); got != 1 {
		t.Fatalf("unconfigured channel: got %v, want 1", got)
	}
	if got := GetChannelPeakValleyRatio(0, now); got != 1 {
		t.Fatalf("zero channel: got %v, want 1", got)
	}

	common.OptionMapRWMutex.Lock()
	common.OptionMap[ChannelPeakValleyOptionKey] = "{not json"
	common.OptionMapRWMutex.Unlock()
	if got := GetChannelPeakValleyRatio(27, now); got != 1 {
		t.Fatalf("malformed config: got %v, want 1", got)
	}
}
