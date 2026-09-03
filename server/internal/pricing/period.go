package pricing

import "time"

// Period is one budget window. Boundaries are always computed in UTC, whatever
// the caller's location: a day is the UTC calendar day, a week starts Monday
// 00:00 UTC and a month starts on the 1st at 00:00 UTC.
type Period string

const (
	PeriodDaily   Period = "daily"
	PeriodWeekly  Period = "weekly"
	PeriodMonthly Period = "monthly"
)

// AllPeriods lists the periods in display order.
var AllPeriods = []Period{PeriodDaily, PeriodWeekly, PeriodMonthly}

// ParsePeriod accepts the wire spelling of a period.
func ParsePeriod(s string) (Period, bool) {
	switch Period(s) {
	case PeriodDaily, PeriodWeekly, PeriodMonthly:
		return Period(s), true
	default:
		return "", false
	}
}

// PeriodStart returns the UTC start of the period containing now.
func PeriodStart(now time.Time, p Period) time.Time {
	u := now.UTC()
	y, m, d := u.Date()
	switch p {
	case PeriodWeekly:
		// time.Weekday counts Sunday as 0; shift so Monday is day 0.
		daysSinceMonday := (int(u.Weekday()) + 6) % 7
		return time.Date(y, m, d-daysSinceMonday, 0, 0, 0, 0, time.UTC)
	case PeriodMonthly:
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	default:
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
}

// NextPeriodStart returns the UTC instant the current period resets.
func NextPeriodStart(now time.Time, p Period) time.Time {
	start := PeriodStart(now, p)
	switch p {
	case PeriodWeekly:
		return start.AddDate(0, 0, 7)
	case PeriodMonthly:
		return start.AddDate(0, 1, 0)
	default:
		return start.AddDate(0, 0, 1)
	}
}
