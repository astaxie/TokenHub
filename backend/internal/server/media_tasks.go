package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxMediaTaskBindingsPerSubmission = 128

func (s *Server) handleMediaTask(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := r.PathValue("task_id")
	if !strings.HasPrefix(id, "task_") || !validMediaTaskID(id) {
		writeError(w, r, mediaTaskNotFound())
		return
	}
	query, err := parseMediaTaskQuery(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	binding, upstreamID, err := s.loadOwnedMediaTask(r.Context(), id, project, key)
	if err != nil {
		writeError(w, r, err)
		return
	}
	fields := map[string]json.RawMessage{}
	setRawJSONField(fields, "model", binding.Model, true)
	setRawJSONField(fields, "task_id", id, true)
	if query != "" {
		setRawJSONField(fields, "language", query, true)
	}
	s.handleAdmittedMedia(w, r, project, key, mediaRequest{Fields: fields, Path: r.URL.Path, Task: &binding, UpstreamTaskID: upstreamID, TaskLanguage: query})
}

func parseMediaTaskQuery(r *http.Request) (string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", NewHTTPError(400, "invalid_task_query", "Invalid task query parameters")
	}
	for key, values := range query {
		if key != "language" || len(values) != 1 {
			return "", NewHTTPError(400, "invalid_task_query", "Only one language query parameter is supported")
		}
	}
	language := query.Get("language")
	switch language {
	case "", "en", "zh", "ja", "ko", "ru", "fr", "de", "id", "pt", "es":
		return language, nil
	default:
		return "", NewHTTPError(400, "invalid_task_query", "Unsupported task error language")
	}
}

func validMediaTaskID(id string) bool {
	if id == "" || len(id) > 512 || id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c)) {
			return false
		}
	}
	return true
}

func mediaTaskNotFound() error {
	return NewHTTPError(404, "media_task_not_found", "Media task not found")
}
func mediaTaskStorageError() error {
	return NewHTTPError(503, "media_task_binding_unavailable", "Media task routing could not be retained or loaded")
}
func mediaTaskRouteUnavailable() error {
	return &ProviderInvocationError{Err: NewHTTPError(503, "media_task_route_unavailable", "The original media task provider account is unavailable or has changed"), Disposition: ProviderErrorPolicy}
}

func (s *Server) mediaTaskIdentity(route RouteSelection) string {
	// Hash credentials with the server key. A replaced account or endpoint must
	// never receive a task belonging to the previous account, even with reused IDs.
	data, _ := json.Marshal(struct {
		Type, BaseURL, APIKey string
		Headers, Options      map[string]string
	}{route.Provider.Type, route.Provider.BaseURL, route.Provider.APIKey, route.Provider.Headers, route.Provider.Options})
	return deriveSessionAffinityKey(s.config.SecretKey, route.Provider.ID, "media-task-account\x00"+string(data))
}

func mediaTaskMatchesRoute(task mediaTaskBinding, route RouteSelection) bool {
	return task.RouteID == route.Route.ID && task.ProviderID == route.Provider.ID && task.ResourceID == routeResourceID(route) && task.ProviderModel == route.ProviderModel
}

// Inspect only documented task envelope locations; asset IDs and arbitrary
// vendor extension identifiers must remain untouched.
func mediaTaskObjects(payload map[string]json.RawMessage) []map[string]json.RawMessage {
	if _, ok := payload["task_id"]; ok {
		return []map[string]json.RawMessage{payload}
	}
	var items []map[string]json.RawMessage
	if json.Unmarshal(payload["data"], &items) == nil {
		return items
	}
	var item map[string]json.RawMessage
	if json.Unmarshal(payload["data"], &item) == nil && item != nil {
		return []map[string]json.RawMessage{item}
	}
	return nil
}

func writeMediaTaskObjects(payload map[string]json.RawMessage, objects []map[string]json.RawMessage) {
	if _, ok := payload["task_id"]; ok {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(string(payload["data"])), "[") {
		setRawJSONField(payload, "data", objects, true)
	} else if len(objects) == 1 {
		setRawJSONField(payload, "data", objects[0], true)
	}
}

func mediaTaskIdentifier(raw json.RawMessage) (string, bool) {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id, validMediaTaskID(id)
	}
	var number json.Number
	if json.Unmarshal(raw, &number) == nil {
		id = number.String()
		for _, char := range id {
			if char < '0' || char > '9' {
				return "", false
			}
		}
		return id, validMediaTaskID(id)
	}
	return "", false
}

func (s *Server) bindMediaSubmission(r *http.Request, call CallContext, route RouteSelection, result *mediaResponse) error {
	if !mediaTaskSubmissionPath(r.URL.Path) {
		return nil
	}
	if !mediaResponseIsJSON(result.ContentType) {
		return nil
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(result.Body, &payload) != nil {
		return NewHTTPError(502, "invalid_media_response", "Provider returned invalid media task JSON")
	}
	objects := mediaTaskObjects(payload)
	var bindings []mediaTaskBinding
	var ids []string
	for _, item := range objects {
		raw, found := item["task_id"]
		if !found {
			continue
		}
		if len(bindings) >= maxMediaTaskBindingsPerSubmission {
			return NewHTTPError(502, "invalid_media_task", "Provider returned too many media tasks")
		}
		upstreamID, valid := mediaTaskIdentifier(raw)
		if !valid {
			return NewHTTPError(502, "invalid_media_task", "Provider returned an invalid task identifier")
		}
		binding := mediaTaskBinding{ID: NewID("task"), ProjectID: call.Project.ID, APIKeyID: call.Key.ID, AttributedUserID: call.AttributedUserID, Model: call.Model.Name, RouteID: route.Route.ID, ProviderID: route.Provider.ID, ProviderModel: route.ProviderModel, ResourceID: routeResourceID(route), IdentityHash: s.mediaTaskIdentity(route), ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix()}
		bindings = append(bindings, binding)
		ids = append(ids, upstreamID)
		setRawJSONField(item, "task_id", binding.ID, true)
	}
	if len(bindings) == 0 {
		return nil
	}
	store, ok := s.store.(mediaTaskStore)
	if !ok {
		return mediaTaskStorageError()
	}
	if err := store.SaveMediaTaskBindings(r.Context(), bindings, ids); err != nil {
		return mediaTaskStorageError()
	}
	writeMediaTaskObjects(payload, objects)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	result.Body = body
	return nil
}

func rewriteMediaTaskResult(result *mediaResponse, task mediaTaskBinding, upstreamID string) error {
	if !mediaResponseIsJSON(result.ContentType) {
		return NewHTTPError(502, "invalid_media_task", "Provider returned a non-JSON task result")
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(result.Body, &payload) != nil {
		return NewHTTPError(502, "invalid_media_task", "Provider returned an invalid task result")
	}
	var item map[string]json.RawMessage
	if raw, nested := payload["data"]; nested {
		if json.Unmarshal(raw, &item) != nil || item == nil {
			return NewHTTPError(502, "invalid_media_task", "Provider returned an invalid task result object")
		}
	} else {
		item = payload
	}
	hasID := false
	for _, field := range []string{"id", "task_id"} {
		if raw, found := item[field]; found {
			hasID = true
			id, valid := mediaTaskIdentifier(raw)
			if !valid || id != upstreamID {
				return NewHTTPError(502, "invalid_media_task", "Provider returned a mismatched task identifier")
			}
			setRawJSONField(item, field, task.ID, true)
		}
	}
	if !hasID {
		return NewHTTPError(502, "invalid_media_task", "Provider returned a task result without an identifier")
	}
	if _, nested := payload["data"]; nested {
		setRawJSONField(payload, "data", item, true)
	}
	body, err := json.Marshal(payload)
	result.Body = body
	return err
}

// Response hooks may transform content, but cannot forge, drop, or rebind the
// task handles produced by the provider call and persisted by the gateway.
func mediaTaskResponseIdentity(response mediaResponse, poll bool) string {
	if !mediaResponseIsJSON(response.ContentType) {
		return ""
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(response.Body, &payload) != nil {
		return ""
	}
	var ids []string
	if poll {
		var item map[string]json.RawMessage
		if json.Unmarshal(payload["data"], &item) != nil || item == nil {
			item = payload
		}
		for _, name := range []string{"id", "task_id"} {
			if id, found := item[name]; found {
				ids = append(ids, name+":"+string(id))
			}
		}
	} else {
		for _, item := range mediaTaskObjects(payload) {
			if id, found := item["task_id"]; found {
				ids = append(ids, string(id))
			}
		}
	}
	return strings.Join(ids, "\x00")
}

func mediaTaskSubmissionPath(path string) bool {
	switch path {
	case "/v1/videos/generations", "/v1/images/generations", "/v1/images/edits", "/v1/images/variations":
		return true
	default:
		return false
	}
}
