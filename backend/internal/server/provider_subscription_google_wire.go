package server

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
)

func googleSubscriptionSessionID(payload map[string]any) string {
	first := ""
	contents, _ := payload["contents"].([]map[string]any)
	for _, content := range contents {
		if content["role"] != "user" {
			continue
		}
		parts, _ := content["parts"].([]map[string]any)
		for _, part := range parts {
			if text, ok := part["text"].(string); ok && text != "" {
				first = text
				break
			}
		}
		break
	}
	if first == "" {
		first = NewID("session")
	}
	sum := sha256.Sum256([]byte(first))
	return "-" + strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])&0x7fffffffffffffff, 10)
}

func (a *GoogleSubscriptionAdapter) preparePayload(model string, payload map[string]any) error {
	if a.antigravity() {
		if err := prepareAntigravitySubscriptionTools(model, payload); err != nil {
			return err
		}
	}
	contents, _ := payload["contents"].([]map[string]any)
	for _, content := range contents {
		parts, _ := content["parts"].([]map[string]any)
		signed := false
		for _, part := range parts {
			if signature, _ := part["thoughtSignature"].(string); signature != "" {
				signed = true
			}
		}
		for _, part := range parts {
			if call, ok := part["functionCall"].(map[string]any); ok {
				if a.antigravity() && strings.HasPrefix(model, "claude") || (!signed && part["thoughtSignature"] == nil) {
					part["thoughtSignature"] = "skip_thought_signature_validator"
				}
				if a.antigravity() {
					googleSubscriptionToolID(call)
				}
			}
			if result, ok := part["functionResponse"].(map[string]any); ok && a.antigravity() {
				googleSubscriptionToolID(result)
			}
		}
	}
	return nil
}

func googleSubscriptionToolID(call map[string]any) {
	id, _ := call["id"].(string)
	if id == "" {
		return
	}
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, id)
	if clean != id {
		sum := sha256.Sum256([]byte(id))
		call["id"] = clean + "_" + hex.EncodeToString(sum[:4])
	}
}
