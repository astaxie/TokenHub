package server

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

func monitoringParityFixture(now time.Time) []ProviderObservation {
	var rows []ProviderObservation
	for p := 0; p < 3; p++ {
		for i := 0; i < 240; i++ {
			source := []string{"gateway_request", "active_probe", "ignored"}[i%3]
			age := time.Duration(i)*4*time.Hour + time.Duration(i)*time.Millisecond
			if p == 1 {
				age += 25 * time.Hour
			}
			rows = append(rows, ProviderObservation{ID: fmt.Sprintf("monitoring-%d-%d", p, i), ProviderID: fmt.Sprintf("provider-%d", p), Source: source, Success: i%7 != 0, LatencyMS: int64(i%11-2) * 100, ErrorCode: fmt.Sprintf("error-%d", i), ObservedAt: now.Add(-age)})
		}
	}
	// Exercise exact cutoff inclusion, UTC midnight, future dates and even medians.
	for i, at := range []time.Time{now.Add(-24 * time.Hour), now.Add(-24*time.Hour - time.Microsecond), now.Add(-30 * 24 * time.Hour), now.Add(-30*24*time.Hour - time.Microsecond), startOfUTCDay(now), now.Add(48 * time.Hour), now.AddDate(1, 0, 0)} {
		rows = append(rows, ProviderObservation{ID: fmt.Sprintf("edge-%d", i), ProviderID: "provider-2", Source: "gateway_request", Success: i%2 == 0, LatencyMS: int64(101 + i), ObservedAt: at})
	}
	for i, latency := range []int64{40, 10, 20, 30, 0, -10} {
		rows = append(rows, ProviderObservation{ID: fmt.Sprintf("median-%d", i), ProviderID: "median", Source: "gateway_request", Success: true, LatencyMS: latency, ObservedAt: now.Add(-time.Duration(i+1) * time.Second)})
	}
	rows = append(rows, ProviderObservation{ID: "stale", ProviderID: "stale", Source: "active_probe", ErrorCode: "old_error", ObservedAt: now.Add(-48 * time.Hour)})
	return rows
}

func assertMonitoringAggregationParity(t *testing.T, store *GormStore) {
	t.Helper()
	now := time.Date(2026, 11, 12, 10, 23, 45, 0, time.UTC)
	rows := monitoringParityFixture(now)
	if err := store.db.CreateInBatches(rows, 100).Error; err != nil {
		t.Fatal(err)
	}
	ids := []string{"provider-0", "provider-1", "provider-2", "median", "stale", "empty"}
	got, err := store.ProviderMonitoringObservations(context.Background(), now, ids)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		var probes, gateway, all []ProviderObservation
		for _, row := range rows {
			if row.ProviderID != id || row.ObservedAt.Before(now.Add(-30*24*time.Hour)) {
				continue
			}
			switch row.Source {
			case "active_probe":
				probes = append(probes, row)
			case "gateway_request":
				gateway = append(gateway, row)
			default:
				continue
			}
			all = append(all, row)
		}
		want := ProviderObservationSummary{ActiveProbe: observationMonitoringSignal(now, "active_probe", probes), Gateway: observationMonitoringSignal(now, "gateway_request", gateway), Trend: providerObservationTrend(now, all)}
		actual := got[id]
		// PostgreSQL drivers may return an equivalent instant in the local zone.
		for _, signal := range []*ProviderMonitoringSignal{&actual.ActiveProbe, &actual.Gateway} {
			if signal.ObservedAt != nil {
				utc := signal.ObservedAt.UTC()
				signal.ObservedAt = &utc
			}
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatalf("provider %s: got %+v; want %+v", id, got[id], want)
		}
	}
	if got["median"].Gateway.LatencyMS != 20 {
		t.Fatalf("lower median changed: %+v", got["median"].Gateway)
	}
}

func TestProviderMonitoringAggregationParity(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	assertMonitoringAggregationParity(t, store)
}

func TestProviderMonitoringAggregationCancellation(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ProviderMonitoringObservations(ctx, time.Now(), []string{"provider"}); err == nil {
		t.Fatal("canceled query succeeded")
	}
}

type monitoringRowsLogger struct {
	logger.Interface
	maxRows int64
}

func (l *monitoringRowsLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	_, rows := fc()
	if rows > l.maxRows {
		l.maxRows = rows
	}
}

func seedMonitoringHistory(tb testing.TB, store *GormStore, count int, now time.Time) {
	tb.Helper()
	for start := 0; start < count; start += 100 {
		rows := make([]ProviderObservation, 0, 100)
		for i := start; i < start+100 && i < count; i++ {
			rows = append(rows, ProviderObservation{ID: fmt.Sprintf("history-%d", i), ProviderID: "history", Source: "gateway_request", Success: i%20 != 0, LatencyMS: int64(i%1000 + 1), ObservedAt: now.Add(-time.Duration(i%720) * time.Hour)})
		}
		if err := store.db.CreateInBatches(rows, 100).Error; err != nil {
			tb.Fatal(err)
		}
	}
}

func TestProviderMonitoringAggregationReturnsBoundedRows(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	seedMonitoringHistory(t, store, 10000, now)
	log := &monitoringRowsLogger{Interface: logger.Discard}
	store.db.Logger = log
	got, err := store.ProviderMonitoringObservations(context.Background(), now, []string{"history"})
	if err != nil {
		t.Fatal(err)
	}
	if got["history"].Gateway.Samples == 0 || log.maxRows > 32 {
		t.Fatalf("unbounded or empty aggregate: max rows=%d, summary=%+v", log.maxRows, got)
	}
	t.Logf("largest result set with 10000 observations: %d rows", log.maxRows)
}

func BenchmarkProviderMonitoringAggregation(b *testing.B) {
	for _, count := range []int{10000, 100000} {
		b.Run(fmt.Sprintf("rows_%d", count), func(b *testing.B) {
			store := NewMemoryStore()
			b.Cleanup(func() {
				if err := store.Close(); err != nil {
					b.Error(err)
				}
			})
			now := time.Now().UTC()
			seedMonitoringHistory(b, store, count, now)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.ProviderMonitoringObservations(context.Background(), now, []string{"history"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
