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
	"testing"
	"time"
)

func imageDataURL(fill string, count int) string {
	return "data:image/png;base64," + strings.Repeat(fill, count)
}

func TestCompactResponsesHistoricalImagesKeepsLatestUserImage(t *testing.T) {
	historical := imageDataURL("A", 1100)
	latest := imageDataURL("B", 1100)
	remote := "https://example.com/keep.png"
	tiny := "data:image/png;base64,YWJj"
	nested, err := json.Marshal(map[string]any{"image_url": historical})
	if err != nil {
		t.Fatal(err)
	}
	input := []any{
		map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_image", "image_url": historical},
			map[string]any{"type": "input_image", "image_url": tiny},
		}},
		map[string]any{"type": "function_call_output", "output": string(nested)},
		map[string]any{"type": "message", "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": "ok"},
		}},
		map[string]any{"type": "message", "role": "user", "content": []any{
			map[string]any{"type": "input_text", "text": "again"},
			map[string]any{"type": "input_image", "image_url": latest},
			map[string]any{"type": "input_image", "image_url": remote},
		}},
	}

	compacted := compactResponsesHistoricalImages(input)
	encoded, err := json.Marshal(compacted)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	if strings.Contains(body, historical) || !strings.Contains(body, responsesOmittedImageURL) {
		t.Fatalf("historical image was not replaced: %s", body)
	}
	if !strings.Contains(body, latest) || !strings.Contains(body, remote) || !strings.Contains(body, tiny) {
		t.Fatalf("current image, remote URL, or tiny fixture changed: %s", body)
	}
	if strings.Count(body, responsesOmittedImageURL) != 2 {
		t.Fatalf("placeholder count = %d, want 2: %s", strings.Count(body, responsesOmittedImageURL), body)
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

func TestPrepareResponsesImageStoreSkipsUnsafeRoutes(t *testing.T) {
	image := imageDataURL("C", 1200)
	headers := http.Header{"Session-Id": []string{"sess-skip"}}
	base := func() (ResponsesRequest, RoutedCall) {
		request := responsesRequestFromJSON(t, fmt.Sprintf(`{"model":"vision-replay","store":false,"input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]}]}`, image))
		routed := RoutedCall{Call: CallContext{Model: Model{Name: "vision-replay"}, Key: APIKey{ID: "key-skip"}}, Routes: []RouteSelection{{
			Provider: Provider{ID: "provider-a", Type: ProviderOpenAICompatible}, Route: ModelRoute{ID: "route-a"}, ProviderModel: "vendor-a",
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
	binding := responsesImageStoreBinding{KeyHash: "image-store-key", RouteID: "route-a", ProviderID: "provider-a", ProviderModel: "vendor-a", ResourceID: "", UpstreamID: "resp_old", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := store.SaveResponsesImageStore(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.UpstreamID = "resp_new"
	binding.ExpiresAt = time.Now().Add(time.Hour).Unix()
	if err := store.SaveResponsesImageStore(ctx, binding); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := store.LoadResponsesImageStore(ctx, binding.KeyHash)
	if err != nil || !found || loaded.UpstreamID != "resp_new" || loaded.RouteID != "route-a" {
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

func TestPrepareResponsesImageStoreCompactsWhenBoundRouteIsGone(t *testing.T) {
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
		KeyHash: key, RouteID: "route-old", ProviderID: "provider-old", ProviderModel: "vendor-old", UpstreamID: "resp_old", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
	routed := RoutedCall{Call: call, Routes: []RouteSelection{{
		Provider: Provider{ID: "provider-new", Type: ProviderOpenAICompatible}, Route: ModelRoute{ID: "route-new"}, ProviderModel: "vendor-new",
	}}}
	if err := server.prepareResponsesImageStore(context.Background(), &routed, &request, headers); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if responsesRawStringField(request, "previous_response_id") != "" || strings.Contains(string(encoded), historical) || request.Store == nil || !*request.Store {
		t.Fatalf("unbound replay = %s", encoded)
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
	store.AddProvider(Provider{ID: "provider-a", Name: "Provider A", Type: ProviderOpenAICompatible, BaseURL: upstreamA.URL + "/v1", APIKey: "upstream-a", Status: StatusActive, Healthy: true})
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

	firstTurn := postResponses(t, server.Handler(), imageTurn(model, small, ""), secret, "sess-image")
	if firstTurn.Code != http.StatusOK || firstTurn.Header.Get("x-tokenhub-provider") != "provider-a" {
		t.Fatalf("first image turn: %d provider=%s body=%s", firstTurn.Code, firstTurn.Header.Get("x-tokenhub-provider"), firstTurn.Body)
	}
	forwarded := upstreamObject(t, first.last())
	if forwarded["store"] != true || forwarded["previous_response_id"] != nil || !strings.Contains(string(first.last()), small) {
		t.Fatalf("first forward = %#v", forwarded)
	}

	store.AddProvider(Provider{ID: "provider-b", Name: "Provider B", Type: ProviderOpenAICompatible, BaseURL: upstreamB.URL + "/v1", APIKey: "upstream-b", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{ID: "route-b", ModelName: model, ProviderID: "provider-b", ProviderModel: "vendor-b", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	huge := imageDataURL("H", 9000)
	secondTurn := postResponses(t, server.Handler(), imageTurn(model, huge, "next"), secret, "sess-image")
	if secondTurn.Code != http.StatusOK || secondTurn.Header.Get("x-tokenhub-provider") != "provider-a" {
		t.Fatalf("continuation: %d provider=%s body=%s", secondTurn.Code, secondTurn.Header.Get("x-tokenhub-provider"), secondTurn.Body)
	}
	if second.calls() != 0 {
		t.Fatalf("better-priority provider was called %d times", second.calls())
	}
	continued := string(first.last())
	if !strings.Contains(continued, `"previous_response_id":"resp_provider_a"`) || strings.Contains(continued, huge) || !strings.Contains(continued, responsesOmittedImageURL) {
		t.Fatalf("continuation body = %s", continued)
	}

	other := postResponses(t, server.Handler(), imageTurn(model, small, ""), secret, "sess-other")
	if other.Code != http.StatusOK || other.Header.Get("x-tokenhub-provider") != "provider-b" {
		t.Fatalf("new session: %d provider=%s body=%s", other.Code, other.Header.Get("x-tokenhub-provider"), other.Body)
	}

	noSession := postResponses(t, server.Handler(), imageTurn(model, huge, ""), secret, "")
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
	store.AddProvider(Provider{ID: "provider-stream", Name: "Stream", Type: ProviderOpenAICompatible, BaseURL: remote.URL + "/v1", APIKey: "upstream-stream", Status: StatusActive, Healthy: true})
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route-stream", ModelName: model, ProviderID: "provider-stream", ProviderModel: "vendor-stream", Status: StatusActive, Priority: 1, Weight: 100, Strategy: RouteStrategyPriorityOnly})
	server := NewWithConfig(store, Config{AdminToken: "image-stream-admin", SecretKey: "image-stream-secret", MaxMultimodalRequestBytes: 8000})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	small := imageDataURL("S", 1500)
	first := postResponses(t, server.Handler(), strings.TrimSuffix(imageTurn(model, small, ""), "}")+`,"stream":true}`, secret, "sess-stream")
	if first.Code != http.StatusOK || !strings.Contains(first.Body, "response.completed") {
		t.Fatalf("stream turn: %d %s", first.Code, first.Body)
	}
	huge := imageDataURL("H", 9000)
	second := postResponses(t, server.Handler(), imageTurn(model, huge, "next"), secret, "sess-stream")
	if second.Code != http.StatusOK {
		t.Fatalf("follow-up: %d %s", second.Code, second.Body)
	}
	if !strings.Contains(string(upstream.last()), `"previous_response_id":"resp_stream"`) || strings.Contains(string(upstream.last()), huge) {
		t.Fatalf("stream continuation body = %s", upstream.last())
	}
}

func imageTurn(model string, image string, followUp string) string {
	if followUp == "" {
		return fmt.Sprintf(`{"model":%q,"store":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"look"},{"type":"input_image","image_url":%q}]}]}`, model, image)
	}
	return fmt.Sprintf(`{"model":%q,"store":false,"input":[{"type":"message","role":"user","content":[{"type":"input_image","image_url":%q}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},{"type":"message","role":"user","content":[{"type":"input_text","text":%q}]}]}`, model, image, followUp)
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
			_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n", id)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":%q,"status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, id)
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
