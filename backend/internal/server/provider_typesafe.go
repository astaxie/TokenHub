package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// TypeSafe adapter. TypeSafe serves System One evaluation models (Jev) instead
// of an OpenAI-compatible chat API: its only endpoints are POST /v1/systemone
// ({state, questions} -> {answers}) and GET /v1/models. This adapter translates
// one Chat Completions request into one System One evaluation and renders the
// typed answers (probability, confidence) back into an OpenAI-shaped chat
// response, so a TypeSafe provider behaves like any other routed model.
//
// Two request-level extensions drive the evaluation:
//
//	"typesafe_questions": {...}  System One questions (choice/score/noul)
//	"typesafe_state":     ...    the content to evaluate (text, object, array)
//
// When they are absent, the manual's duplicate-charge ticket case is used, so a
// plain chat request still returns a meaningful evaluation.
const typesafeDefaultBaseURL = "https://api.typesafe.ai/v1"

type TypesafeAdapter struct {
	Client *http.Client
	// StreamClient carries no total deadline; see provider_stream_timeout.go.
	// Streaming calls fall back to Client when it is unset.
	StreamClient      *http.Client
	StreamIdleTimeout time.Duration
}

func (a TypesafeAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	payload, err := buildTypesafeRequest(providerModel, req)
	if err != nil {
		return nil, Usage{}, err
	}
	var body map[string]any
	if err := a.doJSON(ctx, provider, payload, &body); err != nil {
		return nil, Usage{}, err
	}
	usage := usageFromMap(body)
	converted, err := typesafeChatResponse(body, req.Model, usage)
	if err != nil {
		return nil, usage, err
	}
	return converted, usage, nil
}

// ChatStream emits the evaluation as a single SSE delta: System One answers are
// typed decisions, not generated prose, so there is nothing to stream
// incrementally.
func (a TypesafeAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, w io.Writer) (Usage, error) {
	resp, usage, err := a.Chat(ctx, provider, providerModel, req)
	if err != nil {
		return Usage{}, err
	}
	encoder := newOpenAIChatStreamEncoder(w, req.Model, streamUsageRequested(req))
	if err := encoder.EmitRole(); err != nil {
		return usage, err
	}
	if asMap, ok := resp.(map[string]any); ok {
		if text := choiceText(asMap); text != "" {
			if err := encoder.EmitText(text); err != nil {
				return usage, err
			}
		}
	}
	if err := encoder.Finalize("stop", usage); err != nil {
		return usage, err
	}
	return usage, nil
}

func (a TypesafeAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	chatReq := ChatCompletionRequest{
		Model:     req.Model,
		Messages:  []ChatMessage{{Role: "user", Content: req.Input}},
		MaxTokens: req.MaxTokens,
	}
	resp, usage, err := a.Chat(ctx, provider, providerModel, chatReq)
	if err != nil {
		return nil, Usage{}, err
	}
	text := ""
	if asMap, ok := resp.(map[string]any); ok {
		text = choiceText(asMap)
	}
	return responseObject(req.Model, text, usage), usage, nil
}

func (a TypesafeAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(501, "provider_capability_not_supported", "TypeSafe does not provide embeddings")
}

func (a TypesafeAdapter) doJSON(ctx context.Context, provider Provider, payload any, target any) error {
	resp, err := a.doRaw(ctx, provider, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(target)
}

func (a TypesafeAdapter) doRaw(ctx context.Context, provider Provider, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" {
		baseURL = typesafeDefaultBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, typesafeEndpointURL(baseURL, "/v1/systemone"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	if provider.APIKey != "" {
		req.Header.Set("authorization", "Bearer "+provider.APIKey)
	}
	applyProviderHeaders(req.Header, provider.Headers)
	resp, err := sendUpstream(a.Client, a.StreamClient, a.StreamIdleTimeout, req, false)
	if err != nil {
		return nil, err
	}
	if err := checkProviderResponseForProvider(resp, provider); err != nil {
		return nil, err
	}
	return resp, nil
}

// typesafeEndpointURL joins a base URL and a path without duplicating the /v1
// segment, so both "https://api.typesafe.ai" and ".../v1" work.
func typesafeEndpointURL(baseURL string, path string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	if strings.HasSuffix(strings.ToLower(base), "/v1") && strings.HasPrefix(strings.ToLower(path), "/v1/") {
		path = path[len("/v1"):]
	}
	return base + path
}

func buildTypesafeRequest(providerModel string, req ChatCompletionRequest) (map[string]any, error) {
	state := any(typesafeStateFromMessages(req.Messages))
	if raw := req.raw["typesafe_state"]; len(raw) > 0 {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_typesafe_state_invalid", "typesafe_state must be valid JSON")
		}
		state = decoded
	}
	questions := typesafeDefaultQuestions()
	if raw := req.raw["typesafe_questions"]; len(raw) > 0 {
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) == 0 {
			return nil, NewHTTPError(http.StatusBadRequest, "provider_typesafe_questions_invalid", "typesafe_questions must be a non-empty object")
		}
		questions = decoded
	}
	if strings.TrimSpace(compactTypesafeJSON(state)) == `""` {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_typesafe_state_missing", "TypeSafe needs content to evaluate: send a user message or typesafe_state")
	}
	return map[string]any{"model": providerModel, "state": state, "questions": questions}, nil
}

// typesafeDefaultQuestions mirrors the four-question case from the TypeSafe
// manual (state page): two Noul judgments, one Choice and one Score over the
// duplicate-charge ticket.
func typesafeDefaultQuestions() map[string]any {
	return map[string]any{
		"refund_requested": map[string]any{
			"type":         "noul",
			"instructions": "Does the customer request a refund or ask for money back?",
		},
		"department": map[string]any{
			"type":         "choice",
			"instructions": "Which team should handle this request?",
			"criteria": map[string]any{
				"billing":   "Charges, invoices, refunds or payments",
				"technical": "Bugs, outages or product defects",
				"account":   "Login, password or account access",
				"other":     "Anything that does not fit the options above",
			},
		},
		"severity": map[string]any{
			"type":         "score",
			"instructions": "How severe is the problem described?",
			"criteria":     []any{"Cosmetic", "Workaround exists", "Blocking; no workaround"},
		},
	}
}

func typesafeStateFromMessages(messages []ChatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(typesafeMessageText(message.Content))
		if text == "" {
			continue
		}
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "user"
		}
		parts = append(parts, role+": "+text)
	}
	return strings.Join(parts, "\n\n")
}

func typesafeMessageText(content any) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return value
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := part["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return compactTypesafeJSON(value)
	}
}

func typesafeChatResponse(body map[string]any, model string, usage Usage) (map[string]any, error) {
	answers, ok := body["answers"].(map[string]any)
	if !ok || len(answers) == 0 {
		return nil, NewHTTPError(http.StatusBadGateway, "provider_typesafe_invalid_response", "TypeSafe response did not include any answers")
	}
	served := ""
	if value, ok := body["model"].(string); ok {
		served = strings.TrimSpace(value)
	}
	return map[string]any{
		"id":      NewID("chatcmpl"),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   firstNonEmpty(served, model),
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": renderTypesafeAnswers(answers)},
			"finish_reason": "stop",
		}},
		"usage": openAIChatUsageObject(usage),
	}, nil
}

func renderTypesafeAnswers(answers map[string]any) string {
	keys := make([]string, 0, len(answers))
	for key := range answers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString("TypeSafe System One evaluation (typed answers, not generated text)")
	for _, key := range keys {
		answer, ok := answers[key].(map[string]any)
		if !ok {
			continue
		}
		switch {
		case answer["noul"] != nil:
			if value, ok := typesafeNumber(answer["noul"]); ok {
				fmt.Fprintf(&builder, "\n\n%s (noul, 1 = yes): %.3f", key, value)
			}
		case answer["choice"] != nil:
			fmt.Fprintf(&builder, "\n\n%s (choice): %v", key, answer["choice"])
		case answer["score"] != nil:
			if value, ok := typesafeNumber(answer["score"]); ok {
				fmt.Fprintf(&builder, "\n\n%s (score): %v", key, value)
			} else {
				fmt.Fprintf(&builder, "\n\n%s (score): %v", key, answer["score"])
			}
		}
		if legend, ok := answer["legend"]; ok && legend != nil {
			fmt.Fprintf(&builder, "\n  legend: %s", compactTypesafeJSON(legend))
		}
		if probabilities, ok := answer["probabilities"].(map[string]any); ok && len(probabilities) > 0 {
			fmt.Fprintf(&builder, "\n  probabilities: %s", compactTypesafeJSON(probabilities))
		}
		if confidence, ok := typesafeNumber(answer["confidence"]); ok {
			fmt.Fprintf(&builder, "\n  confidence: %.3f", confidence)
		}
	}
	builder.WriteString("\n\nanswers: " + compactTypesafeJSON(answers))
	return builder.String()
}

func typesafeNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func compactTypesafeJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
