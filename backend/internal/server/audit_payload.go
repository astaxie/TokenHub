package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
	"unicode/utf8"
)

const (
	defaultAuditPayloadMaxBytes   = 64 * 1024
	defaultAuditPayloadScanBudget = 256 * 1024
	defaultAuditPayloadScanKB     = 256
	defaultAuditPayloadMaxKB      = 64
	// The scan budget multiplies per-request rendering copies by concurrency,
	// so it gets a hard ceiling even though the operator can raise it.
	maximumAuditPayloadScanKB = 4096
	auditPayloadMaxDepth      = 64
	auditPayloadOmittedReason = "payload_exceeds_audit_limit"
)

// auditPayloadOmitted marks the snapshot of a body that the scan budget
// rejected. The active limit and the setting that raises it travel with the
// marker, so an operator reading the stored snapshot sees the fix next to the
// symptom.
func auditPayloadOmitted(limits auditPayloadLimits) string {
	return fmt.Sprintf(`{"omitted":%q,"scan_kb":%d,"adjust":"TOKENHUB_AUDIT_PAYLOAD_SCAN_KB"}`, auditPayloadOmittedReason, limits.scanBytes/1024)
}

// auditPayloadLimits bounds the work spent rendering one audit snapshot and the
// size of the inline preview kept in request_payload_logs. Both values come
// from TOKENHUB_AUDIT_PAYLOAD_SCAN_KB / TOKENHUB_AUDIT_PAYLOAD_MAX_KB.
type auditPayloadLimits struct {
	scanBytes int
	maxBytes  int
}

func defaultAuditPayloadLimits() auditPayloadLimits {
	return auditPayloadLimits{scanBytes: defaultAuditPayloadScanBudget, maxBytes: defaultAuditPayloadMaxBytes}
}

// renderAuditPayload produces the redacted, indented snapshot for value under
// the given limits. Oversized inputs are still rejected before serialization,
// but a body that only exceeds maxBytes is returned in full: the store keeps
// large snapshots as content-addressed chunks and stores only a preview in the
// legacy column.
func renderAuditPayload(value any, limits auditPayloadLimits) (string, bool) {
	if value == nil {
		return "", false
	}
	// Reject large bodies before marshaling, decoding, and redacting them. A
	// final output cap alone still creates several full copies of a multimodal
	// request. The bounded scan also stops very wide or cyclic input graphs.
	budget := limits.scanBytes
	if !auditPayloadFits(reflect.ValueOf(value), &budget, 0) {
		return auditPayloadOmitted(limits), true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return `{"error":"payload_not_serializable"}`, false
	}
	if len(raw) > limits.scanBytes {
		return auditPayloadOmitted(limits), true
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return `{"error":"payload_not_serializable"}`, false
	}
	// RawMessage and custom marshalers can hide nested structures from the
	// first scan. Check the normalized graph before recursive redaction and
	// indentation, whose output otherwise grows quadratically with depth.
	budget = limits.scanBytes
	if !auditPayloadFits(reflect.ValueOf(decoded), &budget, 0) {
		return auditPayloadOmitted(limits), true
	}
	raw, err = json.MarshalIndent(redactAuditValue(decoded), "", "  ")
	if err != nil {
		return `{"error":"payload_not_serializable"}`, false
	}
	return string(raw), len(raw) > limits.maxBytes
}

// truncateAuditPreview caps a rendered snapshot at limits.maxBytes, preserving
// UTF-8 boundaries. It keeps the byte budget of the inline request_body /
// response_body columns no matter how large the configured scan budget is.
func truncateAuditPreview(body string, limits auditPayloadLimits) string {
	if len(body) <= limits.maxBytes {
		return body
	}
	const suffix = "\n... truncated"
	end := limits.maxBytes - len(suffix)
	for end > 0 && !utf8.RuneStart(body[end]) {
		end--
	}
	return body[:end] + suffix
}

// auditPayloadBody renders with the built-in default limits and truncates to
// the default preview cap. Gateway code uses the limits-aware variants on the
// server instead; this entry point stays for the documented default behavior.
func auditPayloadBody(value any) (string, bool) {
	limits := defaultAuditPayloadLimits()
	body, truncated := renderAuditPayload(value, limits)
	return truncateAuditPreview(body, limits), truncated
}

// auditPayloadFits estimates input size and traversal work, not encoded JSON
// size. Keep the final byte cap as well: escaping and indentation expand JSON.
// Count sensitive values too because redaction first normalizes through JSON.
func auditPayloadFits(value reflect.Value, budget *int, depth int) bool {
	*budget -= 8
	if *budget < 0 || depth > auditPayloadMaxDepth {
		return false
	}
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		return value.IsNil() || auditPayloadFits(value.Elem(), budget, depth+1)
	case reflect.String:
		*budget -= value.Len()
	case reflect.Slice, reflect.Array:
		// Byte slices (including RawMessage) otherwise take one reflection step
		// per byte even though their length alone answers the size question.
		if value.Type().Elem().Kind() == reflect.Uint8 {
			*budget -= value.Len()
			break
		}
		for index := 0; index < value.Len(); index++ {
			if !auditPayloadFits(value.Index(index), budget, depth+1) {
				return false
			}
		}
	case reflect.Map:
		iterator := value.MapRange()
		for iterator.Next() {
			if !auditPayloadFits(iterator.Key(), budget, depth+1) || !auditPayloadFits(iterator.Value(), budget, depth+1) {
				return false
			}
		}
	case reflect.Struct:
		if value.Type() == reflect.TypeFor[time.Time]() {
			*budget -= 64
			break
		}
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			// Custom request marshalers also serialize private raw extension
			// fields. Include those and json:"-" fields in the input budget.
			*budget -= len(field.Name) + len(field.Tag)
			if !auditPayloadFits(value.Field(index), budget, depth+1) {
				return false
			}
		}
	}
	return *budget >= 0
}
