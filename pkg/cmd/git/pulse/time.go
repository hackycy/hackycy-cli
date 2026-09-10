package pulse

import "time"

const gitDateLayout = "2006-01-02 15:04:05"

// SinceBoundary returns the legacy local start-of-day boundary for a day count.
// Zero and negative values remain representable because the legacy CLI accepts them.
func SinceBoundary(now time.Time, days int) string {
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return startOfDay.AddDate(0, 0, 1-days).Format(gitDateLayout)
}
