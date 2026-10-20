package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// BenchmarkUsageDashboardDaily keeps today's data fixed while historical rows
// grow to the reported production scale. Run with -benchmem -benchtime=1x.
func BenchmarkUsageDashboardDaily(b *testing.B) {
	for _, history := range []int{0, 140000} {
		b.Run(fmt.Sprintf("history_%d", history), func(b *testing.B) {
			store := NewMemoryStore()
			b.Cleanup(func() { _ = store.Close() })
			now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
			const current = 1000
			for offset := 0; offset < history+current; offset += 100 {
				records := make([]UsageRecord, 0, 100)
				logs := make([]RequestLog, 0, 100)
				for index := offset; index < min(offset+100, history+current); index++ {
					timestamp := now.Add(-time.Hour)
					if index < history {
						timestamp = now.AddDate(-1, 0, -(index % 365))
					}
					id := fmt.Sprintf("dashboard_bench_%d", index)
					record := usageSummaryRecord(id, "project_bench", "key_bench", "", 10, timestamp)
					record.ModelName, record.ProviderID = "model_bench", "provider_bench"
					records = append(records, record)
					logs = append(logs, usageSummaryLog(id, "project_bench", "key_bench", "", http.StatusOK, timestamp))
				}
				if err := store.db.Create(&records).Error; err != nil {
					b.Fatal(err)
				}
				if err := store.db.Create(&logs).Error; err != nil {
					b.Fatal(err)
				}
			}
			server := &Server{store: store}
			user := AdminUser{Role: "admin"}
			b.Run("sql_window", func(b *testing.B) {
				b.ReportAllocs()
				for index := 0; index < b.N; index++ {
					daily, err := server.usageDailyForUser(b.Context(), user, now)
					if err != nil {
						b.Fatal(err)
					}
					if daily["summary"].(map[string]any)["usage_record_count"] != int64(current) {
						b.Fatal("daily summary included historical rows")
					}
				}
			})
			b.Run("legacy_history_load", func(b *testing.B) {
				b.ReportAllocs()
				start := now.Add(-12 * time.Hour)
				end := start.AddDate(0, 0, 1)
				for index := 0; index < b.N; index++ {
					query := server.usageSummaryQueryForUser(user)
					query.UsageRecords.CreatedAtFrom, query.UsageRecords.CreatedAtBefore = start, end
					query.RequestLogs.CreatedAtFrom, query.RequestLogs.CreatedAtBefore = start, end
					if _, err := store.QueryUsageSummary(b.Context(), query); err != nil {
						b.Fatal(err)
					}
					var records []UsageRecord
					for _, record := range store.ListUsageRecords() {
						if !record.CreatedAt.Before(start) && record.CreatedAt.Before(end) {
							records = append(records, record)
						}
					}
					projects := indexProjectsByID(store.ListProjects())
					breakdown := server.usageBreakdownFromRecords(records, projects)
					breakdown["api_keys"] = aggregateUsage(records, func(record UsageRecord) string { return record.APIKeyID })
					breakdown["members"] = server.aggregateUsageByMember(user, records, projects)
					if len(records) != current {
						b.Fatal("legacy window included historical rows")
					}
				}
			})
		})
	}
}
