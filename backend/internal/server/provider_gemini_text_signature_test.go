package server

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestGeminiTextSignatureRoundTrip(t *testing.T) {
	var body map[string]any
	if err := json.Unmarshal([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"Prefix. "},{"text":"署名。","thoughtSignature":"opaque-A"}]},"finishReason":"STOP"}]}`), &body); err != nil {
		t.Fatal(err)
	}
	response, err := geminiChatResponse(body, "gemini-4-neon", Usage{})
	if err != nil {
		t.Fatal(err)
	}
	message := response["choices"].([]map[string]any)[0]["message"]
	wire, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var assistant ChatMessage
	if err := json.Unmarshal(wire, &assistant); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{{"text": "Prefix. "}, {"text": "署名。", "thoughtSignature": "opaque-A"}}
	assertGeminiTextReplay(t, assistant, want)

	assistant.Content = "Edited. 署名。"
	assertGeminiTextReplay(t, assistant, []map[string]any{{"text": "Edited. 署名。"}})
	assistant.Content = "Prefix. 修改。"
	assertGeminiTextReplay(t, assistant, []map[string]any{{"text": "Prefix. 修改。"}})
	assistant.ReasoningSignature = encodeProviderSignature(anthropicSignatureProvider, "other-provider")
	assertGeminiTextReplay(t, assistant, []map[string]any{{"text": "Prefix. 修改。"}})
}

func TestGeminiStreamPreservesEmptyFinalTextSignature(t *testing.T) {
	stream := strings.NewReader("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"reply\"}]}}]}\n\n" +
		"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"\",\"thoughtSignature\":\"opaque-B\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	var output bytes.Buffer
	if _, err := streamGeminiChat(stream, newOpenAIChatStreamEncoder(&output, "gemini-4-neon", false)); err != nil {
		t.Fatal(err)
	}
	assistant := ChatMessage{Role: "assistant"}
	var text strings.Builder
	for _, line := range strings.Split(output.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var frame struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					Signature string `json:"reasoning_signature"`
				}
			}
		}
		if err := json.Unmarshal([]byte(payload), &frame); err != nil {
			t.Fatal(err)
		}
		for _, choice := range frame.Choices {
			text.WriteString(choice.Delta.Content)
			if choice.Delta.Signature != "" {
				assistant.ReasoningSignature = choice.Delta.Signature
			}
		}
	}
	assistant.Content = text.String()
	assertGeminiTextReplay(t, assistant, []map[string]any{{"text": "reply"}, {"text": "", "thoughtSignature": "opaque-B"}})
	assistant.Content = "revis"
	assertGeminiTextReplay(t, assistant, []map[string]any{{"text": "revis"}})
}

func assertGeminiTextReplay(t *testing.T, message ChatMessage, want []map[string]any) {
	t.Helper()
	payload, err := buildGeminiRequest("gemini-4-neon", ChatCompletionRequest{Messages: []ChatMessage{message}})
	if err != nil {
		t.Fatal(err)
	}
	contents := payload["contents"].([]map[string]any)
	if len(contents) != 1 || !reflect.DeepEqual(contents[0]["parts"], want) {
		t.Fatalf("unexpected replayed parts: %+v", contents)
	}
}
