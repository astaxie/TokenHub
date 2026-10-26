package server

import "time"

// A date without a vendor timezone is definitely past only after its final
// possible civil day (UTC-12) has ended. This is a publication guard, not a
// fabricated official shutdown instant. Existing routes remain unaffected.
const catalogCalendarDateGrace = 36 * time.Hour

func catalogShutdownReached(metadata map[string]string, now time.Time) bool {
	if shutdown, err := time.Parse(time.RFC3339, metadata["shutdown_at"]); err == nil {
		return !now.Before(shutdown)
	}
	date, err := time.Parse("2006-01-02", metadata["shutdown_date"])
	return err == nil && !now.Before(date.Add(catalogCalendarDateGrace))
}
