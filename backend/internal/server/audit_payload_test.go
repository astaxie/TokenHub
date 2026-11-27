package server

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAuditPayloadPreservesSmallRedactedJSON(t *testing.T) {
	payload := map[string]any{
		"messages":      []any{map[string]any{"content": "Hello", "api_key": "hidden-key"}},
		"authorization": "hidden-token",
		"usage":         map[string]any{"total_tokens": 42},
	}
	body, truncated := auditPayloadBody(payload)
	if truncated || !json.Valid([]byte(body)) || strings.Contains(body, "hidden-") {
		t.Fatalf("invalid redacted payload: truncated=%v body=%s", truncated, body)
	}
	if !strings.Contains(body, `"content": "Hello"`) || strings.Count(body, "[redacted]") != 2 {
		t.Fatalf("redacted payload lost expected fields: %s", body)
	}
	if payload["authorization"] != "hidden-token" {
		t.Fatal("audit rendering mutated the original payload")
	}
}

func TestAuditPayloadCapsEncodedBytesAndPreservesUTF8(t *testing.T) {
	for _, content := range []string{
		strings.Repeat("x", 96*1024),
		strings.Repeat("界", 30000),
		strings.Repeat("\x00", 30000),
	} {
		body, truncated := auditPayloadBody(map[string]any{"content": content, "password": "hidden"})
		if !truncated || len(body) > defaultAuditPayloadMaxBytes || !utf8.ValidString(body) || !strings.HasSuffix(body, "\n... truncated") {
			t.Fatalf("invalid byte-limited snapshot: bytes=%d truncated=%v valid_utf8=%v", len(body), truncated, utf8.ValidString(body))
		}
		if strings.Contains(body, "hidden") {
			t.Fatal("truncated snapshot leaked a credential")
		}
	}
}

func TestAuditPayloadOmitsLargeOrComplexInputsBeforeSerialization(t *testing.T) {
	large := strings.Repeat("x", 8*1024*1024)
	cycle := map[string]any{}
	cycle["self"] = cycle
	tests := []struct {
		name  string
		value any
	}{
		{"large string", large},
		{"large map value", map[string]any{"content": large}},
		{"large secret", map[string]any{"api_key": large}},
		{"chat extension", ChatCompletionRequest{raw: map[string]json.RawMessage{"extension": json.RawMessage(`"` + large + `"`)}}},
		{"message extension", ChatMessage{raw: map[string]json.RawMessage{"extension": json.RawMessage(`"` + large + `"`)}}},
		{"responses extension", ResponsesRequest{raw: map[string]json.RawMessage{"extension": json.RawMessage(`"` + large + `"`)}}},
		{"large raw JSON", json.RawMessage(`{"content":"` + large + `"}`)},
		{"deep raw JSON", json.RawMessage(strings.Repeat("[", 5000) + "0" + strings.Repeat("]", 5000))},
		{"deep raw extension", ResponsesRequest{raw: map[string]json.RawMessage{"extension": json.RawMessage(strings.Repeat("[", 5000) + "0" + strings.Repeat("]", 5000))}}},
		{"large struct", struct {
			Content string `json:"content"`
		}{large}},
		{"wide array", make([]any, defaultAuditPayloadScanBudget)},
		{"cycle", cycle},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, truncated := auditPayloadBody(test.value)
			if !truncated || !json.Valid([]byte(body)) {
				t.Fatalf("expected omission marker, got %q, truncated=%v", body, truncated)
			}
			var marker struct {
				Omitted string `json:"omitted"`
				ScanKB  int    `json:"scan_kb"`
				Adjust  string `json:"adjust"`
			}
			if err := json.Unmarshal([]byte(body), &marker); err != nil {
				t.Fatalf("omission marker is not a valid object: %q", body)
			}
			if marker.Omitted != auditPayloadOmittedReason || marker.ScanKB != defaultAuditPayloadScanKB || marker.Adjust != "TOKENHUB_AUDIT_PAYLOAD_SCAN_KB" {
				t.Fatalf("omission marker lost its self-description: %q", body)
			}
		})
	}
}

func TestAuditPayloadNilAndUnserializableInputs(t *testing.T) {
	if body, truncated := auditPayloadBody(nil); body != "" || truncated {
		t.Fatalf("nil payload = %q, %v", body, truncated)
	}
	if body, truncated := auditPayloadBody(make(chan int)); body != `{"error":"payload_not_serializable"}` || truncated {
		t.Fatalf("unserializable payload = %q, %v", body, truncated)
	}
}
