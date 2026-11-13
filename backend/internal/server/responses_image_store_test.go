package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func imageDataURL(fill string, count int) string {
	return "data:image/png;base64," + strings.Repeat(fill, count)
}

func TestResponsesImageStoreNewItemsDropStoredHistory(t *testing.T) {
	historical := imageDataURL("A", 1100)
	latest := imageDataURL("B", 1100)
	remote := "https://example.com/keep.png"
	first := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_image", "image_url": historical},
	}}
	assistant := map[string]any{"type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "ok"},
	}}
	call := map[string]any{"type": "function_call", "name": "look", "arguments": "{}"}
	output := map[string]any{"type": "function_call_output", "call_id": "call-1", "output": "done"}
	next := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "again"},
		map[string]any{"type": "input_image", "image_url": latest},
		map[string]any{"type": "input_image", "image_url": remote},
	}}
	boundary := responsesImageStoreWithOutput(responsesImageStoreBoundary([]any{first}), map[string]any{"output": []any{assistant, call}})
	if boundary == "" {
		t.Fatal("stored output identity was not recorded")
	}
	suffix, ok := responsesImageStoreNewItems([]any{first, assistant, call, output, next}, boundary)
	if !ok {
		t.Fatal("matching replay was not continued")
	}
	encoded, err := json.Marshal(suffix)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if strings.Contains(body, historical) || strings.Contains(body, "tokenhub.invalid") || strings.Contains(body, `"function_call"`) || strings.Contains(body, `"ok"`) {
		t.Fatalf("stored history was forwarded: %s", body)
	}
	if !strings.Contains(body, latest) || !strings.Contains(body, remote) || !strings.Contains(body, "done") {
		t.Fatalf("new tool result or image was dropped: %s", body)
	}
	if _, ok := responsesImageStoreNewItems([]any{next, assistant}, boundary); ok {
		t.Fatal("divergent replay matched the stored boundary")
	}
	if _, ok := responsesImageStoreNewItems([]any{first, assistant}, boundary); ok {
		t.Fatal("replay with no new input was continued")
	}
	other := map[string]any{"type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "cat"},
	}}
	if _, ok := responsesImageStoreNewItems([]any{first, other, call, output, next}, boundary); ok {
		t.Fatal("a different assistant answer matched the stored output")
	}
	if _, ok := responsesImageStoreNewItems([]any{first, assistant, call, output, next}, responsesImageStoreBoundary([]any{first})); ok {
		t.Fatal("unverified assistant echo matched an output-less boundary")
	}
}

func TestResponsesImageStoreNewItemsMatchStoredAssistantHistory(t *testing.T) {
	prior := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "earlier"},
	}}
	assistant := map[string]any{"type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "seen"},
	}}
	image := imageDataURL("C", 1100)
	firstImage := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_image", "image_url": image},
	}}
	later := map[string]any{"type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "later"},
	}}
	boundary := responsesImageStoreWithOutput(responsesImageStoreBoundary([]any{prior, assistant, firstImage}), map[string]any{"output": []any{later}})
	if boundary == "" {
		t.Fatal("stored output identity was not recorded")
	}
	next := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "next-turn"},
	}}
	suffix, ok := responsesImageStoreNewItems([]any{prior, assistant, firstImage, later, next}, boundary)
	if !ok {
		t.Fatal("stored assistant history did not match")
	}
	encoded, err := json.Marshal(suffix)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if strings.Contains(body, image) || strings.Contains(body, "seen") || strings.Contains(body, "later") || strings.Contains(body, "earlier") {
		t.Fatalf("stored history was forwarded: %s", body)
	}
	if !strings.Contains(body, "next-turn") {
		t.Fatalf("new input was dropped: %s", body)
	}
	if _, ok := responsesImageStoreNewItems([]any{prior, next}, boundary); ok {
		t.Fatal("replay that omitted the stored assistant matched")
	}
	different := map[string]any{"type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "other"},
	}}
	if _, ok := responsesImageStoreNewItems([]any{prior, assistant, firstImage, different, next}, boundary); ok {
		t.Fatal("a different later answer matched the stored output")
	}
	annotated := map[string]any{
		"id": "msg_later", "status": "completed", "type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": "later", "annotations": []any{map[string]any{"type": "url"}}}},
	}
	if responsesEchoIdentity(annotated) == "" || responsesEchoIdentity(annotated) != responsesEchoIdentity(later) {
		t.Fatal("response metadata changed the stored assistant identity")
	}
	searchA := map[string]any{"id": "ws_a", "status": "completed", "type": "web_search_call", "action": map[string]any{"type": "search", "query": "cat"}}
	searchReplay := map[string]any{"type": "web_search_call", "action": map[string]any{"type": "search", "query": "cat"}}
	searchB := map[string]any{"type": "web_search_call", "action": map[string]any{"type": "search", "query": "dog"}}
	if responsesEchoIdentity(searchA) == "" || responsesEchoIdentity(searchA) != responsesEchoIdentity(searchReplay) {
		t.Fatal("volatile web search metadata changed the stored identity")
	}
	if responsesEchoIdentity(searchA) == responsesEchoIdentity(searchB) {
		t.Fatal("different web search queries collapsed to one identity")
	}
	generatedA := map[string]any{"id": "ig_a", "status": "completed", "type": "image_generation_call", "result": "cat-image"}
	generatedB := map[string]any{"type": "image_generation_call", "result": "dog-image"}
	if responsesEchoIdentity(generatedA) == "" || responsesEchoIdentity(generatedA) == responsesEchoIdentity(generatedB) {
		t.Fatal("different generated images collapsed to one identity")
	}
	searchBoundary := responsesImageStoreWithOutput(responsesImageStoreBoundary([]any{prior}), map[string]any{"output": []any{searchA}})
	if _, ok := responsesImageStoreNewItems([]any{prior, searchB, next}, searchBoundary); ok {
		t.Fatal("a different web search was attached to the stored response")
	}
	suffix, ok = responsesImageStoreNewItems([]any{prior, searchReplay, next}, searchBoundary)
	if !ok {
		t.Fatal("the same web search with a new id was not continued")
	}
	encoded, err = json.Marshal(suffix)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "cat") || !strings.Contains(string(encoded), "next-turn") {
		t.Fatalf("matching web search was forwarded: %s", encoded)
	}
}

func TestResponsesImageStoreReadsLegacyBoundary(t *testing.T) {
	first := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "look"},
	}}
	assistant := map[string]any{"type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "ok"},
	}}
	call := map[string]any{"type": "function_call", "name": "noop", "arguments": "{}"}
	second := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "between"},
	}}
	next := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "next"},
	}}
	other := map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": "other"},
	}}
	boundary := fmt.Sprintf("[%q,%q]", responsesInputItemHash(first), responsesInputItemHash(second))
	suffix, ok := responsesImageStoreNewItems([]any{first, assistant, call, second, assistant, next}, boundary)
	if !ok {
		t.Fatal("legacy hash array did not skip echoes")
	}
	encoded, err := json.Marshal(suffix)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "look") || strings.Contains(string(encoded), "between") || strings.Contains(string(encoded), `"ok"`) || !strings.Contains(string(encoded), "next") {
		t.Fatalf("legacy suffix = %s", encoded)
	}
	if _, ok := responsesImageStoreNewItems([]any{first, assistant, other, next}, boundary); ok {
		t.Fatal("a different input item matched the legacy boundary")
	}
	for _, raw := range []string{`[]`, `[""]`, `[1]`, `{"turns":null}`, `not-json`} {
		if _, ok := decodeResponsesImageBoundary(raw); ok {
			t.Fatalf("invalid boundary was accepted: %s", raw)
		}
	}
	empty, ok := decodeResponsesImageBoundary(`{"turns":[]}`)
	if !ok || len(empty.Turns) != 0 {
		t.Fatal("empty turn list was rejected")
	}
	extended := responsesImageStoreExtendedBoundary(fmt.Sprintf("[%q]", responsesInputItemHash(first)), []any{next})
	if !strings.Contains(extended, `"legacy":true`) || strings.HasPrefix(strings.TrimSpace(extended), "[") {
		t.Fatalf("legacy boundary was not rewritten: %s", extended)
	}
	replay := []any{first, assistant, call, next, assistant, call, other}
	if _, ok := responsesImageStoreNewItems(replay, extended); ok {
		t.Fatal("unverified echo on the new turn matched a legacy upgrade")
	}
	withOutput := responsesImageStoreApplyOutput(extended, []string{responsesEchoIdentity(assistant), responsesEchoIdentity(call)})
	if withOutput == "" {
		t.Fatal("legacy upgrade did not record the new output")
	}
	suffix, ok = responsesImageStoreNewItems(replay, withOutput)
	if !ok {
		t.Fatal("verified new turn after a legacy prefix was not continued")
	}
	encoded, err = json.Marshal(suffix)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "look") || strings.Contains(string(encoded), `"ok"`) || !strings.Contains(string(encoded), "other") {
		t.Fatalf("verified suffix = %s", encoded)
	}
	different := map[string]any{"type": "message", "role": "assistant", "content": []any{
		map[string]any{"type": "output_text", "text": "dog"},
	}}
	if _, ok := responsesImageStoreNewItems([]any{first, assistant, call, next, different, call, other}, withOutput); ok {
		t.Fatal("a different answer on the new turn matched the legacy upgrade")
	}
}

func TestResponsesDecodeLimitHonorsConfiguredCeiling(t *testing.T) {
	server := &Server{config: Config{MaxMultimodalRequestBytes: 32 << 20}}
	if server.responsesDecodeLimit() != responsesImageReplayBytes {
		t.Fatalf("decode limit = %d, want %d", server.responsesDecodeLimit(), responsesImageReplayBytes)
	}
	server.config.MaxMultimodalRequestBytes = 200 << 20
	if server.responsesDecodeLimit() != 200<<20 {
		t.Fatalf("decode limit = %d, want the larger configured limit", server.responsesDecodeLimit())
	}
	server.config.MaxMultimodalRequestBytes = maxConfigurableRequestBytes + 1
	if server.responsesDecodeLimit() != maxConfigurableRequestBytes {
		t.Fatalf("decode limit = %d, want ceiling %d", server.responsesDecodeLimit(), maxConfigurableRequestBytes)
	}
}

func TestSubscriptionRoutesForwardAboveConfiguredLimit(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	server := NewWithConfig(store, Config{SecretKey: "subscription-ceiling", MaxMultimodalRequestBytes: 2000})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	image := imageDataURL("Z", 2500)
	headers := http.Header{"Session-Id": []string{"sess-sub"}}
	requestFor := func() ResponsesRequest {
		return responsesRequestFromJSON(t, fmt.Sprintf(`{"model":"vision-replay","store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]}]}`, image))
	}
	call := CallContext{Model: Model{Name: "vision-replay"}, Key: APIKey{ID: "key-sub"}}
	subscription := RoutedCall{Call: call, Routes: []RouteSelection{{Provider: Provider{Type: ProviderChatGPTSubscription}}}}
	request := requestFor()
	if err := server.applyResponsesImageStore(context.Background(), &subscription, &request, headers); err != nil {
		t.Fatalf("subscription replay: %v", err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if request.Store == nil || *request.Store || responsesRawStringField(request, "previous_response_id") != "" || !strings.Contains(string(encoded), image) {
		t.Fatal("subscription replay rewrote store or dropped the image")
	}
	codex := RoutedCall{Call: call, Routes: []RouteSelection{{Provider: Provider{Type: ProviderOpenAICodex, Options: map[string]string{"responses_store": "on"}}}}}
	request = requestFor()
	if err := server.applyResponsesImageStore(context.Background(), &codex, &request, headers); err != nil {
		t.Fatalf("codex replay: %v", err)
	}
	openai := RoutedCall{Call: call, Routes: []RouteSelection{{Provider: Provider{Type: ProviderOpenAI}}}}
	request = requestFor()
	if err := server.applyResponsesImageStore(context.Background(), &openai, &request, headers); err == nil || AsHTTPError(err).Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("openai replay error = %v", err)
	}
	mixed := RoutedCall{Call: call, Routes: []RouteSelection{{Provider: Provider{Type: ProviderChatGPTSubscription}}, {Provider: Provider{Type: ProviderOpenAI}}}}
	request = requestFor()
	if err := server.applyResponsesImageStore(context.Background(), &mixed, &request, headers); err == nil || AsHTTPError(err).Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("mixed replay error = %v", err)
	}
	if server.responsesForwardLimit(subscription.Routes) != server.responsesDecodeLimit() || server.responsesForwardLimit(nil) != server.configuredMultimodalLimit() {
		t.Fatal("forward limit did not follow the route set")
	}
}

func TestPrepareResponsesImageStoreSkipsUnsafeRoutes(t *testing.T) {
	image := imageDataURL("C", 1200)
	headers := http.Header{"Session-Id": []string{"sess-skip"}}
	base := func() (ResponsesRequest, RoutedCall) {
		request := responsesRequestFromJSON(t, fmt.Sprintf(`{"model":"vision-replay","store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]}]}`, image))
		routed := RoutedCall{Call: CallContext{Model: Model{Name: "vision-replay"}, Key: APIKey{ID: "key-skip"}}, Routes: []RouteSelection{{
			Provider: Provider{ID: "provider-a", Type: ProviderOpenAICompatible, Options: map[string]string{"responses_store": "on"}}, Route: ModelRoute{ID: "route-a"}, ProviderModel: "vendor-a",
		}}}
		return request, routed
	}

	t.Run("client continuation", func(t *testing.T) {
		_, routed := base()
		request := responsesRequestFromJSON(t, fmt.Sprintf(`{"model":"vision-replay","store":false,"previous_response_id":"resp_client","input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]}]}`, image))
		if err := (&Server{}).prepareResponsesImageStore(context.Background(), &routed, &request, headers); err != nil {
			t.Fatal(err)
		}
		if request.Store == nil || *request.Store || responsesRawStringField(request, "previous_response_id") != "resp_client" {
			t.Fatalf("client continuation changed: store=%v previous=%s", request.Store, responsesRawStringField(request, "previous_response_id"))
		}
	})

	t.Run("mixed codex route", func(t *testing.T) {
		request, routed := base()
		routed.Routes = append(routed.Routes, RouteSelection{Provider: Provider{ID: "provider-codex", Type: ProviderOpenAICodex}, Route: ModelRoute{ID: "route-codex"}, ProviderModel: "gpt-codex"})
		if err := (&Server{}).prepareResponsesImageStore(context.Background(), &routed, &request, headers); err != nil {
			t.Fatal(err)
		}
		if request.Store == nil || *request.Store || responsesRawStringField(request, "previous_response_id") != "" {
			t.Fatalf("mixed route rewrote the request: store=%v previous=%s", request.Store, responsesRawStringField(request, "previous_response_id"))
		}
	})

	t.Run("responses store off", func(t *testing.T) {
		request, routed := base()
		routed.Routes[0].Provider.Options = map[string]string{"responses_store": "off"}
		if err := (&Server{}).prepareResponsesImageStore(context.Background(), &routed, &request, headers); err != nil {
			t.Fatal(err)
		}
		if request.Store == nil || *request.Store {
			t.Fatal("responses_store=off still forced store")
		}
	})

	t.Run("invalid session", func(t *testing.T) {
		request, routed := base()
		headers := http.Header{"Session-Id": []string{"bad\nsession"}}
		if err := (&Server{}).prepareResponsesImageStore(context.Background(), &routed, &request, headers); err != nil {
			t.Fatal(err)
		}
		if request.Store == nil || *request.Store {
			t.Fatal("invalid session id still rewrote the request")
		}
	})

	t.Run("prompt cache key is not a session", func(t *testing.T) {
		_, routed := base()
		request := responsesRequestFromJSON(t, fmt.Sprintf(`{"model":"vision-replay","store":false,"prompt_cache_key":"shared-prefix","input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]}]}`, image))
		if err := (&Server{}).prepareResponsesImageStore(context.Background(), &routed, &request, http.Header{}); err != nil {
			t.Fatal(err)
		}
		if request.Store == nil || *request.Store || responsesRawStringField(request, "previous_response_id") != "" {
			t.Fatal("prompt_cache_key started an image continuation")
		}
	})

	t.Run("conversation is left untouched", func(t *testing.T) {
		_, routed := base()
		request := responsesRequestFromJSON(t, fmt.Sprintf(`{"model":"vision-replay","store":false,"conversation":{"id":"conv_1"},"input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]}]}`, image))
		if err := (&Server{}).prepareResponsesImageStore(context.Background(), &routed, &request, headers); err != nil {
			t.Fatal(err)
		}
		if request.Store == nil || *request.Store || responsesRawStringField(request, "previous_response_id") != "" || !responsesRawFieldPresent(request, "conversation") {
			t.Fatalf("conversation request changed: store=%v previous=%s", request.Store, responsesRawStringField(request, "previous_response_id"))
		}
	})

	t.Run("subscription and opted out types", func(t *testing.T) {
		if routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: ProviderOpenAICodex, Options: map[string]string{"responses_store": "on"}}}) {
			t.Fatal("codex subscription accepted responses_store=on")
		}
		if routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: ProviderChatGPTSubscription}}) {
			t.Fatal("chatgpt subscription accepted image store")
		}
		if routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: "deepseek"}}) {
			t.Fatal("deepseek accepted image store without responses_store=on")
		}
		if routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: ProviderOpenAICompatible}}) {
			t.Fatal("openai-compatible accepted image store without responses_store=on")
		}
		if !routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: ProviderOpenAI}}) || !routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: ProviderAzureOpenAI}}) {
			t.Fatal("official OpenAI or Azure store support was not on by default")
		}
		if !routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: ProviderOpenAICompatible, Options: map[string]string{"responses_store": "on"}}}) {
			t.Fatal("explicit responses_store=on did not enable an openai-compatible provider")
		}
		if !routeAllowsResponsesImageStore(RouteSelection{Provider: Provider{Type: "deepseek", Options: map[string]string{"responses_store": "on"}}}) {
			t.Fatal("explicit responses_store=on did not enable a custom provider")
		}
		resourceOff := RouteSelection{Provider: Provider{Type: ProviderOpenAI, Options: map[string]string{"responses_store": "on"}}, Resource: &ProviderResource{Options: map[string]string{"responses_store": "off"}}}
		if routeAllowsResponsesImageStore(resourceOff) {
			t.Fatal("resource responses_store=off did not win")
		}
	})
}

func TestResponsesImageStoreBindingUpsertsAndExpires(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	binding := responsesImageStoreBinding{KeyHash: "image-store-key", RouteID: "route-a", ProviderID: "provider-a", ProviderModel: "vendor-a", ResourceID: "", UpstreamID: "resp_old", InputBoundary: `["old"]`, ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := store.SaveResponsesImageStore(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.UpstreamID = "resp_new"
	binding.InputBoundary = `["old","new"]`
	binding.ExpiresAt = time.Now().Add(time.Hour).Unix()
	if err := store.SaveResponsesImageStore(ctx, binding); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.LoadResponsesImageStore(ctx, binding.KeyHash)
	if err != nil || !found || loaded.UpstreamID != "resp_new" || loaded.RouteID != "route-a" || loaded.InputBoundary != `["old","new"]` {
		t.Fatalf("loaded = %+v found=%v err=%v", loaded, found, err)
	}
	expired := binding
	expired.KeyHash = "image-store-expired"
	expired.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	if err := store.SaveResponsesImageStore(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadResponsesImageStore(ctx, expired.KeyHash); err != nil || found {
		t.Fatalf("expired binding found=%v err=%v", found, err)
	}
}

func TestPrepareResponsesImageStoreKeepsImagesWhenContinuationUnavailable(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	server := NewWithConfig(store, Config{SecretKey: "image-store-secret", MaxMultimodalRequestBytes: 2000})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	historical := imageDataURL("G", 2500)
	request := responsesRequestFromJSON(t, fmt.Sprintf(`{"model":"vision-replay","store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]},{"role":"user","content":[{"type":"input_text","text":"next"}]}]}`, historical))
	call := CallContext{Model: Model{Name: "vision-replay"}, Key: APIKey{ID: "key-gone"}}
	headers := http.Header{"Session-Id": []string{"sess-gone"}}
	key, ok := server.responsesImageStoreKey(call, request, headers)
	if !ok {
		t.Fatal("missing session key")
	}
	if err := store.SaveResponsesImageStore(context.Background(), responsesImageStoreBinding{
		KeyHash: key, RouteID: "route-old", ProviderID: "provider-old", ProviderModel: "vendor-old", UpstreamID: "resp_old", InputBoundary: responsesImageStoreBoundary(request.Input), ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	routed := RoutedCall{Call: call, Routes: []RouteSelection{{
		Provider: Provider{ID: "provider-new", Type: ProviderOpenAICompatible, Options: map[string]string{"responses_store": "on"}}, Route: ModelRoute{ID: "route-new"}, ProviderModel: "vendor-new",
	}}}
	err := server.applyResponsesImageStore(context.Background(), &routed, &request, headers)
	if err == nil || AsHTTPError(err).Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized unbound replay error = %v", err)
	}
	encoded, marshalErr := json.Marshal(request)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if responsesRawStringField(request, "previous_response_id") != "" || !strings.Contains(string(encoded), historical) || strings.Contains(string(encoded), "tokenhub.invalid") {
		t.Fatalf("unbound replay dropped or rewrote images: %s", encoded)
	}
	if len(routed.Routes) != 1 || routed.Routes[0].Route.ID != "route-new" {
		t.Fatalf("routes = %+v", routed.Routes)
	}
}

func TestResponsesImageStoreReplaysContinuationOnSameProvider(t *testing.T) {
	var first, second recordedResponses
	upstreamA := httptest.NewServer(first.handler("resp_provider_a"))
	upstreamB := httptest.NewServer(second.handler("resp_provider_b"))
	t.Cleanup(upstreamA.Close)
	t.Cleanup(upstreamB.Close)

	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	const model = "vision-replay"
	project := store.CreateProject(Project{Name: "Image store", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Image key", Allowed: []string{model}, Status: StatusActive}, "thk_image_store")
	if err != nil {
		t.Fatal(err)
	}
	store.AddProvider(Provider{ID: "provider-a", Name: "Provider A", Type: ProviderOpenAICompatible, BaseURL: upstreamA.URL + "/v1", APIKey: "upstream-a", Status: StatusActive, Healthy: true, Options: map[string]string{"responses_store": "on"}})
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route-a", ModelName: model, ProviderID: "provider-a", ProviderModel: "vendor-a", Status: StatusActive, Priority: 2, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	server := NewWithConfig(store, Config{AdminToken: "image-store-admin", SecretKey: "image-store-secret", MaxMultimodalRequestBytes: 8000})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	small := imageDataURL("A", 1500)
	text := postResponses(t, server.Handler(), fmt.Sprintf(`{"model":%q,"store":false,"input":"hello"}`, model), secret, "sess-image")
	if text.Code != http.StatusOK {
		t.Fatalf("text turn: %d %s", text.Code, text.Body)
	}
	if got := upstreamBool(t, first.last(), "store"); got {
		t.Fatal("text turn forced store")
	}

	firstTurn := postResponses(t, server.Handler(), imageTurn(model, small), secret, "sess-image")
	if firstTurn.Code != http.StatusOK || firstTurn.Header.Get("x-tokenhub-provider") != "provider-a" {
		t.Fatalf("first image turn: %d provider=%s body=%s", firstTurn.Code, firstTurn.Header.Get("x-tokenhub-provider"), firstTurn.Body)
	}
	forwarded := upstreamObject(t, first.last())
	if forwarded["store"] != true || forwarded["previous_response_id"] != nil || !strings.Contains(string(first.last()), small) {
		t.Fatalf("first forward = %#v", forwarded)
	}

	store.AddProvider(Provider{ID: "provider-b", Name: "Provider B", Type: ProviderOpenAICompatible, BaseURL: upstreamB.URL + "/v1", APIKey: "upstream-b", Status: StatusActive, Healthy: true, Options: map[string]string{"responses_store": "on"}})
	store.AddRoute(ModelRoute{ID: "route-b", ModelName: model, ProviderID: "provider-b", ProviderModel: "vendor-b", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	secondTurn := postResponses(t, server.Handler(), continuedImageTurn(model, small, "next"), secret, "sess-image")
	if secondTurn.Code != http.StatusOK || secondTurn.Header.Get("x-tokenhub-provider") != "provider-a" {
		t.Fatalf("continuation: %d provider=%s body=%s", secondTurn.Code, secondTurn.Header.Get("x-tokenhub-provider"), secondTurn.Body)
	}
	if second.calls() != 0 {
		t.Fatalf("better-priority provider was called %d times", second.calls())
	}
	continued := string(first.last())
	if !strings.Contains(continued, `"previous_response_id":"resp_provider_a"`) || strings.Contains(continued, small) || strings.Contains(continued, "look") || strings.Contains(continued, "function_call") || strings.Contains(continued, "tokenhub.invalid") || !strings.Contains(continued, "next") {
		t.Fatalf("continuation body = %s", continued)
	}
	callsBeforeReject := first.calls()
	huge := imageDataURL("H", 9000)
	rejected := postResponses(t, server.Handler(), imageTurn(model, huge), secret, "sess-image")
	if rejected.Code != http.StatusRequestEntityTooLarge || !strings.Contains(rejected.Body, "request body exceeds 8000 bytes") || first.calls() != callsBeforeReject {
		t.Fatalf("divergent replay: %d calls=%d body=%s", rejected.Code, first.calls(), rejected.Body)
	}
	conversation := postResponses(t, server.Handler(), strings.TrimSuffix(continuedImageTurn(model, small, "via-conversation"), "}")+`,"conversation":{"id":"conv_keep"}}`, secret, "sess-image")
	if conversation.Code != http.StatusOK || strings.Contains(string(second.last()), `"previous_response_id"`) || upstreamBool(t, second.last(), "store") {
		t.Fatalf("conversation replay: %d body=%s", conversation.Code, second.last())
	}

	other := postResponses(t, server.Handler(), imageTurn(model, small), secret, "sess-other")
	if other.Code != http.StatusOK || other.Header.Get("x-tokenhub-provider") != "provider-b" {
		t.Fatalf("new session: %d provider=%s body=%s", other.Code, other.Header.Get("x-tokenhub-provider"), other.Body)
	}

	noSession := postResponses(t, server.Handler(), imageTurn(model, huge), secret, "")
	if noSession.Code != http.StatusRequestEntityTooLarge || !strings.Contains(noSession.Body, "request body exceeds 8000 bytes") {
		t.Fatalf("no session: %d %s", noSession.Code, noSession.Body)
	}
}

func TestResponsesImageStoreStreamingRemember(t *testing.T) {
	var upstream recordedResponses
	remote := httptest.NewServer(upstream.handler("resp_stream"))
	t.Cleanup(remote.Close)
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	const model = "vision-stream"
	project := store.CreateProject(Project{Name: "Image stream", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Stream key", Allowed: []string{model}, Status: StatusActive}, "thk_image_stream")
	if err != nil {
		t.Fatal(err)
	}
	store.AddProvider(Provider{ID: "provider-stream", Name: "Stream", Type: ProviderOpenAICompatible, BaseURL: remote.URL + "/v1", APIKey: "upstream-stream", Status: StatusActive, Healthy: true, Options: map[string]string{"responses_store": "on"}})
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route-stream", ModelName: model, ProviderID: "provider-stream", ProviderModel: "vendor-stream", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	server := NewWithConfig(store, Config{AdminToken: "image-stream-admin", SecretKey: "image-stream-secret", MaxMultimodalRequestBytes: 8000})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	small := imageDataURL("S", 1500)
	first := postResponses(t, server.Handler(), strings.TrimSuffix(imageTurn(model, small), "}")+`,"stream":true}`, secret, "sess-stream")
	if first.Code != http.StatusOK || !strings.Contains(first.Body, "response.completed") {
		t.Fatalf("stream turn: %d %s", first.Code, first.Body)
	}
	second := postResponses(t, server.Handler(), continuedImageTurn(model, small, "next"), secret, "sess-stream")
	if second.Code != http.StatusOK {
		t.Fatalf("follow-up: %d %s", second.Code, second.Body)
	}
	if !strings.Contains(string(upstream.last()), `"previous_response_id":"resp_stream"`) || strings.Contains(string(upstream.last()), small) || strings.Contains(string(upstream.last()), "tokenhub.invalid") {
		t.Fatalf("stream continuation body = %s", upstream.last())
	}
}

// responsesImageStoreEchoOutput is the assistant and tool call that
// continuedImageTurn replays. Extra id, status, and annotation fields must not
// change the identity used to verify that echo.
const responsesImageStoreEchoOutput = `[{"id":"msg_ok","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok","annotations":[]}]},{"id":"fc_noop","type":"function_call","name":"noop","arguments":"{}"}]`

func TestPrepareResponsesImageStoreRetryIgnoresUnrelatedClientError(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	server := NewWithConfig(store, Config{SecretKey: "retry-secret", MaxMultimodalRequestBytes: 8000})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	binding := responsesImageStoreBinding{
		KeyHash: "retry-key", RouteID: "route", ProviderID: "provider", ProviderModel: "model",
		UpstreamID: "resp_keep", InputBoundary: `{"turns":[{"input":["abc"],"output":[]}]}`,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}
	if err := store.SaveResponsesImageStore(context.Background(), binding); err != nil {
		t.Fatal(err)
	}
	request := ResponsesRequest{imageContinuation: &responsesImageContinuation{
		key: binding.KeyHash, rewritten: true, original: ResponsesRequest{Input: "hello"},
	}}
	route := RouteSelection{Provider: Provider{Type: ProviderOpenAICompatible, Options: map[string]string{"responses_store": "on"}}, Route: ModelRoute{ID: "route"}}
	unrelated := newProviderHTTPError(http.StatusBadRequest, nil, []byte(`{"error":{"message":"model not found"}}`))
	if _, ok, err := server.prepareResponsesImageStoreRetry(context.Background(), route, &request, unrelated); err != nil || ok {
		t.Fatalf("unrelated 400 retried: ok=%v err=%v", ok, err)
	}
	if _, found, err := store.LoadResponsesImageStore(context.Background(), binding.KeyHash); err != nil || !found {
		t.Fatalf("unrelated 400 deleted the binding: found=%v err=%v", found, err)
	}
}

func TestResponsesImageStoreKeepsDivergentAssistantBranch(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), body...))
		call := len(bodies)
		mu.Unlock()
		answer, id := "cat", "resp_cat"
		if call > 1 {
			answer, id = "dog", "resp_dog"
		}
		output := fmt.Sprintf(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]}]`, answer)
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":"completed","output":%s,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, id, output)
	}))
	t.Cleanup(upstream.Close)
	server, _, secret := newImageContinuationServer(t, upstream.URL, "vision-branch", 8000)
	small := imageDataURL("C", 1500)
	const model = "vision-branch"
	first := postResponses(t, server.Handler(), imageTurn(model, small), secret, "sess-branch")
	if first.Code != http.StatusOK {
		t.Fatalf("first image turn: %d %s", first.Code, first.Body)
	}
	regenerated := postResponses(t, server.Handler(), imageTurn(model, small), secret, "sess-branch")
	if regenerated.Code != http.StatusOK {
		t.Fatalf("regenerated image turn: %d %s", regenerated.Code, regenerated.Body)
	}
	replay := postResponses(t, server.Handler(), answeredImageTurn(model, small, "cat", "use the cat"), secret, "sess-branch")
	if replay.Code != http.StatusOK {
		t.Fatalf("cat replay: %d %s", replay.Code, replay.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("upstream calls = %d", len(bodies))
	}
	if strings.Contains(string(bodies[1]), `"previous_response_id"`) || !strings.Contains(string(bodies[1]), small) {
		t.Fatalf("regenerated turn = %s", bodies[1])
	}
	forwarded := string(bodies[2])
	if strings.Contains(forwarded, `"previous_response_id"`) || !strings.Contains(forwarded, small) || !strings.Contains(forwarded, "use the cat") {
		t.Fatalf("divergent branch was attached to the regenerated response: %s", forwarded)
	}
}

func TestResponsesImageStoreRecoversMissingUpstreamResponse(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var nextID int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), body...))
		nextID++
		id := fmt.Sprintf("resp_replay_%d", nextID)
		mu.Unlock()
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if _, ok := payload["previous_response_id"]; ok {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Previous response with id not found","code":"previous_response_not_found"}}`)
			return
		}
		if payload["stream"] == true {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"output\":%s,\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n", id, responsesImageStoreEchoOutput)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":"completed","output":%s,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, id, responsesImageStoreEchoOutput)
	}))
	t.Cleanup(upstream.Close)
	server, store, secret := newImageContinuationServer(t, upstream.URL, "vision-missing", 8000)
	const model = "vision-missing"
	small := imageDataURL("M", 1500)
	first := postResponses(t, server.Handler(), imageTurn(model, small), secret, "sess-missing")
	if first.Code != http.StatusOK || !strings.Contains(first.Body, "resp_replay_1") {
		t.Fatalf("first image turn: %d %s", first.Code, first.Body)
	}
	continued := continuedImageTurn(model, small, "next")
	extended := strings.TrimSuffix(continued, "]}") + `,{"type":"message","role":"user","content":[{"type":"input_text","text":"after"}]}]}`
	streamed := strings.TrimSuffix(strings.TrimSuffix(extended, "]}")+`,{"type":"message","role":"user","content":[{"type":"input_text","text":"streamed"}]}]}`, "}") + `,"stream":true}`
	for _, followUp := range []string{continued, extended, streamed} {
		response := postResponses(t, server.Handler(), followUp, secret, "sess-missing")
		if response.Code != http.StatusOK {
			t.Fatalf("follow-up: %d %s", response.Code, response.Body)
		}
	}
	mu.Lock()
	calls := len(bodies)
	copied := append([][]byte(nil), bodies...)
	mu.Unlock()
	if calls != 7 {
		t.Fatalf("upstream calls = %d", calls)
	}
	stale := previousResponseID(t, copied[1])
	if stale == "" || previousResponseID(t, copied[2]) != "" || previousResponseID(t, copied[4]) != "" || previousResponseID(t, copied[6]) != "" {
		t.Fatalf("recovery did not replace the stored response: %q / %q / %q / %q", stale, previousResponseID(t, copied[2]), previousResponseID(t, copied[4]), previousResponseID(t, copied[6]))
	}
	for i, body := range copied[3:] {
		if previousResponseID(t, body) == stale {
			t.Fatalf("stale response id was reused on call %d", i+3)
		}
	}
	last := string(copied[6])
	if !strings.Contains(last, small) || !strings.Contains(last, "streamed") {
		t.Fatalf("recovered stream replay = %s", last)
	}
	var binding responsesImageStoreBinding
	if err := store.db.First(&binding).Error; err != nil || binding.UpstreamID == "" || binding.UpstreamID == stale {
		t.Fatalf("stale binding remained: %+v err=%v", binding, err)
	}
}

func TestResponsesImageStoreDropsMissingResponseWhenReplayExceedsLimit(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), body...))
		mu.Unlock()
		var payload map[string]any
		_ = json.Unmarshal(body, &payload)
		if _, ok := payload["previous_response_id"]; ok {
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"Previous response with id not found","code":"previous_response_not_found"}}`)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"resp_fit","status":"completed","output":%s,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, responsesImageStoreEchoOutput)
	}))
	t.Cleanup(upstream.Close)
	const limit = 8000
	const model = "vision-oversize"
	server, store, secret := newImageContinuationServer(t, upstream.URL, model, limit)
	image := imageDataURL("Q", 1500)
	var firstBody, nextBody string
	foundSize := false
	for n := 0; n < 2000; n++ {
		candidate := image + strings.Repeat("Q", n*10)
		firstBody = imageTurn(model, candidate)
		nextBody = continuedImageTurn(model, candidate, "next")
		firstSize := marshaledResponsesSize(t, firstBody)
		nextSize := marshaledResponsesSize(t, nextBody)
		// The continuation adds only the echoed answer, so the two sizes stay
		// close. Keep the stored turn under the limit and the recovered
		// transcript over it.
		if firstSize < limit-80 && nextSize > limit {
			foundSize = true
			break
		}
	}
	if !foundSize {
		t.Fatalf("could not bracket the forward limit: first=%d next=%d", marshaledResponsesSize(t, firstBody), marshaledResponsesSize(t, nextBody))
	}
	first := postResponses(t, server.Handler(), firstBody, secret, "sess-oversize")
	if first.Code != http.StatusOK {
		t.Fatalf("first image turn: %d %s", first.Code, first.Body)
	}
	rejected := postResponses(t, server.Handler(), nextBody, secret, "sess-oversize")
	if rejected.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized recovery: %d %s", rejected.Code, rejected.Body)
	}
	mu.Lock()
	callsAfterReject := len(bodies)
	var attempted []byte
	if callsAfterReject >= 2 {
		attempted = bodies[1]
	}
	mu.Unlock()
	if callsAfterReject != 2 || !strings.Contains(string(attempted), `"previous_response_id"`) {
		t.Fatalf("missing response was not attempted once: calls=%d body=%s", callsAfterReject, attempted)
	}
	again := postResponses(t, server.Handler(), nextBody, secret, "sess-oversize")
	if again.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("second oversized replay: %d %s", again.Code, again.Body)
	}
	mu.Lock()
	calls := len(bodies)
	mu.Unlock()
	if calls != 2 {
		t.Fatalf("stale response id was reused: calls=%d", calls)
	}
	var count int64
	if err := store.db.Model(&responsesImageStoreBinding{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("binding count = %d err=%v", count, err)
	}
}

func TestResponsesImageStoreBackgroundBindingWaitsForApproval(t *testing.T) {
	for _, outcome := range []string{"approved", "denied", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			var recorded recordedResponses
			upstream := httptest.NewServer(recorded.handler("resp_background"))
			t.Cleanup(upstream.Close)
			server, store, secret := newImageContinuationServer(t, upstream.URL, "vision-background", 8000)
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			var hookCalls atomic.Int32
			hook := pluginmeta.GatewayHookDescriptor{PluginID: "tokenhub.test-image-approval", HookID: "approval", Stage: pluginmeta.StageGuardrailPost, Priority: 1000, FailurePolicy: pluginmeta.FailurePolicyFailClosed}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatal(err)
			}
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(ctx context.Context, _ pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				if hookCalls.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return pluginmeta.GatewayHookResult{}, ctx.Err()
					}
					if outcome == "denied" {
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionDeny}, nil
					}
				}
				return pluginmeta.GatewayHookResult{}, nil
			})); err != nil {
				t.Fatal(err)
			}
			const model = "vision-background"
			small := imageDataURL("B", 1500)
			first := postResponses(t, server.Handler(), strings.TrimSuffix(imageTurn(model, small), "}")+`,"background":true}`, secret, "sess-background")
			if first.Code != http.StatusOK {
				t.Fatalf("background submission: %d %s", first.Code, first.Body)
			}
			var envelope map[string]any
			if err := json.Unmarshal([]byte(first.Body), &envelope); err != nil {
				t.Fatal(err)
			}
			id, _ := envelope["id"].(string)
			if id == "" {
				t.Fatalf("missing background job id: %s", first.Body)
			}
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("output hook not entered")
			}
			if count := imageStoreBindingCount(t, store); count != 0 {
				t.Fatalf("binding published before approval: %d", count)
			}
			if outcome == "cancelled" {
				cancelled := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses/"+id+"/cancel", nil, secret)
				if cancelled.Code != http.StatusOK {
					t.Fatalf("cancel: %d %s", cancelled.Code, cancelled.Body)
				}
			}
			unblock()
			status := "completed"
			if outcome == "denied" {
				status = "failed"
			}
			if outcome == "cancelled" {
				status = "cancelled"
			}
			waitForResponseJobStatus(t, server.Handler(), secret, id, status)
			want := int64(0)
			if outcome == "approved" {
				want = 1
			}
			if count := imageStoreBindingCount(t, store); count != want {
				t.Fatalf("binding count = %d, want %d", count, want)
			}
			followUp := postResponses(t, server.Handler(), continuedImageTurn(model, small, "next"), secret, "sess-background")
			if followUp.Code != http.StatusOK {
				t.Fatalf("follow-up: %d %s", followUp.Code, followUp.Body)
			}
			forwarded := string(recorded.last())
			if outcome == "approved" {
				if !strings.Contains(forwarded, `"previous_response_id":"resp_background"`) || strings.Contains(forwarded, small) {
					t.Fatalf("approved continuation = %s", forwarded)
				}
				return
			}
			if strings.Contains(forwarded, `"previous_response_id"`) || !strings.Contains(forwarded, small) {
				t.Fatalf("unapproved output was reused: %s", forwarded)
			}
		})
	}
}

func TestResponsesImageStoreContinuesOversizedLegacyBoundary(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var nextID atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		mu.Lock()
		bodies = append(bodies, append([]byte(nil), body...))
		mu.Unlock()
		id := fmt.Sprintf("resp_legacy_%d", nextID.Add(1))
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":"completed","output":%s,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, id, responsesImageStoreEchoOutput)
	}))
	t.Cleanup(upstream.Close)
	const limit = 8000
	const model = "vision-legacy"
	server, store, secret := newImageContinuationServer(t, upstream.URL, model, limit)
	image := imageDataURL("L", 12000)
	continued := continuedImageTurn(model, image, "next")
	if marshaledResponsesSize(t, continued) <= limit {
		t.Fatalf("continuation size = %d, want above %d", marshaledResponsesSize(t, continued), limit)
	}
	parsed := responsesRequestFromJSON(t, continued)
	items, ok := parsed.Input.([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("input = %#v", parsed.Input)
	}
	var keyID string
	for _, key := range store.ListAPIKeys() {
		if key.Name == "Image key" {
			keyID = key.ID
		}
	}
	if keyID == "" {
		t.Fatal("image api key was not created")
	}
	headers := http.Header{"Session-Id": []string{"sess-legacy"}}
	storeKey, ok := server.responsesImageStoreKey(CallContext{Model: Model{Name: model}, Key: APIKey{ID: keyID}}, parsed, headers)
	if !ok {
		t.Fatal("missing session key")
	}
	if err := store.SaveResponsesImageStore(context.Background(), responsesImageStoreBinding{
		KeyHash: storeKey, RouteID: "route-image", ProviderID: "provider-image", ProviderModel: "vendor-image",
		UpstreamID: "resp_seeded", InputBoundary: fmt.Sprintf("[%q]", responsesInputItemHash(items[0])),
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	first := postResponses(t, server.Handler(), continued, secret, "sess-legacy")
	if first.Code != http.StatusOK {
		t.Fatalf("legacy continuation: %d %s", first.Code, first.Body)
	}
	mu.Lock()
	if len(bodies) != 1 {
		t.Fatalf("upstream calls = %d", len(bodies))
	}
	forwarded := string(bodies[0])
	mu.Unlock()
	if !strings.Contains(forwarded, `"previous_response_id":"resp_seeded"`) || strings.Contains(forwarded, image) {
		t.Fatalf("legacy continuation body = %s", forwarded)
	}
	var binding responsesImageStoreBinding
	if err := store.db.First(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if binding.UpstreamID == "resp_seeded" || !strings.Contains(binding.InputBoundary, `"legacy":true`) || strings.HasPrefix(strings.TrimSpace(binding.InputBoundary), "[") {
		t.Fatalf("saved boundary = %s id=%s", binding.InputBoundary, binding.UpstreamID)
	}
	divergent := strings.TrimSuffix(continued, "]}") + `,{"type":"message","role":"assistant","content":[{"type":"output_text","text":"dog"}]},{"type":"function_call","name":"noop","arguments":"{}"},{"type":"message","role":"user","content":[{"type":"input_text","text":"after"}]}]}`
	rejected := postResponses(t, server.Handler(), divergent, secret, "sess-legacy")
	if rejected.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("divergent legacy follow-up: %d %s", rejected.Code, rejected.Body)
	}
	mu.Lock()
	calls := len(bodies)
	mu.Unlock()
	if calls != 1 {
		t.Fatalf("divergent follow-up called upstream %d times", calls)
	}
	correct := strings.TrimSuffix(continued, "]}") + `,{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","name":"noop","arguments":"{}"},{"type":"message","role":"user","content":[{"type":"input_text","text":"after"}]}]}`
	second := postResponses(t, server.Handler(), correct, secret, "sess-legacy")
	if second.Code != http.StatusOK {
		t.Fatalf("verified follow-up: %d %s", second.Code, second.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !strings.Contains(string(bodies[1]), `"previous_response_id":"`+binding.UpstreamID+`"`) || strings.Contains(string(bodies[1]), image) {
		t.Fatalf("verified follow-up body = %s", bodies[1])
	}
}

func newImageContinuationServer(t *testing.T, upstreamURL string, model string, limit int64) (*Server, *GormStore, string) {
	t.Helper()
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	project := store.CreateProject(Project{Name: "Image continuation", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Image key", Allowed: []string{model}, Status: StatusActive}, "thk_image_"+model)
	if err != nil {
		t.Fatal(err)
	}
	store.AddProvider(Provider{ID: "provider-image", Name: "Image", Type: ProviderOpenAICompatible, BaseURL: upstreamURL + "/v1", APIKey: "upstream-image", Status: StatusActive, Healthy: true, Options: map[string]string{"responses_store": "on"}})
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route-image", ModelName: model, ProviderID: "provider-image", ProviderModel: "vendor-image", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	server := NewWithConfig(store, Config{AdminToken: "image-review-admin", SecretKey: "image-review-secret", MaxMultimodalRequestBytes: limit, ResponsePollIntervalMillis: 10})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return server, store, secret
}

func imageStoreBindingCount(t *testing.T, store *GormStore) int64 {
	t.Helper()
	var count int64
	if err := store.db.Model(&responsesImageStoreBinding{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func previousResponseID(t *testing.T, body []byte) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("upstream body: %v %s", err, body)
	}
	id, _ := payload["previous_response_id"].(string)
	return id
}

func marshaledResponsesSize(t *testing.T, body string) int {
	t.Helper()
	encoded, err := json.Marshal(responsesRequestFromJSON(t, body))
	if err != nil {
		t.Fatal(err)
	}
	return len(encoded)
}

func imageTurn(model string, image string) string {
	return fmt.Sprintf(`{"model":%q,"store":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":%q}]}]}`, model, image)
}

func continuedImageTurn(model string, image string, followUp string) string {
	return fmt.Sprintf(`{"model":%q,"store":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":%q}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},{"type":"function_call","name":"noop","arguments":"{}"},{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}]}`, model, image, followUp)
}

func answeredImageTurn(model string, image string, answer string, followUp string) string {
	return fmt.Sprintf(`{"model":%q,"store":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":%q}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":%q}]},{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}]}`, model, image, answer, followUp)
}

func postResponses(t *testing.T, handler http.Handler, body string, key string, session string) responseBody {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
	request.Header.Set("content-type", "application/json")
	request.Header.Set("authorization", "Bearer "+key)
	if session != "" {
		request.Header.Set("session-id", session)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return responseBody{Code: recorder.Code, Header: recorder.Header(), Body: recorder.Body.String()}
}

type recordedResponses struct {
	mu     sync.Mutex
	bodies [][]byte
}

func (r *recordedResponses) handler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.bodies = append(r.bodies, body)
		r.mu.Unlock()
		var probe struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.Stream {
			w.Header().Set("content-type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"output\":%s,\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n", id, responsesImageStoreEchoOutput)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":"completed","output":%s,"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, id, responsesImageStoreEchoOutput)
	}
}

func (r *recordedResponses) last() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return nil
	}
	return r.bodies[len(r.bodies)-1]
}

func (r *recordedResponses) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func upstreamObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("upstream body: %v %s", err, body)
	}
	return payload
}

func upstreamBool(t *testing.T, body []byte, key string) bool {
	t.Helper()
	value, _ := upstreamObject(t, body)[key].(bool)
	return value
}
