package server

import (
	"sort"
	"time"
)

// Legacy in-memory implementations retained as independent parity oracles.
func observationMonitoringSignal(now time.Time, source string, observations []ProviderObservation) ProviderMonitoringSignal {
	if len(observations) == 0 {
		return ProviderMonitoringSignal{State: "unknown", Source: source, Detail: "no_samples"}
	}
	sort.Slice(observations, func(i, j int) bool { return observations[i].ObservedAt.After(observations[j].ObservedAt) })
	cutoff := now.Add(-24 * time.Hour)
	var recent []ProviderObservation
	for _, observation := range observations {
		if !observation.ObservedAt.Before(cutoff) {
			recent = append(recent, observation)
		}
	}
	if len(recent) == 0 {
		latest := observations[0]
		return ProviderMonitoringSignal{State: "unknown", Source: source, Detail: "samples_stale", ErrorCode: latest.ErrorCode, ObservedAt: &latest.ObservedAt}
	}
	successes := 0
	var successfulLatencies []int64
	for _, observation := range recent {
		if observation.Success {
			successes++
			if observation.LatencyMS > 0 {
				successfulLatencies = append(successfulLatencies, observation.LatencyMS)
			}
		}
	}
	rate := float64(successes) / float64(len(recent)) * 100
	state := "healthy"
	if source == "active_probe" && !recent[0].Success {
		state = "degraded"
	} else if !recent[0].Success && rate < 90 {
		state = "down"
	} else if rate < 99 {
		state = "degraded"
	}
	latest := recent[0]
	return ProviderMonitoringSignal{
		State:       state,
		Source:      source,
		Detail:      "observed",
		Samples:     len(recent),
		SuccessRate: rate,
		LatencyMS:   medianLatency(successfulLatencies),
		ErrorCode:   latest.ErrorCode,
		ObservedAt:  &latest.ObservedAt,
	}
}

func medianLatency(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[(len(values)-1)/2]
}

func providerObservationTrend(now time.Time, observations []ProviderObservation) []string {
	trend := make([]string, 30)
	for index := range trend {
		trend[index] = "none"
	}
	for day := 0; day < 30; day++ {
		start := startOfUTCDay(now.AddDate(0, 0, day-29))
		end := start.Add(24 * time.Hour)
		total := 0
		success := 0
		for _, observation := range observations {
			if observation.ObservedAt.Before(start) || !observation.ObservedAt.Before(end) {
				continue
			}
			total++
			if observation.Success {
				success++
			}
		}
		if total == 0 {
			continue
		}
		rate := float64(success) / float64(total) * 100
		switch {
		case rate >= 99:
			trend[day] = "success"
		case rate >= 90:
			trend[day] = "warning"
		default:
			trend[day] = "failure"
		}
	}
	return trend
}
