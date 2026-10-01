//go:build integration

package server

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func testPostgresRequestHealthBuckets(t *testing.T, store *GormStore) {
	t.Helper()
	projectID := "prj_" + NewID("request-health")
	hour := time.Date(2026, time.September, 30, 6, 0, 0, 0, time.UTC)
	shanghai := time.FixedZone("UTC+8", 8*60*60)
	logs := []RequestLog{
		{StatusCode: http.StatusOK, LatencyMS: 80, CreatedAt: hour.Add(10 * time.Minute).In(shanghai)},
		{StatusCode: http.StatusBadGateway, LatencyMS: 80, CreatedAt: hour.Add(20 * time.Minute)},
		{StatusCode: http.StatusOK, LatencyMS: 6000, CreatedAt: hour.Add(59 * time.Minute)},
		{StatusCode: http.StatusFound, LatencyMS: 80, CreatedAt: hour.Add(61 * time.Minute)},
	}
	for index, log := range logs {
		log.ID = fmt.Sprintf("log_%s_%02d", projectID, index)
		log.RequestID = fmt.Sprintf("req_%s_%02d", projectID, index)
		log.ProjectID = projectID
		if err := store.db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	counts, err := store.CountRequestHealth(RequestLogQuery{
		ProjectIDs: []string{projectID},
		Since:      hour.Add(-time.Hour),
		Until:      hour.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	byBucket := map[string]RequestHealthCount{}
	for _, count := range counts {
		byBucket[count.Bucket] = count
	}
	if len(byBucket) != 2 {
		t.Fatalf("expected two hourly buckets, got %+v", counts)
	}
	if got := byBucket["2026-09-30T06:00:00Z"]; got.Total != 3 || got.Warning != 1 || got.Failure != 1 {
		t.Fatalf("unexpected 06:00 bucket: %+v", got)
	}
	if got := byBucket["2026-09-30T07:00:00Z"]; got.Total != 1 || got.Warning != 1 || got.Failure != 0 {
		t.Fatalf("unexpected 07:00 bucket: %+v", got)
	}
}
