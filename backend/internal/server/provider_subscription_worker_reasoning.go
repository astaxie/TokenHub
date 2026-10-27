package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

type subscriptionWorkerReasoningSetting struct {
	effort, thinking string
	budget           int64
}

// The pinned adapters declare SDK options, not one common wire shape. Only
// explicit efforts or named, complete thinking controls become selectable.
func subscriptionWorkerReasoningSettings(model subscriptionWorkerModel) map[string]subscriptionWorkerReasoningSetting {
	settings := map[string]subscriptionWorkerReasoningSetting{}
	for name, raw := range model.Variants {
		var variant struct {
			ReasoningEffort string `json:"reasoningEffort"`
			Effort          string `json:"effort"`
			Thinking        *struct {
				Type         string `json:"type"`
				BudgetTokens int64  `json:"budgetTokens"`
			} `json:"thinking"`
		}
		if json.Unmarshal(raw, &variant) != nil {
			continue
		}
		setting := subscriptionWorkerReasoningSetting{effort: strings.TrimSpace(variant.ReasoningEffort)}
		selector := setting.effort
		if model.Protocol == "anthropic" {
			if setting.effort == "" {
				setting.effort = strings.TrimSpace(variant.Effort)
				selector = setting.effort
			}
			if thinking := variant.Thinking; thinking != nil {
				switch thinking.Type {
				case "adaptive", "disabled":
					setting.thinking = thinking.Type
				case "enabled":
					if thinking.BudgetTokens > 0 {
						setting.thinking, setting.budget = thinking.Type, thinking.BudgetTokens
					}
				}
				if selector == "" && setting.thinking != "" {
					switch name {
					case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
						selector = name
					}
				}
			}
		}
		if selector != "" {
			settings[selector] = setting
		}
	}
	return settings
}

func selectSubscriptionWorkerReasoning(model subscriptionWorkerModel, request ChatCompletionRequest) (subscriptionWorkerModel, error) {
	effort := normalizedReasoningEffort(request.ReasoningEffort)
	if model.Protocol != "anthropic" || effort == nil {
		return model, nil
	}
	settings := subscriptionWorkerReasoningSettings(model)
	if setting, ok := settings[*effort]; ok {
		model.reasoning = &setting
		return model, nil
	}
	if len(settings) == 0 && anthropicReasoningEffortSupported(model.ID, *effort) {
		return model, nil
	}
	return model, NewHTTPError(http.StatusBadRequest, "unsupported_reasoning_effort", "This subscription model does not support the requested reasoning effort")
}

func applySubscriptionWorkerReasoning(payload map[string]json.RawMessage, model subscriptionWorkerModel) error {
	setting := model.reasoning
	if setting == nil {
		return nil
	}
	config := map[string]any{}
	if raw := payload["output_config"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			return err
		}
	}
	if config == nil {
		config = map[string]any{}
	}
	delete(config, "effort")
	if setting.effort != "" {
		config["effort"] = setting.effort
	}
	delete(payload, "output_config")
	if len(config) > 0 {
		payload["output_config"], _ = json.Marshal(config)
	}
	if setting.thinking != "" {
		thinking := map[string]any{"type": setting.thinking}
		if setting.budget > 0 {
			thinking["budget_tokens"] = setting.budget
		}
		payload["thinking"], _ = json.Marshal(thinking)
	}
	return nil
}
