package server

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Audit snapshots for agent-sized requests easily reach hundreds of KiB while
// repeating most of their content (tool schemas, conversation history) across
// requests of the same session. Large bodies are therefore split into fixed
// chunks, deduplicated by SHA-256 in payload_chunks, and referenced in order
// from payload_chunk_refs. The legacy request_body / response_body columns
// keep only a bounded preview, so their size stays constant no matter how the
// operator tunes TOKENHUB_AUDIT_PAYLOAD_*.
const (
	auditPayloadChunkSize = 16 * 1024
)

type RequestPayloadRecord struct {
	// Body is the full redacted snapshot; Preview is its byte-capped inline
	// form. Chunks are written only when the preview lost bytes, so small
	// payloads keep today's single-row shape.
	RequestBody       string
	RequestPreview    string
	RequestTruncated  bool
	ResponseBody      string
	ResponsePreview   string
	ResponseTruncated bool
}

type PayloadChunk struct {
	Hash      string    `gorm:"primaryKey;size:64"`
	Body      string    `gorm:"not null"`
	CreatedAt time.Time `gorm:"index"`
}

type PayloadChunkRef struct {
	RequestID string `gorm:"primaryKey;size:64"`
	Kind      string `gorm:"primaryKey;size:8"` // "request" or "response"
	Position  int    `gorm:"primaryKey"`
	ChunkHash string `gorm:"column:chunk_hash;index"`
}

type auditPayloadChunk struct {
	hash     string
	body     string
	position int
}

func splitAuditPayloadChunks(body string) []auditPayloadChunk {
	if body == "" {
		return nil
	}
	var chunks []auditPayloadChunk
	for offset, position := 0, 0; offset < len(body); offset, position = offset+auditPayloadChunkSize, position+1 {
		end := offset + auditPayloadChunkSize
		if end > len(body) {
			end = len(body)
		}
		part := body[offset:end]
		digest := sha256.Sum256([]byte(part))
		chunks = append(chunks, auditPayloadChunk{
			hash:     hex.EncodeToString(digest[:]),
			body:     part,
			position: position,
		})
	}
	return chunks
}

// storePayloadChunkRefs inserts the missing chunk bodies and all references
// for one rendered snapshot. Existing chunks are left untouched, which is
// where the deduplication saving comes from.
func storePayloadChunkRefs(tx *gorm.DB, requestID string, kind string, body string) error {
	chunks := splitAuditPayloadChunks(body)
	if len(chunks) == 0 {
		return nil
	}
	hashes := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		hashes = append(hashes, chunk.hash)
	}
	var known []string
	if err := tx.Model(&PayloadChunk{}).Where("hash IN ?", hashes).Pluck("hash", &known).Error; err != nil {
		return err
	}
	knownSet := make(map[string]struct{}, len(known))
	for _, hash := range known {
		knownSet[hash] = struct{}{}
	}
	now := time.Now().UTC()
	for _, chunk := range chunks {
		if _, ok := knownSet[chunk.hash]; ok {
			continue
		}
		if err := tx.Create(&PayloadChunk{Hash: chunk.hash, Body: chunk.body, CreatedAt: now}).Error; err != nil {
			return err
		}
	}
	refs := make([]PayloadChunkRef, 0, len(chunks))
	for _, chunk := range chunks {
		refs = append(refs, PayloadChunkRef{
			RequestID: requestID,
			Kind:      kind,
			Position:  chunk.position,
			ChunkHash: chunk.hash,
		})
	}
	return tx.Create(&refs).Error
}

func reassemblePayloadChunks(db *gorm.DB, requestID string, kind string) string {
	var refs []PayloadChunkRef
	if err := db.Where("request_id = ? AND kind = ?", requestID, kind).Order("position ASC").Find(&refs).Error; err != nil || len(refs) == 0 {
		return ""
	}
	hashes := make([]string, 0, len(refs))
	for _, ref := range refs {
		hashes = append(hashes, ref.ChunkHash)
	}
	var chunks []PayloadChunk
	if err := db.Where("hash IN ?", hashes).Find(&chunks).Error; err != nil {
		return ""
	}
	bodies := make(map[string]string, len(chunks))
	for _, chunk := range chunks {
		bodies[chunk.Hash] = chunk.Body
	}
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		body, ok := bodies[ref.ChunkHash]
		if !ok {
			// A missing chunk means the GC raced a live read; the preview in
			// the payload row remains the trustworthy fallback.
			return ""
		}
		parts = append(parts, body)
	}
	return strings.Join(parts, "")
}
