package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
)

func (s *Server) runGatewaySystemOneDecodeNormalizeHooks(ctx context.Context, call CallContext, headers http.Header, req *SystemOneRequest) error {
	return s.runGatewayDecodeNormalizeHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}

func (s *Server) runGatewaySystemOnePrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, req *SystemOneRequest) error {
	return s.runGatewayPrivacyPreHooks(ctx, call, headers, *req, func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}

func (s *Server) runGatewaySystemOneGuardrailPreHooks(ctx context.Context, call CallContext, req *SystemOneRequest) error {
	return s.runGatewayGuardrailPreHooks(ctx, call, *req, systemOneGuardrailTargets(req), func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}

func (s *Server) runGatewaySystemOneContextOptimizeHooks(ctx context.Context, call CallContext, req *SystemOneRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		return applySystemOneRequestPatch(req, data)
	})
}

func systemOneGuardrailTargets(req *SystemOneRequest) []guardrailTextTarget {
	targets := []guardrailTextTarget{}
	appendSystemOneJSONTargets(&targets, req.State, "state", func(next json.RawMessage) { req.State = next }, &req.keyRedacted)
	ids := make([]string, 0, len(req.Questions))
	for id := range req.Questions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for index, id := range ids {
		question := req.Questions[id]
		prefix := fmt.Sprintf("questions.%d", index)
		// IDs and criteria keys are part of the response contract. Inspect them,
		// but fail closed if a policy asks to redact rather than silently rename.
		appendGuardrailStringTarget(&targets, id, prefix+".id", func(any) { req.keyRedacted = true })
		appendSystemOneJSONTargets(&targets, question.Instructions, prefix+".instructions", func(next json.RawMessage) {
			q := req.Questions[id]
			q.Instructions = next
			req.Questions[id] = q
		}, &req.keyRedacted)
		appendSystemOneJSONTargets(&targets, question.Criteria, prefix+".criteria", func(next json.RawMessage) {
			q := req.Questions[id]
			q.Criteria = next
			req.Questions[id] = q
		}, &req.keyRedacted)
	}
	return targets
}

func appendSystemOneJSONTargets(targets *[]guardrailTextTarget, raw json.RawMessage, id string, set func(json.RawMessage), keyRedacted *bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil && string(raw) != "null" {
		appendGuardrailStringTarget(targets, text, id, func(next any) {
			encoded, _ := json.Marshal(next)
			set(encoded)
		})
		return
	}
	var array []json.RawMessage
	if json.Unmarshal(raw, &array) == nil && array != nil {
		for index, child := range array {
			appendSystemOneJSONTargets(targets, child, fmt.Sprintf("%s.%d", id, index), func(next json.RawMessage) {
				array[index] = next
				encoded, _ := json.Marshal(array)
				set(encoded)
			}, keyRedacted)
		}
		return
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index, key := range keys {
		path := fmt.Sprintf("%s.%d", id, index)
		appendGuardrailStringTarget(targets, key, path+".key", func(any) { *keyRedacted = true })
		appendSystemOneJSONTargets(targets, object[key], path+".value", func(next json.RawMessage) {
			object[key] = next
			encoded, _ := json.Marshal(object)
			set(encoded)
		}, keyRedacted)
	}
}
