package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// capturedRequest is what an upstream saw, recorded before the handler answers.
type capturedRequest struct {
	method      string
	escapedPath string
	rawQuery    string
	header      http.Header
	body        map[string]any
}

// chatUpstream answers one non-streaming chat or embeddings call and records the
// request the adapter built.
func chatUpstream(t *testing.T, captured *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.method = r.Method
		captured.escapedPath = r.URL.EscapedPath()
		captured.rawQuery = r.URL.RawQuery
		captured.header = r.Header.Clone()
		decodeFixtureRequest(t, r.Body, &captured.body)
		w.Header().Set("content-type", "application/json")
		writeFixture(t, w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{}}`)
	}))
}

func azureProvider(baseURL string, apiVersion string) Provider {
	provider := Provider{Type: ProviderAzureOpenAI, BaseURL: baseURL, APIKey: "azure-key"}
	if apiVersion != "" {
		provider.Options = map[string]string{"api_version": apiVersion}
	}
	return provider
}

func simpleChatRequest() ChatCompletionRequest {
	return ChatCompletionRequest{Model: "gateway-model", Messages: []ChatMessage{{Role: "user", Content: "hi"}}}
}

// Azure addresses a deployment by name in the path, so a deployment containing a
// slash, a space or a percent sign must not be able to reshape the URL.
func TestAzureOpenAIAdapterChatEscapesDeploymentAndAPIVersion(t *testing.T) {
	tests := []struct {
		name       string
		deployment string
		apiVersion string
		wantPath   string
		wantQuery  string
	}{
		{
			name:       "default api version",
			deployment: "gpt-4o",
			wantPath:   "/openai/deployments/gpt-4o/chat/completions",
			wantQuery:  "api-version=" + azureOpenAIDefaultAPIVersion,
		},
		{
			name:       "slash in deployment",
			deployment: "team/gpt-4o",
			apiVersion: "2025-01-01-preview",
			wantPath:   "/openai/deployments/team%2Fgpt-4o/chat/completions",
			wantQuery:  "api-version=2025-01-01-preview",
		},
		{
			name:       "space in deployment",
			deployment: "gpt 4o",
			apiVersion: "2025-01-01-preview",
			wantPath:   "/openai/deployments/gpt%204o/chat/completions",
			wantQuery:  "api-version=2025-01-01-preview",
		},
		{
			name:       "percent in deployment",
			deployment: "gpt%4o",
			apiVersion: "2025-01-01-preview",
			wantPath:   "/openai/deployments/gpt%254o/chat/completions",
			wantQuery:  "api-version=2025-01-01-preview",
		},
		{
			name:       "api version cannot add query parameters",
			deployment: "gpt-4o",
			apiVersion: "2025-01-01-preview&api-key=leak",
			wantPath:   "/openai/deployments/gpt-4o/chat/completions",
			wantQuery:  "api-version=2025-01-01-preview%26api-key%3Dleak",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured capturedRequest
			upstream := chatUpstream(t, &captured)
			defer upstream.Close()

			adapter := AzureOpenAIAdapter{Client: upstream.Client()}
			_, _, err := adapter.Chat(context.Background(), azureProvider(upstream.URL, tt.apiVersion), tt.deployment, simpleChatRequest())
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}

			if captured.method != http.MethodPost {
				t.Fatalf("method = %s, want POST", captured.method)
			}
			if captured.escapedPath != tt.wantPath {
				t.Fatalf("path = %q, want %q", captured.escapedPath, tt.wantPath)
			}
			if captured.rawQuery != tt.wantQuery {
				t.Fatalf("query = %q, want %q", captured.rawQuery, tt.wantQuery)
			}
			// The deployment names the URL; the body still carries the provider model.
			if captured.body["model"] != tt.deployment {
				t.Fatalf("body model = %v, want %q", captured.body["model"], tt.deployment)
			}
		})
	}
}

// Azure authenticates with api-key and has never forwarded Provider.Headers or
// the OpenAI account headers. Sharing request machinery with the OpenAI adapter
// must not start sending either.
func TestAzureOpenAIAdapterSendsOnlyAzureHeaders(t *testing.T) {
	var captured capturedRequest
	upstream := chatUpstream(t, &captured)
	defer upstream.Close()

	provider := azureProvider(upstream.URL, "")
	provider.Headers = map[string]string{"x-custom": "forwarded-by-openai-only"}
	provider.Options = map[string]string{
		"organization_id":   "org-1",
		"openai_project_id": "proj-1",
		"account_id":        "acct-1",
	}

	adapter := AzureOpenAIAdapter{Client: upstream.Client()}
	if _, _, err := adapter.Chat(context.Background(), provider, "gpt-4o", simpleChatRequest()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if got := captured.header.Get("api-key"); got != "azure-key" {
		t.Fatalf("api-key = %q, want azure-key", got)
	}
	if got := captured.header.Get("content-type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	for _, name := range []string{"authorization", "x-custom", "OpenAI-Organization", "OpenAI-Project", "X-TokenHub-Upstream-Account"} {
		if got := captured.header.Get(name); got != "" {
			t.Fatalf("Azure request carried %s = %q", name, got)
		}
	}
}

// An empty API key still sets the header, which is what makes a misconfigured
// deployment fail at Azure with its own error rather than silently anonymously.
func TestAzureOpenAIAdapterSetsAPIKeyHeaderEvenWhenEmpty(t *testing.T) {
	var captured capturedRequest
	upstream := chatUpstream(t, &captured)
	defer upstream.Close()

	provider := azureProvider(upstream.URL, "")
	provider.APIKey = ""

	adapter := AzureOpenAIAdapter{Client: upstream.Client()}
	if _, _, err := adapter.Chat(context.Background(), provider, "gpt-4o", simpleChatRequest()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if _, present := captured.header["Api-Key"]; !present {
		t.Fatalf("api-key header was omitted: %v", captured.header)
	}
}

func TestAzureOpenAIAdapterEmbeddingsUsesDeploymentURL(t *testing.T) {
	var captured capturedRequest
	upstream := chatUpstream(t, &captured)
	defer upstream.Close()

	adapter := AzureOpenAIAdapter{Client: upstream.Client()}
	_, _, err := adapter.Embeddings(context.Background(), azureProvider(upstream.URL, ""), "text-embed", EmbeddingsRequest{Model: "gateway-embed", Input: "hi"})
	if err != nil {
		t.Fatalf("Embeddings: %v", err)
	}

	if captured.escapedPath != "/openai/deployments/text-embed/embeddings" {
		t.Fatalf("path = %q", captured.escapedPath)
	}
	if captured.body["model"] != "text-embed" {
		t.Fatalf("body model = %v, want text-embed", captured.body["model"])
	}
}

// reasoning_content is a TokenHub extension that only DeepSeek understands, so
// Azure must keep stripping it along with the other gateway-local fields.
func TestAzureOpenAIAdapterDoesNotForwardGatewayExtensions(t *testing.T) {
	var captured capturedRequest
	upstream := chatUpstream(t, &captured)
	defer upstream.Close()

	adapter := AzureOpenAIAdapter{Client: upstream.Client()}
	_, _, err := adapter.Chat(context.Background(), azureProvider(upstream.URL, ""), "gpt-4o", ChatCompletionRequest{
		Model: "gateway-model",
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

	messages, _ := captured.body["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("no messages were forwarded: %v", captured.body)
	}
	message, _ := messages[0].(map[string]any)
	for _, field := range []string{"reasoning_content", "reasoning_signature", "redacted_reasoning_content"} {
		if _, present := message[field]; present {
			t.Fatalf("%s must remain gateway-local: %v", field, message)
		}
	}
}

// Stripping is the Azure adapter's own rule, not something inferred from the
// provider type: nothing enforces which types an adapter is registered under,
// so a provider row typed "deepseek" must not talk the Azure adapter into
// forwarding the extension.
func TestAzureOpenAIAdapterStripsReasoningContentForAnyProviderType(t *testing.T) {
	var captured capturedRequest
	upstream := chatUpstream(t, &captured)
	defer upstream.Close()

	provider := azureProvider(upstream.URL, "")
	provider.Type = "deepseek"

	adapter := AzureOpenAIAdapter{Client: upstream.Client()}
	_, _, err := adapter.Chat(context.Background(), provider, "gpt-4o", ChatCompletionRequest{
		Model:    "gateway-model",
		Messages: []ChatMessage{{Role: "assistant", Content: "answer", ReasoningContent: "thinking"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	messages, _ := captured.body["messages"].([]any)
	message, _ := messages[0].(map[string]any)
	if _, present := message["reasoning_content"]; present {
		t.Fatalf("Azure forwarded reasoning_content: %v", message)
	}
}

func TestAzureOpenAIAdapterRequiresBaseURL(t *testing.T) {
	adapter := AzureOpenAIAdapter{}
	_, _, err := adapter.Chat(context.Background(), Provider{Type: ProviderAzureOpenAI, APIKey: "azure-key"}, "gpt-4o", simpleChatRequest())

	httpErr := AsHTTPError(err)
	if httpErr == nil || httpErr.Message != "Azure OpenAI base_url is required" {
		t.Fatalf("error = %v, want the Azure-specific misconfiguration message", err)
	}
	if httpErr.Code != "provider_not_configured" {
		t.Fatalf("code = %q, want provider_not_configured", httpErr.Code)
	}
}

// /v1/responses dispatches on the adapter's Responses method without consulting
// the capability registry, so Azure's explicit 501 is the only thing standing
// between a caller and a request to an endpoint Azure deployments do not serve.
func TestAzureOpenAIAdapterResponsesIsNotImplemented(t *testing.T) {
	adapter := AzureOpenAIAdapter{}
	_, _, err := adapter.Responses(context.Background(), azureProvider("https://example.invalid", ""), "gpt-4o", ResponsesRequest{Model: "gateway-model"})

	httpErr := AsHTTPError(err)
	if httpErr == nil || httpErr.Status != 501 {
		t.Fatalf("error = %v, want a 501", err)
	}
	if httpErr.Code != "provider_capability_not_supported" {
		t.Fatalf("code = %q, want provider_capability_not_supported", httpErr.Code)
	}
	if httpErr.Message != "Azure responses adapter is not implemented in MVP" {
		t.Fatalf("message = %q", httpErr.Message)
	}
}

// The streaming half of the same rule: the responses streaming path picks its
// adapter by interface, so Azure must not satisfy ResponsesStreamOpener.
func TestAzureOpenAIAdapterIsNotAResponsesStreamOpener(t *testing.T) {
	var adapter any = AzureOpenAIAdapter{}
	if _, ok := adapter.(ResponsesStreamOpener); ok {
		t.Fatal("AzureOpenAIAdapter must not implement ResponsesStreamOpener: /v1/responses would stream from an endpoint Azure does not serve")
	}
	if _, ok := adapter.(ProviderAdapter); !ok {
		t.Fatal("AzureOpenAIAdapter must still be a ProviderAdapter")
	}
}

// Streaming runs on StreamClient precisely so the non-streaming total deadline
// cannot truncate a live stream. Azure shares that machinery and must keep the
// same policy.
func TestAzureChatStreamOutlivesTheNonStreamingTimeout(t *testing.T) {
	upstream := sseUpstream(t, 0, []time.Duration{80 * time.Millisecond, 80 * time.Millisecond, 80 * time.Millisecond})
	defer upstream.Close()

	client := upstream.Client()
	client.Timeout = 100 * time.Millisecond
	adapter := AzureOpenAIAdapter{
		Client:            client,
		StreamClient:      &http.Client{Transport: client.Transport},
		StreamIdleTimeout: time.Second,
	}

	var out bytes.Buffer
	_, err := adapter.ChatStream(context.Background(), azureProvider(upstream.URL, ""), "gpt-4o", simpleChatRequest(), &out)
	if err != nil {
		t.Fatalf("an active Azure stream was cut short: %v", err)
	}
	if !strings.Contains(out.String(), "[DONE]") {
		t.Fatalf("stream was truncated before [DONE]: %q", out.String())
	}
	if !strings.Contains(out.String(), "chunk-2") {
		t.Fatalf("stream lost its last chunk: %q", out.String())
	}
}

func TestAzureOpenAIAdapterChatStreamUsesDeploymentURL(t *testing.T) {
	var captured capturedRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.escapedPath = r.URL.EscapedPath()
		captured.rawQuery = r.URL.RawQuery
		captured.header = r.Header.Clone()
		decodeFixtureRequest(t, r.Body, &captured.body)
		w.Header().Set("content-type", "text/event-stream")
		writeFixture(t, w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()

	adapter := AzureOpenAIAdapter{Client: upstream.Client()}
	var out bytes.Buffer
	if _, err := adapter.ChatStream(context.Background(), azureProvider(upstream.URL, ""), "gpt-4o", simpleChatRequest(), &out); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if captured.escapedPath != "/openai/deployments/gpt-4o/chat/completions" {
		t.Fatalf("path = %q", captured.escapedPath)
	}
	if captured.rawQuery != "api-version="+azureOpenAIDefaultAPIVersion {
		t.Fatalf("query = %q", captured.rawQuery)
	}
	if captured.body["stream"] != true {
		t.Fatalf("stream flag = %v, want true", captured.body["stream"])
	}
	streamOptions, _ := captured.body["stream_options"].(map[string]any)
	if streamOptions["include_usage"] != true {
		t.Fatalf("include_usage = %v, want true", streamOptions["include_usage"])
	}
	if got := captured.header.Get("api-key"); got != "azure-key" {
		t.Fatalf("api-key = %q", got)
	}
}

// Adapters are initialised as struct literals across the repository, so the zero
// value has to reach an upstream on its own: no client and no configured
// request builder.
func TestZeroValueOpenAICompatibleAdapterCallsUpstream(t *testing.T) {
	var captured capturedRequest
	upstream := chatUpstream(t, &captured)
	defer upstream.Close()

	adapter := OpenAICompatibleAdapter{}
	provider := Provider{Type: ProviderOpenAICompatible, BaseURL: upstream.URL, APIKey: "openai-key"}
	if _, _, err := adapter.Chat(context.Background(), provider, "model-x", simpleChatRequest()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if captured.escapedPath != "/chat/completions" {
		t.Fatalf("path = %q, want /chat/completions", captured.escapedPath)
	}
	if got := captured.header.Get("authorization"); got != "Bearer openai-key" {
		t.Fatalf("authorization = %q", got)
	}
}

func TestOpenAICompatibleAdapterSendsAccountAndProviderHeaders(t *testing.T) {
	var captured capturedRequest
	upstream := chatUpstream(t, &captured)
	defer upstream.Close()

	provider := Provider{
		Type:    ProviderOpenAICompatible,
		BaseURL: upstream.URL,
		APIKey:  "openai-key",
		Headers: map[string]string{"x-custom": "kept"},
		Options: map[string]string{
			"organization_id":   "org-1",
			"openai_project_id": "proj-1",
			"account_id":        "acct-1",
		},
	}

	adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
	if _, _, err := adapter.Chat(context.Background(), provider, "model-x", simpleChatRequest()); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	for name, want := range map[string]string{
		"authorization":               "Bearer openai-key",
		"OpenAI-Organization":         "org-1",
		"OpenAI-Project":              "proj-1",
		"X-TokenHub-Upstream-Account": "acct-1",
		"x-custom":                    "kept",
	} {
		if got := captured.header.Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestOpenAICompatibleAdapterRequiresBaseURL(t *testing.T) {
	adapter := OpenAICompatibleAdapter{}
	_, _, err := adapter.Chat(context.Background(), Provider{Type: ProviderOpenAICompatible, APIKey: "openai-key"}, "model-x", simpleChatRequest())

	httpErr := AsHTTPError(err)
	if httpErr == nil || httpErr.Message != "Provider base_url is required" {
		t.Fatalf("error = %v, want the provider misconfiguration message", err)
	}
}

// closeTrackingBody reports whether the response body was closed.
type closeTrackingBody struct {
	io.Reader
	closed bool
}

func (b *closeTrackingBody) Close() error {
	b.closed = true
	return nil
}

func TestCheckProviderResponseClassifiesAndBoundsTheBody(t *testing.T) {
	body := &closeTrackingBody{Reader: strings.NewReader(strings.Repeat("x", providerErrorBodyPrefix*3))}
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"7"}},
		Body:       body,
	}

	err := checkProviderResponse(resp)
	if err == nil {
		t.Fatal("a 429 must be reported as a provider error")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "provider_rate_limited" || httpErr.UpstreamStatus != http.StatusTooManyRequests {
		t.Fatalf("error = %+v, want a rate-limit classification", httpErr)
	}
	if len(httpErr.Message) != providerErrorBodyPrefix {
		t.Fatalf("message length = %d, want the body read to stop at %d", len(httpErr.Message), providerErrorBodyPrefix)
	}
	var invocation *ProviderInvocationError
	if !errors.As(err, &invocation) || invocation.RetryAfter != 7*time.Second {
		t.Fatalf("retry-after was lost: %v", err)
	}
	if !body.closed {
		t.Fatal("a failed response body must be closed")
	}
}

// The success path hands the body back still open, because the caller streams or
// decodes it.
func TestCheckProviderResponseLeavesSuccessfulResponsesAlone(t *testing.T) {
	body := &closeTrackingBody{Reader: strings.NewReader("{}")}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: body}

	if err := checkProviderResponse(resp); err != nil {
		t.Fatalf("checkProviderResponse: %v", err)
	}
	if body.closed {
		t.Fatal("a successful response body must stay open for the caller")
	}
}
