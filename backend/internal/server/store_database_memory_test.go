package server

import (
	"testing"
	"time"
)

func TestPrivateSQLiteMemoryStoreUsageQueries(t *testing.T) {
	for _, dsn := range []string{
		"sqlite::memory:",
		"sqlite::memory:?cache=shared",
		"file::memory:",
		"file:%3Amemory%3A",
		"file:private_usage?mode=memory&cache=private",
		"file:named_usage?mode=memory",
		"file:private_usage?vfs=memdb",
		"file:private_encoded_usage?%76fs=%6demdb",
		"private_bare_usage?vfs=memdb&cache=shared",
		"file:/private_mode_usage?vfs=memdb&mode=memory",
	} {
		t.Run(dsn, func(t *testing.T) {
			store, err := NewSQLiteStoreWithConfig(dsn, Config{})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if pools := store.WithContext(t.Context()).databasePools(); len(pools) != 1 || pools[0].name != "primary" {
				t.Fatalf("private memory store should own one pool: %+v", pools)
			}
			now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
			record := usageSummaryRecord("private_usage", "project_memory", "key_memory", "", 10, now)
			if err := store.db.Create(&record).Error; err != nil {
				t.Fatal(err)
			}
			server := &Server{store: store}
			summary, err := server.usageSummaryForUser(t.Context(), AdminUser{Role: "admin"})
			if err != nil {
				t.Fatal(err)
			}
			if summary["usage_record_count"] != int64(1) {
				t.Fatalf("usage summary did not read the primary database: %#v", summary)
			}
			daily, err := server.usageDailyForUser(t.Context(), AdminUser{Role: "admin"}, now)
			if err != nil {
				t.Fatal(err)
			}
			projects := daily["breakdown"].(map[string]any)["projects"].([]map[string]any)
			if len(projects) != 1 || projects[0]["request_count"] != int64(1) {
				t.Fatalf("daily breakdown lost primary usage: %#v", projects)
			}
			series, err := server.usageTimeseriesForUser(t.Context(), AdminUser{Role: "admin"}, 2, now)
			if err != nil || len(series) != 2 || series[1]["request_count"] != int64(1) {
				t.Fatalf("timeseries lost primary usage: %#v, error %v", series, err)
			}
			report, err := server.usageReportForUser(t.Context(), AdminUser{Role: "admin"}, "today", now.Add(time.Minute))
			if err != nil || report["summary"].(map[string]any)["usage_record_count"] != int64(1) {
				t.Fatalf("report lost primary usage: %#v, error %v", report, err)
			}
			if _, err := store.QueryTokenCostPage(t.Context(), TokenCostQuery{}); err != nil {
				t.Fatalf("analytics page did not read the primary database: %v", err)
			}
			if err := store.db.Model(&UsageRecord{}).Where("id = ?", record.ID).Update("total_tokens", 25).Error; err != nil {
				t.Fatalf("analytics made the primary pool read-only: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			pool, _ := store.db.DB()
			if err := pool.Ping(); err == nil {
				t.Fatal("private memory pool stayed open after Close")
			}
		})
	}
}
