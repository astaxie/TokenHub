package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const typesafeUpstreamBody = `{
  "model": "jev-1.13.0",
  "answers": {
    "refund_requested": {"type": "noul", "noul": 0.99},
    "department": {"type": "choice", "choice": "billing", "confidence": 1.0,
                   "probabilities": {"billing": 1.0, "account": 0.0}},
    "severity": {"type": "score", "score": 1.7, "confidence": 0.55,
                 "legend": {"0": "Cosmetic", "1": "Blocking"}, "probabilities": {"0": 0.3, "1": 0.7}}
  },
  "usage": {"input_tokens": 470, "output_tokens": 76}
}`

func newTypesafeUpstream(t *testing.T) (*httptest.Server, *string, *string, *map[string]any) {
	t.Helper()
	var (
		gotPath = new(string)
		gotAuth = new(string)
		gotBody = new(map[string]any)
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		*gotAuth = r.Header.Get("authorization")
		var decoded map[string]any
		if err := json.NewDecoder(r.Body).Decode(&decoded); err != nil {
			t.Errorf("upstream could not decode request body: %v", err)
		}
		*gotBody = decoded
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, typesafeUpstreamBody)
	}))
	t.Cleanup(upstream.Close)
	return upstream, gotPath, gotAuth, gotBody
}

func TestTypesafeEndpointURLKeepsSingleV1Segment(t *testing.T) {
	cases := []struct {
		base string
		want string
	}{
		{"https://api.typesafe.ai/v1", "https://api.typesafe.ai/v1/systemone"},
		{"https://api.typesafe.ai", "https://api.typesafe.ai/v1/systemone"},
		{"https://api.typesafe.ai/v1/", "https://api.typesafe.ai/v1/systemone"},
	}
	for _, testCase := range cases {
		if got := typesafeEndpointURL(testCase.base, "/v1/systemone"); got != testCase.want {
			t.Errorf("typesafeEndpointURL(%q) = %q, want %q", testCase.base, got, testCase.want)
		}
	}
}

func TestBuildTypesafeRequestDefaultsToManualQuestionSet(t *testing.T) {
	payload, err := buildTypesafeRequest("jev-latest", ChatCompletionRequest{
		Model:    "TypeSafe/jev-latest",
		Messages: []ChatMessage{{Role: "user", Content: "I was charged twice, please refund."}},
	})
	if err != nil {
		t.Fatalf("buildTypesafeRequest() error = %v", err)
	}
	if payload["model"] != "jev-latest" {
		t.Errorf("model = %v, want the upstream model name", payload["model"])
	}
	if payload["state"] != "user: I was charged twice, please refund." {
		t.Errorf("state = %v, want the rendered conversation", payload["state"])
	}
	questions, ok := payload["questions"].(map[string]any)
	if !ok {
		t.Fatalf("questions type = %T, want map[string]any", payload["questions"])
	}
	for _, key := range []string{"refund_requested", "department", "severity"} {
		if _, ok := questions[key]; !ok {
			t.Errorf("default questions missing %q", key)
		}
	}
}

func TestBuildTypesafeRequestHonorsRequestExtensions(t *testing.T) {
	state, err := json.Marshal(map[string]any{"ticket": "dashboard returns 500", "plan": "enterprise"})
	if err != nil {
		t.Fatal(err)
	}
	questions, err := json.Marshal(map[string]any{
		"blocking": map[string]any{"type": "noul", "instructions": "Is `ticket` blocking the business?"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := buildTypesafeRequest("jev-latest", ChatCompletionRequest{
		Model:    "TypeSafe/jev-latest",
		Messages: []ChatMessage{{Role: "user", Content: "ignored when typesafe_state is present"}},
		raw: map[string]json.RawMessage{
			"typesafe_state":     state,
			"typesafe_questions": questions,
		},
	})
	if err != nil {
		t.Fatalf("buildTypesafeRequest() error = %v", err)
	}
	decodedState, ok := payload["state"].(map[string]any)
	if !ok {
		t.Fatalf("state type = %T, want the decoded typesafe_state object", payload["state"])
	}
	if decodedState["plan"] != "enterprise" {
		t.Errorf("state[plan] = %v, want enterprise", decodedState["plan"])
	}
	decodedQuestions, ok := payload["questions"].(map[string]any)
	if !ok || len(decodedQuestions) != 1 {
		t.Fatalf("questions = %#v, want the single caller-supplied question", payload["questions"])
	}
	if _, ok := decodedQuestions["blocking"]; !ok {
		t.Errorf("questions = %#v, want the blocking question only", decodedQuestions)
	}
}

func TestBuildTypesafeRequestRejectsInvalidExtensions(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]json.RawMessage
		code string
	}{
		{
			name: "questions is not an object",
			raw:  map[string]json.RawMessage{"typesafe_questions": json.RawMessage(`["nope"]`)},
			code: "provider_typesafe_questions_invalid",
		},
		{
			name: "questions is empty",
			raw:  map[string]json.RawMessage{"typesafe_questions": json.RawMessage(`{}`)},
			code: "provider_typesafe_questions_invalid",
		},
		{
			name: "state is malformed",
			raw:  map[string]json.RawMessage{"typesafe_state": json.RawMessage(`{`)},
			code: "provider_typesafe_state_invalid",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := buildTypesafeRequest("jev-latest", ChatCompletionRequest{
				Model:    "TypeSafe/jev-latest",
				Messages: []ChatMessage{{Role: "user", Content: "x"}},
				raw:      testCase.raw,
			})
			if err == nil {
				t.Fatal("buildTypesafeRequest() error = nil, want a rejection")
			}
			if got := AsHTTPError(err).Code; got != testCase.code {
				t.Errorf("error code = %q, want %q", got, testCase.code)
			}
		})
	}
}

func TestBuildTypesafeRequestRequiresContentToEvaluate(t *testing.T) {
	_, err := buildTypesafeRequest("jev-latest", ChatCompletionRequest{Model: "TypeSafe/jev-latest"})
	if err == nil {
		t.Fatal("buildTypesafeRequest() error = nil, want a rejection when there is nothing to evaluate")
	}
	if got := AsHTTPError(err).Code; got != "provider_typesafe_state_missing" {
		t.Errorf("error code = %q, want provider_typesafe_state_missing", got)
	}
}

func TestTypesafeChatResponseRendersTypedAnswers(t *testing.T) {
	body := map[string]any{
		"model": "jev-1.13.0",
		"answers": map[string]any{
			"refund_requested": map[string]any{"type": "noul", "noul": 0.99},
			"department": map[string]any{"type": "choice", "choice": "billing", "confidence": 1.0,
				"probabilities": map[string]any{"billing": 1.0, "account": 0.0}},
			"severity": map[string]any{"type": "score", "score": 1.7, "confidence": 0.55,
				"legend": map[string]any{"0": "Cosmetic", "1": "Blocking"}, "probabilities": map[string]any{"0": 0.3, "1": 0.7}},
		},
		"usage": map[string]any{"input_tokens": float64(470), "output_tokens": float64(76)},
	}
	usage := usageFromMap(body)
	response, err := typesafeChatResponse(body, "TypeSafe/jev-latest", usage)
	if err != nil {
		t.Fatalf("typesafeChatResponse() error = %v", err)
	}
	if response["model"] != "jev-1.13.0" {
		t.Errorf("model = %v, want the versioned model reported upstream", response["model"])
	}
	choices, ok := response["choices"].([]map[string]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices = %#v, want exactly one choice", response["choices"])
	}
	message, ok := choices[0]["message"].(map[string]any)
	if !ok {
		t.Fatalf("message type = %T, want map[string]any", choices[0]["message"])
	}
	content, _ := message["content"].(string)
	for _, want := range []string{"refund_requested", "0.990", "billing", "1.7", "confidence: 0.550", `"noul":0.99`} {
		if !strings.Contains(content, want) {
			t.Errorf("rendered content is missing %q:\n%s", want, content)
		}
	}
	if usage.PromptTokens != 470 || usage.CompletionTokens != 76 {
		t.Errorf("usage = %+v, want prompt 470 / completion 76", usage)
	}
}

func TestTypesafeChatResponseRejectsMissingAnswers(t *testing.T) {
	_, err := typesafeChatResponse(map[string]any{"model": "jev-1.13.0"}, "TypeSafe/jev-latest", Usage{})
	if err == nil {
		t.Fatal("typesafeChatResponse() error = nil, want a rejection when answers are missing")
	}
	if got := AsHTTPError(err).Code; got != "provider_typesafe_invalid_response" {
		t.Errorf("error code = %q, want provider_typesafe_invalid_response", got)
	}
}

func TestTypesafeAdapterChatTranslatesRequestAndResponse(t *testing.T) {
	upstream, gotPath, gotAuth, gotBody := newTypesafeUpstream(t)
	adapter := TypesafeAdapter{Client: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL + "/v1", APIKey: "sk-unit-test"}

	response, usage, err := adapter.Chat(context.Background(), provider, "jev-latest", ChatCompletionRequest{
		Model:    "TypeSafe/jev-latest",
		Messages: []ChatMessage{{Role: "user", Content: "I was charged twice, please refund."}},
	})
	if err != nil {
		t.Fatalf("Chat() error = %v", err)
	}
	if *gotPath != "/v1/systemone" {
		t.Errorf("upstream path = %q, want /v1/systemone", *gotPath)
	}
	if *gotAuth != "Bearer sk-unit-test" {
		t.Errorf("authorization = %q, want the provider key as a bearer token", *gotAuth)
	}
	if (*gotBody)["model"] != "jev-latest" {
		t.Errorf("upstream model = %v, want jev-latest", (*gotBody)["model"])
	}
	if _, ok := (*gotBody)["questions"].(map[string]any); !ok {
		t.Errorf("upstream questions = %#v, want the question map", (*gotBody)["questions"])
	}
	if usage.PromptTokens != 470 || usage.CompletionTokens != 76 {
		t.Errorf("usage = %+v, want prompt 470 / completion 76", usage)
	}
	asMap, ok := response.(map[string]any)
	if !ok {
		t.Fatalf("response type = %T, want map[string]any", response)
	}
	if asMap["object"] != "chat.completion" {
		t.Errorf("object = %v, want chat.completion", asMap["object"])
	}
}

func TestTypesafeAdapterChatStreamEmitsSingleDelta(t *testing.T) {
	upstream, _, _, _ := newTypesafeUpstream(t)
	adapter := TypesafeAdapter{Client: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL + "/v1", APIKey: "sk-unit-test"}

	var stream bytes.Buffer
	usage, err := adapter.ChatStream(context.Background(), provider, "jev-latest", ChatCompletionRequest{
		Model:    "TypeSafe/jev-latest",
		Messages: []ChatMessage{{Role: "user", Content: "refund please"}},
	}, &stream)
	if err != nil {
		t.Fatalf("ChatStream() error = %v", err)
	}
	output := stream.String()
	for _, want := range []string{"chat.completion.chunk", "0.990", "[DONE]"} {
		if !strings.Contains(output, want) {
			t.Errorf("stream output is missing %q:\n%s", want, output)
		}
	}
	if usage.PromptTokens != 470 {
		t.Errorf("usage = %+v, want prompt 470", usage)
	}
}

func TestTypesafeAdapterEmbeddingsUnsupported(t *testing.T) {
	_, _, err := TypesafeAdapter{}.Embeddings(context.Background(), Provider{}, "jev-latest", EmbeddingsRequest{})
	if err == nil {
		t.Fatal("Embeddings() error = nil, want an unsupported error")
	}
	if got := AsHTTPError(err).Code; got != "provider_capability_not_supported" {
		t.Errorf("error code = %q, want provider_capability_not_supported", got)
	}
}
