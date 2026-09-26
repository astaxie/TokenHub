package server

import (
	"fmt"
	"strings"
)

// Media vendors put prompts outside the standard Responses content blocks.
// Visit only text fields and message containers; assets and IDs remain opaque.
func routedResponsesGuardrailTargets(call CallContext, request *ResponsesRequest) []guardrailTextTarget {
	targets := responsesGuardrailTargets(request)
	if !modelHasMediaOutput(call.Model) {
		return targets
	}
	appendMediaResponseTextTargets(&targets, request.Input, "input", func(value any) { request.Input = value })
	for _, name := range []string{"prompt", "negative_prompt", "text", "lyrics", "clone_prompt"} {
		var value any
		if err := decodeResponsesJSON(request.raw[name], &value); err != nil {
			continue
		}
		appendMediaResponseTextTargets(&targets, value, name, func(value any) {
			setRawJSONField(request.raw, name, value, true)
		})
	}
	seen := map[string]bool{}
	unique := targets[:0]
	for _, target := range targets {
		if !seen[target.fragment.ID] {
			seen[target.fragment.ID] = true
			unique = append(unique, target)
		}
	}
	return unique
}

func appendMediaResponseTextTargets(targets *[]guardrailTextTarget, value any, id string, set func(any)) {
	switch typed := value.(type) {
	case string:
		appendGuardrailStringTarget(targets, typed, id, set)
	case []any:
		for index, item := range typed {
			appendMediaResponseTextTargets(targets, item, fmt.Sprintf("%s.%d", id, index), func(next any) {
				typed[index] = next
				set(typed)
			})
		}
	case map[string]any:
		kind := strings.ToLower(strings.TrimSpace(guardrailStringValue(typed["type"])))
		if kind != "" && kind != "message" && kind != "text" && kind != "input_text" && kind != "output_text" {
			return
		}
		for _, name := range []string{"messages", "content", "text", "prompt", "negative_prompt", "lyrics", "prompt_text"} {
			if child, exists := typed[name]; exists {
				appendMediaResponseTextTargets(targets, child, id+"."+name, func(next any) {
					typed[name] = next
					set(typed)
				})
			}
		}
	}
}
