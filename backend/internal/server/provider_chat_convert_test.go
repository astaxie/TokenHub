package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithoutGatewayExtensionsStripsReasoningFields(t *testing.T) {
	req := ChatCompletionRequest{Messages: []ChatMessage{
		{Role: "user", Content: "hi"},
		{
			Role:                     "assistant",
			Content:                  "answer",
			ReasoningContent:         "thinking",
			ReasoningSignature:       "anthropic:sig",
			RedactedReasoningContent: "opaque",
		},
	}}

	sanitized := withoutGatewayExtensions(req, false)

	for _, message := range sanitized.Messages {
		if message.ReasoningContent != "" || message.ReasoningSignature != "" || message.RedactedReasoningContent != "" {
			t.Fatalf("gateway extension fields must not survive: %+v", message)
		}
	}
	// The caller's slice must not be mutated; the same request is reused across
	// failover attempts to other routes.
	if req.Messages[1].ReasoningContent != "thinking" {
		t.Fatalf("the original request was mutated: %+v", req.Messages[1])
	}
}

func TestWithoutGatewayExtensionsStripsExplicitEmptyRawFields(t *testing.T) {
	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{
		"messages":[{
			"role":"assistant",
			"content":"answer",
			"reasoning_content":"",
			"reasoning_signature":"",
			"redacted_reasoning_content":"",
			"reasoning_details":[{"type":"reasoning.encrypted","id":"call_1","data":"codex:opaque-state"}],
			"provider_message":"preserve me"
		}]
	}`), &req); err != nil {
		t.Fatal(err)
	}

	sanitized := withoutGatewayExtensions(req, false)
	encoded, err := json.Marshal(sanitized)
	if err != nil {
		t.Fatal(err)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(encoded, &forwarded); err != nil {
		t.Fatal(err)
	}
	messages, _ := forwarded["messages"].([]any)
	message, _ := messages[0].(map[string]any)
	for _, field := range []string{"reasoning_content", "reasoning_signature", "redacted_reasoning_content", "reasoning_details"} {
		if _, present := message[field]; present {
			t.Fatalf("explicit empty %s must not be forwarded: %v", field, message)
		}
	}
	if message["provider_message"] != "preserve me" {
		t.Fatalf("unrelated provider field was lost: %v", message)
	}

	if _, present := req.Messages[0].raw["reasoning_signature"]; !present {
		t.Fatal("the original request was mutated")
	}
	if _, present := req.Messages[0].raw["reasoning_details"]; !present {
		t.Fatal("the original reasoning details were mutated")
	}
}

func TestDeepSeekAdapterForwardsReasoningContentOnly(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeFixtureRequest(t, r.Body, &received)
		w.Header().Set("content-type", "application/json")
		writeFixture(t, w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer upstream.Close()

	adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
	provider := Provider{Type: "deepseek", BaseURL: upstream.URL, APIKey: "test-key"}
	_, _, err := adapter.Chat(context.Background(), provider, "model-x", ChatCompletionRequest{
		Model: "model-x",
		Messages: []ChatMessage{{
			Role:                     "assistant",
			Content:                  "answer",
			ReasoningContent:         "deepseek continuation",
			ReasoningSignature:       "anthropic:sig",
			RedactedReasoningContent: "opaque",
			raw: map[string]json.RawMessage{
				"reasoning_details": json.RawMessage(`[{"type":"reasoning.encrypted","id":"call_foreign","data":"openrouter-state"},{"type":"reasoning.encrypted","id":"call_1","data":"codex:opaque-state"}]`),
			},
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	messages, _ := received["messages"].([]any)
	message, _ := messages[0].(map[string]any)
	if message["reasoning_content"] != "deepseek continuation" {
		t.Fatalf("DeepSeek reasoning_content was not forwarded: %v", message)
	}
	for _, field := range []string{"reasoning_signature", "redacted_reasoning_content"} {
		if _, present := message[field]; present {
			t.Fatalf("%s must remain gateway-local: %v", field, message)
		}
	}
	details, _ := message["reasoning_details"].([]any)
	if len(details) != 1 {
		t.Fatalf("non-Codex reasoning details were not preserved: %v", message)
	}
	detail, _ := details[0].(map[string]any)
	if detail["id"] != "call_foreign" || detail["data"] != "openrouter-state" {
		t.Fatalf("unexpected forwarded reasoning detail: %v", detail)
	}
}

func TestDeepSeekAdapterCanDisableReasoningContent(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeFixtureRequest(t, r.Body, &received)
		w.Header().Set("content-type", "application/json")
		writeFixture(t, w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer upstream.Close()

	adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
	provider := Provider{
		Type:    "deepseek",
		BaseURL: upstream.URL,
		APIKey:  "test-key",
		Options: map[string]string{reasoningContentOption: "false"},
	}
	_, _, err := adapter.Chat(context.Background(), provider, "model-x", ChatCompletionRequest{
		Model: "model-x",
		Messages: []ChatMessage{{
			Role:             "assistant",
			Content:          "answer",
			ReasoningContent: "do not replay",
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	messages, _ := received["messages"].([]any)
	message, _ := messages[0].(map[string]any)
	if _, present := message["reasoning_content"]; present {
		t.Fatalf("DeepSeek explicit false still forwarded reasoning_content: %v", message)
	}
}

func TestOpenAICompatibleAdapterDoesNotForwardGatewayExtensions(t *testing.T) {
	var received map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeFixtureRequest(t, r.Body, &received)
		w.Header().Set("content-type", "application/json")
		writeFixture(t, w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
	defer upstream.Close()

	adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
	provider := Provider{Type: ProviderOpenAICompatible, BaseURL: upstream.URL, APIKey: "test-key"}

	_, _, err := adapter.Chat(context.Background(), provider, "model-x", ChatCompletionRequest{
		Model: "model-x",
		Messages: []ChatMessage{{
			Role:                     "assistant",
			Content:                  "answer",
			ReasoningContent:         "thinking",
			ReasoningSignature:       "anthropic:sig",
			RedactedReasoningContent: "opaque",
		}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	messages, _ := received["messages"].([]any)
	message, _ := messages[0].(map[string]any)
	// These fields are not part of the OpenAI schema; strict upstreams reject
	// unknown message fields outright.
	for _, field := range []string{"reasoning_content", "reasoning_signature", "redacted_reasoning_content"} {
		if _, present := message[field]; present {
			t.Fatalf("%s must not be forwarded to an OpenAI-compatible upstream: %v", field, message)
		}
	}
	if message["content"] != "answer" {
		t.Fatalf("the message body must otherwise be forwarded unchanged: %v", message)
	}
}

func TestParseDataURIRejectsUnsupportedForms(t *testing.T) {
	cases := map[string]string{
		"missing comma": "data:image/png;base64",
		"not base64":    "data:image/png,rawbytes",
		"empty payload": "data:image/png;base64,",
		"no media type": "data:;base64,AAAA",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseDataURI(value); err == nil {
				t.Fatalf("expected %q to be rejected", value)
			}
		})
	}
}

func TestParseChatContentRejectsNonObjectParts(t *testing.T) {
	if _, err := parseChatContent([]any{"bare string"}); err == nil {
		t.Fatalf("content array entries must be objects")
	}
}

func TestProviderSignatureRoundTrip(t *testing.T) {
	encoded := encodeProviderSignature(anthropicSignatureProvider, "payload")

	if decoded, ok := decodeProviderSignature(anthropicSignatureProvider, encoded); !ok || decoded != "payload" {
		t.Fatalf("a signature must decode for the provider that minted it, got %q ok=%v", decoded, ok)
	}
	// Replaying one provider's blob to another is rejected upstream, so it must
	// not survive decoding.
	if _, ok := decodeProviderSignature(geminiSignatureProvider, encoded); ok {
		t.Fatalf("a signature must not decode for a different provider")
	}
	if _, ok := decodeProviderSignature(anthropicSignatureProvider, "unprefixed"); ok {
		t.Fatalf("an untagged signature must not be accepted")
	}
	if encodeProviderSignature(anthropicSignatureProvider, "") != "" {
		t.Fatalf("an empty signature must stay empty")
	}
}
