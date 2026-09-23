package server

import "testing"

func providerResourceRequestTotal(t *testing.T, store *GormStore, resourceID string) int64 {
	t.Helper()
	var total int64
	if err := store.db.Model(&ProviderResourceBucket{}).
		Where("resource_id = ?", resourceID).
		Select("COALESCE(SUM(requests), 0)").
		Scan(&total).Error; err != nil {
		t.Fatal(err)
	}
	return total
}

func TestProviderResourceRequestTotalSpansMinuteBuckets(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	resourceID := "resource_minute_boundary"
	buckets := []ProviderResourceBucket{
		{ResourceID: resourceID, Bucket: "2026-08-24T16:40", Requests: 1},
		{ResourceID: resourceID, Bucket: "2026-08-24T16:41", Requests: 1},
	}
	if err := store.db.Create(&buckets).Error; err != nil {
		t.Fatal(err)
	}
	if got := providerResourceRequestTotal(t, store, resourceID); got != 2 {
		t.Fatalf("expected both minute buckets to count, got %d", got)
	}
}
