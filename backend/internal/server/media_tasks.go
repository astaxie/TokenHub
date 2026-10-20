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
	envelope, err := parseMediaTaskEnvelope(payload, false)
	if err != nil {
		return err
	}
	var bindings []mediaTaskBinding
	var ids []string
	for _, item := range envelope.tasks {
		if len(bindings) >= maxMediaTaskBindingsPerSubmission {
			return NewHTTPError(502, "invalid_media_task", "Provider returned too many media tasks")
		}
		upstreamID, err := item.upstreamID()
		if err != nil {
			return err
		}
		binding := mediaTaskBinding{ID: NewID("task"), ProjectID: call.Project.ID, APIKeyID: call.Key.ID, AttributedUserID: call.AttributedUserID, Model: call.Model.Name, RouteID: route.Route.ID, ProviderID: route.Provider.ID, ProviderModel: route.ProviderModel, ResourceID: routeResourceID(route), IdentityHash: s.mediaTaskIdentity(route), ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix()}
		bindings = append(bindings, binding)
		ids = append(ids, upstreamID)
		item.rewrite(binding.ID)
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
	envelope.write(payload)
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
	envelope, err := parseMediaTaskEnvelope(payload, true)
	if err != nil {
		return err
	}
	if len(envelope.tasks) != 1 {
		return NewHTTPError(502, "invalid_media_task", "Provider returned a task result without an identifier")
	}
	item := envelope.tasks[0]
	id, err := item.upstreamID()
	if err != nil || id != upstreamID {
		return NewHTTPError(502, "invalid_media_task", "Provider returned a mismatched task identifier")
	}
	item.rewrite(task.ID)
	envelope.write(payload)
	body, err := json.Marshal(payload)
	result.Body = body
	return err
}

func mediaTaskSubmissionPath(path string) bool {
	switch path {
	case "/v1/videos/generations", "/v1/images/generations", "/v1/images/edits", "/v1/images/variations":
		return true
	default:
		return false
	}
}
