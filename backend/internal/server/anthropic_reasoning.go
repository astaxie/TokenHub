package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

const (
	reasoningEffortMapOption         = "reasoning_effort_map"
	reasoningEffortValuesOption      = "reasoning_effort_values"
	reasoningEffortUnsupportedOption = "reasoning_effort_unsupported"
	reasoningBudgetMapOption         = "reasoning_budget_map"
	reasoningContentOption           = "preserve_reasoning_content"
	reasoningSignatureProvider       = "tokenhub-openai-compatible"
)

// applyAnthropicReasoningOptions translates Claude reasoning controls into the
// vocabulary accepted by an OpenAI-compatible provider. Resource options are
// already merged over provider options when a route is selected.
func applyAnthropicReasoningOptions(req anthropicMessagesRequest, provider Provider, chatReq *ChatCompletionRequest) error {
	effort, err := anthropicReasoningEffort(req.Raw, provider.Options[reasoningBudgetMapOption])
	if err != nil {
		return err
	}
	if effort == "" {
		chatReq.ReasoningEffort = nil
		return nil
	}

	if raw := strings.TrimSpace(provider.Options[reasoningEffortMapOption]); raw != "" {
		mapping := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &mapping); err != nil {
			return NewHTTPError(http.StatusInternalServerError, "invalid_provider_option", reasoningEffortMapOption+" must be a JSON object")
		}
		if mapped, ok := mapping[effort]; ok {
			effort = strings.ToLower(strings.TrimSpace(mapped))
		}
	}

	if effort == "" || effort == "omit" {
		chatReq.ReasoningEffort = nil
		return nil
	}
	if supported := reasoningEffortValues(provider.Options[reasoningEffortValuesOption]); len(supported) > 0 {
		if _, ok := supported[effort]; !ok {
			switch strings.ToLower(strings.TrimSpace(provider.Options[reasoningEffortUnsupportedOption])) {
			case "passthrough":
				// Explicit opt-in for upstreams that accept values beyond the
				// configured allowlist.
			case "reject":
				values := make([]string, 0, len(supported))
				for value := range supported {
					values = append(values, value)
				}
				sort.Strings(values)
				return NewHTTPError(http.StatusBadRequest, "unsupported_reasoning_effort", fmt.Sprintf("reasoning effort %q is not supported by this provider; supported values: %s", effort, strings.Join(values, ", ")))
			default:
				chatReq.ReasoningEffort = nil
				return nil
			}
		}
	}
	chatReq.ReasoningEffort = &effort
	return nil
}

func anthropicReasoningEffort(raw map[string]any, budgetMap string) (string, error) {
	effort := ""
	if value, ok := raw["effort"].(string); ok {
		effort = strings.ToLower(strings.TrimSpace(value))
	}
	if outputConfig, ok := raw["output_config"].(map[string]any); ok {
		if value, ok := outputConfig["effort"].(string); ok && strings.TrimSpace(value) != "" {
			effort = strings.ToLower(strings.TrimSpace(value))
		}
	}
	thinking, _ := raw["thinking"].(map[string]any)
	switch strings.ToLower(strings.TrimSpace(stringFromAny(thinking["type"]))) {
	case "disabled":
		return "none", nil
	case "adaptive", "auto":
		return effort, nil
	case "enabled":
		if effort != "" {
			return effort, nil
		}
		return reasoningEffortForBudget(int64FromAny(thinking["budget_tokens"]), budgetMap)
	default:
		return effort, nil
	}
}

func reasoningEffortForBudget(budget int64, raw string) (string, error) {
	if strings.TrimSpace(raw) != "" {
		configured := map[string]string{}
		if err := json.Unmarshal([]byte(raw), &configured); err != nil {
			return "", NewHTTPError(http.StatusInternalServerError, "invalid_provider_option", reasoningBudgetMapOption+" must be a JSON object")
		}
		type tier struct {
			maximum int64
			effort  string
		}
		tiers := make([]tier, 0, len(configured))
		fallback := ""
		for maximum, effort := range configured {
			if maximum == "*" {
				fallback = strings.ToLower(strings.TrimSpace(effort))
				continue
			}
			parsed, err := strconv.ParseInt(maximum, 10, 64)
			if err != nil || parsed < 0 {
				return "", NewHTTPError(http.StatusInternalServerError, "invalid_provider_option", reasoningBudgetMapOption+" keys must be non-negative token limits or *")
			}
			tiers = append(tiers, tier{maximum: parsed, effort: strings.ToLower(strings.TrimSpace(effort))})
		}
		sort.Slice(tiers, func(i, j int) bool { return tiers[i].maximum < tiers[j].maximum })
		for _, candidate := range tiers {
			if budget <= candidate.maximum {
				return candidate.effort, nil
			}
		}
		if fallback != "" {
			return fallback, nil
		}
		return "", nil
	}
	switch {
	case budget <= 0:
		return "none", nil
	case budget <= 2048:
		return "low", nil
	case budget <= 8192:
		return "medium", nil
	case budget <= 16384:
		return "high", nil
	default:
		return "xhigh", nil
	}
}

func reasoningEffortValues(raw string) map[string]struct{} {
	values := map[string]struct{}{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
			values[value] = struct{}{}
		}
	}
	return values
}

func providerPreservesReasoningContent(provider Provider) bool {
	raw, configured := provider.Options[reasoningContentOption]
	if !configured {
		return provider.Type == "deepseek"
	}
	value := strings.ToLower(strings.TrimSpace(raw))
	return value == "true" || value == "1" || value == "yes"
}

func gatewayReasoningSignature(provider Provider, reasoning string) string {
	if reasoning == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(provider.ID + "\x00" + reasoning))
	return encodeProviderSignature(reasoningSignatureProvider, provider.ID+":"+hex.EncodeToString(digest[:]))
}

func validGatewayReasoningSignature(provider Provider, reasoning, signature string) bool {
	return signature != "" && signature == gatewayReasoningSignature(provider, reasoning)
}
