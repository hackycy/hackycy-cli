package pulse

import (
	"testing"
	"time"
)

func TestSinceBoundaryUsesInclusiveLocalDayPresets(t *testing.T) {
	location := time.FixedZone("UTC+8", 8*60*60)
	now := time.Date(2026, time.August, 23, 18, 42, 7, 0, location)

	testCases := []struct {
		days int
		want string
	}{
		{days: 1, want: "2026-08-23 00:00:00"},
		{days: 2, want: "2026-08-22 00:00:00"},
		{days: 3, want: "2026-08-21 00:00:00"},
		{days: 7, want: "2026-08-17 00:00:00"},
		{days: 30, want: "2026-07-25 00:00:00"},
	}
	for _, testCase := range testCases {
		t.Run(time.Duration(testCase.days).String(), func(t *testing.T) {
			if got := SinceBoundary(now, testCase.days); got != testCase.want {
				t.Fatalf("SinceBoundary(%d) = %q, want %q", testCase.days, got, testCase.want)
			}
		})
	}
}

func TestSinceBoundaryPreservesLegacyZeroAndNegativeDays(t *testing.T) {
	now := time.Date(2026, time.August, 23, 18, 42, 7, 0, time.UTC)
	if got, want := SinceBoundary(now, 0), "2026-08-24 00:00:00"; got != want {
		t.Fatalf("zero-day boundary = %q, want %q", got, want)
	}
	if got, want := SinceBoundary(now, -1), "2026-08-25 00:00:00"; got != want {
		t.Fatalf("negative-day boundary = %q, want %q", got, want)
	}
}

func TestSinceBoundaryUsesCalendarDaysAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	now := time.Date(2026, time.March, 9, 10, 0, 0, 0, location)
	if got, want := SinceBoundary(now, 2), "2026-03-08 00:00:00"; got != want {
		t.Fatalf("DST boundary = %q, want %q", got, want)
	}
}
