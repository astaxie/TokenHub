package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestChatGPTSubscriptionCatalogAdmitsForwardedControls(t *testing.T) {
	var upstreamCalls atomic.Int32
	adapter, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = fmt.Fprint(w, `{"models":[{"slug":"tool-model"}]}`)
			return
		}
		if r.URL.Path != "/responses" {
			t.Errorf("Unexpected upstream path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		upstreamCalls.Add(1)
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Errorf("Expected one function tool, got %v", tools)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tool, _ := tools[0].(map[string]any)
		if tool["name"] != "lookup" || body["tool_choice"] != "auto" || body["parallel_tool_calls"] != false {
			t.Errorf("Function tool controls did not reach the upstream: %v", body)
		}
		text, _ := body["text"].(map[string]any)
		format, _ := text["format"].(map[string]any)
		includes, _ := body["include"].([]any)
		if format["type"] != "json_schema" || len(includes) != 1 || includes[0] != "reasoning.encrypted_content" {
			t.Errorf("Structured output or continuation controls were lost: %v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"synthetic-response\",\"status\":\"completed\",\"output\":[]}}\n\n")
	})
	catalog, _, err := adapter.ResourceModels(context.Background(), provider, ProviderResource{ID: "account"}, "")
	if err != nil || len(catalog.Models) != 1 {
		t.Fatalf("Model discovery failed: models=%d error=%v", len(catalog.Models), err)
	}
	model := providerModelFromCatalog("provider", catalog.Models[0])
	var chat ChatCompletionRequest
	if err := json.Unmarshal([]byte(`{"model":"public-model","messages":[{"role":"user","content":"Look up the value"}],"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}],"tool_choice":"auto","parallel_tool_calls":false,"response_format":{"type":"json_schema","json_schema":{"name":"answer","schema":{"type":"object"}}}}`), &chat); err != nil {
		t.Fatal(err)
	}
	if compatibility := jevChatCompatibility(model, chat); compatibility.Reason != "" {
		t.Fatalf("Imported ChatGPT model excluded from Chat routing: %+v", compatibility)
	}
	if !semanticModelFitsRequest(model, chat, 20) {
		t.Fatal("Imported ChatGPT model excluded from legacy semantic routing")
	}
	responses, err := chatToSubscriptionResponsesRequest(chat)
	if err != nil {
		t.Fatal(err)
	}
	if compatibility := jevResponsesCompatibility(model, responses); compatibility.Reason != "" {
		t.Fatalf("Imported ChatGPT model excluded from Responses routing: %+v", compatibility)
	}
	if _, _, err := adapter.Responses(context.Background(), provider, model.UpstreamModel, responses); err != nil {
		t.Fatal(err)
	}
	if upstreamCalls.Load() != 1 {
		t.Fatalf("Expected one inference request, got %d", upstreamCalls.Load())
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens", "temperature", "top_p", "stop"} {
		t.Run("unsupported Chat control "+field, func(t *testing.T) {
			var request ChatCompletionRequest
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"model":"public-model","messages":[{"role":"user","content":"Hello"}],%q:1}`, field)), &request); err != nil {
				t.Fatal(err)
			}
			if result := jevChatCompatibility(model, request); result.Reason != "parameter_not_declared" || result.Parameter != field {
				t.Fatalf("Unsupported control was advertised: %+v", result)
			}
		})
	}
	for _, field := range []string{"max_output_tokens", "temperature", "top_p", "truncation", "max_tool_calls"} {
		t.Run("unsupported Responses control "+field, func(t *testing.T) {
			var request ResponsesRequest
			if err := json.Unmarshal([]byte(fmt.Sprintf(`{"model":"public-model","input":"Hello",%q:1}`, field)), &request); err != nil {
				t.Fatal(err)
			}
			if result := jevResponsesCompatibility(model, request); result.Reason != "parameter_not_declared" || result.Parameter != field {
				t.Fatalf("Unsupported control was advertised: %+v", result)
			}
		})
	}
}
