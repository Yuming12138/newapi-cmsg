package billingexpr

import "time"

// DeepSeek's peak window is Monday-Friday 09:00-12:00 and 14:00-18:00
// Beijing time, excluding Chinese public holidays. The 2026 holiday ranges
// come from https://www.gov.cn/zhengce/zhengceku/202511/content_7047091.htm.
// Until a future year's official calendar is published and added here, use
// the lower off-peak rate for that year so an unknown holiday cannot be
// overcharged.
func deepSeekPeakTime(at time.Time) bool {
	local := at.In(time.FixedZone("CST", 8*60*60))
	if local.Year() != 2026 {
		return false
	}
	if local.Weekday() == time.Saturday || local.Weekday() == time.Sunday || deepSeek2026Holiday(local) {
		return false
	}
	hour := local.Hour()
	return (hour >= 9 && hour < 12) || (hour >= 14 && hour < 18)
}

func deepSeek2026Holiday(local time.Time) bool {
	day := local.Day()
	switch local.Month() {
	case time.January:
		return day >= 1 && day <= 3
	case time.February:
		return day >= 15 && day <= 23
	case time.April:
		return day >= 4 && day <= 6
	case time.May:
		return day >= 1 && day <= 5
	case time.June:
		return day >= 19 && day <= 21
	case time.September:
		return day >= 25 && day <= 27
	case time.October:
		return day >= 1 && day <= 7
	default:
		return false
	}
}
