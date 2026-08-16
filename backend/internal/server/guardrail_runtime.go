package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"tokenhub/backend/internal/guardrails"
)

type guardrailTextTarget struct {
	fragment guardrails.Fragment
	replace  func(string)
}

type guardrailAuditSummary struct {
	Model     string              `json:"model"`
	Guardrail guardrails.Decision `json:"guardrail"`
}

type guardrailClientDetails struct {
	Action            string                       `json:"action"`
	Categories        []string                     `json:"categories"`
	ReasonCodes       []string                     `json:"reason_codes"`
	PolicyMatches     []guardrailClientPolicyMatch `json:"policy_matches"`
	DetectionDegraded bool                         `json:"detection_degraded,omitempty"`
}

type guardrailClientPolicyMatch struct {
	PolicyID          string `json:"policy_id"`
	PolicyName        string `json:"policy_name"`
	DetectionItemID   string `json:"detection_item_id"`
	DetectionItemName string `json:"detection_item_name"`
	DetectorType      string `json:"detector_type"`
	Category          string `json:"category"`
	ReasonCode        string `json:"reason_code"`
}

func (s *Server) evaluateOutboundGuardrails(ctx context.Context, projectID string, targets []guardrailTextTarget) (guardrails.Decision, error) {
	policies, err := s.store.ListGuardrailPolicies()
	if err != nil {
		return guardrails.Decision{}, NewHTTPError(http.StatusInternalServerError, "guardrail_policy_load_failed", "Content security policies could not be loaded")
	}
	fragments := make([]guardrails.Fragment, 0, len(targets))
	for _, target := range targets {
		fragments = append(fragments, target.fragment)
	}
	decision, err := s.guardrailEngine.Evaluate(ctx, guardrails.EvaluationRequest{
		ProjectID:  projectID,
		Checkpoint: guardrails.CheckpointBeforeProvider,
		Protocol:   guardrails.ProtocolAll,
		Fragments:  fragments,
		Policies:   policies,
	})
	if err != nil {
		return decision, guardrailEvaluationError(err)
	}
	for _, target := range targets {
		if replacement, ok := decision.Replacements[target.fragment.ID]; ok && target.replace != nil {
			target.replace(replacement)
		}
	}
	if decision.Action == guardrails.ActionBlock {
		return decision, newGuardrailBlockedError(decision)
	}
	return decision, nil
}

func guardrailEvaluationError(err error) error {
	if errors.Is(err, guardrails.ErrDeterministicWorkBudgetExceeded) {
		return NewHTTPError(http.StatusServiceUnavailable, "guardrail_evaluation_budget_exceeded", "Content security evaluation exceeded its work budget")
	}
	return NewHTTPError(http.StatusInternalServerError, "guardrail_evaluation_failed", "Content security evaluation failed")
}

func newGuardrailBlockedError(decision guardrails.Decision) *HTTPError {
	error := NewHTTPError(http.StatusForbidden, "guardrail_blocked", "Request blocked by a content security policy")
	details := guardrailClientDetails{
		Action: decision.Action, DetectionDegraded: decision.DetectionDegraded,
		Categories: make([]string, 0), ReasonCodes: make([]string, 0), PolicyMatches: make([]guardrailClientPolicyMatch, 0),
	}
	categories := map[string]bool{}
	reasonCodes := map[string]bool{}
	policyMatches := map[string]bool{}
	for _, finding := range decision.Findings {
		if category := strings.TrimSpace(finding.Category); category != "" && !categories[category] {
			categories[category] = true
			details.Categories = append(details.Categories, category)
		}
		if reasonCode := strings.TrimSpace(finding.ReasonCode); reasonCode != "" && !reasonCodes[reasonCode] {
			reasonCodes[reasonCode] = true
			details.ReasonCodes = append(details.ReasonCodes, reasonCode)
		}
		match := guardrailClientPolicyMatch{
			PolicyID: strings.TrimSpace(finding.PolicyID), PolicyName: strings.TrimSpace(finding.PolicyName),
			DetectionItemID: strings.TrimSpace(finding.ItemID), DetectionItemName: strings.TrimSpace(finding.ItemName),
			DetectorType: strings.TrimSpace(finding.DetectorType), Category: strings.TrimSpace(finding.Category),
			ReasonCode: strings.TrimSpace(finding.ReasonCode),
		}
		matchKey := strings.Join([]string{match.PolicyID, match.PolicyName, match.DetectionItemID, match.DetectionItemName, match.DetectorType, match.Category, match.ReasonCode}, "\x00")
		if !policyMatches[matchKey] {
			policyMatches[matchKey] = true
			details.PolicyMatches = append(details.PolicyMatches, match)
		}
	}
	error.Details = details
	return error
}

func guardrailRequestAuditPayload(model string, decision guardrails.Decision, requestPayload any) any {
	if len(decision.Findings) == 0 {
		return requestPayload
	}
	decision.Replacements = nil
	return guardrailAuditSummary{Model: model, Guardrail: decision}
}

func chatGuardrailTargets(request *ChatCompletionRequest) []guardrailTextTarget {
	targets := make([]guardrailTextTarget, 0, len(request.Messages))
	for index := range request.Messages {
		messageIndex := index
		appendGuardrailContentTargets(&targets, request.Messages[index].Content, fmt.Sprintf("messages.%d.content", index), func(value any) {
			request.Messages[messageIndex].Content = value
		})
	}
	return targets
}

func responsesGuardrailTargets(request *ResponsesRequest) []guardrailTextTarget {
	targets := make([]guardrailTextTarget, 0)
	if request.Instructions != "" {
		targets = append(targets, guardrailTextTarget{
			fragment: guardrails.Fragment{ID: "instructions", Text: request.Instructions, Mutable: true},
			replace:  func(value string) { request.Instructions = value },
		})
	}
	appendGuardrailResponseInputTargets(&targets, request.Input, "input", func(value any) { request.Input = value })
	return targets
}

// responsesCompactGuardrailTargets mirrors responsesGuardrailTargets for the
// /v1/responses/compact endpoint, whose body is kept as an opaque raw-JSON map
// so unknown Codex fields pass through unchanged. Mask replacements are written
// straight back into the raw map, so only fields the engine actually masked are
// re-encoded and the rest of the request body is forwarded verbatim.
func responsesCompactGuardrailTargets(request map[string]json.RawMessage) []guardrailTextTarget {
	targets := make([]guardrailTextTarget, 0)
	if value, exists := request["instructions"]; exists {
		var instructions string
		if err := json.Unmarshal(value, &instructions); err == nil && instructions != "" {
			targets = append(targets, guardrailTextTarget{
				fragment: guardrails.Fragment{ID: "instructions", Text: instructions, Mutable: true},
				replace:  func(value string) { setRawJSONField(request, "instructions", value, true) },
			})
		}
	}
	if value, exists := request["input"]; exists {
		// Decode with UseNumber so integers beyond 2^53 survive the round trip
		// when a mask forces the whole input to be re-encoded: plain
		// json.Unmarshal would demote them to float64 and silently round them.
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		var input any
		if err := decoder.Decode(&input); err == nil {
			appendGuardrailResponseInputTargets(&targets, input, "input", func(value any) {
				setRawJSONField(request, "input", value, true)
			})
		}
	}
	return targets
}

func anthropicGuardrailTargets(request *anthropicMessagesRequest) []guardrailTextTarget {
	targets := make([]guardrailTextTarget, 0)
	if system, exists := request.Raw["system"]; exists {
		appendGuardrailContentTargets(&targets, system, "system", func(value any) { request.Raw["system"] = value })
	}
	for index, rawMessage := range request.Messages {
		message, ok := rawMessage.(map[string]any)
		if !ok {
			continue
		}
		content, exists := message["content"]
		if !exists {
			continue
		}
		messageIndex := index
		appendGuardrailContentTargets(&targets, content, fmt.Sprintf("messages.%d.content", index), func(value any) {
			message["content"] = value
			request.Messages[messageIndex] = message
		})
	}
	return targets
}

func appendGuardrailResponseInputTargets(targets *[]guardrailTextTarget, value any, id string, set func(any)) {
	switch typed := value.(type) {
	case string:
		appendGuardrailStringTarget(targets, typed, id, set)
	case []any:
		for index, item := range typed {
			itemIndex := index
			appendGuardrailResponseInputTargets(targets, item, fmt.Sprintf("%s.%d", id, index), func(next any) {
				typed[itemIndex] = next
				set(typed)
			})
		}
	case map[string]any:
		kind := strings.ToLower(strings.TrimSpace(guardrailStringValue(typed["type"])))
		if kind == "message" || typed["role"] != nil {
			if content, ok := typed["content"]; ok {
				appendGuardrailContentTargets(targets, content, id+".content", func(next any) {
					typed["content"] = next
					set(typed)
				})
			}
			return
		}
		appendGuardrailContentTargets(targets, typed, id, set)
	}
}

func appendGuardrailContentTargets(targets *[]guardrailTextTarget, value any, id string, set func(any)) {
	switch typed := value.(type) {
	case string:
		appendGuardrailStringTarget(targets, typed, id, set)
	case []any:
		for index, item := range typed {
			itemIndex := index
			appendGuardrailContentTargets(targets, item, fmt.Sprintf("%s.%d", id, index), func(next any) {
				typed[itemIndex] = next
				set(typed)
			})
		}
	case map[string]any:
		kind := strings.ToLower(strings.TrimSpace(guardrailStringValue(typed["type"])))
		if kind != "text" && kind != "input_text" && kind != "output_text" {
			return
		}
		text, ok := typed["text"].(string)
		if !ok {
			return
		}
		appendGuardrailStringTarget(targets, text, id+".text", func(next any) {
			typed["text"] = next
			set(typed)
		})
	}
}

func appendGuardrailStringTarget(targets *[]guardrailTextTarget, value string, id string, set func(any)) {
	*targets = append(*targets, guardrailTextTarget{
		fragment: guardrails.Fragment{ID: id, Text: value, Mutable: true},
		replace:  func(replacement string) { set(replacement) },
	})
}

func guardrailStringValue(value any) string {
	text, _ := value.(string)
	return text
}
