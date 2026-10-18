package server

import (
	"encoding/json"
	"strconv"
	"strings"
)

type mediaTaskObject struct {
	fields    map[string]json.RawMessage
	idFields  []string
	location  string
	versioned bool
}

type mediaTaskEnvelope struct {
	tasks     []mediaTaskObject
	container string
	data      []map[string]json.RawMessage
}

func invalidMediaTaskEnvelope() error {
	return NewHTTPError(502, "invalid_media_task", "Provider returned an invalid or ambiguous media task envelope")
}

// Only documented task positions carry routing identity. An ordinary image's
// id and identifiers inside result/vendor objects remain provider data.
func inspectMediaTaskObject(fields map[string]json.RawMessage, location string, poll bool) (mediaTaskObject, error) {
	item := mediaTaskObject{fields: fields, location: location}
	var object string
	_ = json.Unmarshal(fields["object"], &object)
	item.versioned = object == "generation.task"
	if _, exists := fields["task_id"]; exists {
		item.idFields = append(item.idFields, "task_id")
	}
	if poll || item.versioned {
		if _, exists := fields["id"]; exists {
			item.idFields = append(item.idFields, "id")
		} else if item.versioned {
			return item, invalidMediaTaskEnvelope()
		}
	}
	return item, nil
}

func parseMediaTaskEnvelope(payload map[string]json.RawMessage, poll bool) (mediaTaskEnvelope, error) {
	var envelope mediaTaskEnvelope
	root, err := inspectMediaTaskObject(payload, "root", poll)
	if err != nil {
		return envelope, err
	}
	if len(root.idFields) > 0 {
		envelope.tasks = append(envelope.tasks, root)
	}
	raw, nested := payload["data"]
	if !nested {
		return envelope, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil && object != nil {
		envelope.container, envelope.data = "object", []map[string]json.RawMessage{object}
	} else if !poll {
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return envelope, nil
		}
		envelope.container = "array"
		for _, rawItem := range items {
			var item map[string]json.RawMessage
			if json.Unmarshal(rawItem, &item) != nil {
				item = nil
			}
			envelope.data = append(envelope.data, item)
		}
	} else {
		return envelope, invalidMediaTaskEnvelope()
	}
	var children []mediaTaskObject
	malformed := false
	for i, fields := range envelope.data {
		if fields == nil {
			malformed = true
			continue
		}
		location := "data"
		if envelope.container == "array" {
			location += "[" + strconv.Itoa(i) + "]"
		}
		item, err := inspectMediaTaskObject(fields, location, poll)
		if err != nil {
			return envelope, err
		}
		if len(item.idFields) > 0 {
			children = append(children, item)
		}
	}
	if len(children) > 0 && (len(envelope.tasks) > 0 || malformed) || poll && (len(envelope.tasks) > 0 || len(children) != 1) {
		return envelope, invalidMediaTaskEnvelope()
	}
	if len(children) > 0 {
		envelope.tasks = children
	} else {
		// No nested tasks need to be rewritten. Preserve arbitrary sync data.
		envelope.container = ""
	}
	return envelope, nil
}

func (item mediaTaskObject) upstreamID() (string, error) {
	var identifier string
	for _, field := range item.idFields {
		id, valid := mediaTaskIdentifier(item.fields[field])
		if !valid || identifier != "" && identifier != id {
			return "", invalidMediaTaskEnvelope()
		}
		identifier = id
	}
	return identifier, nil
}

func (item mediaTaskObject) rewrite(id string) {
	for _, field := range item.idFields {
		setRawJSONField(item.fields, field, id, true)
	}
	if _, exists := item.fields["poll_url"]; exists {
		setRawJSONField(item.fields, "poll_url", "/v1/tasks/"+id, true)
	}
}

func (envelope mediaTaskEnvelope) write(payload map[string]json.RawMessage) {
	switch envelope.container {
	case "array":
		setRawJSONField(payload, "data", envelope.data, true)
	case "object":
		setRawJSONField(payload, "data", envelope.data[0], true)
	}
}

// Compare semantic string identities, including their envelope locations. JSON
// escaping may change in hooks, but handles and their polling URLs must not.
func mediaTaskResponseIdentity(response mediaResponse, poll bool) string {
	if !mediaResponseIsJSON(response.ContentType) {
		return ""
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(response.Body, &payload) != nil {
		return "invalid"
	}
	envelope, err := parseMediaTaskEnvelope(payload, poll)
	if err != nil {
		return "invalid"
	}
	var identities []string
	for _, item := range envelope.tasks {
		fields := append([]string(nil), item.idFields...)
		if item.versioned {
			fields = append(fields, "object")
		}
		if _, exists := item.fields["poll_url"]; exists {
			fields = append(fields, "poll_url")
		}
		values := map[string]string{"location": item.location}
		for _, field := range fields {
			var value *string
			if json.Unmarshal(item.fields[field], &value) != nil || value == nil {
				return "invalid"
			}
			values[field] = *value
		}
		encoded, _ := json.Marshal(values)
		identities = append(identities, string(encoded))
	}
	return strings.Join(identities, "\x00")
}
