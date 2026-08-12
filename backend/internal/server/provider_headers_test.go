package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeProviderHeadersAndMergeResourceOverrides(t *testing.T) {
	providerHeaders, err := normalizeProviderHeaders(map[string]string{
		"user-agent":    "TokenHub-Custom-Client/1.0",
		"X-Client-Name": "provider-client",
	})
	if err != nil {
		t.Fatalf("normalize provider headers: %v", err)
	}
	resourceHeaders, err := normalizeProviderHeaders(map[string]string{
		"x-client-name": "resource-client",
		"X-Region":      "cn-east-1",
	})
	if err != nil {
		t.Fatalf("normalize resource headers: %v", err)
	}

	merged := mergeProviderHeaders(providerHeaders, resourceHeaders)
	if got := merged["User-Agent"]; got != "TokenHub-Custom-Client/1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
	if got := merged["X-Client-Name"]; got != "resource-client" {
		t.Fatalf("resource override = %q", got)
	}
	if got := merged["X-Region"]; got != "cn-east-1" {
		t.Fatalf("resource header = %q", got)
	}
	if len(merged) != 3 {
		t.Fatalf("merged headers = %#v", merged)
	}
}

func TestApplyProviderHeadersCannotOverrideSystemHeaders(t *testing.T) {
	headers := http.Header{
		"Authorization": []string{"Bearer system-key"},
		"Content-Type":  []string{"application/json"},
	}
	applyProviderHeaders(headers, map[string]string{
		"Authorization": "Bearer attacker",
		"Content-Type":  "text/plain",
		"User-Agent":    "TokenHub-Custom-Client/1.0",
	})
	if got := headers.Get("Authorization"); got != "Bearer system-key" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := headers.Get("User-Agent"); got != "TokenHub-Custom-Client/1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestAdminConnectionTestUsesValidatedCustomHeaders(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "TokenHub-Custom-Client/1.0" {
			t.Errorf("User-Agent = %q", got)
			http.Error(w, "missing User-Agent", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("X-Client-Name"); got != "internal-ai-platform" {
			t.Errorf("X-Client-Name = %q", got)
			http.Error(w, "missing X-Client-Name", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "model-one"}}})
	}))
	defer upstream.Close()

	app := newTestServer()
	response := doJSON(t, app, http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name":     "Custom Headers",
		"type":     ProviderOpenAICompatible,
		"base_url": upstream.URL,
		"api_key":  "test-key",
		"headers": map[string]string{
			"User-Agent":    "TokenHub-Custom-Client/1.0",
			"X-Client-Name": "internal-ai-platform",
		},
	}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("connection test = %d: %s", response.Code, response.Body)
	}

	reserved := doJSON(t, app, http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name":     "Unsafe Headers",
		"type":     ProviderOpenAICompatible,
		"base_url": upstream.URL,
		"api_key":  "test-key",
		"headers":  map[string]string{"Authorization": "Bearer attacker"},
	}, "")
	if reserved.Code != http.StatusBadRequest || !strings.Contains(reserved.Body, `"code":"provider_header_reserved"`) {
		t.Fatalf("reserved header = %d: %s", reserved.Code, reserved.Body)
	}
	unsupported := doJSON(t, app, http.MethodPost, "/api/admin/providers/test-connection", map[string]any{
		"name": "Azure", "type": ProviderAzureOpenAI, "base_url": upstream.URL, "api_key": "test-key",
		"headers": map[string]string{"User-Agent": "custom"},
	}, "")
	if unsupported.Code != http.StatusBadRequest || !strings.Contains(unsupported.Body, `"code":"provider_headers_unsupported"`) {
		t.Fatalf("unsupported adapter headers = %d: %s", unsupported.Code, unsupported.Body)
	}
}

func TestCustomCatalogReloadRetainsStoredSensitiveHeaderValue(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Tenant-Secret"); got != "tenant-secret" {
			t.Errorf("X-Tenant-Secret = %q", got)
			http.Error(w, "missing tenant secret", http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "model-one"}}})
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":                "prv_catalog_sensitive_header",
		"name":              "Catalog sensitive header",
		"type":              ProviderOpenAICompatible,
		"base_url":          upstream.URL,
		"api_key":           "test-key",
		"status":            StatusActive,
		"healthy":           true,
		"headers":           map[string]string{"X-Tenant-Secret": "tenant-secret"},
		"sensitive_headers": []string{"X-Tenant-Secret"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider = %d: %s", created.Code, created.Body)
	}

	reloaded := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{
		"provider_id":       "prv_catalog_sensitive_header",
		"headers":           map[string]string{"x-tenant-secret": providerHeaderMask},
		"sensitive_headers": []string{"x-tenant-secret"},
	}, "")
	if reloaded.Code != http.StatusOK {
		t.Fatalf("reload custom catalog = %d: %s", reloaded.Code, reloaded.Body)
	}
}

func TestGeminiProviderTestUsesCustomHeadersAndRestoresHealth(t *testing.T) {
	failingUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "first resource unavailable", http.StatusServiceUnavailable)
	}))
	defer failingUpstream.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.URL.Query().Get("key") != "gemini-key" {
			t.Errorf("Gemini models request = %s", r.URL.String())
			http.Error(w, "invalid Gemini authentication", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("User-Agent"); got != "TokenHub-Gemini/1.0" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("X-Tenant"); got != "resource-tenant" {
			t.Errorf("X-Tenant = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected Authorization = %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{{"name": "models/gemini-test", "displayName": "Gemini Test"}}})
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_gemini_headers", Name: "Gemini headers", Type: ProviderGemini, BaseURL: upstream.URL,
		Status: StatusActive, Healthy: true, Headers: map[string]string{"User-Agent": "TokenHub-Gemini/1.0", "X-Tenant": "provider-tenant"},
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_gemini_headers_failing", ProviderID: provider.ID, Name: "Gemini headers failing resource",
		ResourceType: ProviderResourceAPIKey, BaseURL: failingUpstream.URL, APIKey: "gemini-key", Status: StatusActive, Healthy: true,
		Priority: 1, Headers: map[string]string{"x-tenant": "failing-tenant"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_gemini_headers", ProviderID: provider.ID, Name: "Gemini headers resource",
		ResourceType: ProviderResourceAPIKey, APIKey: "gemini-key", Status: StatusActive, Healthy: true,
		Priority: 2, Headers: map[string]string{"x-tenant": "resource-tenant"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProviderHealth(provider.ID, false); err != nil {
		t.Fatal(err)
	}

	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/providers/"+provider.ID+"/test", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("Gemini provider test = %d: %s", response.Code, response.Body)
	}
	updated, ok := integrationProvider(store, provider.ID)
	if !ok || !updated.Healthy {
		t.Fatalf("successful resource probe did not restore Provider health: %+v", updated)
	}
}

func TestGeminiProviderTestTriesLaterActiveResource(t *testing.T) {
	var requests []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.URL.Query().Get("key")
		requests = append(requests, apiKey)
		if apiKey == "unavailable-key" {
			http.Error(w, "resource unavailable", http.StatusUnauthorized)
			return
		}
		if apiKey != "working-key" {
			t.Errorf("unexpected Gemini API key %q", apiKey)
			http.Error(w, "invalid Gemini authentication", http.StatusUnauthorized)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{{"name": "models/gemini-test"}}})
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_gemini_fallback", Name: "Gemini fallback", Type: ProviderGemini, BaseURL: upstream.URL,
		Status: StatusActive, Healthy: false, Headers: map[string]string{"User-Agent": "TokenHub-Gemini/1.0"},
	})
	for _, resource := range []ProviderResource{
		{ID: "rsrc_gemini_unavailable", Name: "Unavailable", APIKey: "unavailable-key"},
		{ID: "rsrc_gemini_working", Name: "Working", APIKey: "working-key"},
	} {
		resource.ProviderID = provider.ID
		resource.ResourceType = ProviderResourceAPIKey
		resource.Status = StatusActive
		resource.Healthy = true
		if _, err := store.AddProviderResource(resource); err != nil {
			t.Fatal(err)
		}
	}

	response := doJSON(t, New(store).Handler(), http.MethodPost, "/api/admin/providers/"+provider.ID+"/test", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("Gemini provider test = %d: %s", response.Code, response.Body)
	}
	if len(requests) != 2 || requests[0] != "unavailable-key" || requests[1] != "working-key" {
		t.Fatalf("Gemini probe API keys = %v", requests)
	}
	updated, ok := integrationProvider(store, provider.ID)
	if !ok || !updated.Healthy {
		t.Fatalf("successful fallback probe did not restore Provider health: %+v", updated)
	}
}

func TestOpenAICompatibleRequestsApplyCustomHeadersAcrossEndpoints(t *testing.T) {
	paths := make(chan string, 7)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "TokenHub-Custom-Client/1.0" {
			t.Errorf("%s User-Agent = %q", r.URL.Path, got)
		}
		if got := r.Header.Get("X-Client-Name"); got != "internal-ai-platform" {
			t.Errorf("%s X-Client-Name = %q", r.URL.Path, got)
		}
		paths <- r.URL.Path
		writeJSON(w, http.StatusOK, map[string]any{"data": []any{}, "usage": map[string]any{}})
	}))
	defer upstream.Close()

	provider := Provider{
		BaseURL: upstream.URL + "/v1",
		APIKey:  "system-key",
		Headers: map[string]string{
			"User-Agent":    "TokenHub-Custom-Client/1.0",
			"X-Client-Name": "internal-ai-platform",
		},
	}
	adapter := OpenAICompatibleAdapter{Client: upstream.Client(), StreamClient: upstream.Client()}
	for _, request := range []struct {
		endpoint string
		stream   bool
	}{
		{endpoint: "/chat/completions"},
		{endpoint: "/chat/completions", stream: true},
		{endpoint: "/responses"},
		{endpoint: "/responses", stream: true},
		{endpoint: "/embeddings"},
		{endpoint: "/images/generations"},
	} {
		response, err := adapter.doRaw(t.Context(), provider, http.MethodPost, request.endpoint, map[string]any{"model": "test"}, request.stream)
		if err != nil {
			t.Fatalf("%s stream=%v: %v", request.endpoint, request.stream, err)
		}
		_ = response.Body.Close()
	}
	response, err := adapter.doMultipartRaw(t.Context(), provider, "/images/edits", "multipart/form-data; boundary=test", bytes.NewBufferString("--test--\r\n"))
	if err != nil {
		t.Fatalf("image edit: %v", err)
	}
	_ = response.Body.Close()
	close(paths)
	if len(paths) != 7 {
		t.Fatalf("request count = %d", len(paths))
	}
}

func TestAnthropicAdapterAppliesCustomHeadersForBufferedAndStreamingRequests(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("User-Agent"); got != "TokenHub-Anthropic/1.0" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("X-Client-Name"); got != "internal-ai-platform" {
			t.Errorf("X-Client-Name = %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{"content": []any{}})
	}))
	defer upstream.Close()

	adapter := AnthropicAdapter{Client: upstream.Client(), StreamClient: upstream.Client()}
	provider := Provider{BaseURL: upstream.URL, APIKey: "test-key", Headers: map[string]string{
		"User-Agent": "TokenHub-Anthropic/1.0", "X-Client-Name": "internal-ai-platform",
	}}
	for _, stream := range []bool{false, true} {
		response, err := adapter.doRaw(t.Context(), provider, "/v1/messages", map[string]any{"model": "test"}, stream)
		if err != nil {
			t.Fatalf("stream=%v: %v", stream, err)
		}
		_ = response.Body.Close()
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestProviderErrorsRedactEffectiveSensitiveHeaderValues(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "tenant="+r.Header.Get("X-Tenant")+" key="+r.Header.Get("Authorization"), http.StatusBadRequest)
	}))
	defer upstream.Close()

	provider := effectiveProviderResourceConfig(Provider{
		BaseURL: upstream.URL, APIKey: "provider-api-secret",
		Headers: map[string]string{"X-Tenant": "provider-tenant-secret"}, SensitiveHeaders: []string{"X-Tenant"},
	}, &ProviderResource{Headers: map[string]string{"x-tenant": "resource-tenant-secret"}, SensitiveHeaders: []string{"x-tenant"}})
	_, err := (OpenAICompatibleAdapter{Client: upstream.Client()}).doRaw(t.Context(), provider, http.MethodPost, "/chat/completions", map[string]any{}, false)
	if err == nil {
		t.Fatal("expected upstream error")
	}
	message := err.Error()
	if strings.Contains(message, "provider-tenant-secret") || strings.Contains(message, "resource-tenant-secret") || strings.Contains(message, "provider-api-secret") {
		t.Fatalf("provider error leaked a secret: %s", message)
	}
	if !strings.Contains(message, providerHeaderMask) {
		t.Fatalf("provider error did not show redaction: %s", message)
	}
}

func TestProviderErrorsRedactJSONEscapedSensitiveHeaderValues(t *testing.T) {
	headerSecret := `secret"token<&>/tenant`
	apiSecret := `api\key`
	payload, err := json.Marshal(map[string]any{"error": map[string]string{"message": headerSecret + " " + apiSecret}})
	if err != nil {
		t.Fatal(err)
	}
	provider := Provider{
		APIKey: apiSecret, Headers: map[string]string{"X-Tenant": headerSecret}, SensitiveHeaders: []string{"X-Tenant"},
	}
	redacted := string(redactProviderErrorSecrets(payload, provider))
	for _, secret := range []string{headerSecret, apiSecret} {
		encoded, marshalErr := json.Marshal(secret)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		escaped := string(encoded[1 : len(encoded)-1])
		if strings.Contains(redacted, secret) || strings.Contains(redacted, escaped) {
			t.Fatalf("provider error leaked JSON-escaped secret %q: %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, providerHeaderMask) {
		t.Fatalf("provider error did not show redaction: %s", redacted)
	}

	arbitraryEscape := []byte(`{"error":{"message":"\u0073ecret"}}`)
	arbitraryRedacted := redactProviderErrorSecrets(arbitraryEscape, Provider{
		Headers: map[string]string{"X-Tenant": "secret"}, SensitiveHeaders: []string{"X-Tenant"},
	})
	if strings.Contains(string(arbitraryRedacted), `\u0073ecret`) {
		t.Fatalf("provider error leaked an arbitrary JSON escape: %s", arbitraryRedacted)
	}
	var decoded map[string]map[string]string
	if err := json.Unmarshal(arbitraryRedacted, &decoded); err != nil {
		t.Fatalf("redacted provider error is not valid JSON: %v", err)
	}
	if message := decoded["error"]["message"]; message != providerHeaderMask {
		t.Fatalf("decoded provider error message = %q, want mask", message)
	}
}

func TestProviderErrorsRedactEscapedSecretsFromTruncatedJSON(t *testing.T) {
	prefix := `{"error":{"message":"\u0073ecret"},"padding":"`
	body := prefix + strings.Repeat("x", providerErrorBodyPrefix)
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	provider := Provider{
		Headers:          map[string]string{"X-Tenant": "secret"},
		SensitiveHeaders: []string{"X-Tenant"},
	}

	err := checkProviderResponseForProvider(response, provider)
	if err == nil {
		t.Fatal("expected the truncated upstream error to be reported")
	}
	message := AsHTTPError(err).Message
	if strings.Contains(message, `\u0073ecret`) || strings.Contains(message, "secret") {
		t.Fatalf("truncated provider error leaked a decoder-equivalent secret: %s", message)
	}
	if !strings.Contains(message, providerHeaderMask) {
		t.Fatalf("truncated provider error did not show redaction: %s", message)
	}
}

func TestPendingStreamErrorRedactsEscapedSecretAfterMalformedString(t *testing.T) {
	provider := Provider{
		Headers:          map[string]string{"X-Tenant": "secret"},
		SensitiveHeaders: []string{"X-Tenant"},
	}
	streams := map[string]string{
		"invalid escape before error":    `data: {"bad":"invalid\q","error":"\u0073ecret"}` + "\n",
		"invalid hex before error":       `data: {"bad":"invalid\uZZZZ","error":"\u0073ecret"}` + "\n",
		"invalid surrogate before error": `data: {"bad":"invalid\uD800\u0061","error":"\u0073ecret"}` + "\n",
		"metadata quote before error":    "id: \"\n" + `data: {"error":"\u0073ecret"}` + "\n",
		"invalid escape in error string": `data: {"error":"invalid\q then \u0073ecret"}` + "\n",
	}
	for name, stream := range streams {
		t.Run(name, func(t *testing.T) {
			failure := NewHTTPError(http.StatusGatewayTimeout, "provider_stream_idle", "provider stalled")
			var output strings.Builder

			_, err := copyOpenAIStreamAndUsageForProvider(&output, &failingReader{
				data: []byte(stream),
				err:  failure,
			}, provider)
			if err != failure {
				t.Fatalf("err = %v, want the upstream failure", err)
			}
			redacted := output.String()
			if strings.Contains(redacted, `\u0073ecret`) || strings.Contains(redacted, "secret") {
				t.Fatalf("pending stream error leaked a decoder-equivalent secret: %s", redacted)
			}
			if !strings.Contains(redacted, providerHeaderMask) {
				t.Fatalf("pending stream error did not show redaction: %s", redacted)
			}
			if strings.Contains(stream, "id: \"") && !strings.HasPrefix(redacted, "id: \"\n") {
				t.Fatalf("SSE metadata changed during redaction: %q", redacted)
			}
		})
	}
}

func BenchmarkRedactMalformedProviderErrorNearSSELimit(b *testing.B) {
	prefix := `{"error":"`
	payload := []byte(prefix + strings.Repeat("x", maxSSEEventBytes-len(prefix)))
	provider := Provider{
		Headers:          map[string]string{"X-Tenant": "secret"},
		SensitiveHeaders: []string{"X-Tenant"},
	}
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		redacted := redactProviderErrorSecrets(payload, provider)
		if string(redacted) != providerHeaderMask {
			b.Fatalf("malformed provider error was not masked: %q", redacted)
		}
	}
}

func TestStreamingProviderErrorsRedactEffectiveSensitiveHeaderValues(t *testing.T) {
	provider := Provider{
		APIKey: "provider-api-secret", Headers: map[string]string{"X-Tenant": "tenant-secret"}, SensitiveHeaders: []string{"X-Tenant"},
	}
	openAIStream := "data: {\"error\":{\"message\":\"\\u0074enant-secret provider-api-secret\"}}\n\n"
	var openAIOutput bytes.Buffer
	if _, err := copyOpenAIStreamAndUsageForProvider(&openAIOutput, strings.NewReader(openAIStream), provider); err != nil {
		t.Fatal(err)
	}
	if output := openAIOutput.String(); strings.Contains(output, "tenant-secret") || strings.Contains(output, `\u0074enant-secret`) || strings.Contains(output, "provider-api-secret") {
		t.Fatalf("OpenAI stream leaked a secret: %s", output)
	}
	responsesStream := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"error\":{\"message\":\"tenant-secret\"}}}\n\n"
	var responsesOutput bytes.Buffer
	if _, err := copyOpenAIStreamAndUsageForProvider(&responsesOutput, strings.NewReader(responsesStream), provider); err != nil {
		t.Fatal(err)
	}
	if output := responsesOutput.String(); strings.Contains(output, "tenant-secret") {
		t.Fatalf("OpenAI Responses stream leaked a secret: %s", output)
	}

	anthropicStream := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"tenant-secret\"}}\n\n"
	var anthropicOutput bytes.Buffer
	anthropicEncoder := newOpenAIChatStreamEncoder(&anthropicOutput, "model", false)
	if _, err := streamAnthropicChatForProvider(strings.NewReader(anthropicStream), anthropicEncoder, provider); err == nil || strings.Contains(err.Error(), "tenant-secret") {
		t.Fatalf("Anthropic stream error was not redacted: %v", err)
	}

	geminiStream := "data: {\"error\":{\"message\":\"tenant-secret\"}}\n\n"
	var geminiOutput bytes.Buffer
	geminiEncoder := newOpenAIChatStreamEncoder(&geminiOutput, "model", false)
	if _, err := streamGeminiChatForProvider(strings.NewReader(geminiStream), geminiEncoder, provider); err == nil || strings.Contains(err.Error(), "tenant-secret") {
		t.Fatalf("Gemini stream error was not redacted: %v", err)
	}

	var nativeOutput bytes.Buffer
	if _, err := copyNativeAnthropicStreamForProvider(&nativeOutput, strings.NewReader(anthropicStream), "model", provider); err != nil {
		t.Fatal(err)
	}
	if output := nativeOutput.String(); strings.Contains(output, "tenant-secret") {
		t.Fatalf("native Anthropic stream leaked a secret: %s", output)
	}
}

func TestStreamingProviderRedactionPreservesSuccessfulContent(t *testing.T) {
	provider := Provider{Headers: map[string]string{"X-Tenant": "tenant-secret"}, SensitiveHeaders: []string{"X-Tenant"}}
	openAIStream := "data: {\"choices\":[{\"delta\":{\"content\":\"tenant-secret\"}}]}\n\n"
	var openAIOutput bytes.Buffer
	if _, err := copyOpenAIStreamAndUsageForProvider(&openAIOutput, strings.NewReader(openAIStream), provider); err != nil {
		t.Fatal(err)
	}
	if output := openAIOutput.String(); !strings.Contains(output, "tenant-secret") {
		t.Fatalf("OpenAI success content was modified: %s", output)
	}
	responsesStream := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"error\":null,\"output_text\":\"tenant-secret\"}}\n\n"
	var responsesOutput bytes.Buffer
	if _, err := copyOpenAIStreamAndUsageForProvider(&responsesOutput, strings.NewReader(responsesStream), provider); err != nil {
		t.Fatal(err)
	}
	if output := responsesOutput.String(); !strings.Contains(output, "tenant-secret") {
		t.Fatalf("OpenAI Responses success content was modified: %s", output)
	}

	nativeStream := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"tenant-secret\"}}\n\n"
	var nativeOutput bytes.Buffer
	if _, err := copyNativeAnthropicStreamForProvider(&nativeOutput, strings.NewReader(nativeStream), "model", provider); err != nil {
		t.Fatal(err)
	}
	if output := nativeOutput.String(); !strings.Contains(output, "tenant-secret") {
		t.Fatalf("native Anthropic success content was modified: %s", output)
	}
}

func TestEffectiveProviderHeadersEnforceCombinedLimits(t *testing.T) {
	providerHeaders := make(map[string]string)
	resourceHeaders := make(map[string]string)
	for index := 0; index < 20; index++ {
		providerHeaders[fmt.Sprintf("X-Provider-%02d", index)] = "provider"
		resourceHeaders[fmt.Sprintf("X-Resource-%02d", index)] = "resource"
	}
	if err := validateEffectiveProviderHeaders(ProviderOpenAICompatible, providerHeaders, resourceHeaders); err == nil || AsHTTPError(err).Code != "provider_headers_too_many" {
		t.Fatalf("combined validation error = %v", err)
	}
	effective := effectiveProviderResourceConfig(Provider{Type: ProviderOpenAICompatible, Headers: providerHeaders}, &ProviderResource{Headers: resourceHeaders})
	if len(effective.Headers) != 0 {
		t.Fatalf("oversized combined headers were applied: %d", len(effective.Headers))
	}
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_combined_header_limit", Name: "Combined header limit", Type: ProviderOpenAICompatible, Status: StatusActive, Headers: providerHeaders})
	if _, err := store.AddProviderResource(ProviderResource{ProviderID: provider.ID, Name: "Combined header limit resource", Status: StatusActive, Headers: resourceHeaders}); err == nil || AsHTTPError(err).Code != "provider_headers_too_many" {
		t.Fatalf("resource create combined validation error = %v", err)
	}
}

func TestSavedResourceTestAndModelDiscoveryUseEffectiveHeaders(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.Header.Get("X-Provider"); got != "provider-value" {
			t.Errorf("X-Provider = %q", got)
		}
		if got := r.Header.Get("X-Tenant"); got != "resource-value" {
			t.Errorf("X-Tenant = %q", got)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": []map[string]any{{"id": "model-one"}}})
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_effective_header_probe", Name: "Effective header probe", Type: ProviderOpenAICompatible,
		BaseURL: upstream.URL, APIKey: "test-key", Status: StatusActive, Healthy: true,
		Headers: map[string]string{"X-Provider": "provider-value", "X-Tenant": "provider-value"},
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_effective_header_probe", ProviderID: provider.ID, Name: "Effective header probe resource",
		ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true,
		Headers: map[string]string{"x-tenant": "resource-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	discovery := doJSON(t, app, http.MethodPost, "/api/admin/provider-catalog/custom", map[string]any{"provider_id": provider.ID}, "")
	if discovery.Code != http.StatusOK {
		t.Fatalf("model discovery = %d: %s", discovery.Code, discovery.Body)
	}
	probe := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/"+resource.ID+"/test", map[string]any{}, "")
	if probe.Code != http.StatusOK {
		t.Fatalf("resource test = %d: %s", probe.Code, probe.Body)
	}
	if requests != 2 {
		t.Fatalf("requests = %d", requests)
	}
}

func TestAdminProviderSensitiveHeadersAreEncryptedMaskedAndPreserved(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"id":                "prv_sensitive_headers",
		"name":              "Sensitive Headers",
		"type":              ProviderOpenAICompatible,
		"base_url":          "https://example.invalid/v1",
		"status":            StatusActive,
		"healthy":           true,
		"headers":           map[string]string{"X-Client-Name": "public-client", "X-Tenant-Secret": "tenant-secret"},
		"sensitive_headers": []string{"x-tenant-secret"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create provider = %d: %s", created.Code, created.Body)
	}
	if strings.Contains(created.Body, "tenant-secret") || !strings.Contains(created.Body, providerHeaderMask) {
		t.Fatalf("sensitive value was not masked: %s", created.Body)
	}

	var stored Provider
	if err := store.db.First(&stored, "id = ?", "prv_sensitive_headers").Error; err != nil {
		t.Fatal(err)
	}
	if got := stored.Headers["X-Tenant-Secret"]; !strings.HasPrefix(got, "enc:v1:") || strings.Contains(got, "tenant-secret") {
		t.Fatalf("sensitive value was not encrypted: %q", got)
	}
	internal, ok := store.GetProvider("prv_sensitive_headers")
	if !ok || internal.Headers["X-Tenant-Secret"] != "tenant-secret" {
		t.Fatalf("internal provider did not reveal the configured value: %+v", internal)
	}
	health := doJSON(t, app, http.MethodPost, "/api/admin/providers/prv_sensitive_headers/health", map[string]any{"healthy": true}, "")
	if health.Code != http.StatusOK || strings.Contains(health.Body, "tenant-secret") || strings.Contains(health.Body, "enc:v1:") || !strings.Contains(health.Body, providerHeaderMask) {
		t.Fatalf("provider health response exposed a sensitive value: %d: %s", health.Code, health.Body)
	}

	updated := doJSON(t, app, http.MethodPatch, "/api/admin/providers/prv_sensitive_headers", map[string]any{
		"headers":           map[string]string{"X-Client-Name": "updated-client", "X-Tenant-Secret": ""},
		"sensitive_headers": []string{"X-Tenant-Secret"},
	}, "")
	if updated.Code != http.StatusOK {
		t.Fatalf("preserve masked header = %d: %s", updated.Code, updated.Body)
	}
	internal, _ = store.GetProvider("prv_sensitive_headers")
	if internal.Headers["X-Tenant-Secret"] != "tenant-secret" || internal.Headers["X-Client-Name"] != "updated-client" {
		t.Fatalf("sensitive header update = %+v", internal.Headers)
	}

	removed := doJSON(t, app, http.MethodPatch, "/api/admin/providers/prv_sensitive_headers", map[string]any{
		"headers":           map[string]string{"X-Client-Name": "updated-client"},
		"sensitive_headers": []string{},
	}, "")
	if removed.Code != http.StatusOK {
		t.Fatalf("remove sensitive header = %d: %s", removed.Code, removed.Body)
	}
	internal, _ = store.GetProvider("prv_sensitive_headers")
	if _, exists := internal.Headers["X-Tenant-Secret"]; exists {
		t.Fatalf("explicit deletion retained sensitive header: %+v", internal.Headers)
	}

	listed := doJSON(t, app, http.MethodGet, "/api/admin/providers", nil, "")
	if strings.Contains(listed.Body, "tenant-secret") {
		t.Fatalf("provider list leaked sensitive value: %s", listed.Body)
	}
	var payload struct {
		Data []Provider `json:"data"`
	}
	if err := json.Unmarshal([]byte(listed.Body), &payload); err != nil {
		t.Fatal(err)
	}
	for _, event := range store.ListAuditEvents() {
		snapshot := event.BeforeSnapshot + event.AfterSnapshot
		if strings.Contains(snapshot, "tenant-secret") || strings.Contains(snapshot, "public-client") || strings.Contains(snapshot, "updated-client") {
			t.Fatalf("audit snapshot leaked a header value: %s", snapshot)
		}
	}
}

func TestTrustedProviderCreateDoesNotPersistInvalidSensitiveHeaders(t *testing.T) {
	store := NewMemoryStore()
	created := store.AddProvider(Provider{
		ID: "prv_invalid_trusted_headers", Name: "Invalid trusted headers", Type: ProviderOpenAICompatible,
		Headers: map[string]string{"X-Tenant": "tenant-secret"}, SensitiveHeaders: []string{"X-Missing"},
	})
	if len(created.Headers) != 0 || len(created.SensitiveHeaders) != 0 {
		t.Fatalf("invalid trusted headers were returned: %+v", created)
	}
	var stored Provider
	if err := store.db.First(&stored, "id = ?", created.ID).Error; err != nil {
		t.Fatal(err)
	}
	if len(stored.Headers) != 0 || len(stored.SensitiveHeaders) != 0 {
		t.Fatalf("invalid trusted headers were persisted: %+v", stored)
	}
}

func TestLegacyUnsafeProviderHeadersAreReportedAndNotApplied(t *testing.T) {
	store := NewMemoryStore()
	provider := Provider{
		ID:      "prv_legacy_unsafe_headers",
		Name:    "Legacy unsafe headers",
		Type:    ProviderOpenAICompatible,
		Status:  StatusActive,
		Healthy: true,
		Headers: map[string]string{"Authorization": "Bearer legacy-secret"},
	}
	if err := store.db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "legacy-header-model", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:     "legacy-header-model",
		ProviderID:    provider.ID,
		ProviderModel: "legacy-header-model",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})

	listed := store.ListProviders()
	var found Provider
	for _, item := range listed {
		if item.ID == provider.ID {
			found = item
			break
		}
	}
	if len(found.HeaderValidationErrors) != 1 || found.HeaderValidationErrors[0] != "provider_header_reserved" {
		t.Fatalf("legacy validation errors = %+v", found.HeaderValidationErrors)
	}
	candidates, err := store.SelectRouteCandidates("legacy-header-model")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("route candidates = %+v, %v", candidates, err)
	}
	if len(candidates[0].Provider.Headers) != 0 {
		t.Fatalf("unsafe legacy headers were applied: %+v", candidates[0].Provider.Headers)
	}
}

func TestLegacyUnsupportedProviderHeadersAreReportedAndNotApplied(t *testing.T) {
	store := NewMemoryStore()
	provider := Provider{
		ID:      "prv_legacy_unsupported_headers",
		Name:    "Legacy unsupported headers",
		Type:    ProviderAzureOpenAI,
		Status:  StatusActive,
		Healthy: true,
		Headers: map[string]string{"User-Agent": "legacy-client"},
	}
	if err := store.db.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "legacy-unsupported-header-model", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{
		ModelName:     "legacy-unsupported-header-model",
		ProviderID:    provider.ID,
		ProviderModel: "legacy-unsupported-header-model",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})

	listed := store.ListProviders()
	var found Provider
	for _, item := range listed {
		if item.ID == provider.ID {
			found = item
			break
		}
	}
	if len(found.HeaderValidationErrors) != 1 || found.HeaderValidationErrors[0] != "provider_headers_unsupported" {
		t.Fatalf("legacy validation errors = %+v", found.HeaderValidationErrors)
	}
	candidates, err := store.SelectRouteCandidates("legacy-unsupported-header-model")
	if err != nil || len(candidates) != 1 {
		t.Fatalf("route candidates = %+v, %v", candidates, err)
	}
	if len(candidates[0].Provider.Headers) != 0 {
		t.Fatalf("unsupported legacy headers were applied: %+v", candidates[0].Provider.Headers)
	}
}

func TestAdminRejectsMovingHeaderConfigToUnsupportedProviderAdapter(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:      "prv_header_adapter_transition",
		Name:    "Header adapter transition",
		Type:    ProviderOpenAICompatible,
		Status:  StatusActive,
		Healthy: true,
	})
	resource, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_header_adapter_transition",
		ProviderID:   provider.ID,
		Name:         "Header adapter transition resource",
		ResourceType: ProviderResourceAPIKey,
		Status:       StatusActive,
		Healthy:      true,
		Headers:      map[string]string{"X-Tenant": "tenant-one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	azure := store.AddProvider(Provider{
		ID:      "prv_header_adapter_azure",
		Name:    "Header adapter Azure",
		Type:    ProviderAzureOpenAI,
		Status:  StatusActive,
		Healthy: true,
	})
	app := New(store).Handler()

	providerPatch := doJSON(t, app, http.MethodPatch, "/api/admin/providers/"+provider.ID, map[string]any{
		"type": ProviderAzureOpenAI,
	}, "")
	if providerPatch.Code != http.StatusBadRequest || !strings.Contains(providerPatch.Body, `"code":"provider_headers_unsupported"`) {
		t.Fatalf("provider adapter transition = %d: %s", providerPatch.Code, providerPatch.Body)
	}
	resourcePatch := doJSON(t, app, http.MethodPatch, "/api/admin/provider-resources/"+resource.ID, map[string]any{
		"provider_id": azure.ID,
	}, "")
	if resourcePatch.Code != http.StatusBadRequest || !strings.Contains(resourcePatch.Body, `"code":"provider_headers_unsupported"`) {
		t.Fatalf("resource adapter transition = %d: %s", resourcePatch.Code, resourcePatch.Body)
	}
}

func TestRouteCandidatesRecomputeCaseInsensitiveHeadersPerResource(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID:               "prv_header_routes",
		Name:             "Header Routes",
		Type:             ProviderOpenAICompatible,
		BaseURL:          "https://provider.example/v1",
		Status:           StatusActive,
		Healthy:          true,
		Headers:          map[string]string{"X-Client-Name": "shared", "X-Tenant": "provider-secret"},
		SensitiveHeaders: []string{"X-Tenant"},
	})
	store.AddModel(Model{Name: "header-route-model", Modality: "chat", Status: StatusActive})

	for index, value := range []string{"resource-one-secret", "resource-two-secret"} {
		resource, err := store.AddProviderResource(ProviderResource{
			ID:               "rsrc_header_" + string(rune('a'+index)),
			ProviderID:       provider.ID,
			Name:             "Header resource " + string(rune('1'+index)),
			ResourceType:     ProviderResourceAPIKey,
			Status:           StatusActive,
			Healthy:          true,
			Priority:         index + 1,
			Weight:           100,
			Headers:          map[string]string{"x-tenant": value, "X-Resource": string(rune('1' + index))},
			SensitiveHeaders: []string{"x-tenant"},
		})
		if err != nil {
			t.Fatal(err)
		}
		store.AddRoute(ModelRoute{
			ID:                 "route_header_" + string(rune('a'+index)),
			ModelName:          "header-route-model",
			ProviderID:         provider.ID,
			ProviderResourceID: resource.ID,
			ProviderModel:      "upstream-header-model",
			Priority:           index + 1,
			Weight:             100,
			Status:             StatusActive,
			Strategy:           RouteStrategyPriorityOnly,
		})
	}

	candidates, err := store.SelectRouteCandidates("header-route-model")
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d", len(candidates))
	}
	for index, candidate := range candidates {
		want := []string{"resource-one-secret", "resource-two-secret"}[index]
		if got := candidate.Provider.Headers["X-Tenant"]; got != want {
			t.Fatalf("candidate %d X-Tenant = %q, want %q", index, got, want)
		}
		if got := candidate.Provider.Headers["X-Client-Name"]; got != "shared" {
			t.Fatalf("candidate %d inherited header = %q", index, got)
		}
	}
}

func TestAdminProviderResourceSensitiveHeadersRoundTrip(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	created := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources", map[string]any{
		"id":                "rsrc_sensitive_headers",
		"provider_id":       "prv_mock",
		"name":              "Sensitive header resource",
		"resource_type":     ProviderResourceAPIKey,
		"status":            StatusActive,
		"healthy":           true,
		"headers":           map[string]string{"X-Resource-Secret": "resource-secret"},
		"sensitive_headers": []string{"x-resource-secret"},
	}, "")
	if created.Code != http.StatusCreated || strings.Contains(created.Body, "resource-secret") || !strings.Contains(created.Body, providerHeaderMask) {
		t.Fatalf("create resource = %d: %s", created.Code, created.Body)
	}
	var stored ProviderResource
	if err := store.db.First(&stored, "id = ?", "rsrc_sensitive_headers").Error; err != nil {
		t.Fatal(err)
	}
	if value, _ := providerHeaderValue(stored.Headers, "X-Resource-Secret"); !strings.HasPrefix(value, "enc:v1:") {
		t.Fatalf("resource header was not encrypted: %q", value)
	}
	updated := doJSON(t, app, http.MethodPatch, "/api/admin/provider-resources/rsrc_sensitive_headers", map[string]any{
		"headers":           map[string]string{"x-resource-secret": ""},
		"sensitive_headers": []string{"X-Resource-Secret"},
	}, "")
	if updated.Code != http.StatusOK || strings.Contains(updated.Body, `:"resource-secret"`) {
		t.Fatalf("update resource = %d: %s", updated.Code, updated.Body)
	}
	internal, ok := store.GetProviderResource("rsrc_sensitive_headers")
	if !ok {
		t.Fatal("resource not found")
	}
	if value, _ := providerHeaderValue(internal.Headers, "X-Resource-Secret"); value != "resource-secret" {
		t.Fatalf("resource secret was not preserved: %+v", internal.Headers)
	}
	health := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/rsrc_sensitive_headers/health", map[string]any{"healthy": true}, "")
	if health.Code != http.StatusOK || strings.Contains(health.Body, `:"resource-secret"`) || strings.Contains(health.Body, "enc:v1:") || !strings.Contains(health.Body, providerHeaderMask) {
		t.Fatalf("resource health response exposed a sensitive value: %d: %s", health.Code, health.Body)
	}
	bulk := doJSON(t, app, http.MethodPost, "/api/admin/provider-resources/bulk", map[string]any{
		"action": "test",
		"ids":    []string{"rsrc_sensitive_headers"},
	}, "")
	if bulk.Code != http.StatusOK || strings.Contains(bulk.Body, `:"resource-secret"`) || strings.Contains(bulk.Body, "enc:v1:") || !strings.Contains(bulk.Body, providerHeaderMask) {
		t.Fatalf("resource bulk response exposed a sensitive value: %d: %s", bulk.Code, bulk.Body)
	}
	for _, event := range store.ListAuditEvents() {
		snapshot := event.BeforeSnapshot + event.AfterSnapshot
		if strings.Contains(snapshot, `:"resource-secret"`) || strings.Contains(snapshot, "enc:v1:") {
			t.Fatalf("resource audit snapshot exposed a sensitive value: %s", snapshot)
		}
	}
}

func TestNormalizeProviderHeadersRejectsUnsafeConfiguration(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		code    string
	}{
		{name: "reserved authentication", headers: map[string]string{"Authorization": "Bearer mine"}, code: "provider_header_reserved"},
		{name: "reserved transport", headers: map[string]string{"Transfer-Encoding": "chunked"}, code: "provider_header_reserved"},
		{name: "reserved cookie credential", headers: map[string]string{"Cookie": "session=secret"}, code: "provider_header_reserved"},
		{name: "reserved forwarding identity", headers: map[string]string{"X-Forwarded-For": "127.0.0.1"}, code: "provider_header_reserved"},
		{name: "invalid name", headers: map[string]string{"Bad Header": "value"}, code: "provider_header_name_invalid"},
		{name: "newline", headers: map[string]string{"X-Client": "safe\r\ninjected: true"}, code: "provider_header_value_invalid"},
		{name: "nul", headers: map[string]string{"X-Client": "safe\x00value"}, code: "provider_header_value_invalid"},
		{name: "control", headers: map[string]string{"X-Client": "safe\x01value"}, code: "provider_header_value_invalid"},
		{name: "delete", headers: map[string]string{"X-Client": "safe\x7fvalue"}, code: "provider_header_value_invalid"},
		{name: "empty", headers: map[string]string{"X-Client": ""}, code: "provider_header_value_required"},
		{name: "long value", headers: map[string]string{"X-Client": strings.Repeat("x", providerHeaderValueMaxBytes+1)}, code: "provider_header_value_too_long"},
		{name: "total too large", headers: map[string]string{
			"X-One":   strings.Repeat("1", providerHeaderValueMaxBytes),
			"X-Two":   strings.Repeat("2", providerHeaderValueMaxBytes),
			"X-Three": strings.Repeat("3", providerHeaderValueMaxBytes),
			"X-Four":  strings.Repeat("4", providerHeaderValueMaxBytes),
		}, code: "provider_headers_too_large"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeProviderHeaders(test.headers)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if got := AsHTTPError(err).Code; got != test.code {
				t.Fatalf("error code = %q, want %q", got, test.code)
			}
		})
	}
	if _, err := normalizeProviderHeaders(map[string]string{"X-Client": "tab\tvalue"}); err != nil {
		t.Fatalf("horizontal tab should be transport-safe: %v", err)
	}
}
