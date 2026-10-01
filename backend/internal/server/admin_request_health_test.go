package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type requestHealthPayload struct {
	BucketSeconds int                   `json:"bucket_seconds"`
	WindowStart   time.Time             `json:"window_start"`
	WindowEnd     time.Time             `json:"window_end"`
	Data          []RequestHealthBucket `json:"data"`
}

func TestRequestHealthWindowEndsWithTheCurrentHour(t *testing.T) {
	start, end := requestHealthWindow(time.Date(2026, time.September, 30, 14, 25, 0, 0, time.FixedZone("UTC+8", 8*60*60)))
	if want := time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC); !end.Equal(want) {
		t.Fatalf("expected window to end at %s, got %s", want, end)
	}
	if want := end.Add(-7 * 24 * time.Hour); !start.Equal(want) {
		t.Fatalf("expected a seven-day window starting at %s, got %s", want, start)
	}
}

func TestRequestHealthBucketsFillEveryHour(t *testing.T) {
	start := time.Date(2026, time.September, 23, 7, 0, 0, 0, time.UTC)
	buckets := requestHealthBuckets(start, []RequestHealthCount{
		{Bucket: "2026-09-23T07:00:00Z", Total: 3, Warning: 1, Failure: 1},
		{Bucket: "2026-09-30T06:00:00Z", Total: 2},
		{Bucket: "2026-09-23T06:00:00Z", Total: 9},
		{Bucket: "2026-09-30T07:00:00Z", Total: 9},
		{Bucket: "not-a-time", Total: 9},
	})
	if len(buckets) != requestHealthBucketCount {
		t.Fatalf("expected %d buckets, got %d", requestHealthBucketCount, len(buckets))
	}
	first, last := buckets[0], buckets[len(buckets)-1]
	if !first.Start.Equal(start) || first.Total != 3 || first.Success != 1 || first.Warning != 1 || first.Failure != 1 {
		t.Fatalf("unexpected first bucket: %+v", first)
	}
	if want := start.Add(167 * time.Hour); !last.Start.Equal(want) || last.Total != 2 || last.Success != 2 {
		t.Fatalf("unexpected last bucket: %+v", last)
	}
	var total int64
	for _, bucket := range buckets {
		total += bucket.Total
	}
	if total != 5 {
		t.Fatalf("expected counts outside the window to be dropped, got total %d", total)
	}
}

func TestAdminRequestHealthCountsOutcomesPerHour(t *testing.T) {
	store := NewMemoryStore()
	start, end := requestHealthWindow(time.Now())
	firstHour := start.Add(30 * time.Minute)
	lastHour := end.Add(-90 * time.Minute)
	logs := []RequestLog{
		{StatusCode: http.StatusOK, LatencyMS: 120, CreatedAt: firstHour},
		{StatusCode: http.StatusOK, LatencyMS: 80, CreatedAt: lastHour},
		{StatusCode: http.StatusOK, LatencyMS: 6000, CreatedAt: lastHour},
		{StatusCode: http.StatusFound, LatencyMS: 80, CreatedAt: lastHour},
		{StatusCode: http.StatusBadGateway, LatencyMS: 6000, CreatedAt: lastHour},
		{StatusCode: http.StatusOK, ErrorCode: "upstream_stream_interrupted", LatencyMS: 80, CreatedAt: lastHour},
		{StatusCode: http.StatusOK, LatencyMS: 80, CreatedAt: start.Add(-2 * time.Hour)},
	}
	for index, log := range logs {
		log.ID = fmt.Sprintf("log_health_%02d", index)
		log.RequestID = fmt.Sprintf("req_health_%02d", index)
		log.ProjectID = "project_health"
		log.APIKeyID = "key_health"
		if err := store.db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}

	response := doJSON(t, New(store).Handler(), http.MethodGet, "/api/admin/audit/request-health", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("expected request health 200, got %d: %s", response.Code, response.Body)
	}
	var payload requestHealthPayload
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.BucketSeconds != 3600 || len(payload.Data) != requestHealthBucketCount {
		t.Fatalf("expected %d hourly buckets, got bucket_seconds=%d len=%d", requestHealthBucketCount, payload.BucketSeconds, len(payload.Data))
	}
	if !payload.WindowEnd.Equal(payload.WindowStart.Add(requestHealthBucketCount * time.Hour)) {
		t.Fatalf("unexpected window: %s - %s", payload.WindowStart, payload.WindowEnd)
	}
	byStart := map[time.Time]RequestHealthBucket{}
	var total int64
	for _, bucket := range payload.Data {
		byStart[bucket.Start.UTC()] = bucket
		total += bucket.Total
	}
	if bucket := byStart[lastHour.Truncate(time.Hour)]; bucket.Total != 5 || bucket.Success != 1 || bucket.Warning != 2 || bucket.Failure != 2 {
		t.Fatalf("unexpected outcome split for the busy hour: %+v", bucket)
	}
	if !payload.WindowStart.After(firstHour) {
		if bucket := byStart[firstHour.Truncate(time.Hour)]; bucket.Total != 1 || bucket.Success != 1 {
			t.Fatalf("unexpected first-hour bucket: %+v", bucket)
		}
	}
	if total > 6 {
		t.Fatalf("a log from before the window was counted: total=%d", total)
	}
}

func TestUserRequestHealthOnlyCountsVisibleLogs(t *testing.T) {
	store := NewMemoryStore()
	user, err := store.CreateAdminUser(AdminUser{
		Username: "request-health-user",
		Email:    "request-health-user@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "user123456")
	if err != nil {
		t.Fatal(err)
	}
	otherUser, err := store.CreateAdminUser(AdminUser{
		Username: "request-health-other",
		Email:    "request-health-other@tokenhub.local",
		Role:     "user",
		Status:   StatusActive,
	}, "other123456")
	if err != nil {
		t.Fatal(err)
	}
	project := store.CreateProject(Project{Name: "Shared Request Health", OwnerUserID: otherUser.ID})
	store.CreateResource("project-members", AdminResource{
		Name:   "Request Health Viewer",
		Status: StatusActive,
		Fields: map[string]any{
			"project_id": project.ID,
			"user_id":    user.ID,
			"role":       "viewer",
		},
	})
	ownKey, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:     "request-health-own-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": user.ID},
	}, "thk_request_health_own")
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _, err := store.CreateAPIKey(project.ID, APIKey{
		Name:     "request-health-other-key",
		Status:   StatusActive,
		Metadata: map[string]string{"created_by": otherUser.ID},
	}, "thk_request_health_other")
	if err != nil {
		t.Fatal(err)
	}
	createdAt := time.Now().UTC().Add(-3 * time.Hour)
	for index, keyID := range []string{ownKey.ID, otherKey.ID, otherKey.ID, otherKey.ID} {
		if err := store.db.Create(&RequestLog{
			ID:         fmt.Sprintf("log_health_scope_%02d", index),
			RequestID:  fmt.Sprintf("req_health_scope_%02d", index),
			ProjectID:  project.ID,
			APIKeyID:   keyID,
			StatusCode: http.StatusBadGateway,
			CreatedAt:  createdAt,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	app := New(store).Handler()
	login := doJSON(t, app, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"identity": user.Email,
		"password": "user123456",
	}, "")
	var credentials struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(login.Body), &credentials); err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app, http.MethodGet, "/api/admin/audit/request-health", nil, credentials.Token)
	if response.Code != http.StatusOK {
		t.Fatalf("expected scoped request health 200, got %d: %s", response.Code, response.Body)
	}
	var payload requestHealthPayload
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	var total, failures int64
	for _, bucket := range payload.Data {
		total += bucket.Total
		failures += bucket.Failure
	}
	if total != 1 || failures != 1 {
		t.Fatalf("user scope leaked another key's requests: total=%d failures=%d", total, failures)
	}
}
