//go:build integration

package server

import "testing"

func TestPostgresProviderMonitoringAggregation(t *testing.T) {
	admin, pgURL := openPostgresAdmin(t)
	schema := createPostgresSchema(t, admin, "tokenhub_pg_monitoring_")
	dsn, err := withSearchPath(pgURL, schema)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStoreWithDialect(dsn, Config{Environment: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	sqlDB, err := store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := store.db.Exec("SET TIME ZONE 'Asia/Singapore'").Error; err != nil {
		t.Fatal(err)
	}
	assertMonitoringAggregationParity(t, store)
}
