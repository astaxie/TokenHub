package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func newPayloadChunkTestStore(t *testing.T) *GormStore {
	t.Helper()
	return NewMemoryStoreWithConfig(Config{AdminToken: "dev_admin_token", AuditPayloadScanKB: defaultAuditPayloadScanKB, AuditPayloadMaxKB: defaultAuditPayloadMaxKB})
}

func TestRequestPayloadChunkRoundTrip(t *testing.T) {
	store := newPayloadChunkTestStore(t)
	// A multi-chunk body exercises split, dedup, reference order, and
	// reassembly through the same detail path the admin console uses.
	full := ""
	for index := range 5 {
		full += fmt.Sprintf(`{"marker":%d,"%s}`, index, strings.Repeat("x", auditPayloadChunkSize))
	}
	record := RequestPayloadRecord{
		RequestBody:      full,
		RequestPreview:   truncateAuditPreview(full, defaultAuditPayloadLimits()),
		RequestTruncated: true,
		ResponseBody:     `{"ok":true}`,
		ResponsePreview:  `{"ok":true}`,
	}
	store.db.Create(&RequestLog{ID: NewID("req"), RequestID: "req_chunk_roundtrip", ModelName: "smart-router", StatusCode: 200, CreatedAt: time.Now().UTC()})
	store.RecordRequestPayload("req_chunk_roundtrip", record)

	var payload RequestPayloadLog
	if err := store.db.First(&payload, "request_id = ?", "req_chunk_roundtrip").Error; err != nil {
		t.Fatal(err)
	}
	if !payload.RequestChunked {
		t.Fatal("large snapshot should be stored as chunks")
	}
	if payload.ResponseChunked {
		t.Fatal("small snapshot should stay inline")
	}
	if len(payload.RequestBody) >= len(full) {
		t.Fatal("inline column should keep only the preview")
	}

	detail, err := store.GetRequestDetail("req_chunk_roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	stored := detail["payload"].(RequestPayloadLog)
	if stored.RequestBody != full {
		t.Fatalf("reassembled body diverges from the original: got %d bytes, want %d", len(stored.RequestBody), len(full))
	}
	if stored.ResponseBody != `{"ok":true}` {
		t.Fatalf("response body mismatch: %q", stored.ResponseBody)
	}

	var chunkCount, refCount int64
	store.db.Model(&PayloadChunk{}).Count(&chunkCount)
	store.db.Model(&PayloadChunkRef{}).Count(&refCount)
	if chunkCount == 0 || refCount < chunkCount {
		t.Fatalf("unexpected chunk bookkeeping: chunks=%d refs=%d", chunkCount, refCount)
	}
}

func TestRequestPayloadChunkDeduplicatesSharedContent(t *testing.T) {
	store := newPayloadChunkTestStore(t)
	shared := strings.Repeat("y", auditPayloadChunkSize*2)
	for index, requestID := range []string{"req_dedup_a", "req_dedup_b"} {
		body := fmt.Sprintf(`{"session":%d,"shared":"%s"}`, index, shared[:len(shared)-20])
		store.RecordRequestPayload(requestID, RequestPayloadRecord{
			RequestBody:      body,
			RequestPreview:   truncateAuditPreview(body, defaultAuditPayloadLimits()),
			RequestTruncated: true,
		})
	}
	var chunkCount int64
	store.db.Model(&PayloadChunk{}).Count(&chunkCount)
	// The shared middle chunk is stored once; only the small distinct head and
	// tail chunks differ between the two requests.
	if chunkCount > 6 {
		t.Fatalf("shared content was re-stored: %d chunks for two sessions", chunkCount)
	}
}

func TestRequestPayloadGCRemovesOnlyOrphanedChunks(t *testing.T) {
	store := newPayloadChunkTestStore(t)
	shared := strings.Repeat("z", auditPayloadChunkSize*2)
	expired := "req_gc_expired"
	current := "req_gc_current"
	for index, requestID := range []string{expired, current} {
		if err := store.db.Create(&RequestLog{ID: NewID("req"), RequestID: requestID, ModelName: "smart-router", StatusCode: 200, CreatedAt: time.Now().UTC().Add(time.Duration(index) * time.Minute)}).Error; err != nil {
			t.Fatalf("seed request log %s: %v", requestID, err)
		}
		body := fmt.Sprintf(`{"session":"%s","shared":"%s"}`, requestID, shared[:len(shared)-20])
		store.RecordRequestPayload(requestID, RequestPayloadRecord{
			RequestBody:      body,
			RequestPreview:   truncateAuditPreview(body, defaultAuditPayloadLimits()),
			RequestTruncated: true,
		})
	}
	if err := store.db.Model(&RequestPayloadLog{}).Where("request_id = ?", expired).Update("created_at", time.Now().UTC().Add(-48*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteRequestPayloadLogsBefore(t.Context(), time.Now().UTC().Add(-24*time.Hour), 100); err != nil {
		t.Fatal(err)
	}
	var refsForExpired int64
	store.db.Model(&PayloadChunkRef{}).Where("request_id = ?", expired).Count(&refsForExpired)
	if refsForExpired != 0 {
		t.Fatalf("expired references survived GC: %d", refsForExpired)
	}
	detail, err := store.GetRequestDetail(current)
	if err != nil {
		t.Fatal(err)
	}
	stored := detail["payload"].(RequestPayloadLog)
	want := fmt.Sprintf(`{"session":"%s","shared":"%s"}`, current, shared[:len(shared)-20])
	if stored.RequestBody != want {
		t.Fatal("GC removed a chunk still referenced by a current request")
	}
}

func TestConfiguredAuditLimitsRenderOversizedSnapshots(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{
		AdminToken:         "dev_admin_token",
		AuditPayloadScanKB: 256,
		AuditPayloadMaxKB:  64,
	})
	payload := map[string]any{"content": strings.Repeat("x", 100*1024)}
	record, _, _ := server.auditPayloadRecord(payload, nil)
	if record.RequestTruncated != true || !json.Valid([]byte(record.RequestBody)) {
		t.Fatalf("100 KiB payload should render fully at a 256 KiB scan budget: truncated=%v", record.RequestTruncated)
	}
	if strings.Contains(record.RequestBody, auditPayloadOmittedReason) {
		t.Fatal("oversized-but-in-budget snapshot must not be omitted")
	}
	if len(record.RequestPreview) > 64*1024 {
		t.Fatalf("preview exceeded the configured cap: %d", len(record.RequestPreview))
	}
	if record.RequestBody == record.RequestPreview {
		t.Fatal("full body and preview should differ for a truncated snapshot")
	}
}

func TestAuditPreviewCapClampsToScanBudget(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token", AuditPayloadScanKB: 16, AuditPayloadMaxKB: 9999})
	if server.auditLimits.maxBytes != 16*1024 {
		t.Fatalf("preview cap should fold back to the scan budget, got %d", server.auditLimits.maxBytes)
	}
}

func TestRequestPayloadChunkInsertionIsIdempotentForRepeatedContent(t *testing.T) {
	store := newPayloadChunkTestStore(t)
	store.db.Create(&RequestLog{ID: NewID("req"), RequestID: "req_repeated", ModelName: "smart-router", StatusCode: 200, CreatedAt: time.Now().UTC()})
	// A uniform body makes every 16 KiB chunk identical, so the second
	// insertion hits the same primary key unless insertion is idempotent.
	body := strings.Repeat("abcdefgh", auditPayloadChunkSize)
	store.RecordRequestPayload("req_repeated", RequestPayloadRecord{
		RequestBody:      body,
		RequestPreview:   truncateAuditPreview(body, defaultAuditPayloadLimits()),
		RequestTruncated: true,
	})
	var chunkCount, refCount int64
	store.db.Model(&PayloadChunk{}).Count(&chunkCount)
	store.db.Model(&PayloadChunkRef{}).Where("request_id = ?", "req_repeated").Count(&refCount)
	if chunkCount != 1 {
		t.Fatalf("uniform content should store exactly one chunk, got %d", chunkCount)
	}
	if refCount == 0 {
		t.Fatal("references were not recorded")
	}
	detail, err := store.GetRequestDetail("req_repeated")
	if err != nil {
		t.Fatal(err)
	}
	stored := detail["payload"].(RequestPayloadLog)
	if stored.RequestBody != body {
		t.Fatalf("reassembled body diverges: got %d bytes, want %d", len(stored.RequestBody), len(body))
	}
}
