package server

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

type RerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            *int     `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
	Instruction     string   `json:"instruction,omitempty"`
	Truncation      *bool    `json:"truncation,omitempty"`
}

func (r *RerankRequest) UnmarshalJSON(data []byte) error {
	type wire RerankRequest
	var value wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*r = RerankRequest(value)
	return nil
}
func validateRerankRequest(r RerankRequest) error {
	if strings.TrimSpace(r.Model) == "" || strings.TrimSpace(r.Query) == "" {
		return NewHTTPError(400, "invalid_rerank_request", "model and query are required")
	}
	if len(r.Documents) == 0 || len(r.Documents) > 2048 {
		return NewHTTPError(400, "invalid_rerank_request", "documents must contain between 1 and 2048 text strings")
	}
	for _, doc := range r.Documents {
		if strings.TrimSpace(doc) == "" {
			return NewHTTPError(400, "invalid_rerank_request", "documents must not contain empty text")
		}
	}
	if r.TopN != nil && (*r.TopN < 1 || *r.TopN > len(r.Documents)) {
		return NewHTTPError(400, "invalid_rerank_request", "top_n must be between 1 and the document count")
	}
	return nil
}
func rerankGuardrailTargets(r *RerankRequest) []guardrailTextTarget {
	targets := []guardrailTextTarget{}
	appendGuardrailStringTarget(&targets, r.Query, "query", func(value any) { r.Query = guardrailStringValue(value) })
	for i, doc := range r.Documents {
		appendGuardrailStringTarget(&targets, doc, "documents."+strconv.Itoa(i), func(value any) { r.Documents[i] = guardrailStringValue(value) })
	}
	if r.Instruction != "" {
		appendGuardrailStringTarget(&targets, r.Instruction, "instruction", func(value any) { r.Instruction = guardrailStringValue(value) })
	}
	return targets
}
func applyRerankPatch(r *RerankRequest, data json.RawMessage) error {
	var next RerankRequest
	if err := decodeGatewayHookRequestPatch(data, &next); err != nil {
		return err
	}
	if next.Model != r.Model || len(next.Documents) != len(r.Documents) || next.ReturnDocuments != r.ReturnDocuments || !reflect.DeepEqual(next.TopN, r.TopN) {
		return NewHTTPError(502, "gateway_hook_patch_invalid", "Rerank plugins cannot change model, document cardinality, top_n or return_documents")
	}
	if err := validateRerankRequest(next); err != nil {
		return err
	}
	*r = next
	return nil
}
func normalizeRerankResponse(raw any, profile string, r RerankRequest) (map[string]any, error) {
	fail := func() (map[string]any, error) {
		return nil, NewHTTPError(502, "invalid_rerank_response", "Upstream returned invalid ranking indices or scores")
	}
	body, ok := raw.(map[string]any)
	var items []any
	if profile == "tei" {
		items, _ = raw.([]any)
		body = map[string]any{}
	} else {
		if !ok {
			return fail()
		}
		source := body
		if profile == "dashscope" {
			source, _ = body["output"].(map[string]any)
		}
		if profile == "voyage" {
			items, _ = source["data"].([]any)
		} else {
			items, _ = source["results"].([]any)
		}
	}
	expected := len(r.Documents)
	if r.TopN != nil {
		expected = *r.TopN
	}
	if len(items) < expected || len(items) > len(r.Documents) {
		return fail()
	}
	type result struct {
		Index    int               `json:"index"`
		Score    float64           `json:"relevance_score"`
		Document map[string]string `json:"document,omitempty"`
	}
	results := make([]result, 0, len(items))
	seen := map[int]bool{}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			return fail()
		}
		index, ok := entry["index"].(float64)
		if !ok || math.Trunc(index) != index || index < 0 || index >= float64(len(r.Documents)) {
			return fail()
		}
		key := "relevance_score"
		if profile == "tei" {
			key = "score"
		}
		score, ok := entry[key].(float64)
		if !ok || math.IsNaN(score) || math.IsInf(score, 0) || seen[int(index)] {
			return fail()
		}
		seen[int(index)] = true
		result := result{Index: int(index), Score: score}
		if r.ReturnDocuments {
			result.Document = map[string]string{"text": r.Documents[int(index)]}
		}
		results = append(results, result)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	results = results[:expected]
	normalized := map[string]any{"model": r.Model, "results": results}
	if usage, exists := body["usage"]; exists {
		normalized["usage"] = usage
	}
	if meta, exists := body["meta"]; exists {
		normalized["meta"] = meta
	}
	return normalized, nil
}

func validateRerankResult(response any, request RerankRequest) (any, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	var raw any
	if err = json.Unmarshal(encoded, &raw); err != nil {
		return nil, err
	}
	// Validation after safety hooks must not reconstruct documents from input.
	check := request
	check.ReturnDocuments = false
	if _, err := normalizeRerankResponse(raw, "jina", check); err != nil {
		return nil, err
	}
	body := raw.(map[string]any)
	items := body["results"].([]any)
	expected := len(request.Documents)
	if request.TopN != nil {
		expected = *request.TopN
	}
	if len(items) != expected || body["model"] != request.Model {
		return nil, NewHTTPError(502, "invalid_rerank_response", "Final rerank response does not match the request")
	}
	previousScore := math.Inf(1)
	for _, value := range items {
		item := value.(map[string]any)
		score := item["relevance_score"].(float64)
		if score > previousScore {
			return nil, NewHTTPError(502, "invalid_rerank_response", "Final rerank results must be ordered by descending relevance_score")
		}
		previousScore = score
		if _, exists := item["document"]; exists && !request.ReturnDocuments {
			return nil, NewHTTPError(502, "invalid_rerank_response", "Final rerank response cannot include documents unless requested")
		}
		if document, exists := item["document"]; exists && document != nil {
			fields, ok := document.(map[string]any)
			if !ok {
				return nil, NewHTTPError(502, "invalid_rerank_response", "Invalid rerank document")
			}
			if text, exists := fields["text"]; exists {
				if _, ok := text.(string); !ok {
					return nil, NewHTTPError(502, "invalid_rerank_response", "Invalid rerank document text")
				}
			}
		}
	}
	return response, nil
}
