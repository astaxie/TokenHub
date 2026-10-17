package server

import (
	"context"
	"encoding/json"
	"strings"
)

var mediaTaskReferenceFields = []string{"draft_task_id", "draft_from_task_id", "extend_from_task_id", "source_task_id"}

type mediaTaskReference struct {
	Binding    mediaTaskBinding
	UpstreamID string
}

func isMediaTaskReferenceField(name string) bool {
	for _, field := range mediaTaskReferenceFields {
		if name == field {
			return true
		}
	}
	return false
}

func (s *Server) loadOwnedMediaTask(ctx context.Context, id string, project Project, key APIKey) (mediaTaskBinding, string, error) {
	if !strings.HasPrefix(id, "task_") || !validMediaTaskID(id) {
		return mediaTaskBinding{}, "", mediaTaskNotFound()
	}
	store, ok := s.store.(mediaTaskStore)
	if !ok {
		return mediaTaskBinding{}, "", mediaTaskStorageError()
	}
	binding, upstreamID, found, err := store.LoadMediaTaskBinding(ctx, id)
	if err != nil {
		return mediaTaskBinding{}, "", mediaTaskStorageError()
	}
	if !found || !s.mediaTaskAccessible(binding, project, key, usageAttributionUserID(key, project)) {
		return mediaTaskBinding{}, "", mediaTaskNotFound()
	}
	return binding, upstreamID, nil
}

func (s *Server) mediaTaskAccessible(binding mediaTaskBinding, project Project, key APIKey, attributedUserID string) bool {
	return binding.ProjectID == project.ID && binding.APIKeyID == key.ID && binding.AttributedUserID == attributedUserID && keyCanAccessModel(s.store, key, binding.Model)
}

func (s *Server) resolveMediaTaskReferences(ctx context.Context, project Project, key APIKey, request *mediaRequest) error {
	if request.Path != "/v1/videos/generations" {
		return nil
	}
	for _, field := range mediaTaskReferenceFields {
		raw, found := request.Fields[field]
		if !found {
			continue
		}
		var id string
		if json.Unmarshal(raw, &id) != nil {
			return mediaTaskNotFound()
		}
		binding, upstreamID, err := s.loadOwnedMediaTask(ctx, id, project, key)
		if err != nil {
			return err
		}
		for _, previous := range request.TaskReferences {
			if binding.ProviderID != previous.Binding.ProviderID || binding.ResourceID != previous.Binding.ResourceID || binding.IdentityHash != previous.Binding.IdentityHash {
				return NewHTTPError(400, "invalid_media_task_reference", "Media task references must belong to the same provider account")
			}
		}
		if request.TaskReferences == nil {
			request.TaskReferences = make(map[string]mediaTaskReference)
		}
		request.TaskReferences[field] = mediaTaskReference{Binding: binding, UpstreamID: upstreamID}
	}
	return nil
}

func (s *Server) validateMediaTaskOwnership(call CallContext, request mediaRequest) error {
	if request.Task != nil && !s.mediaTaskAccessible(*request.Task, call.Project, call.Key, call.AttributedUserID) {
		return mediaTaskNotFound()
	}
	for _, reference := range request.TaskReferences {
		if !s.mediaTaskAccessible(reference.Binding, call.Project, call.Key, call.AttributedUserID) {
			return mediaTaskNotFound()
		}
	}
	return nil
}

func (m mediaRequest) hasTaskBinding() bool {
	return m.Task != nil || len(m.TaskReferences) > 0
}

func (m mediaRequest) matchesTaskRoute(route RouteSelection) bool {
	if m.Task != nil && !mediaTaskMatchesRoute(*m.Task, route) {
		return false
	}
	for _, reference := range m.TaskReferences {
		if reference.Binding.ProviderID != route.Provider.ID || reference.Binding.ResourceID != routeResourceID(route) {
			return false
		}
	}
	return true
}

func (s *Server) mediaTaskAccountMatches(request mediaRequest, route RouteSelection) bool {
	if !request.hasTaskBinding() {
		return true
	}
	identity := s.mediaTaskIdentity(route)
	if request.Task != nil && request.Task.IdentityHash != identity {
		return false
	}
	for _, reference := range request.TaskReferences {
		if reference.Binding.IdentityHash != identity {
			return false
		}
	}
	return true
}

func (m mediaRequest) validateTaskReferencePatch(fields map[string]json.RawMessage) error {
	if m.Path != "/v1/videos/generations" {
		return nil
	}
	for _, field := range mediaTaskReferenceFields {
		original, present := m.Fields[field]
		patched, patchedPresent := fields[field]
		if present != patchedPresent || string(original) != string(patched) {
			return mediaTaskPatchInvalid("Gateway plugin cannot change media task references")
		}
	}
	return nil
}

func mediaTaskPatchInvalid(message string) error {
	return &ProviderInvocationError{Err: NewHTTPError(502, "gateway_hook_patch_invalid", message), Disposition: ProviderErrorPolicy}
}

// Keep public handles through policy and request transformations. Only the
// provider invocation receives the identifiers for its bound account.
func (m mediaRequest) withUpstreamTaskIdentifiers() mediaRequest {
	if !m.hasTaskBinding() {
		return m
	}
	m.Fields = cloneRawJSON(m.Fields, 0)
	if m.Task != nil {
		setRawJSONField(m.Fields, "task_id", m.UpstreamTaskID, true)
	}
	for field, reference := range m.TaskReferences {
		setRawJSONField(m.Fields, field, reference.UpstreamID, true)
	}
	return m
}
