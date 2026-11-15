package allowance

import "time"

// CalendarRefill is Magpie's Refill: local midnight on an ISO weekday or a
// month day, clamped to the month's last day. See key_balance_own.go at 47b51c8.
type CalendarRefill struct {
	Every string `json:"every"`
	Day   int    `json:"day"`
}

func (r CalendarRefill) Valid() bool {
	return r.Every == "month" && r.Day >= 1 && r.Day <= 31 || r.Every == "week" && r.Day >= 1 && r.Day <= 7
}

func (r CalendarRefill) Last(now time.Time) time.Time {
	y, m, d := now.Date()
	if r.Every == "week" {
		iso := (int(now.Weekday())+6)%7 + 1
		return time.Date(y, m, d-(iso-r.Day+7)%7, 0, 0, 0, 0, now.Location())
	}
	at := calendarMonthDay(y, m, r.Day, now.Location())
	if at.After(now) {
		at = calendarMonthDay(y, m-1, r.Day, now.Location())
	}
	return at
}

func (r CalendarRefill) Next(now time.Time) time.Time {
	at := r.Last(now)
	y, m, d := at.Date()
	if r.Every == "week" {
		return time.Date(y, m, d+7, 0, 0, 0, 0, at.Location())
	}
	return calendarMonthDay(y, m+1, r.Day, at.Location())
}

func calendarMonthDay(year int, month time.Month, day int, loc *time.Location) time.Time {
	last := time.Date(year, month+1, 0, 0, 0, 0, 0, loc).Day()
	return time.Date(year, month, min(day, last), 0, 0, 0, 0, loc)
}

func walletLocation(zone string) *time.Location {
	if zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	return time.UTC
}

// GrantExpiry keeps Magpie's inclusive expiry date, ending at next midnight.
func GrantExpiry(g Grant, zone string) *time.Time {
	if g.ExpiresAt != nil {
		return g.ExpiresAt
	}
	at, err := time.ParseInLocation(time.DateOnly, g.Until, walletLocation(zone))
	if err != nil {
		return nil
	}
	at = at.AddDate(0, 0, 1)
	return &at
}

// SpendCap uses the native day/month/total values and preserves total's origin.
type SpendCap struct {
	USD      float64   `json:"usd"`
	Period   string    `json:"period"`
	Since    time.Time `json:"since,omitzero"`
	Timezone string    `json:"timezone,omitempty"`
}

func (c SpendCap) Window(now time.Time) (time.Time, time.Time) {
	now = now.In(walletLocation(c.Timezone))
	y, m, d := now.Date()
	switch c.Period {
	case "month":
		return time.Date(y, m, 1, 0, 0, 0, 0, now.Location()), time.Date(y, m+1, 1, 0, 0, 0, 0, now.Location())
	case "total":
		return c.Since, time.Time{}
	default:
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
	}
}
