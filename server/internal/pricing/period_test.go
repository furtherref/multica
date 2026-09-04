package pricing

import (
	"testing"
	"time"
)

func TestPeriodStartIsUTCCalendarBoundary(t *testing.T) {
	// Wednesday 2026-09-02 15:04:05 UTC.
	now := time.Date(2026, 9, 2, 15, 4, 5, 0, time.UTC)
	cases := []struct {
		period    Period
		wantStart time.Time
		wantNext  time.Time
	}{
		{PeriodDaily, time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)},
		{PeriodWeekly, time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)},
		{PeriodMonthly, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		if got := PeriodStart(now, tc.period); !got.Equal(tc.wantStart) {
			t.Errorf("PeriodStart(%s) = %s, want %s", tc.period, got, tc.wantStart)
		}
		if got := NextPeriodStart(now, tc.period); !got.Equal(tc.wantNext) {
			t.Errorf("NextPeriodStart(%s) = %s, want %s", tc.period, got, tc.wantNext)
		}
	}
}

func TestPeriodStartIgnoresCallerLocation(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*3600)
	// 2026-09-03 06:00 in Shanghai is 2026-09-02 22:00 UTC: the UTC day is
	// still the 2nd, so the daily window must not have rolled over.
	now := time.Date(2026, 9, 3, 6, 0, 0, 0, shanghai)
	if got := PeriodStart(now, PeriodDaily); !got.Equal(time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily start = %s, want 2026-09-02T00:00Z", got)
	}
}

func TestWeeklyPeriodStartsOnMondayAcrossMonthAndYearEnds(t *testing.T) {
	// Sunday 2027-01-03: the week began Monday 2026-12-28.
	now := time.Date(2027, 1, 3, 12, 0, 0, 0, time.UTC)
	if got := PeriodStart(now, PeriodWeekly); !got.Equal(time.Date(2026, 12, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekly start = %s, want 2026-12-28", got)
	}
	// Monday itself starts its own week.
	monday := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	if got := PeriodStart(monday, PeriodWeekly); !got.Equal(monday) {
		t.Fatalf("weekly start on a Monday = %s, want the same day", got)
	}
}

func TestMonthlyNextPeriodHandlesDecember(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	if got := NextPeriodStart(now, PeriodMonthly); !got.Equal(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("next monthly = %s, want 2027-01-01", got)
	}
}

func TestParsePeriod(t *testing.T) {
	for _, s := range []string{"daily", "weekly", "monthly"} {
		if p, ok := ParsePeriod(s); !ok || string(p) != s {
			t.Fatalf("ParsePeriod(%q) = %q, %v", s, p, ok)
		}
	}
	if _, ok := ParsePeriod("yearly"); ok {
		t.Fatal("ParsePeriod accepted an unknown period")
	}
}
