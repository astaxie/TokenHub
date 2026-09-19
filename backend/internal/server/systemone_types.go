package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// These are gateway resource limits, independently of the model's token budget.
const (
	systemOneMaxQuestions = 1024
	systemOneMaxCriteria  = 4096
)

type SystemOneRequest struct {
	Model       string                       `json:"model"`
	State       json.RawMessage              `json:"state"`
	Questions   map[string]SystemOneQuestion `json:"questions"`
	keyRedacted bool
}

type SystemOneQuestion struct {
	Type         string          `json:"type"`
	Instructions json.RawMessage `json:"instructions,omitempty"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type SystemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   SystemOneUsage             `json:"usage"`
}

type SystemOneUsage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
}

type SystemOneInvoker interface {
	SystemOne(context.Context, Provider, string, SystemOneRequest) (SystemOneResponse, Usage, error)
}

func (r *SystemOneRequest) UnmarshalJSON(data []byte) error {
	type wire SystemOneRequest
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid System One request: %w", err)
	}
	*r = SystemOneRequest(value)
	return nil
}

func (r SystemOneRequest) validate() error {
	if strings.TrimSpace(r.Model) == "" {
		return NewHTTPError(http.StatusBadRequest, "missing_model", "model is required")
	}
	if !systemOneEntry(r.State, true) {
		return systemOneInvalid("state must be a string, object, array, or null")
	}
	if len(r.Questions) == 0 || len(r.Questions) > systemOneMaxQuestions {
		return systemOneInvalid("questions must contain between 1 and 1024 entries")
	}
	for _, question := range r.Questions {
		if len(question.Instructions) != 0 && !systemOneEntry(question.Instructions, true) {
			return systemOneInvalid("instructions must be a string, object, array, or null")
		}
		switch question.Type {
		case "choice", "noul":
			if question.Type == "noul" && (len(question.Criteria) == 0 || bytes.Equal(bytes.TrimSpace(question.Criteria), []byte("null"))) {
				continue
			}
			var criteria map[string]json.RawMessage
			if err := json.Unmarshal(question.Criteria, &criteria); err != nil || criteria == nil || len(criteria) > systemOneMaxCriteria {
				return systemOneInvalid("choice and noul criteria must be objects with at most 4096 entries")
			}
			if question.Type == "choice" && len(criteria) == 0 {
				return systemOneInvalid("choice criteria must not be empty")
			}
			for key, value := range criteria {
				if question.Type == "noul" && key != "true" && key != "false" {
					return systemOneInvalid("noul criteria only supports true and false")
				}
				if !systemOneEntry(value, true) {
					return systemOneInvalid("criteria descriptions must be strings, objects, arrays, or null")
				}
			}
		case "score":
			var criteria []json.RawMessage
			if err := json.Unmarshal(question.Criteria, &criteria); err != nil || len(criteria) < 2 || len(criteria) > systemOneMaxCriteria {
				return systemOneInvalid("score criteria must contain between 2 and 4096 levels")
			}
			for _, value := range criteria {
				if !systemOneEntry(value, true) {
					return systemOneInvalid("score levels must be strings, objects, arrays, or null")
				}
			}
		default:
			return systemOneInvalid("question type must be choice, noul, or score")
		}
	}
	return nil
}

func systemOneEntry(data json.RawMessage, nullable bool) bool {
	data = bytes.TrimSpace(data)
	if !json.Valid(data) || len(data) == 0 || !systemOneJSONDepthAllowed(data) {
		return false
	}
	return data[0] == '"' || data[0] == '{' || data[0] == '[' || nullable && bytes.Equal(data, []byte("null"))
}

// Bound traversal and redaction work for deeply nested caller-controlled JSON.
func systemOneJSONDepthAllowed(data []byte) bool {
	depth, quoted, escaped := 0, false, false
	for _, char := range data {
		if quoted {
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				quoted = false
			}
			continue
		}
		switch char {
		case '"':
			quoted = true
		case '{', '[':
			depth++
			if depth > 64 {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}

func systemOneInvalid(message string) error {
	return NewHTTPError(http.StatusUnprocessableEntity, "invalid_systemone_request", message)
}

func applySystemOneRequestPatch(req *SystemOneRequest, data json.RawMessage) error {
	var patched SystemOneRequest
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	if patched.Model != req.Model {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	if err := patched.validate(); err != nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid System One request")
	}
	*req = patched
	return nil
}

func systemOneTokenReservation(req SystemOneRequest) int64 {
	// Estimate the shared state once, plus every rubric and the bounded answer shape.
	// This is quota admission only; final billing uses upstream usage.
	total := saturatingAddNonNegative(estimateRawJSONTokens(req.State), estimateJSONTokens(req.Questions))
	for _, question := range req.Questions {
		total = saturatingAddNonNegative(total, saturatingAddNonNegative(32, estimateRawJSONTokens(question.Criteria)))
	}
	return total
}
