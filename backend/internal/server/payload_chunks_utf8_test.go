package server

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestRequestPayloadChunkSplitPreservesUTF8(t *testing.T) {
	store := newPayloadChunkTestStore(t)
	store.db.Create(&RequestLog{ID: NewID("req"), RequestID: "req_utf8", ModelName: "smart-router", StatusCode: 200, CreatedAt: time.Now().UTC()})
	// Multibyte content large enough to span several 16 KiB chunk boundaries.
	body := "x" + strings.Repeat("汉", 40000)
	store.RecordRequestPayload("req_utf8", RequestPayloadRecord{
		RequestBody:      body,
		RequestPreview:   truncateAuditPreview(body, defaultAuditPayloadLimits()),
		RequestTruncated: true,
	})
	chunks := splitAuditPayloadChunks(body)
	for _, chunk := range chunks {
		if !utf8.ValidString(chunk.body) {
			t.Fatalf("chunk %d is not valid UTF-8", chunk.position)
		}
	}
	detail, err := store.GetRequestDetail("req_utf8")
	if err != nil {
		t.Fatal(err)
	}
	stored := detail["payload"].(RequestPayloadLog)
	if stored.RequestBody != body {
		t.Fatalf("reassembled multibyte body diverges: got %d bytes, want %d", len(stored.RequestBody), len(body))
	}
	var storedChunks int64
	store.db.Model(&PayloadChunk{}).Where(
		"hash IN (SELECT chunk_hash FROM payload_chunk_refs WHERE request_id = ?)", "req_utf8",
	).Count(&storedChunks)
	if storedChunks == 0 {
		t.Fatal("expected stored chunks for an oversized multibyte snapshot")
	}
}
