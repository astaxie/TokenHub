package server

import "encoding/json"

// Protocol field names are public schema, even when a short credential happens
// to match one. Unknown extension keys and all provider-controlled string values
// still undergo the ordinary credential redaction.
var mediaTaskProtocolKeys = map[string]bool{
	"data": true, "code": true, "message": true, "id": true, "task_id": true,
	"object": true, "poll_url": true, "type": true, "status": true, "progress": true,
	"result": true, "error": true, "cost": true, "credits_cost": true,
	"created_at": true, "updated_at": true, "completed_at": true, "expires_at": true,
	"images": true, "videos": true, "url": true, "b64_json": true,
}

func redactMediaTaskResult(result *mediaResponse, provider Provider) error {
	var raw map[string]json.RawMessage
	if json.Unmarshal(result.Body, &raw) != nil {
		return invalidMediaTaskEnvelope()
	}
	envelope, err := parseMediaTaskEnvelope(raw, true)
	if err != nil || len(envelope.tasks) != 1 {
		return invalidMediaTaskEnvelope()
	}
	var payload map[string]any
	if decodeResponsesJSON(result.Body, &payload) != nil {
		return invalidMediaTaskEnvelope()
	}
	item := envelope.tasks[0]
	fields := payload
	if envelope.container == "object" {
		fields = payload["data"].(map[string]any)
	}
	protected := map[string]any{}
	protect := func(name string) {
		if value, exists := fields[name]; exists {
			protected[name] = value
			delete(fields, name)
		}
	}
	for _, name := range item.idFields {
		protect(name)
	}
	protect("poll_url")
	if item.versioned {
		protect("object")
	}
	// Known status constants are protocol data, not an echoed credential.
	switch fields["status"] {
	case "submitted", "pending", "processing", "completed", "failed", "cancelled":
		protect("status")
	}
	redacted, _ := redactProviderJSONValue(payload, providerErrorSecretValues(provider), mediaTaskProtocolKeys)
	payload = redacted.(map[string]any)
	fields = payload
	if envelope.container == "object" {
		fields = payload["data"].(map[string]any)
	}
	for name, value := range protected {
		fields[name] = value
	}
	result.Body, err = json.Marshal(payload)
	return err
}
