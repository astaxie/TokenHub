package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func difyTestRequest() ChatCompletionRequest {
	return ChatCompletionRequest{
		Model: "dify-app-x",
		Messages: []ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "summarize this"},
		},
		User: "user-1",
	}
}

func difyTestProvider(baseURL string) Provider {
	return Provider{Type: ProviderDify, BaseURL: baseURL, APIKey: "app-key"}
}

func TestDifyAdapterChatBlockingMapsChatMessages(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat-messages" {
			t.Errorf("upstream path = %s, want /v1/chat-messages", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer app-key" {
			t.Errorf("authorization header = %q, want the app key as a bearer token", got)
		}
		var payload difyRequest
		decodeFixtureRequest(t, r.Body, &payload)
		if payload.ResponseMode != "blocking" {
			t.Errorf("response_mode = %q, want blocking", payload.ResponseMode)
		}
		if payload.User != "user-1" {
			t.Errorf("user = %q, want user-1 from the OpenAI request", payload.User)
		}
		if !strings.Contains(payload.Query, "hello") || !strings.Contains(payload.Query, "summarize this") {
			t.Errorf("query = %q, want the flattened conversation", payload.Query)
		}
		writeFixture(t, w, `{"answer":"final answer","conversation_id":"c1","metadata":{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}}`)
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	response, usage, err := adapter.Chat(context.Background(), difyTestProvider(upstream.URL), "dify-app-x", difyTestRequest())
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	body, ok := response.(map[string]any)
	if !ok {
		t.Fatalf("response type = %T, want a chat completion object", response)
	}
	if body["object"] != "chat.completion" || body["model"] != "dify-app-x" {
		t.Errorf("response object/model = %v/%v, want chat.completion/dify-app-x", body["object"], body["model"])
	}
	choices, _ := body["choices"].([]map[string]any)
	if len(choices) != 1 {
		t.Fatalf("choices length = %d, want 1", len(choices))
	}
	message, _ := choices[0]["message"].(map[string]any)
	if message["content"] != "final answer" {
		t.Errorf("message content = %v, want the Dify answer", message["content"])
	}
	if choices[0]["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choices[0]["finish_reason"])
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens != 7 || usage.TotalTokens != 18 {
		t.Errorf("usage tokens = %d/%d/%d, want 11/7/18 from metadata.usage", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
	if usage.Transport != "http_json" {
		t.Errorf("usage transport = %q, want http_json", usage.Transport)
	}
}

func TestDifyAdapterChatStreamMapsMessageEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload difyRequest
		decodeFixtureRequest(t, r.Body, &payload)
		if payload.ResponseMode != "streaming" {
			t.Errorf("response_mode = %q, want streaming", payload.ResponseMode)
		}
		w.Header().Set("content-type", "text/event-stream")
		writeFixture(t, w, `data: {"event":"ping"}`+"\n\n")
		writeFixture(t, w, `data: {"event":"message","answer":"Hel"}`+"\n\n")
		writeFixture(t, w, `data: {"event":"message","answer":"lo"}`+"\n\n")
		writeFixture(t, w, `data: {"event":"message_end","metadata":{"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}}`+"\n\n")
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	var stream strings.Builder
	usage, err := adapter.ChatStream(context.Background(), difyTestProvider(upstream.URL), "dify-app-x", difyTestRequest(), &stream)
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}
	frames := stream.String()
	if !strings.Contains(frames, "Hel") || !strings.Contains(frames, "lo") {
		t.Errorf("stream frames missing answer deltas:\n%s", frames)
	}
	if !strings.Contains(frames, `"finish_reason":"stop"`) {
		t.Errorf("stream frames missing the stop finish_reason:\n%s", frames)
	}
	if !strings.Contains(frames, "data: [DONE]") {
		t.Errorf("stream frames missing the [DONE] sentinel:\n%s", frames)
	}
	if usage.PromptTokens != 5 || usage.CompletionTokens != 2 || usage.TotalTokens != 7 {
		t.Errorf("usage tokens = %d/%d/%d, want 5/2/7 from message_end", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
	if usage.Transport != "http_sse" {
		t.Errorf("usage transport = %q, want http_sse", usage.Transport)
	}
}

func TestDifyAdapterWorkflowBlockingMapsOutputs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workflows/run" {
			t.Errorf("upstream path = %s, want /v1/workflows/run", r.URL.Path)
		}
		var payload difyRequest
		decodeFixtureRequest(t, r.Body, &payload)
		if got, ok := payload.Inputs["topic"].(string); !ok || !strings.Contains(got, "summarize this") {
			t.Errorf("inputs[topic] = %v, want the flattened conversation", payload.Inputs["topic"])
		}
		writeFixture(t, w, `{"workflow_run_id":"r1","data":{"id":"run","status":"succeeded","outputs":{"summary":"done"},"error":null,"total_tokens":42}}`)
	}))
	defer upstream.Close()

	provider := difyTestProvider(upstream.URL)
	provider.Options = map[string]string{
		difyAppTypeOption:        "workflow",
		difyInputVariableOption:  "topic",
		difyOutputVariableOption: "summary",
	}
	adapter := DifyAdapter{Client: upstream.Client()}
	response, usage, err := adapter.Chat(context.Background(), provider, "dify-app-x", difyTestRequest())
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	body := response.(map[string]any)
	choices, _ := body["choices"].([]map[string]any)
	message, _ := choices[0]["message"].(map[string]any)
	if message["content"] != "done" {
		t.Errorf("message content = %v, want the workflow summary output", message["content"])
	}
	// Workflows report only the run total; inventing a prompt/completion split
	// would fabricate billed components.
	if usage.TotalTokens != 42 || usage.PromptTokens != 0 || usage.CompletionTokens != 0 {
		t.Errorf("usage tokens = %d/%d/%d, want total 42 with no fabricated split", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
}

func TestDifyAdapterWorkflowStreamMapsTextChunks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		writeFixture(t, w, `data: {"event":"workflow_started","data":{"id":"wf"}}`+"\n\n")
		writeFixture(t, w, `data: {"event":"node_started","data":{"id":"n1"}}`+"\n\n")
		writeFixture(t, w, `data: {"event":"text_chunk","data":{"text":"par"}}`+"\n\n")
		writeFixture(t, w, `data: {"event":"text_chunk","data":{"text":"tial"}}`+"\n\n")
		writeFixture(t, w, `data: {"event":"workflow_finished","data":{"id":"run","status":"succeeded","outputs":{},"total_tokens":9}}`+"\n\n")
	}))
	defer upstream.Close()

	provider := difyTestProvider(upstream.URL)
	provider.Options = map[string]string{difyAppTypeOption: "workflow"}
	adapter := DifyAdapter{Client: upstream.Client()}
	var stream strings.Builder
	usage, err := adapter.ChatStream(context.Background(), provider, "dify-app-x", difyTestRequest(), &stream)
	if err != nil {
		t.Fatalf("chat stream: %v", err)
	}
	frames := stream.String()
	if !strings.Contains(frames, "par") || !strings.Contains(frames, "tial") {
		t.Errorf("stream frames missing text_chunk deltas:\n%s", frames)
	}
	if !strings.Contains(frames, "data: [DONE]") {
		t.Errorf("stream frames missing the [DONE] sentinel:\n%s", frames)
	}
	if usage.TotalTokens != 9 {
		t.Errorf("usage total tokens = %d, want 9 from workflow_finished", usage.TotalTokens)
	}
}

func TestDifyAdapterWorkflowStreamFailureAbortsStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		writeFixture(t, w, `data: {"event":"text_chunk","data":{"text":"partial"}}`+"\n\n")
		writeFixture(t, w, `data: {"event":"workflow_finished","data":{"id":"run","status":"failed","error":{"message":"boom"},"total_tokens":3}}`+"\n\n")
	}))
	defer upstream.Close()

	provider := difyTestProvider(upstream.URL)
	provider.Options = map[string]string{difyAppTypeOption: "workflow"}
	adapter := DifyAdapter{Client: upstream.Client()}
	var stream strings.Builder
	_, err := adapter.ChatStream(context.Background(), provider, "dify-app-x", difyTestRequest(), &stream)
	if err == nil {
		t.Fatal("stream error = nil, want the workflow failure to surface")
	}
	if httpErr := AsHTTPError(err); httpErr.Code != "provider_upstream_error" || !strings.Contains(httpErr.Message, "boom") {
		t.Errorf("stream error = %s/%s, want provider_upstream_error mentioning boom", httpErr.Code, httpErr.Message)
	}
	if strings.Contains(stream.String(), "data: [DONE]") {
		t.Error("failed stream emitted [DONE]; a client must see the failure, not a completed answer")
	}
}

func TestDifyAdapterStreamErrorEventFailsRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		writeFixture(t, w, `data: {"event":"error","code":"invalid_param","message":"bad input"}`+"\n\n")
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	var stream strings.Builder
	_, err := adapter.ChatStream(context.Background(), difyTestProvider(upstream.URL), "dify-app-x", difyTestRequest(), &stream)
	if err == nil {
		t.Fatal("stream error = nil, want the in-stream error event to surface")
	}
	if httpErr := AsHTTPError(err); httpErr.Code != "provider_stream_error" || !strings.Contains(httpErr.Message, "bad input") {
		t.Errorf("stream error = %s/%s, want provider_stream_error mentioning bad input", httpErr.Code, httpErr.Message)
	}
	if strings.Contains(stream.String(), "data: [DONE]") {
		t.Error("failed stream emitted [DONE]")
	}
}

func TestDifyAdapterStreamEOFWithoutTerminalAbortsStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		writeFixture(t, w, `data: {"event":"message","answer":"partial"}`+"\n\n")
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	var stream strings.Builder
	_, err := adapter.ChatStream(context.Background(), difyTestProvider(upstream.URL), "dify-app-x", difyTestRequest(), &stream)
	if err == nil {
		t.Fatal("stream error = nil, want a dropped connection to surface")
	}
	if httpErr := AsHTTPError(err); httpErr.Code != "provider_invalid_response" {
		t.Errorf("stream error code = %s, want provider_invalid_response", httpErr.Code)
	}
	if strings.Contains(stream.String(), "data: [DONE]") {
		t.Error("truncated stream emitted [DONE]")
	}
}

func TestDifyAdapterSurfacesUpstreamErrorStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeFixture(t, w, `{"code":"unauthorized","message":"app key is invalid"}`)
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	_, _, err := adapter.Chat(context.Background(), difyTestProvider(upstream.URL), "dify-app-x", difyTestRequest())
	if err == nil {
		t.Fatal("chat error = nil, want the upstream 401 to surface")
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "provider_auth_error" {
		t.Errorf("error code = %s, want provider_auth_error", httpErr.Code)
	}
	if httpErr.UpstreamStatus != http.StatusUnauthorized {
		t.Errorf("upstream status = %d, want 401 for failover accounting", httpErr.UpstreamStatus)
	}
	// Auth failures intentionally do not forward the upstream detail: the body
	// echoes the credential TokenHub sent, which must not leak into responses.
	if strings.Contains(httpErr.Message, "app key is invalid") {
		t.Errorf("error message = %q, want the generic credential-rejected wording", httpErr.Message)
	}
}

func TestDifyOutputTextResolution(t *testing.T) {
	testCases := []struct {
		name       string
		outputs    map[string]any
		configured string
		want       string
		wantErr    bool
	}{
		{name: "configured variable wins", outputs: map[string]any{"summary": "s", "answer": "a"}, configured: "summary", want: "s"},
		{name: "answer fallback", outputs: map[string]any{"answer": "a", "other": 1}, want: "a"},
		{name: "text fallback", outputs: map[string]any{"text": "t"}, want: "t"},
		{name: "sole string output", outputs: map[string]any{"whatever": "w"}, want: "w"},
		{name: "ambiguous outputs without configuration", outputs: map[string]any{"a": "1", "b": "2"}, wantErr: true},
		{name: "no string outputs", outputs: map[string]any{"count": float64(3)}, wantErr: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			provider := Provider{Options: map[string]string{difyOutputVariableOption: testCase.configured}}
			got, err := difyOutputText(testCase.outputs, provider)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("output text = %q, want an error naming dify_output_variable", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("output text: %v", err)
			}
			if got != testCase.want {
				t.Errorf("output text = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestDifyAdapterBaseURLToleratesTrailingV1(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat-messages" {
			t.Errorf("upstream path = %s, want /v1/chat-messages without a doubled /v1", r.URL.Path)
		}
		writeFixture(t, w, `{"answer":"ok","metadata":{"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}}`)
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	provider := difyTestProvider(upstream.URL + "/v1")
	if _, _, err := adapter.Chat(context.Background(), provider, "dify-app-x", difyTestRequest()); err != nil {
		t.Fatalf("chat: %v", err)
	}
}

func TestDifyAdapterProbeFetchesParameters(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("probe method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v1/parameters" {
			t.Errorf("probe path = %s, want /v1/parameters", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer app-key" {
			t.Errorf("authorization header = %q, want the app key", got)
		}
		writeFixture(t, w, `{"user_input_form":[{"paragraph":{"variable":"query"}}]}`)
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	result, err := adapter.Probe(context.Background(), difyTestProvider(upstream.URL), ProviderResource{ID: "res-1"}, adapter.DefaultProbeRequest())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if result.ResourceID != "res-1" {
		t.Errorf("probe resource id = %q, want res-1", result.ResourceID)
	}
	if _, ok := result.Response["user_input_form"]; !ok {
		t.Errorf("probe response missing user_input_form: %v", result.Response)
	}
}

func TestDifyAdapterProbeSurfacesInvalidAppKey(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		writeFixture(t, w, `{"code":"unauthorized","message":"app key is invalid"}`)
	}))
	defer upstream.Close()

	adapter := DifyAdapter{Client: upstream.Client()}
	_, err := adapter.Probe(context.Background(), difyTestProvider(upstream.URL), ProviderResource{ID: "res-1"}, adapter.DefaultProbeRequest())
	if err == nil {
		t.Fatal("probe error = nil, want the upstream 401 to surface")
	}
	if httpErr := AsHTTPError(err); httpErr.UpstreamStatus != http.StatusUnauthorized {
		t.Errorf("probe upstream status = %d, want 401", httpErr.UpstreamStatus)
	}
}

func TestDifyAdapterDefaultsAnonymousUserAndChatApp(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat-messages" {
			t.Errorf("default app type routed to %s, want /v1/chat-messages", r.URL.Path)
		}
		var payload difyRequest
		decodeFixtureRequest(t, r.Body, &payload)
		if payload.User != "anonymous" {
			t.Errorf("default user = %q, want anonymous", payload.User)
		}
		writeFixture(t, w, `{"answer":"ok","metadata":{}}`)
	}))
	defer upstream.Close()

	req := difyTestRequest()
	req.User = nil
	adapter := DifyAdapter{Client: upstream.Client()}
	if _, _, err := adapter.Chat(context.Background(), difyTestProvider(upstream.URL), "dify-app-x", req); err != nil {
		t.Fatalf("chat: %v", err)
	}
}

// TestGatewayRoutesChatCompletionsToDifyWorkflow is the end-to-end proof for
// external callers: a service authenticates with a TokenHub API key against
// /v1/chat/completions, and the routed dify provider runs the Dify workflow.
func TestGatewayRoutesChatCompletionsToDifyWorkflow(t *testing.T) {
	var upstreamAuth, upstreamQuery, upstreamUser string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workflows/run" {
			t.Errorf("workflow provider path = %s, want /v1/workflows/run", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		upstreamAuth = r.Header.Get("Authorization")
		var payload difyRequest
		decodeFixtureRequest(t, r.Body, &payload)
		if query, ok := payload.Inputs["query"].(string); ok {
			upstreamQuery = query
		}
		upstreamUser = payload.User
		writeFixture(t, w, `{"workflow_run_id":"run-1","data":{"id":"run-1","status":"succeeded","outputs":{"answer":"workflow answer"},"total_tokens":31}}`)
	}))
	defer upstream.Close()

	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	// The demo key is allowlisted to its demo models, so the caller gets its
	// own key the way a real service would.
	if _, _, err := store.CreateAPIKey("prj_demo", APIKey{
		ID:     "key_dify_caller",
		Name:   "Report Service Key",
		Status: StatusActive,
	}, "thk_report_service"); err != nil {
		t.Fatal(err)
	}
	store.AddProvider(Provider{
		ID:      "prv_dify_flow",
		Name:    "Dify Report Flow",
		Type:    ProviderDify,
		BaseURL: upstream.URL,
		APIKey:  "app-dify-key",
		Status:  StatusActive,
		Options: map[string]string{difyAppTypeOption: "workflow"},
	})
	store.AddModel(Model{Name: "report-flow", Modality: "chat", Status: StatusActive})
	store.AddRoute(ModelRoute{ModelName: "report-flow", ProviderID: "prv_dify_flow", ProviderModel: "report-flow", Status: StatusActive})
	app := New(store).Handler()

	response := doJSON(t, app, http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    "report-flow",
		"messages": []map[string]any{{"role": "user", "content": "generate the weekly report"}},
		"user":     "billing-service",
	}, "thk_report_service")
	if response.Code != http.StatusOK {
		t.Fatalf("chat completion status = %d, body = %s", response.Code, response.Body)
	}

	if upstreamAuth != "Bearer app-dify-key" {
		t.Errorf("dify saw authorization %q, want the provider's app key", upstreamAuth)
	}
	if upstreamQuery != "generate the weekly report" {
		t.Errorf("dify saw inputs[query] = %q, want the caller's message", upstreamQuery)
	}
	if upstreamUser != "billing-service" {
		t.Errorf("dify saw user = %q, want the caller's user field", upstreamUser)
	}

	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	decodeFixtureRequest(t, strings.NewReader(response.Body), &completion)
	if len(completion.Choices) != 1 || completion.Choices[0].Message.Content != "workflow answer" {
		t.Errorf("completion choices = %+v, want the workflow output as the answer", completion.Choices)
	}
	if completion.Usage.TotalTokens != 31 {
		t.Errorf("completion usage total tokens = %d, want 31 reported by the workflow run", completion.Usage.TotalTokens)
	}
}

// TestAdminAPICreatesDifyProviderWithWorkflowOptions proves the documented
// setup path: POST /api/admin/providers with an explicit dify type keeps the
// type, the dify options, and imports the declared custom model inventory.
func TestAdminAPICreatesDifyProviderWithWorkflowOptions(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()

	created := doJSON(t, app, http.MethodPost, "/api/admin/providers", map[string]any{
		"name":       "Dify Report Flow",
		"type":       "dify",
		"base_url":   "https://dify.internal.example.com",
		"api_key":    "app-dify-key",
		"catalog_id": "custom",
		"options": map[string]string{
			difyAppTypeOption:        "workflow",
			difyInputVariableOption:  "topic",
			difyOutputVariableOption: "summary",
		},
		"custom_models":   []map[string]any{{"id": "report-flow", "name": "report-flow"}},
		"selected_models": []string{"report-flow"},
	}, "")
	if created.Code != http.StatusCreated {
		t.Fatalf("provider creation status = %d, body = %s", created.Code, created.Body)
	}

	var payload struct {
		Provider Provider `json:"provider"`
	}
	decodeFixtureRequest(t, strings.NewReader(created.Body), &payload)
	if payload.Provider.Type != ProviderDify {
		t.Errorf("created provider type = %q, want dify to survive creation", payload.Provider.Type)
	}
	if payload.Provider.Options[difyAppTypeOption] != "workflow" ||
		payload.Provider.Options[difyInputVariableOption] != "topic" ||
		payload.Provider.Options[difyOutputVariableOption] != "summary" {
		t.Errorf("created provider dify options = %v, want them preserved", payload.Provider.Options)
	}

	imported := false
	for _, model := range store.ListProviderModels() {
		if model.ProviderID == payload.Provider.ID && model.UpstreamModel == "report-flow" {
			imported = true
		}
	}
	if !imported {
		t.Errorf("provider models = %+v, want the declared custom model imported", store.ListProviderModels())
	}
}
