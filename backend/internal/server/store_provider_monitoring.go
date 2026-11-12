package server

import (
	"context"
	"database/sql"
	"time"

	"gorm.io/gorm"
)

// ProviderObservationSummary contains bounded monitoring output, never raw history.
type ProviderObservationSummary struct {
	ActiveProbe ProviderMonitoringSignal
	Gateway     ProviderMonitoringSignal
	Trend       []string
}

type providerObservationBucket struct {
	ProviderID        string
	Source            string
	Day               string
	Total             int64
	Successes         int64
	Recent            int64
	RecentSuccesses   int64
	PositiveLatencies int64
}

// ProviderMonitoringObservations aggregates at most 32 UTC day buckets per
// provider/source. Exact lower medians and latest samples are selected by SQL;
// the application never materializes historical observations or latency arrays.
func (s *GormStore) ProviderMonitoringObservations(ctx context.Context, now time.Time, providerIDs []string) (map[string]ProviderObservationSummary, error) {
	var result map[string]ProviderObservationSummary
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = (&GormStore{db: tx}).providerMonitoringObservations(ctx, now, providerIDs)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}

func (s *GormStore) providerMonitoringObservations(ctx context.Context, now time.Time, providerIDs []string) (map[string]ProviderObservationSummary, error) {
	result := make(map[string]ProviderObservationSummary, len(providerIDs))
	if len(providerIDs) == 0 {
		return result, nil
	}
	cutoff := now.UTC().Add(-30 * 24 * time.Hour)
	recent := now.UTC().Add(-24 * time.Hour)
	daySQL := "strftime('%Y-%m-%d', observed_at)"
	if s.db.Dialector.Name() == "postgres" {
		daySQL = "to_char(observed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD')"
	}
	// Future-dated samples retain existing signal semantics but cannot create an
	// unbounded number of day groups. They do not belong to the 30-day trend.
	daySQL = "CASE WHEN observed_at >= ? THEN '' ELSE " + daySQL + " END"
	var buckets []providerObservationBucket
	err := s.db.WithContext(ctx).Model(&ProviderObservation{}).
		Select("provider_id, source, "+daySQL+" AS day, COUNT(*) AS total, SUM(CASE WHEN success THEN 1 ELSE 0 END) AS successes, SUM(CASE WHEN observed_at >= ? THEN 1 ELSE 0 END) AS recent, SUM(CASE WHEN observed_at >= ? AND success THEN 1 ELSE 0 END) AS recent_successes, SUM(CASE WHEN observed_at >= ? AND success AND latency_ms > 0 THEN 1 ELSE 0 END) AS positive_latencies", startOfUTCDay(now).Add(24*time.Hour), recent, recent, recent).
		Where("provider_id IN ? AND observed_at >= ? AND source IN ?", providerIDs, cutoff, []string{"active_probe", "gateway_request"}).
		Group("provider_id, source, day").Scan(&buckets).Error
	if err != nil {
		return nil, err
	}
	type key struct{ provider, source string }
	totals := make(map[key]providerObservationBucket)
	days := make(map[string]map[string]providerObservationBucket)
	for _, bucket := range buckets {
		k := key{bucket.ProviderID, bucket.Source}
		total := totals[k]
		total.Recent += bucket.Recent
		total.RecentSuccesses += bucket.RecentSuccesses
		total.PositiveLatencies += bucket.PositiveLatencies
		totals[k] = total
		if days[bucket.ProviderID] == nil {
			days[bucket.ProviderID] = make(map[string]providerObservationBucket)
		}
		day := days[bucket.ProviderID][bucket.Day]
		day.Total += bucket.Total
		day.Successes += bucket.Successes
		days[bucket.ProviderID][bucket.Day] = day
	}
	for _, id := range providerIDs {
		summary := ProviderObservationSummary{Trend: make([]string, 30)}
		for _, source := range []string{"active_probe", "gateway_request"} {
			signal := ProviderMonitoringSignal{State: "unknown", Source: source, Detail: "no_samples"}
			if total, ok := totals[key{id, source}]; ok {
				signal, err = s.providerMonitoringSignal(ctx, id, source, cutoff, recent, total)
				if err != nil {
					return nil, err
				}
			}
			if source == "active_probe" {
				summary.ActiveProbe = signal
			} else {
				summary.Gateway = signal
			}
		}
		for day := range summary.Trend {
			bucket := days[id][startOfUTCDay(now.AddDate(0, 0, day-29)).Format("2006-01-02")]
			summary.Trend[day] = "none"
			if bucket.Total == 0 {
				continue
			}
			rate := float64(bucket.Successes) / float64(bucket.Total) * 100
			switch {
			case rate >= 99:
				summary.Trend[day] = "success"
			case rate >= 90:
				summary.Trend[day] = "warning"
			default:
				summary.Trend[day] = "failure"
			}
		}
		result[id] = summary
	}
	return result, nil
}

func (s *GormStore) providerMonitoringSignal(ctx context.Context, providerID, source string, cutoff, recent time.Time, total providerObservationBucket) (ProviderMonitoringSignal, error) {
	var latest ProviderObservation
	err := s.db.WithContext(ctx).Select("success", "error_code", "observed_at").
		Where("provider_id = ? AND source = ? AND observed_at >= ?", providerID, source, cutoff).
		Order("observed_at DESC, id DESC").Take(&latest).Error
	if err != nil {
		return ProviderMonitoringSignal{}, err
	}
	signal := ProviderMonitoringSignal{State: "unknown", Source: source, Detail: "samples_stale", ErrorCode: latest.ErrorCode, ObservedAt: &latest.ObservedAt}
	if total.Recent == 0 {
		return signal, nil
	}
	signal.State = "healthy"
	signal.Detail = "observed"
	signal.Samples = int(total.Recent)
	signal.SuccessRate = float64(total.RecentSuccesses) / float64(total.Recent) * 100
	if source == "active_probe" && !latest.Success {
		signal.State = "degraded"
	} else if !latest.Success && signal.SuccessRate < 90 {
		signal.State = "down"
	} else if signal.SuccessRate < 99 {
		signal.State = "degraded"
	}
	if total.PositiveLatencies > 0 {
		// Match medianLatency's lower median, including even sample counts.
		var latency struct{ LatencyMS int64 }
		err = s.db.WithContext(ctx).Model(&ProviderObservation{}).Select("latency_ms").
			Where("provider_id = ? AND source = ? AND observed_at >= ? AND success = ? AND latency_ms > 0", providerID, source, recent, true).
			Order("latency_ms").Offset(int((total.PositiveLatencies - 1) / 2)).Limit(1).Scan(&latency).Error
		if err != nil {
			return ProviderMonitoringSignal{}, err
		}
		signal.LatencyMS = latency.LatencyMS
	}
	return signal, nil
}
