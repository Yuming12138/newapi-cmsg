package ratio_setting

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// ChannelPeakValleyOptionKey stores the channel-level peak/valley schedule.
//
// Value format (channel id -> slots, times are Beijing time HH:MM):
//
//	{
//	  "27": [
//	    {"from": "23:00", "to": "08:00", "ratio": 0.25},
//	    {"from": "08:00", "to": "14:00", "ratio": 0.55}
//	  ]
//	}
//
// A slot whose "from" is later than "to" wraps over midnight. The "to" bound
// is exclusive, so adjacent slots do not overlap. Channels without an entry,
// and malformed configuration, keep the multiplier at 1.
const ChannelPeakValleyOptionKey = "ChannelPeakValleyRatio"

type channelPeakValleySlot struct {
	From  string  `json:"from"`
	To    string  `json:"to"`
	Ratio float64 `json:"ratio"`
}

var beijingZone = time.FixedZone("CST", 8*60*60)

func parseHHMM(value string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// GetChannelPeakValleyRatio returns the multiplier to apply on top of the
// existing group/model ratios for a channel at the given instant. It is
// evaluated per request, so no scheduler is needed at time boundaries.
func GetChannelPeakValleyRatio(channelID int, now time.Time) float64 {
	if channelID <= 0 {
		return 1
	}
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[ChannelPeakValleyOptionKey]
	common.OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 1
	}

	var schedule map[string][]channelPeakValleySlot
	if err := json.Unmarshal([]byte(raw), &schedule); err != nil {
		return 1
	}
	slots := schedule[strconv.Itoa(channelID)]
	if len(slots) == 0 {
		return 1
	}

	local := now.In(beijingZone)
	minuteOfDay := local.Hour()*60 + local.Minute()
	for _, slot := range slots {
		from, fromOK := parseHHMM(slot.From)
		to, toOK := parseHHMM(slot.To)
		if !fromOK || !toOK || slot.Ratio < 0 || from == to {
			continue
		}
		matched := false
		if from < to {
			matched = minuteOfDay >= from && minuteOfDay < to
		} else {
			matched = minuteOfDay >= from || minuteOfDay < to
		}
		if matched {
			return slot.Ratio
		}
	}
	return 1
}
