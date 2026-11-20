package server

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestSQLiteOverviewAccountsForExternalConflictWrites(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprintf("recursive_%d", recursive), func(t *testing.T) {
			dsn := "sqlite://" + filepath.Join(t.TempDir(), "replacements.db")
			store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			_, writer, err := OpenRawDatabase(dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = writer.Close() })
			for _, statement := range []string{
				fmt.Sprintf("PRAGMA recursive_triggers=%d", recursive),
				"INSERT INTO usage_records(id,project_id,input_tokens,cost_usd) VALUES ('a','old',5,0.08)",
				"INSERT OR IGNORE INTO usage_records(id,input_tokens) VALUES ('a',90)",
				"INSERT INTO usage_records(id,input_tokens) VALUES ('a',7) ON CONFLICT(id) DO UPDATE SET input_tokens=excluded.input_tokens",
				"INSERT OR REPLACE INTO usage_records(id,project_id,input_tokens,cost_usd) VALUES ('a','new',8,0.12),('b','new',2,0.03)",
				"INSERT OR IGNORE INTO usage_records(id) VALUES ('b')",
				"DELETE FROM usage_records WHERE id='b'",
				"INSERT INTO usage_records(id,project_id,input_tokens,cost_usd) VALUES ('b','new',1,0.01)",
				"INSERT INTO usage_records(rowid,id,project_id,input_tokens) VALUES (100,'x','old',3),(200,'y','old',4)",
				"REPLACE INTO usage_records(rowid,id,project_id,input_tokens) VALUES (200,'x','new',2)",
				"INSERT INTO request_logs(id,project_id,status_code) VALUES ('req','old',200)",
				"REPLACE INTO request_logs(id,project_id,status_code) VALUES ('req','new',503)",
			} {
				if _, err := writer.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := writer.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec("REPLACE INTO usage_records(id,input_tokens) VALUES ('a',100)"); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			for _, scope := range []UsageSummaryScope{{Global: true}, {ProjectIDs: []string{"new"}}, {ProjectIDs: []string{"old"}}} {
				query := UsageSummaryQuery{UsageRecords: scope, RequestLogs: scope}
				want, err := store.QueryUsageSummary(t.Context(), query)
				if err != nil {
					t.Fatal(err)
				}
				got, progress, err := store.QueryOverviewSummary(t.Context(), query)
				if err != nil || !progress.Ready || progress.useRaw {
					t.Fatalf("progress=%+v err=%v", progress, err)
				}
				assertUsageSummaryEqual(t, usageSummaryPayload(got), usageSummaryPayload(want))
			}
		})
	}
}
