package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayForwardsChatReasoningEffort(t *testing.T) {
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		upstreamRequests = append(upstreamRequests, payload)
		if streaming, _ := payload["stream"].(bool); streaming {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl_test\",\"choices\":[]}\n\ndata: [DONE]\n\n")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"model":   "upstream-reasoning-model",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, "deepseek")
	app := server.Handler()
	effort := "high"
	for _, stream := range []bool{false, true} {
		resp := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
			"model": "reasoning-model",
			"messages": []map[string]any{{
				"role":              "assistant",
				"content":           "",
				"reasoning_content": "provider reasoning state",
				"provider_message":  "preserve me",
			}},
			"reasoning_effort": effort,
			"stream":           stream,
			"thinking":         map[string]any{"type": "disabled"},
			"provider_option":  "preserve me",
		}, secret)
		if resp.Code != http.StatusOK {
			t.Fatalf("stream=%v expected 200, got %d: %s", stream, resp.Code, resp.Body)
		}
	}

	if len(upstreamRequests) != 2 {
		t.Fatalf("expected two upstream requests, got %d", len(upstreamRequests))
	}
	for _, payload := range upstreamRequests {
		if payload["reasoning_effort"] != effort {
			t.Fatalf("expected reasoning_effort %q, got %#v", effort, payload["reasoning_effort"])
		}
		if payload["model"] != "upstream-reasoning-model" {
			t.Fatalf("expected routed provider model, got %#v", payload["model"])
		}
		thinking, _ := payload["thinking"].(map[string]any)
		if thinking["type"] != "disabled" || payload["provider_option"] != "preserve me" {
			t.Fatalf("provider-specific request fields were not preserved: %#v", payload)
		}
		messages, _ := payload["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("expected one upstream message, got %#v", payload["messages"])
		}
		message, _ := messages[0].(map[string]any)
		if message["reasoning_content"] != "provider reasoning state" || message["provider_message"] != "preserve me" {
			t.Fatalf("provider-specific message fields were not preserved: %#v", message)
		}
	}
}

func TestGatewayForwardsResponsesReasoningEffort(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_test",
			"object": "response",
			"model":  "upstream-reasoning-model",
			"output": []map[string]any{},
			"usage":  map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model": "reasoning-model",
		"input": "reason carefully",
		"reasoning": map[string]any{
			"effort": "medium",
		},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}

	reasoning, ok := upstreamRequest["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "medium" {
		t.Fatalf("expected nested reasoning effort, got %#v", upstreamRequest["reasoning"])
	}
	if upstreamRequest["model"] != "upstream-reasoning-model" {
		t.Fatalf("expected routed provider model, got %#v", upstreamRequest["model"])
	}
}

func TestGatewayStreamsOpenAICompatibleResponses(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		w.Header().Set("content-type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"STREAM_OK\"}\n\n")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := postStream(t, server.Handler(), "/v1/responses", map[string]any{
		"model":              "reasoning-model",
		"input":              "stream this",
		"stream":             true,
		"thinking":           map[string]any{"type": "disabled"},
		"provider_extension": "preserve me",
	}, secret)

	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte("STREAM_OK")) ||
		!bytes.Contains(resp.Body.Bytes(), []byte("response.completed")) {
		t.Fatalf("expected Responses SSE events, got %s", resp.Body)
	}
	if resp.Header().Get("content-type") != "text/event-stream" {
		t.Fatalf("unexpected content type %q", resp.Header().Get("content-type"))
	}
	if resp.Header().Get("x-tokenhub-route-id") != "route_reasoning_0" {
		t.Fatalf("unexpected route header %q", resp.Header().Get("x-tokenhub-route-id"))
	}
	if upstreamRequest["model"] != "upstream-reasoning-model" || upstreamRequest["stream"] != true {
		t.Fatalf("unexpected upstream request: %#v", upstreamRequest)
	}
	thinking, _ := upstreamRequest["thinking"].(map[string]any)
	if thinking["type"] != "disabled" || upstreamRequest["provider_extension"] != "preserve me" {
		t.Fatalf("provider extensions were not preserved: %#v", upstreamRequest)
	}
}

func TestGatewayOmitsUnspecifiedReasoningEffort(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "reasoning-model",
		"messages": []map[string]any{{"role": "user", "content": "use the default"}},
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if _, exists := upstreamRequest["reasoning_effort"]; exists {
		t.Fatalf("unspecified reasoning_effort should be omitted: %#v", upstreamRequest)
	}
}

func TestGatewayKeepsRouteOrderWhenPrimaryCannotApplyReasoningEffort(t *testing.T) {
	var upstreamPaths []string
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamPaths = append(upstreamPaths, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		upstreamRequests = append(upstreamRequests, payload)
		if r.URL.Path == "/v1/messages" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ok"}},
				"usage":   map[string]any{},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, store, secret := newReasoningEffortGateway(
		t,
		upstream.URL,
		ProviderAnthropic,
		ProviderOpenAICompatible,
	)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "use minimal effort"}},
		"reasoning_effort": "minimal",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if len(upstreamPaths) != 1 || upstreamPaths[0] != "/v1/messages" {
		t.Fatalf("expected the primary Anthropic route to remain selected, got %#v", upstreamPaths)
	}
	if _, exists := upstreamRequests[0]["output_config"]; exists {
		t.Fatalf("unsupported effort should be omitted from the primary request: %#v", upstreamRequests[0])
	}

	for _, route := range store.ListRoutes() {
		switch route.ProviderID {
		case "prv_reasoning_0":
			if route.LastUsedAt == nil {
				t.Fatalf("primary Anthropic route should be marked used")
			}
		case "prv_reasoning_1":
			if route.LastUsedAt != nil {
				t.Fatalf("backup OpenAI route should not be used")
			}
		}
	}
}

func TestGatewayOmitsUnsupportedReasoningEffort(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderAnthropic)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "use minimal effort"}},
		"reasoning_effort": "minimal",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if _, exists := upstreamRequest["output_config"]; exists {
		t.Fatalf("unsupported Anthropic effort should be omitted: %#v", upstreamRequest)
	}
}

func TestGatewayOmitsBlankReasoningEffort(t *testing.T) {
	var upstreamRequest map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, _, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "blank effort"}},
		"reasoning_effort": "   ",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body)
	}
	if _, exists := upstreamRequest["reasoning_effort"]; exists {
		t.Fatalf("blank reasoning_effort should be omitted: %#v", upstreamRequest)
	}
}

func TestAnthropicAdapterTranslatesReasoningEffort(t *testing.T) {
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("unexpected upstream path %q", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		upstreamRequests = append(upstreamRequests, payload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	effort := "medium"
	adapter := AnthropicAdapter{}
	provider := Provider{BaseURL: upstream.URL + "/v1"}
	_, _, err := adapter.Chat(context.Background(), provider, "claude-sonnet-5", ChatCompletionRequest{
		Model:           "reasoning-model",
		Messages:        []ChatMessage{{Role: "user", Content: "reason"}},
		ReasoningEffort: &effort,
	})
	if err != nil {
		t.Fatalf("chat translation failed: %v", err)
	}
	_, _, err = adapter.Responses(context.Background(), provider, "claude-sonnet-5", ResponsesRequest{
		Model: "reasoning-model",
		Input: "reason",
		Reasoning: &ResponsesReasoning{
			Effort: &effort,
		},
	})
	if err != nil {
		t.Fatalf("responses translation failed: %v", err)
	}

	if len(upstreamRequests) != 2 {
		t.Fatalf("expected two upstream requests, got %d", len(upstreamRequests))
	}
	for _, payload := range upstreamRequests {
		outputConfig, ok := payload["output_config"].(map[string]any)
		if !ok || outputConfig["effort"] != effort {
			t.Fatalf("expected Anthropic output_config.effort, got %#v", payload["output_config"])
		}
	}
}

func TestAnthropicReasoningEffortUsesModelSupportMatrix(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		effort    string
		supported bool
	}{
		{name: "Sonnet 5 xhigh", model: "claude-sonnet-5", effort: "xhigh", supported: true},
		{name: "Bedrock-style Sonnet 4.6 max", model: "anthropic.claude-sonnet-4-6-20260217-v1:0", effort: "max", supported: true},
		{name: "Opus 4.6 xhigh", model: "claude-opus-4-6", effort: "xhigh"},
		{name: "Opus 4.5 max", model: "claude-opus-4-5", effort: "max"},
		{name: "legacy model", model: "claude-3-5-sonnet", effort: "high"},
		{name: "model family boundary", model: "claude-sonnet-50", effort: "high"},
		{name: "unknown alias", model: "company-reasoning-model", effort: "high"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := anthropicReasoningEffortSupported(test.model, test.effort); got != test.supported {
				t.Fatalf("anthropicReasoningEffortSupported(%q, %q) = %v, want %v", test.model, test.effort, got, test.supported)
			}
		})
	}
}

func TestGeminiAdapterTranslatesReasoningEffort(t *testing.T) {
	tests := []struct {
		name          string
		providerModel string
		effort        string
		useResponses  bool
		configKey     string
		configValue   any
	}{
		{
			name:          "Gemini 3 thinking level",
			providerModel: "gemini-3.5-flash",
			effort:        "high",
			configKey:     "thinkingLevel",
			configValue:   "high",
		},
		{
			name:          "Gemini 2.5 thinking budget",
			providerModel: "gemini-2.5-flash",
			effort:        "medium",
			useResponses:  true,
			configKey:     "thinkingBudget",
			configValue:   float64(8192),
		},
		{
			name:          "Gemini 3.1 Pro minimal maps to low",
			providerModel: "gemini-3.1-pro",
			effort:        "minimal",
			configKey:     "thinkingLevel",
			configValue:   "low",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamRequest map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
					t.Errorf("decode upstream request: %v", err)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{
							"parts": []map[string]any{{"text": "ok"}},
						},
					}},
					"usageMetadata": map[string]any{},
				})
			}))
			defer upstream.Close()

			adapter := GeminiAdapter{}
			provider := Provider{BaseURL: upstream.URL}
			if test.useResponses {
				_, _, err := adapter.Responses(context.Background(), provider, test.providerModel, ResponsesRequest{
					Model: "reasoning-model",
					Input: "reason",
					Reasoning: &ResponsesReasoning{
						Effort: &test.effort,
					},
				})
				if err != nil {
					t.Fatalf("responses translation failed: %v", err)
				}
			} else {
				_, _, err := adapter.Chat(context.Background(), provider, test.providerModel, ChatCompletionRequest{
					Model:           "reasoning-model",
					Messages:        []ChatMessage{{Role: "user", Content: "reason"}},
					ReasoningEffort: &test.effort,
				})
				if err != nil {
					t.Fatalf("chat translation failed: %v", err)
				}
			}

			generationConfig, ok := upstreamRequest["generationConfig"].(map[string]any)
			if !ok {
				t.Fatalf("expected generationConfig, got %#v", upstreamRequest)
			}
			thinkingConfig, ok := generationConfig["thinkingConfig"].(map[string]any)
			if !ok || thinkingConfig[test.configKey] != test.configValue {
				t.Fatalf("expected %s=%#v, got %#v", test.configKey, test.configValue, thinkingConfig)
			}
		})
	}
}

func TestNativeAdaptersOmitUnmappableReasoningEffort(t *testing.T) {
	tests := []struct {
		name          string
		adapter       ProviderAdapter
		providerModel string
		effort        string
		anthropic     bool
	}{
		{name: "Anthropic minimal", adapter: AnthropicAdapter{}, providerModel: "claude-sonnet-5", effort: "minimal", anthropic: true},
		{name: "Anthropic legacy model", adapter: AnthropicAdapter{}, providerModel: "claude-3-5-sonnet", effort: "high", anthropic: true},
		{name: "Anthropic unsupported model level", adapter: AnthropicAdapter{}, providerModel: "claude-opus-4-6", effort: "xhigh", anthropic: true},
		{name: "Gemini xhigh", adapter: GeminiAdapter{}, providerModel: "gemini-3.5-flash", effort: "xhigh"},
		{name: "Gemini legacy model", adapter: GeminiAdapter{}, providerModel: "gemini-2.0-flash", effort: "high"},
		{name: "Gemini 2.5 Pro none", adapter: GeminiAdapter{}, providerModel: "gemini-2.5-pro", effort: "none"},
		{name: "Gemini 3.1 Flash Lite Image low", adapter: GeminiAdapter{}, providerModel: "gemini-3.1-flash-lite-image", effort: "low"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamRequest map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&upstreamRequest); err != nil {
					t.Errorf("decode upstream request: %v", err)
					return
				}
				if test.anthropic {
					_ = json.NewEncoder(w).Encode(map[string]any{
						"content": []map[string]any{{"type": "text", "text": "ok"}},
						"usage":   map[string]any{},
					})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"candidates": []map[string]any{{
						"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}},
					}},
					"usageMetadata": map[string]any{},
				})
			}))
			defer upstream.Close()

			_, _, err := test.adapter.Chat(context.Background(), Provider{BaseURL: upstream.URL}, test.providerModel, ChatCompletionRequest{
				Model:           "reasoning-model",
				Messages:        []ChatMessage{{Role: "user", Content: "reason"}},
				ReasoningEffort: &test.effort,
			})
			if err != nil {
				t.Fatalf("unsupported effort should not fail: %v", err)
			}
			if test.anthropic {
				if _, exists := upstreamRequest["output_config"]; exists {
					t.Fatalf("unsupported Anthropic effort should be omitted: %#v", upstreamRequest)
				}
				return
			}
			if generationConfig, ok := upstreamRequest["generationConfig"].(map[string]any); ok {
				if _, exists := generationConfig["thinkingConfig"]; exists {
					t.Fatalf("unsupported Gemini effort should be omitted: %#v", upstreamRequest)
				}
			}
		})
	}
}

func TestUnsupportedAnthropicModelOmitsEffortWithoutRetry(t *testing.T) {
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		upstreamRequests = append(upstreamRequests, payload)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]any{{"type": "text", "text": "ok"}},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, store, secret := newReasoningEffortGateway(t, upstream.URL, ProviderAnthropic)
	resource := addReasoningProviderResource(t, store, upstream.URL, 1, 0)
	if _, err := store.UpdateRoute("route_reasoning_0", ModelRoute{
		ProviderResourceID: resource.ID,
		ProviderModel:      "claude-3-5-sonnet",
	}); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "reason"}},
		"reasoning_effort": "high",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("unsupported model should use its default effort without retry, got %d: %s", resp.Code, resp.Body)
	}
	if len(upstreamRequests) != 1 {
		t.Fatalf("expected exactly one upstream request, got %d", len(upstreamRequests))
	}
	if _, exists := upstreamRequests[0]["output_config"]; exists {
		t.Fatalf("unsupported model should not receive output_config: %#v", upstreamRequests[0])
	}
	var bucket ProviderResourceBucket
	if err := store.db.First(&bucket, "resource_id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bucket.Requests != 1 {
		t.Fatalf("expected one physical request to consume RPM, got %d", bucket.Requests)
	}
}

func TestGeminiReasoningEffortUsesParsedModelFamily(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		effort    string
		want      map[string]any
		supported bool
	}{
		{
			name:      "project path does not turn 2.5 Flash into Pro",
			model:     "projects/acme/locations/us/models/gemini-2.5-flash",
			effort:    "none",
			want:      map[string]any{"thinkingBudget": 0},
			supported: true,
		},
		{
			name:      "project path does not turn 3.1 Flash into Pro",
			model:     "projects/acme/locations/us/models/gemini-3.1-flash",
			effort:    "minimal",
			want:      map[string]any{"thinkingLevel": "minimal"},
			supported: true,
		},
		{
			name:      "custom prefix still identifies Pro",
			model:     "my-production-endpoint/gemini-3.1-pro-preview",
			effort:    "minimal",
			want:      map[string]any{"thinkingLevel": "low"},
			supported: true,
		},
		{
			name:   "project path preserves Flash Lite Image restrictions",
			model:  "projects/acme/models/gemini-3.1-flash-lite-image",
			effort: "low",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, supported := geminiThinkingConfig(test.model, test.effort)
			if supported != test.supported {
				t.Fatalf("supported = %v, want %v; config=%#v", supported, test.supported, got)
			}
			if fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Fatalf("config = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestGatewayRetriesWithoutRejectedReasoningEffort(t *testing.T) {
	var upstreamRequests []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		upstreamRequests = append(upstreamRequests, payload)
		if _, exists := payload["reasoning_effort"]; exists {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"reasoning_effort is unsupported"}}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_test",
			"object":  "chat.completion",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer upstream.Close()

	server, store, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_reasoning_fallback",
		ProviderID:   "prv_reasoning_0",
		Name:         "Reasoning fallback resource",
		BaseURL:      upstream.URL,
		Status:       StatusActive,
		Healthy:      true,
		RateLimitRPM: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRoute("route_reasoning_0", ModelRoute{ProviderResourceID: resource.ID}); err != nil {
		t.Fatal(err)
	}

	effort := "high"
	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "reason"}},
		"reasoning_effort": effort,
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("effort rejection should retry without effort, got %d: %s", resp.Code, resp.Body)
	}
	if len(upstreamRequests) != 2 {
		t.Fatalf("expected an effort request and one fallback request, got %d", len(upstreamRequests))
	}
	if upstreamRequests[0]["reasoning_effort"] != effort {
		t.Fatalf("first request should include effort: %#v", upstreamRequests[0])
	}
	if _, exists := upstreamRequests[1]["reasoning_effort"]; exists {
		t.Fatalf("fallback request should omit effort: %#v", upstreamRequests[1])
	}

	var routeAttempts []RouteAttemptLog
	if err := store.db.Order("attempt_index asc").Find(&routeAttempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(routeAttempts) != 2 ||
		routeAttempts[0].StatusCode != http.StatusBadRequest ||
		routeAttempts[0].ErrorCode != "reasoning_effort_rejected" ||
		routeAttempts[1].StatusCode != http.StatusOK {
		t.Fatalf("expected audited effort fallback attempts, got %#v", routeAttempts)
	}

	var bucket ProviderResourceBucket
	if err := store.db.First(&bucket, "resource_id = ?", resource.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bucket.Requests != 2 {
		t.Fatalf("expected both physical requests to count toward RPM, got %d", bucket.Requests)
	}
}

func TestNativeGatewayRoutesRetryWithoutRejectedReasoningEffort(t *testing.T) {
	tests := []struct {
		name          string
		providerType  string
		providerModel string
		hasEffort     func(map[string]any) bool
		successBody   map[string]any
	}{
		{
			name:          "Anthropic",
			providerType:  ProviderAnthropic,
			providerModel: "claude-sonnet-5",
			hasEffort: func(payload map[string]any) bool {
				_, exists := payload["output_config"]
				return exists
			},
			successBody: map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ok"}},
				"usage":   map[string]any{},
			},
		},
		{
			name:          "Gemini",
			providerType:  ProviderGemini,
			providerModel: "gemini-3.5-flash",
			hasEffort: func(payload map[string]any) bool {
				generationConfig, ok := payload["generationConfig"].(map[string]any)
				if !ok {
					return false
				}
				_, exists := generationConfig["thinkingConfig"]
				return exists
			},
			successBody: map[string]any{
				"candidates": []map[string]any{{
					"content": map[string]any{"parts": []map[string]any{{"text": "ok"}}},
				}},
				"usageMetadata": map[string]any{},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamRequests []map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode upstream request: %v", err)
					return
				}
				upstreamRequests = append(upstreamRequests, payload)
				if test.hasEffort(payload) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"outputConfig or thinkingLevel effort is unsupported"}}`)
					return
				}
				_ = json.NewEncoder(w).Encode(test.successBody)
			}))
			defer upstream.Close()

			server, store, secret := newReasoningEffortGateway(t, upstream.URL, test.providerType)
			route := store.ListRoutes()[0]
			route.ProviderModel = test.providerModel
			store.AddRoute(route)
			effort := "high"
			resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
				"model":            "reasoning-model",
				"messages":         []map[string]any{{"role": "user", "content": "reason"}},
				"reasoning_effort": effort,
			}, secret)
			if resp.Code != http.StatusOK {
				t.Fatalf("effort rejection should retry without effort, got %d: %s", resp.Code, resp.Body)
			}
			if len(upstreamRequests) != 2 {
				t.Fatalf("expected an effort request and one fallback request, got %d", len(upstreamRequests))
			}
			if !test.hasEffort(upstreamRequests[0]) || test.hasEffort(upstreamRequests[1]) {
				t.Fatalf("expected effort only in the first request: %#v", upstreamRequests)
			}
		})
	}
}

func TestReasoningEffortRejectionMatcher(t *testing.T) {
	tests := []struct {
		name           string
		upstreamStatus int
		body           string
		want           bool
	}{
		{
			name:           "invalid OpenAI field",
			upstreamStatus: http.StatusBadRequest,
			body:           `{"error":{"message":"Invalid reasoning_effort: high"}}`,
			want:           true,
		},
		{
			name:           "unknown Gemini thinking config",
			upstreamStatus: http.StatusUnprocessableEntity,
			body:           `{"error":{"message":"Unknown name thinkingLevel in thinkingConfig"}}`,
			want:           true,
		},
		{
			name:           "server error mentioning effort",
			upstreamStatus: http.StatusInternalServerError,
			body:           `{"error":{"message":"reasoning_effort worker failed"}}`,
		},
		{
			name:           "rate limit mentioning reasoning",
			upstreamStatus: http.StatusTooManyRequests,
			body:           `{"error":{"message":"reasoning_effort rate limit reached"}}`,
		},
		{
			name:           "unrelated effort text",
			upstreamStatus: http.StatusBadRequest,
			body:           `{"error":{"message":"Unable to complete the requested effort"}}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := newProviderHTTPError(test.upstreamStatus, nil, []byte(test.body))
			if got := isReasoningEffortRejection(err); got != test.want {
				t.Fatalf("isReasoningEffortRejection() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGatewayRetriesResponsesAndStreamingChatWithoutRejectedEffort(t *testing.T) {
	tests := []struct {
		name         string
		providerType string
		path         string
		request      map[string]any
		effortField  func(map[string]any) bool
	}{
		{
			name:         "OpenAI responses",
			providerType: ProviderOpenAICompatible,
			path:         "/v1/responses",
			request: map[string]any{
				"model":     "reasoning-model",
				"input":     "reason",
				"reasoning": map[string]any{"effort": "high"},
			},
			effortField: func(payload map[string]any) bool {
				_, exists := payload["reasoning"]
				return exists
			},
		},
		{
			name:         "Azure streaming chat",
			providerType: ProviderAzureOpenAI,
			path:         "/v1/chat/completions",
			request: map[string]any{
				"model":            "reasoning-model",
				"messages":         []map[string]any{{"role": "user", "content": "reason"}},
				"reasoning_effort": "high",
				"stream":           true,
			},
			effortField: func(payload map[string]any) bool {
				_, exists := payload["reasoning_effort"]
				return exists
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamRequests []map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode upstream request: %v", err)
					return
				}
				upstreamRequests = append(upstreamRequests, payload)
				if test.effortField(payload) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"reasoning_effort is unsupported"}}`)
					return
				}
				if streaming, _ := payload["stream"].(bool); streaming {
					w.Header().Set("content-type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\ndata: [DONE]\n\n")
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":     "resp_test",
					"object": "response",
					"output": []map[string]any{},
					"usage":  map[string]any{},
				})
			}))
			defer upstream.Close()

			server, _, secret := newReasoningEffortGateway(t, upstream.URL, test.providerType)
			resp := doReasoningJSON(t, server.Handler(), test.path, test.request, secret)
			if resp.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
			}
			if len(upstreamRequests) != 2 ||
				!test.effortField(upstreamRequests[0]) ||
				test.effortField(upstreamRequests[1]) {
				t.Fatalf("expected one effort request and one fallback request: %#v", upstreamRequests)
			}
		})
	}
}

func TestEffortFallbackTracksResponsesAndStreamingPhysicalAttempts(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		request map[string]any
		stream  bool
	}{
		{
			name: "responses",
			path: "/v1/responses",
			request: map[string]any{
				"model":     "reasoning-model",
				"input":     "reason",
				"reasoning": map[string]any{"effort": "high"},
			},
		},
		{
			name:   "streaming chat",
			path:   "/v1/chat/completions",
			stream: true,
			request: map[string]any{
				"model":            "reasoning-model",
				"messages":         []map[string]any{{"role": "user", "content": "reason"}},
				"reasoning_effort": "high",
				"stream":           true,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var upstreamRequests []map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode upstream request: %v", err)
					return
				}
				upstreamRequests = append(upstreamRequests, payload)
				hasEffort := payload["reasoning_effort"] != nil || payload["reasoning"] != nil
				if hasEffort {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"reasoning_effort is unsupported"}}`)
					return
				}
				if test.stream {
					w.Header().Set("content-type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"choices\":[]}\n\ndata: [DONE]\n\n")
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id":     "resp_test",
					"object": "response",
					"output": []map[string]any{},
					"usage":  map[string]any{},
				})
			}))
			defer upstream.Close()

			server, store, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
			resource := addReasoningProviderResource(t, store, upstream.URL, 10, 1)
			probe := &retryConcurrencyProbeStore{
				Store:      store,
				base:       store,
				resourceID: resource.ID,
			}
			server.store = probe

			resp := doReasoningJSON(t, server.Handler(), test.path, test.request, secret)
			if resp.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", resp.Code, resp.Body.String())
			}
			if resp.Header().Get("x-tokenhub-route-attempts") != "2" {
				t.Fatalf("expected two route attempts, got headers %#v", resp.Header())
			}
			if len(upstreamRequests) != 2 {
				t.Fatalf("expected two physical upstream requests, got %d", len(upstreamRequests))
			}
			probeHTTPError := AsHTTPError(probe.probeErr)
			if probe.calls != 1 || probeHTTPError == nil || probeHTTPError.Code != "provider_resource_concurrency_exceeded" {
				t.Fatalf("expected the original concurrency lease to remain held during retry, calls=%d err=%v", probe.calls, probe.probeErr)
			}

			var bucket ProviderResourceBucket
			if err := store.db.First(&bucket, "resource_id = ?", resource.ID).Error; err != nil {
				t.Fatal(err)
			}
			if bucket.Requests != 2 {
				t.Fatalf("expected both physical requests to count toward RPM, got %d", bucket.Requests)
			}
			var attempts []RouteAttemptLog
			if err := store.db.Order("attempt_index asc").Find(&attempts).Error; err != nil {
				t.Fatal(err)
			}
			if len(attempts) != 2 ||
				attempts[0].ErrorCode != "reasoning_effort_rejected" ||
				attempts[1].StatusCode != http.StatusOK {
				t.Fatalf("unexpected audited attempt sequence: %#v", attempts)
			}
		})
	}
}

func TestStreamingEffortFallbackSurfacesPreStreamErrors(t *testing.T) {
	tests := []struct {
		name             string
		rateLimitRPM     int64
		secondStatus     int
		wantStatus       int
		wantCode         string
		wantUpstreamCall int
		wantRequests     int64
	}{
		{
			name:             "second upstream request fails",
			rateLimitRPM:     10,
			secondStatus:     http.StatusInternalServerError,
			wantStatus:       http.StatusBadGateway,
			wantCode:         "provider_upstream_error",
			wantUpstreamCall: 2,
			wantRequests:     2,
		},
		{
			name:             "retry exceeds provider RPM",
			rateLimitRPM:     1,
			secondStatus:     http.StatusOK,
			wantStatus:       http.StatusTooManyRequests,
			wantCode:         "provider_resource_rpm_exceeded",
			wantUpstreamCall: 1,
			wantRequests:     1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			upstreamCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls++
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode upstream request: %v", err)
					return
				}
				if payload["reasoning_effort"] != nil {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"reasoning_effort is unsupported"}}`)
					return
				}
				w.WriteHeader(test.secondStatus)
				if test.secondStatus >= 400 {
					_, _ = io.WriteString(w, `{"error":{"message":"temporary upstream failure"}}`)
					return
				}
				_, _ = io.WriteString(w, "data: [DONE]\n\n")
			}))
			defer upstream.Close()

			server, store, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
			resource := addReasoningProviderResource(t, store, upstream.URL, test.rateLimitRPM, 1)
			resp := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
				"model":            "reasoning-model",
				"messages":         []map[string]any{{"role": "user", "content": "reason"}},
				"reasoning_effort": "high",
				"stream":           true,
			}, secret)

			if resp.Code != test.wantStatus {
				t.Fatalf("expected %d, got %d: %s", test.wantStatus, resp.Code, resp.Body.String())
			}
			if contentType := resp.Header().Get("content-type"); contentType != "application/json" {
				t.Fatalf("expected JSON error response, got content-type %q", contentType)
			}
			if resp.Header().Get("x-tokenhub-route-attempts") != "2" {
				t.Fatalf("expected two route attempts, got headers %#v", resp.Header())
			}
			var responseBody map[string]any
			if err := json.Unmarshal(resp.Body.Bytes(), &responseBody); err != nil {
				t.Fatalf("decode gateway error: %v", err)
			}
			errorBody, _ := responseBody["error"].(map[string]any)
			if errorBody["code"] != test.wantCode {
				t.Fatalf("expected error code %q, got %#v", test.wantCode, responseBody)
			}
			requestID, _ := responseBody["request_id"].(string)
			if requestID == "" || requestID != resp.Header().Get("x-request-id") {
				t.Fatalf("expected matching audited request IDs, header=%q body=%q", resp.Header().Get("x-request-id"), requestID)
			}
			var requestLog RequestLog
			if err := store.db.First(&requestLog, "request_id = ?", requestID).Error; err != nil {
				t.Fatalf("expected request ID %q to resolve to an audit record: %v", requestID, err)
			}
			if upstreamCalls != test.wantUpstreamCall {
				t.Fatalf("expected %d upstream calls, got %d", test.wantUpstreamCall, upstreamCalls)
			}
			var bucket ProviderResourceBucket
			if err := store.db.First(&bucket, "resource_id = ?", resource.ID).Error; err != nil {
				t.Fatal(err)
			}
			if bucket.Requests != test.wantRequests {
				t.Fatalf("expected %d counted requests, got %d", test.wantRequests, bucket.Requests)
			}
		})
	}
}

func TestStreamingEffortFallbackDoesNotRetryAfterWriting(t *testing.T) {
	server, _, secret := newReasoningEffortGateway(t, "http://127.0.0.1:1", ProviderOpenAICompatible)
	adapter := &partialStreamEffortRejectAdapter{}
	server.adapterRegistry.Register(
		ProviderOpenAICompatible,
		adapter,
		AdapterCapabilityChat,
		AdapterCapabilityChatStream,
		AdapterCapabilityResponses,
		AdapterCapabilityEmbeddings,
		AdapterCapabilityProbe,
	)

	resp := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "reason"}},
		"reasoning_effort": "high",
		"stream":           true,
	}, secret)
	if resp.Code != http.StatusOK || resp.Body.String() != "data: partial\n\n" {
		t.Fatalf("expected the already-started stream to close without a second response, got %d: %q", resp.Code, resp.Body.String())
	}
	if !resp.Flushed {
		t.Fatal("expected the streaming response headers to be flushed before the first event")
	}
	if adapter.calls != 1 {
		t.Fatalf("expected no retry after stream bytes were written, got %d calls", adapter.calls)
	}
}

func TestRoutedErrorsReuseAuditedRequestID(t *testing.T) {
	assertAuditedRequestID := func(t *testing.T, store *GormStore, resp *httptest.ResponseRecorder) {
		t.Helper()
		var responseBody map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &responseBody); err != nil {
			t.Fatalf("decode gateway error: %v", err)
		}
		requestID, _ := responseBody["request_id"].(string)
		if requestID == "" || requestID != resp.Header().Get("x-request-id") {
			t.Fatalf("expected matching request IDs, header=%q body=%q", resp.Header().Get("x-request-id"), requestID)
		}
		var requestLog RequestLog
		if err := store.db.First(&requestLog, "request_id = ?", requestID).Error; err != nil {
			t.Fatalf("expected request ID %q to resolve to an audit record: %v", requestID, err)
		}
	}

	t.Run("provider failure", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"temporary upstream failure"}}`)
		}))
		defer upstream.Close()

		server, store, secret := newReasoningEffortGateway(t, upstream.URL, ProviderOpenAICompatible)
		resp := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
			"model":    "reasoning-model",
			"messages": []map[string]any{{"role": "user", "content": "reason"}},
		}, secret)
		if resp.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d: %s", resp.Code, resp.Body.String())
		}
		assertAuditedRequestID(t, store, resp)
	})

	t.Run("route selection failure", func(t *testing.T) {
		server, store, secret := newReasoningEffortGateway(t, "http://127.0.0.1:1", ProviderOpenAICompatible)
		if err := store.DeleteRoute("route_reasoning_0"); err != nil {
			t.Fatal(err)
		}
		resp := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", map[string]any{
			"model":    "reasoning-model",
			"messages": []map[string]any{{"role": "user", "content": "reason"}},
		}, secret)
		if resp.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d: %s", resp.Code, resp.Body.String())
		}
		assertAuditedRequestID(t, store, resp)
	})

	t.Run("quota rejection", func(t *testing.T) {
		server, store, secret := newReasoningEffortGateway(t, "http://127.0.0.1:1", ProviderMock)
		keys := store.ListAPIKeys()
		if len(keys) != 1 {
			t.Fatalf("expected one API key, got %d", len(keys))
		}
		if _, err := store.UpdateAPIKey(keys[0].ID, APIKey{
			Limits: QuotaLimits{DailyRequests: 1, MonthlyRequests: 1},
		}); err != nil {
			t.Fatal(err)
		}
		request := map[string]any{
			"model":    "reasoning-model",
			"messages": []map[string]any{{"role": "user", "content": "reason"}},
		}
		first := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", request, secret)
		if first.Code != http.StatusOK {
			t.Fatalf("expected first request to succeed, got %d: %s", first.Code, first.Body.String())
		}
		second := doReasoningJSON(t, server.Handler(), "/v1/chat/completions", request, secret)
		if second.Code != http.StatusTooManyRequests {
			t.Fatalf("expected quota rejection, got %d: %s", second.Code, second.Body.String())
		}
		assertAuditedRequestID(t, store, second)
	})
}

func TestGatewayFallsBackToBackupAfterEffortRetryFails(t *testing.T) {
	var primaryRequests []map[string]any
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode primary request: %v", err)
			return
		}
		primaryRequests = append(primaryRequests, payload)
		if _, exists := payload["reasoning_effort"]; exists {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"reasoning_effort is unsupported"}}`)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"temporary primary failure"}}`)
	}))
	defer primary.Close()

	backupCalls := 0
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl_backup",
			"object":  "chat.completion",
			"choices": []map[string]any{},
			"usage":   map[string]any{},
		})
	}))
	defer backup.Close()

	server, store, secret := newReasoningEffortGateway(
		t,
		primary.URL,
		ProviderOpenAICompatible,
		ProviderOpenAICompatible,
	)
	providers := store.ListProviders()
	for _, provider := range providers {
		if provider.ID == "prv_reasoning_1" {
			provider.BaseURL = backup.URL
			store.AddProvider(provider)
		}
	}

	resp := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":            "reasoning-model",
		"messages":         []map[string]any{{"role": "user", "content": "reason"}},
		"reasoning_effort": "high",
	}, secret)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected backup success, got %d: %s", resp.Code, resp.Body)
	}
	if len(primaryRequests) != 2 || backupCalls != 1 {
		t.Fatalf("expected primary effort attempt, primary fallback, and one backup call; primary=%d backup=%d", len(primaryRequests), backupCalls)
	}

	var attempts []RouteAttemptLog
	if err := store.db.Order("attempt_index asc").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 3 ||
		attempts[0].ErrorCode != "reasoning_effort_rejected" ||
		attempts[1].StatusCode != http.StatusBadGateway ||
		attempts[2].StatusCode != http.StatusOK {
		t.Fatalf("unexpected audited attempt sequence: %#v", attempts)
	}
}

func TestResponsesStreamingRejectsRoutesWithoutCapability(t *testing.T) {
	server, store, secret := newReasoningEffortGateway(t, "http://127.0.0.1:1", ProviderMock)
	assertAuditedError := func(t *testing.T, resp *httptest.ResponseRecorder, wantStatus int, wantCode string) {
		t.Helper()
		if resp.Code != wantStatus {
			t.Fatalf("expected %d, got %d: %s", wantStatus, resp.Code, resp.Body.String())
		}
		var responseBody map[string]any
		if err := json.Unmarshal(resp.Body.Bytes(), &responseBody); err != nil {
			t.Fatalf("decode gateway error: %v", err)
		}
		errorBody, _ := responseBody["error"].(map[string]any)
		if errorBody["code"] != wantCode {
			t.Fatalf("expected error code %q, got %#v", wantCode, responseBody)
		}
		requestID, _ := responseBody["request_id"].(string)
		if requestID == "" || requestID != resp.Header().Get("x-request-id") {
			t.Fatalf("expected matching request IDs, header=%q body=%q", resp.Header().Get("x-request-id"), requestID)
		}
		var requestLog RequestLog
		if err := store.db.First(&requestLog, "request_id = ?", requestID).Error; err != nil {
			t.Fatalf("expected request ID %q to resolve to an audit record: %v", requestID, err)
		}
		if requestLog.StatusCode != wantStatus || requestLog.ErrorCode != wantCode {
			t.Fatalf("unexpected request log: %#v", requestLog)
		}
	}

	t.Run("authorized model", func(t *testing.T) {
		resp := doReasoningJSON(t, server.Handler(), "/v1/responses", map[string]any{
			"model":     "reasoning-model",
			"input":     "reason",
			"stream":    true,
			"reasoning": map[string]any{"effort": "high"},
		}, secret)
		assertAuditedError(t, resp, http.StatusNotImplemented, "provider_capability_not_supported")
	})

	t.Run("model permission is checked first", func(t *testing.T) {
		store.AddModel(Model{
			Name:         "restricted-reasoning-model",
			Modality:     "chat",
			Capabilities: []string{"chat", "reasoning"},
			Status:       StatusActive,
		})
		resp := doReasoningJSON(t, server.Handler(), "/v1/responses", map[string]any{
			"model":  "restricted-reasoning-model",
			"input":  "reason",
			"stream": true,
		}, secret)
		assertAuditedError(t, resp, http.StatusForbidden, "model_not_allowed")
	})
}

func newReasoningEffortGateway(t *testing.T, upstreamURL string, providerTypes ...string) (*Server, *GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Reasoning Effort Project"})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "reasoning-effort-key",
		Allowed: []string{"reasoning-model"},
		Status:  StatusActive,
	}, "thk_reasoning_effort")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{
		Name:                "reasoning-model",
		Modality:            "chat",
		Capabilities:        []string{"chat", "reasoning"},
		SupportedParameters: []string{"reasoning"},
		Status:              StatusActive,
	})
	for index, providerType := range providerTypes {
		providerID := fmt.Sprintf("prv_reasoning_%d", index)
		provider := store.AddProvider(Provider{
			ID:      providerID,
			Name:    fmt.Sprintf("Reasoning Provider %d", index),
			Type:    providerType,
			BaseURL: upstreamURL,
			Status:  StatusActive,
			Healthy: true,
		})
		store.AddRoute(ModelRoute{
			ID:            fmt.Sprintf("route_reasoning_%d", index),
			ModelName:     "reasoning-model",
			ProviderID:    provider.ID,
			ProviderModel: "upstream-reasoning-model",
			Priority:      index + 1,
			Weight:        100,
			Status:        StatusActive,
			Strategy:      RouteStrategyPriorityOnly,
		})
	}
	return New(store), store, secret
}

func addReasoningProviderResource(t *testing.T, store *GormStore, upstreamURL string, rateLimitRPM int64, maxConcurrency int64) ProviderResource {
	t.Helper()
	resource, err := store.AddProviderResource(ProviderResource{
		ID:             "rsrc_reasoning_test",
		ProviderID:     "prv_reasoning_0",
		Name:           "Reasoning test resource",
		BaseURL:        upstreamURL,
		Status:         StatusActive,
		Healthy:        true,
		RateLimitRPM:   rateLimitRPM,
		MaxConcurrency: maxConcurrency,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRoute("route_reasoning_0", ModelRoute{ProviderResourceID: resource.ID}); err != nil {
		t.Fatal(err)
	}
	return resource
}

func doReasoningJSON(t *testing.T, handler http.Handler, path string, payload any, token string) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+token)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

type retryConcurrencyProbeStore struct {
	Store
	base       *GormStore
	resourceID string
	probeErr   error
	calls      int
}

func (s *retryConcurrencyProbeStore) CheckProviderResourceRetryCapacity(ctx context.Context, resourceID string, leaseID string) error {
	s.calls++
	probeLeaseID, _, err := s.base.CheckProviderResourceCapacity(context.Background(), s.resourceID)
	s.probeErr = err
	if err == nil {
		s.base.ReleaseProviderResourceCapacity(s.resourceID, probeLeaseID)
	}
	return s.Store.CheckProviderResourceRetryCapacity(ctx, resourceID, leaseID)
}

type partialStreamEffortRejectAdapter struct {
	calls int
}

func (a *partialStreamEffortRejectAdapter) Chat(context.Context, Provider, string, ChatCompletionRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusNotImplemented, "not_implemented", "not implemented")
}

func (a *partialStreamEffortRejectAdapter) ChatStream(_ context.Context, _ Provider, _ string, _ ChatCompletionRequest, w io.Writer) (Usage, error) {
	a.calls++
	_, _ = io.WriteString(w, "data: partial\n\n")
	return Usage{}, newProviderHTTPError(http.StatusBadRequest, nil, []byte(`{"error":{"message":"reasoning_effort is unsupported"}}`))
}

func (a *partialStreamEffortRejectAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusNotImplemented, "not_implemented", "not implemented")
}

func (a *partialStreamEffortRejectAdapter) Embeddings(context.Context, Provider, string, EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, NewHTTPError(http.StatusNotImplemented, "not_implemented", "not implemented")
}
