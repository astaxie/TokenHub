package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAnthropicRequestForRouteStripsOnlyLeadingClaudeCodeAttribution(t *testing.T) {
	attribution := map[string]any{
		"type":          "text",
		"text":          "x-anthropic-billing-header: cc_version=2.1.220.134; cc_entrypoint=sdk-cli; cch=abc12;",
		"cache_control": map[string]any{"type": "ephemeral"},
	}
	stable := map[string]any{
		"type":          "text",
		"text":          "Stable system instructions.",
		"cache_control": map[string]any{"type": "ephemeral"},
	}
	route := RouteSelection{Provider: Provider{Options: map[string]string{
		claudeCodeAttributionPolicyOption: claudeCodeAttributionStrip,
	}}}

	t.Run("matching first block", func(t *testing.T) {
		raw := map[string]any{
			"system":   []any{attribution, stable},
			"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
		}
		original := anthropicMessagesRequest{Raw: raw, Messages: raw["messages"].([]any)}

		filtered := anthropicRequestForRoute(original, route)
		blocks, ok := filtered.Raw["system"].([]any)
		if !ok || len(blocks) != 1 || !reflect.DeepEqual(blocks[0], stable) {
			t.Fatalf("unexpected filtered system blocks: %#v", filtered.Raw["system"])
		}
		originalBlocks := original.Raw["system"].([]any)
		if len(originalBlocks) != 2 || !reflect.DeepEqual(originalBlocks[0], attribution) {
			t.Fatalf("original request was mutated: %#v", original.Raw["system"])
		}
	})

	t.Run("only matching block removes system field", func(t *testing.T) {
		raw := map[string]any{"system": []any{attribution}}
		filtered := anthropicRequestForRoute(anthropicMessagesRequest{Raw: raw}, route)
		if _, exists := filtered.Raw["system"]; exists {
			t.Fatalf("empty filtered system field should be omitted: %#v", filtered.Raw)
		}
		if len(raw["system"].([]any)) != 1 {
			t.Fatalf("original request was mutated: %#v", raw)
		}
	})

	for _, test := range []struct {
		name   string
		system any
	}{
		{name: "plain string", system: "x-anthropic-billing-header: business text"},
		{name: "non-leading block", system: []any{stable, attribution}},
		{name: "leading whitespace", system: []any{map[string]any{"type": "text", "text": " x-anthropic-billing-header: value"}, stable}},
		{name: "different block type", system: []any{map[string]any{"type": "tool", "text": "x-anthropic-billing-header: value"}, stable}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := map[string]any{"system": test.system}
			filtered := anthropicRequestForRoute(anthropicMessagesRequest{Raw: raw}, route)
			if !reflect.DeepEqual(filtered.Raw, raw) {
				t.Fatalf("non-matching request changed: got %#v want %#v", filtered.Raw, raw)
			}
		})
	}

	t.Run("preserve policy", func(t *testing.T) {
		raw := map[string]any{"system": []any{attribution, stable}}
		preserved := anthropicRequestForRoute(anthropicMessagesRequest{Raw: raw}, RouteSelection{})
		if !reflect.DeepEqual(preserved.Raw, raw) {
			t.Fatalf("preserve policy changed the request: got %#v want %#v", preserved.Raw, raw)
		}
	})
}

func TestAnthropicRequestForRouteNormalizesDynamicAttributionPrefixes(t *testing.T) {
	route := RouteSelection{Provider: Provider{Options: map[string]string{
		claudeCodeAttributionPolicyOption: claudeCodeAttributionStrip,
	}}}
	filter := func(text string) map[string]any {
		t.Helper()
		raw := map[string]any{"system": []any{
			map[string]any{"type": "text", "text": text},
			map[string]any{"type": "text", "text": "Stable system instructions."},
		}}
		return anthropicRequestForRoute(anthropicMessagesRequest{Raw: raw}, route).Raw
	}

	first := filter("x-anthropic-billing-header: cc_version=2.1.220.134; cc_entrypoint=sdk-cli;")
	second := filter("x-anthropic-billing-header: cch=fffff; cc_entrypoint=sdk-cli; cc_version=2.1.220.25c;")
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("dynamic attribution variants produced different payloads:\nfirst=%#v\nsecond=%#v", first, second)
	}
}

func TestDefaultClaudeCodeAttributionPolicyForNewProvider(t *testing.T) {
	for _, test := range []struct {
		name         string
		providerType string
		catalogID    string
		want         string
	}{
		{name: "official Anthropic catalog", providerType: ProviderAnthropic, catalogID: "anthropic", want: claudeCodeAttributionPreserve},
		{name: "unknown custom Anthropic", providerType: ProviderAnthropic, catalogID: "custom", want: claudeCodeAttributionPreserve},
		{name: "unknown direct Anthropic", providerType: ProviderAnthropic, want: claudeCodeAttributionPreserve},
		{name: "known third-party Anthropic", providerType: ProviderAnthropic, catalogID: "google-vertex-anthropic", want: claudeCodeAttributionStrip},
		{name: "OpenAI compatible", providerType: ProviderOpenAICompatible, catalogID: "requesty", want: claudeCodeAttributionStrip},
		{name: "OpenAI official", providerType: ProviderOpenAI, catalogID: "openai", want: claudeCodeAttributionStrip},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := defaultClaudeCodeAttributionPolicy(test.providerType, test.catalogID); got != test.want {
				t.Fatalf("default policy = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAdminProviderClaudeCodeAttributionDefaultsOnlyOnCreate(t *testing.T) {
	app := newTestServer()
	thirdParty := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":       "prv_default_strip",
		"name":     "Default Strip",
		"type":     ProviderOpenAICompatible,
		"base_url": "https://example.invalid/v1",
		"status":   StatusActive,
		"healthy":  true,
	}, "")
	if thirdParty.Code != http.StatusCreated {
		t.Fatalf("expected third-party provider creation, got %d: %s", thirdParty.Code, thirdParty.Body)
	}
	var thirdPartyResult ProviderCreateResult
	if err := json.Unmarshal([]byte(thirdParty.Body), &thirdPartyResult); err != nil {
		t.Fatal(err)
	}
	if got := thirdPartyResult.Provider.Options[claudeCodeAttributionPolicyOption]; got != claudeCodeAttributionStrip {
		t.Fatalf("third-party default policy = %q, want strip", got)
	}

	unknownAnthropic := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":       "prv_unknown_anthropic",
		"name":     "Unknown Anthropic",
		"type":     ProviderAnthropic,
		"base_url": "https://anthropic-proxy.example.invalid",
		"status":   StatusActive,
		"healthy":  true,
	}, "")
	if unknownAnthropic.Code != http.StatusCreated {
		t.Fatalf("expected unknown Anthropic provider creation, got %d: %s", unknownAnthropic.Code, unknownAnthropic.Body)
	}
	var anthropicResult ProviderCreateResult
	if err := json.Unmarshal([]byte(unknownAnthropic.Body), &anthropicResult); err != nil {
		t.Fatal(err)
	}
	if got := anthropicResult.Provider.Options[claudeCodeAttributionPolicyOption]; got != claudeCodeAttributionPreserve {
		t.Fatalf("unknown Anthropic default policy = %q, want preserve", got)
	}

	store := NewMemoryStore()
	store.AddProvider(Provider{
		ID:      "prv_legacy_attribution",
		Name:    "Legacy Attribution",
		Type:    ProviderOpenAICompatible,
		BaseURL: "https://legacy.example.invalid/v1",
		Status:  StatusActive,
		Healthy: true,
		Options: map[string]string{"region": "legacy"},
	})
	legacyApp := New(store).Handler()
	patched := doJSON(t, legacyApp, http.MethodPatch, "/api/admin/providers/prv_legacy_attribution", map[string]any{
		"name": "Legacy Attribution Updated",
	}, "")
	if patched.Code != http.StatusOK {
		t.Fatalf("expected legacy provider patch, got %d: %s", patched.Code, patched.Body)
	}
	var patchedResult ProviderCreateResult
	if err := json.Unmarshal([]byte(patched.Body), &patchedResult); err != nil {
		t.Fatal(err)
	}
	if _, exists := patchedResult.Provider.Options[claudeCodeAttributionPolicyOption]; exists {
		t.Fatalf("legacy patch must not add a new default policy: %+v", patchedResult.Provider.Options)
	}
}

func TestAnthropicAttributionPolicyAppliesPerFailoverResource(t *testing.T) {
	const attribution = "x-anthropic-billing-header: cc_version=2.1.220.134; cc_entrypoint=sdk-cli;"
	var mu sync.Mutex
	upstreamPayloads := map[string]map[string]any{}
	upstream := func(name string, status int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode %s payload: %v", name, err)
				return
			}
			mu.Lock()
			upstreamPayloads[name] = payload
			mu.Unlock()
			w.Header().Set("content-type", "application/json")
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"error":{"message":"retry elsewhere"}}`)
				return
			}
			_, _ = io.WriteString(w, `{
				"id":"chatcmpl_attribution",
				"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}
			}`)
		}))
	}
	primary := upstream("primary", http.StatusServiceUnavailable)
	defer primary.Close()
	secondary := upstream("secondary", http.StatusOK)
	defer secondary.Close()

	handler, store, secret := newAnthropicAttributionFailoverGateway(t, primary.URL, secondary.URL)
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-attribution-test",
		"max_tokens": 64,
		"system": []any{
			map[string]any{"type": "text", "text": attribution},
			map[string]any{"type": "text", "text": "Stable system instructions."},
		},
		"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected failover success, got %d: %s", resp.Code, resp.Body)
	}

	mu.Lock()
	primaryPayload := upstreamPayloads["primary"]
	secondaryPayload := upstreamPayloads["secondary"]
	mu.Unlock()
	if got := upstreamSystemMessage(primaryPayload); got != "Stable system instructions." {
		t.Fatalf("strip resource sent unexpected system message %q", got)
	}
	if got := upstreamSystemMessage(secondaryPayload); got != attribution+"\nStable system instructions." {
		t.Fatalf("preserve resource sent unexpected system message %q", got)
	}

	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("expected one request log, got %d", len(logs))
	}
	detail, err := store.GetRequestDetail(logs[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := detail["payload"].(RequestPayloadLog)
	if !ok || !strings.Contains(payload.RequestBody, attribution) {
		t.Fatalf("audit payload did not retain the original attribution block: %#v", detail["payload"])
	}
}

func TestAnthropicAttributionFailoverToNativePreservesToolsAndTurns(t *testing.T) {
	const attribution = "x-anthropic-billing-header: cc_version=2.1.220.134; cc_entrypoint=sdk-cli; cch=abc12;"
	var mu sync.Mutex
	upstreamPayloads := map[string]map[string]any{}
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode primary payload: %v", err)
			return
		}
		mu.Lock()
		upstreamPayloads["primary"] = payload
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"retry natively"}}`)
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode native payload: %v", err)
			return
		}
		mu.Lock()
		upstreamPayloads["secondary"] = payload
		mu.Unlock()
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"msg_native_failover",
			"type":"message",
			"role":"assistant",
			"model":"native-upstream-model",
			"content":[{"type":"text","text":"ok"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":20,"output_tokens":1}
		}`)
	}))
	defer secondary.Close()

	handler, store, secret := newAnthropicAttributionFailoverGateway(t, primary.URL, secondary.URL)
	native, ok := store.GetProvider("prv_attribution_b")
	if !ok {
		t.Fatal("native failover provider not found")
	}
	native.Type = ProviderAnthropic
	if _, err := store.UpdateProvider(native.ID, native); err != nil {
		t.Fatal(err)
	}
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-attribution-test",
		"max_tokens": 64,
		"system": []any{
			map[string]any{"type": "text", "text": attribution},
			map[string]any{"type": "text", "text": "Stable system instructions."},
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "Read the file."},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "text", "text": "I will inspect it."},
				map[string]any{"type": "tool_use", "id": "toolu_read", "name": "Read", "input": map[string]any{"path": "README.md"}},
			}},
			map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_read", "content": "TokenHub"},
			}},
		},
		"tools": []any{map[string]any{
			"name":         "Read",
			"input_schema": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK {
		t.Fatalf("expected native failover success, got %d: %s", resp.Code, resp.Body)
	}

	mu.Lock()
	primaryPayload := upstreamPayloads["primary"]
	secondaryPayload := upstreamPayloads["secondary"]
	mu.Unlock()
	if got := upstreamSystemMessage(primaryPayload); got != "Stable system instructions." {
		t.Fatalf("strip resource sent unexpected system message %q", got)
	}
	nativeSystem, _ := secondaryPayload["system"].([]any)
	if len(nativeSystem) != 2 || nativeSystem[0].(map[string]any)["text"] != attribution {
		t.Fatalf("native preserve resource did not receive the original system blocks: %#v", secondaryPayload["system"])
	}
	nativeMessages, _ := secondaryPayload["messages"].([]any)
	if len(nativeMessages) != 3 || len(secondaryPayload["tools"].([]any)) != 1 {
		t.Fatalf("native failover did not preserve messages and tools: %#v", secondaryPayload)
	}
	assistantContent := nativeMessages[1].(map[string]any)["content"].([]any)
	toolResultContent := nativeMessages[2].(map[string]any)["content"].([]any)
	if assistantContent[1].(map[string]any)["type"] != "tool_use" || toolResultContent[0].(map[string]any)["type"] != "tool_result" {
		t.Fatalf("native failover changed tool blocks: %#v", nativeMessages)
	}

	logs := store.ListRequestLogs()
	detail, err := store.GetRequestDetail(logs[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	payload := detail["payload"].(RequestPayloadLog)
	if !strings.Contains(payload.RequestBody, attribution) || !strings.Contains(payload.RequestBody, `"tool_use"`) {
		t.Fatalf("audit payload did not retain the original attribution and tool blocks: %s", payload.RequestBody)
	}
}

func TestAnthropicStreamingAttributionPolicyAppliesPerFailoverResource(t *testing.T) {
	const attribution = "x-anthropic-billing-header: cc_version=2.1.220.25c; cc_entrypoint=sdk-cli; cch=12345;"
	var mu sync.Mutex
	upstreamPayloads := map[string]map[string]any{}
	upstream := func(name string, fail bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode %s payload: %v", name, err)
				return
			}
			mu.Lock()
			upstreamPayloads[name] = payload
			mu.Unlock()
			if fail {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, `{"error":{"message":"retry elsewhere"}}`)
				return
			}
			writeChatStreamChunk(w, "ok")
		}))
	}
	primary := upstream("primary", true)
	defer primary.Close()
	secondary := upstream("secondary", false)
	defer secondary.Close()

	handler, store, secret := newAnthropicAttributionFailoverGateway(t, primary.URL, secondary.URL)
	resp := doAnthropicRequest(t, handler, "/v1/messages", map[string]any{
		"model":      "claude-attribution-test",
		"max_tokens": 64,
		"stream":     true,
		"system": []any{
			map[string]any{"type": "text", "text": attribution},
			map[string]any{"type": "text", "text": "Stable system instructions."},
		},
		"messages": []any{map[string]any{"role": "user", "content": "Hello"}},
	}, "Bearer "+secret, "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "ok") {
		t.Fatalf("expected streaming failover success, got %d: %s", resp.Code, resp.Body)
	}

	mu.Lock()
	primaryPayload := upstreamPayloads["primary"]
	secondaryPayload := upstreamPayloads["secondary"]
	mu.Unlock()
	if got := upstreamSystemMessage(primaryPayload); got != "Stable system instructions." {
		t.Fatalf("streaming strip resource sent unexpected system message %q", got)
	}
	if got := upstreamSystemMessage(secondaryPayload); got != attribution+"\nStable system instructions." {
		t.Fatalf("streaming preserve resource sent unexpected system message %q", got)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 {
		t.Fatalf("expected one streaming request log, got %d", len(logs))
	}
	detail, err := store.GetRequestDetail(logs[0].RequestID)
	if err != nil {
		t.Fatal(err)
	}
	payload := detail["payload"].(RequestPayloadLog)
	if !strings.Contains(payload.RequestBody, attribution) {
		t.Fatalf("streaming audit payload did not retain the original attribution block: %s", payload.RequestBody)
	}
}

func TestAdminProviderClaudeCodeAttributionPolicyPreservesOtherOptions(t *testing.T) {
	app := newTestServer()
	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":                             "prv_attribution_policy",
		"name":                           "Attribution Policy",
		"type":                           ProviderOpenAICompatible,
		"base_url":                       "https://example.invalid/v1",
		"status":                         StatusActive,
		"healthy":                        true,
		"options":                        map[string]string{"region": "test"},
		"claude_code_attribution_policy": claudeCodeAttributionStrip,
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("expected provider creation, got %d: %s", created.Code, created.Body)
	}
	var createResult ProviderCreateResult
	if err := json.Unmarshal([]byte(created.Body), &createResult); err != nil {
		t.Fatal(err)
	}
	if createResult.Provider.Options["region"] != "test" ||
		createResult.Provider.Options[claudeCodeAttributionPolicyOption] != claudeCodeAttributionStrip {
		t.Fatalf("provider options were not merged: %+v", createResult.Provider.Options)
	}

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/providers/prv_attribution_policy", map[string]any{
		"claude_code_attribution_policy": claudeCodeAttributionPreserve,
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("expected provider update, got %d: %s", updated.Code, updated.Body)
	}
	var updateResult ProviderCreateResult
	if err := json.Unmarshal([]byte(updated.Body), &updateResult); err != nil {
		t.Fatal(err)
	}
	if updateResult.Provider.Options["region"] != "test" ||
		updateResult.Provider.Options[claudeCodeAttributionPolicyOption] != claudeCodeAttributionPreserve {
		t.Fatalf("provider policy update erased options: %+v", updateResult.Provider.Options)
	}
}

func TestAdminRejectsInvalidClaudeCodeAttributionPolicies(t *testing.T) {
	app := newTestServer()
	providerField := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":                           "Invalid Attribution Policy",
		"type":                           ProviderOpenAICompatible,
		"base_url":                       "https://example.invalid/v1",
		"status":                         StatusActive,
		"healthy":                        true,
		"claude_code_attribution_policy": "normalize",
	}, "")
	if providerField.Code != http.StatusBadRequest || !strings.Contains(providerField.Body, `"code":"invalid_claude_code_attribution_policy"`) {
		t.Fatalf("expected invalid provider field policy error, got %d: %s", providerField.Code, providerField.Body)
	}

	providerOptions := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":     "Invalid Attribution Options",
		"type":     ProviderOpenAICompatible,
		"base_url": "https://example.invalid/v1",
		"status":   StatusActive,
		"healthy":  true,
		"options": map[string]string{
			claudeCodeAttributionPolicyOption: "normalize",
		},
	}, "")
	if providerOptions.Code != http.StatusBadRequest || !strings.Contains(providerOptions.Body, `"code":"invalid_claude_code_attribution_policy"`) {
		t.Fatalf("expected invalid provider options policy error, got %d: %s", providerOptions.Code, providerOptions.Body)
	}

	resource := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"provider_id":   "prv_mock",
		"name":          "Invalid Attribution Resource Policy",
		"resource_type": ProviderResourceAPIKey,
		"status":        StatusActive,
		"healthy":       true,
		"options": map[string]string{
			claudeCodeAttributionPolicyOption: "normalize",
		},
	}, "")
	if resource.Code != http.StatusBadRequest || !strings.Contains(resource.Body, `"code":"invalid_claude_code_attribution_policy"`) {
		t.Fatalf("expected invalid resource policy error, got %d: %s", resource.Code, resource.Body)
	}
}

func newAnthropicAttributionFailoverGateway(t *testing.T, primaryURL string, secondaryURL string) (http.Handler, *GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Claude attribution", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name:    "claude-attribution-key",
		Allowed: []string{"claude-attribution-test"},
		Status:  StatusActive,
	}, "thk_claude_attribution")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "claude-attribution-test", Family: "claude", Modality: "chat", Status: StatusActive})

	for index, baseURL := range []string{primaryURL, secondaryURL} {
		provider := store.AddProvider(Provider{
			ID:      "prv_attribution_" + string(rune('a'+index)),
			Name:    "Attribution Provider",
			Type:    ProviderOpenAICompatible,
			BaseURL: baseURL,
			APIKey:  "upstream-secret",
			Status:  StatusActive,
			Healthy: true,
			Options: map[string]string{claudeCodeAttributionPolicyOption: claudeCodeAttributionPreserve},
		})
		resourceOptions := map[string]string{}
		if index == 0 {
			resourceOptions[claudeCodeAttributionPolicyOption] = claudeCodeAttributionStrip
		}
		resource, addErr := store.AddProviderResource(ProviderResource{
			ProviderID:   provider.ID,
			Name:         "Attribution Resource " + string(rune('A'+index)),
			ResourceType: ProviderResourceAPIKey,
			Status:       StatusActive,
			Healthy:      true,
			Weight:       100,
			Options:      resourceOptions,
		})
		if addErr != nil {
			t.Fatal(addErr)
		}
		store.AddRoute(ModelRoute{
			ID:                 "route_attribution_" + string(rune('a'+index)),
			ModelName:          "claude-attribution-test",
			ProviderID:         provider.ID,
			ProviderResourceID: resource.ID,
			ProviderModel:      "upstream-model",
			Priority:           index + 1,
			Weight:             100,
			Status:             StatusActive,
			Strategy:           RouteStrategyPriorityOnly,
		})
	}
	return New(store).Handler(), store, secret
}

func upstreamSystemMessage(payload map[string]any) string {
	messages, _ := payload["messages"].([]any)
	if len(messages) == 0 {
		return ""
	}
	message, _ := messages[0].(map[string]any)
	text, _ := message["content"].(string)
	return text
}
