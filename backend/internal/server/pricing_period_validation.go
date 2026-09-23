package server

import (
	"strings"
	"time"
)

func validatePricingSchedule(periods []ModelPricingPeriod) error {
	for i, p := range periods {
		seen := map[int]bool{}
		for _, day := range p.Weekdays {
			if day < 0 || day > 6 || seen[day] {
				return invalidModelPricingPeriod(i, "weekdays must contain unique integers from 0 (Sunday) to 6 (Saturday)")
			}
			seen[day] = true
		}
		from, _ := time.Parse(time.RFC3339, strings.TrimSpace(p.EffectiveFrom))
		until, _ := time.Parse(time.RFC3339, strings.TrimSpace(p.EffectiveUntil))
		if !from.IsZero() && !until.IsZero() && !from.Before(until) {
			return invalidModelPricingPeriod(i, "effective_from must precede effective_until")
		}
		for j := 0; j < i; j++ {
			other := periods[j]
			if !pricingEffectiveRangesOverlap(p, other) {
				continue
			}
			zone := func(v string) string {
				v = strings.TrimSpace(v)
				if v == "" {
					return "UTC"
				}
				return v
			}
			// A single timezone makes weekly overlap checks independent of DST changes.
			if zone(p.Timezone) != zone(other.Timezone) {
				return invalidModelPricingPeriod(i, "periods with overlapping effective ranges must use the same timezone")
			}
			left, right := pricingWeeklyMinutes(p), pricingWeeklyMinutes(other)
			for minute := range left {
				if left[minute] && right[minute] {
					return invalidModelPricingPeriod(i, "pricing windows must not overlap")
				}
			}
		}
	}
	return nil
}

func pricingEffectiveRangesOverlap(a, b ModelPricingPeriod) bool {
	af, _ := time.Parse(time.RFC3339, strings.TrimSpace(a.EffectiveFrom))
	au, _ := time.Parse(time.RFC3339, strings.TrimSpace(a.EffectiveUntil))
	bf, _ := time.Parse(time.RFC3339, strings.TrimSpace(b.EffectiveFrom))
	bu, _ := time.Parse(time.RFC3339, strings.TrimSpace(b.EffectiveUntil))
	return (au.IsZero() || bf.IsZero() || bf.Before(au)) && (bu.IsZero() || af.IsZero() || af.Before(bu))
}

func pricingWeeklyMinutes(p ModelPricingPeriod) [7 * 1440]bool {
	var minutes [7 * 1440]bool
	start, hasStart := parsePricingClock(p.StartTime)
	end, _ := parsePricingClock(p.EndTime)
	duration := end - start
	if !hasStart || duration == 0 {
		start = 0
		duration = 1440
	} else if duration < 0 {
		duration += 1440
	}
	for day := 0; day < 7; day++ {
		if !pricingWeekdayMatches(p.Weekdays, day) {
			continue
		}
		for offset := 0; offset < duration; offset++ {
			minutes[(day*1440+start+offset)%len(minutes)] = true
		}
	}
	return minutes
}
