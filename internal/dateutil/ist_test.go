package dateutil

import (
	"testing"
	"time"
)

func TestISTDayRangeUsesIndianCalendarBoundaries(t *testing.T) {
	start, end, err := ISTDayRange("2026-08-01", "2026-08-31")
	if err != nil {
		t.Fatalf("ISTDayRange: %v", err)
	}

	wantStart := time.Date(2026, time.July, 31, 18, 30, 0, 0, time.UTC)
	wantEnd := time.Date(2026, time.August, 31, 18, 29, 59, int(time.Second-time.Nanosecond), time.UTC)
	if !start.Equal(wantStart) {
		t.Fatalf("start = %s, want %s", start, wantStart)
	}
	if !end.Equal(wantEnd) {
		t.Fatalf("end = %s, want %s", end, wantEnd)
	}

	augustFirst := time.Date(2026, time.July, 31, 18, 30, 0, 0, time.UTC)
	septemberFirst := time.Date(2026, time.August, 31, 18, 30, 0, 0, time.UTC)
	if augustFirst.Before(start) || augustFirst.After(end) {
		t.Fatal("August 1 IST must be included")
	}
	if !septemberFirst.After(end) {
		t.Fatal("September 1 IST must be excluded")
	}
}

func TestISTDayRangeRejectsInvalidDates(t *testing.T) {
	if _, _, err := ISTDayRange("bad", "2026-08-31"); err == nil {
		t.Fatal("expected invalid start date error")
	}
	if _, _, err := ISTDayRange("2026-08-01", "bad"); err == nil {
		t.Fatal("expected invalid end date error")
	}
}
