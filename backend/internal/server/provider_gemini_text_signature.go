package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash"
	"io"
)

const geminiTextSignatureProvider = "gemini-text"

type geminiSignedTextPart struct {
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Signature string `json:"signature"`
}

// Track the signed part boundaries in the flattened OpenAI message text.
type geminiTextSignatures struct {
	bytes int
	parts []geminiSignedTextPart
	hash  hash.Hash
}

type geminiTextSignatureEnvelope struct {
	Digest string                 `json:"digest"`
	Parts  []geminiSignedTextPart `json:"parts"`
}

func (s *geminiTextSignatures) record(text string, signature string) {
	if s.hash == nil {
		s.hash = sha256.New()
	}
	_, _ = io.WriteString(s.hash, text)
	end := s.bytes + len(text)
	if signature != "" {
		s.parts = append(s.parts, geminiSignedTextPart{
			Start: s.bytes, End: end, Signature: signature,
		})
	}
	s.bytes = end
}

func (s geminiTextSignatures) encode() string {
	if len(s.parts) == 0 {
		return ""
	}
	data, _ := json.Marshal(geminiTextSignatureEnvelope{Digest: hex.EncodeToString(s.hash.Sum(nil)), Parts: s.parts})
	return string(data)
}

// Recreate signed Gemini parts only when the client returns the same text.
// Gemini can put the signature on an empty final text part in SSE.
func replayGeminiTextParts(text, encoded string) ([]map[string]any, bool) {
	data, ok := decodeProviderSignature(geminiTextSignatureProvider, encoded)
	if !ok {
		return nil, false
	}
	var envelope geminiTextSignatureEnvelope
	digest := sha256.Sum256([]byte(text))
	if json.Unmarshal([]byte(data), &envelope) != nil || envelope.Digest != hex.EncodeToString(digest[:]) || len(envelope.Parts) == 0 {
		return nil, false
	}
	parts := make([]map[string]any, 0, len(envelope.Parts)+1)
	at := 0
	for _, part := range envelope.Parts {
		if part.Start < at || part.End < part.Start || part.End > len(text) || part.Signature == "" {
			return nil, false
		}
		if part.Start > at {
			parts = append(parts, map[string]any{"text": text[at:part.Start]})
		}
		parts = append(parts, map[string]any{"text": text[part.Start:part.End], "thoughtSignature": part.Signature})
		at = part.End
	}
	if at < len(text) {
		parts = append(parts, map[string]any{"text": text[at:]})
	}
	return parts, true
}
