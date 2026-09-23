package server

import (
	"encoding/json"
	"strings"
)

func backgroundJobAuditSnapshot(payload json.RawMessage) string {
	if len(payload) == 0 {
		return ""
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return auditSnapshotJSON(map[string]any{"error": "background_job_payload_not_serializable"})
	}
	return auditSnapshotJSON(decoded)
}

func backgroundJobRedactErrorText(payload json.RawMessage, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if containsSensitiveBackgroundJobText(text) {
		return "[redacted]"
	}
	for _, value := range backgroundJobStringValues(payload) {
		if value == "" {
			continue
		}
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	if containsSensitiveBackgroundJobText(text) {
		return "[redacted]"
	}
	return text
}

func backgroundJobStringValues(payload json.RawMessage) []string {
	if len(payload) == 0 {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return nil
	}
	values := []string{}
	collectBackgroundJobStringValues(&values, decoded)
	return values
}

func collectBackgroundJobStringValues(values *[]string, current any) {
	switch typed := current.(type) {
	case map[string]any:
		for _, child := range typed {
			collectBackgroundJobStringValues(values, child)
		}
	case []any:
		for _, child := range typed {
			collectBackgroundJobStringValues(values, child)
		}
	case string:
		*values = append(*values, strings.TrimSpace(typed))
	}
}

func containsSensitiveBackgroundJobText(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}
	for _, needle := range []string{"secret", "token", "password", "authorization", "cookie", "private_key", "credential"} {
		if strings.Contains(normalized, needle) {
			return true
		}
	}
	return false
}
