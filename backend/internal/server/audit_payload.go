package server

import (
	"encoding/json"
	"reflect"
	"time"
	"unicode/utf8"
)

const (
	auditPayloadMaxBytes   = 8 * 1024
	auditPayloadScanBudget = 64 * 1024
	auditPayloadMaxDepth   = 64
	auditPayloadOmitted    = `{"omitted":"payload_exceeds_audit_limit"}`
)

func auditPayloadBody(value any) (string, bool) {
	if value == nil {
		return "", false
	}
	// Reject large bodies before marshaling, decoding, and redacting them. A
	// final output cap alone still creates several full copies of a multimodal
	// request. The bounded scan also stops very wide or cyclic input graphs.
	budget := auditPayloadScanBudget
	if !auditPayloadFits(reflect.ValueOf(value), &budget, 0) {
		return auditPayloadOmitted, true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return `{"error":"payload_not_serializable"}`, false
	}
	if len(raw) > auditPayloadScanBudget {
		return auditPayloadOmitted, true
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return `{"error":"payload_not_serializable"}`, false
	}
	// RawMessage and custom marshalers can hide nested structures from the
	// first scan. Check the normalized graph before recursive redaction and
	// indentation, whose output otherwise grows quadratically with depth.
	budget = auditPayloadScanBudget
	if !auditPayloadFits(reflect.ValueOf(decoded), &budget, 0) {
		return auditPayloadOmitted, true
	}
	raw, err = json.MarshalIndent(redactAuditValue(decoded), "", "  ")
	if err != nil {
		return `{"error":"payload_not_serializable"}`, false
	}
	if len(raw) <= auditPayloadMaxBytes {
		return string(raw), false
	}
	const suffix = "\n... truncated"
	end := auditPayloadMaxBytes - len(suffix)
	for end > 0 && !utf8.RuneStart(raw[end]) {
		end--
	}
	return string(raw[:end]) + suffix, true
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
