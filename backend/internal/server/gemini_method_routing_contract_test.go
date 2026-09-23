package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const geminiMethodModelPath = "/v1beta/models/" + geminiCodexTestModel

type geminiMethodRoute struct {
	method string
	wrong  string
	path   string
}

var geminiMethodRoutes = []geminiMethodRoute{
	{method: http.MethodGet, wrong: http.MethodPost, path: "/v1beta/models"},
	{method: http.MethodGet, wrong: http.MethodPut, path: geminiMethodModelPath},
	{method: http.MethodPost, wrong: http.MethodGet, path: geminiMethodModelPath + ":generateContent"},
	{method: http.MethodPost, wrong: http.MethodGet, path: geminiMethodModelPath + ":streamGenerateContent"},
	{method: http.MethodPost, wrong: http.MethodGet, path: geminiMethodModelPath + ":countTokens"},
}

func TestGeminiMethodRoutesRejectWrongMethods(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	app := server.Handler()
	requestLogsBefore := len(store.ListRequestLogs())
	auditEventsBefore := len(store.ListAuditEvents())
	imageJobsBefore := len(store.ListImageJobs(1000))

	for _, route := range geminiMethodRoutes {
		t.Run(route.wrong+" "+route.path, func(t *testing.T) {
			response := methodRoutingRequest(app, route.wrong, route.path, "")
			assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			assertAllowHeader(t, response, route.method)
		})
	}

	if got := len(store.ListRequestLogs()); got != requestLogsBefore {
		t.Fatalf("wrong methods created request logs: got %d, want %d", got, requestLogsBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditEventsBefore {
		t.Fatalf("wrong methods created audit events: got %d, want %d", got, auditEventsBefore)
	}
	if got := len(store.ListImageJobs(1000)); got != imageJobsBefore {
		t.Fatalf("wrong methods created image jobs: got %d, want %d", got, imageJobsBefore)
	}
}

func TestGeminiMethodRoutesRejectMethodMatrix(t *testing.T) {
	app := newTestServer()
	for _, route := range geminiMethodRoutes {
		for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			t.Run(method+" "+route.path, func(t *testing.T) {
				response := methodRoutingRequest(app, method, route.path, "")
				assertJSONError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
				assertAllowHeader(t, response, route.method)
			})
		}
	}
}

func TestGeminiMethodRoutesReachAuthentication(t *testing.T) {
	app := newTestServer()
	for _, route := range geminiMethodRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := methodRoutingRequest(app, route.method, route.path, "")
			assertJSONError(t, response, http.StatusUnauthorized, "invalid_api_key")
			assertAllowHeader(t, response, "")
		})
	}
}

func TestGeminiGETRoutesRejectRealHEAD(t *testing.T) {
	server := httptest.NewServer(newTestServer())
	t.Cleanup(server.Close)

	for _, route := range geminiMethodRoutes {
		for _, path := range []string{route.path, route.path + "/"} {
			t.Run(path, func(t *testing.T) {
				request, err := http.NewRequest(http.MethodHead, server.URL+path, nil)
				if err != nil {
					t.Fatal(err)
				}
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()

				wantStatus := http.StatusMethodNotAllowed
				wantAllow := route.method
				if route.path == "/v1beta/models" && strings.HasSuffix(path, "/") {
					wantStatus = http.StatusNotFound
					wantAllow = ""
				}
				assertGeminiRealHEAD(t, response, wantStatus, wantAllow)
			})
		}
	}
}

func TestGeminiMethodRoutesPreserveCORSPreflight(t *testing.T) {
	app := newTestServer()
	for _, route := range geminiMethodRoutes {
		t.Run(route.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodOptions, route.path, nil)
			request.Header.Set("origin", "https://console.example.com")
			request.Header.Set("access-control-request-method", route.method)
			response := httptest.NewRecorder()

			app.ServeHTTP(response, request)

			if response.Code != http.StatusNoContent {
				t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("access-control-allow-methods"); got != "GET,POST,PUT,PATCH,DELETE,OPTIONS" {
				t.Fatalf("access-control-allow-methods = %q", got)
			}
			assertAllowHeader(t, response, "")
		})
	}
}

func TestGeminiMethodRoutesPreservePathValidationOrder(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "empty dynamic path GET", method: http.MethodGet, path: "/v1beta/models/", wantStatus: http.StatusNotFound, wantCode: "model_not_found"},
		{name: "empty dynamic path POST", method: http.MethodPost, path: "/v1beta/models/", wantStatus: http.StatusNotFound, wantCode: "model_not_found"},
		{name: "model detail POST", method: http.MethodPost, path: geminiMethodModelPath, wantStatus: http.StatusNotFound, wantCode: "operation_not_found"},
		{name: "known action GET", method: http.MethodGet, path: geminiMethodModelPath + ":generateContent", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "unknown action GET", method: http.MethodGet, path: geminiMethodModelPath + ":unknown", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "unknown action POST", method: http.MethodPost, path: geminiMethodModelPath + ":unknown", wantStatus: http.StatusNotFound, wantCode: "operation_not_found"},
		{name: "unknown action PUT", method: http.MethodPut, path: geminiMethodModelPath + ":unknown", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "extra action segment POST", method: http.MethodPost, path: geminiMethodModelPath + ":generateContent/extra", wantStatus: http.StatusNotFound, wantCode: "operation_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "")
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestGeminiMethodRoutesPreserveEscapedPathSemantics(t *testing.T) {
	app := newTestServer()
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "encoded action GET", method: http.MethodGet, path: geminiMethodModelPath + "%3AgenerateContent", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodPost},
		{name: "encoded action POST", method: http.MethodPost, path: geminiMethodModelPath + "%3AgenerateContent", wantStatus: http.StatusUnauthorized, wantCode: "invalid_api_key"},
		{name: "encoded model separator GET", method: http.MethodGet, path: "/v1beta/models/provider%2Fmodel", wantStatus: http.StatusUnauthorized, wantCode: "invalid_api_key"},
		{name: "encoded model separator PUT", method: http.MethodPut, path: "/v1beta/models/provider%2Fmodel", wantStatus: http.StatusMethodNotAllowed, wantCode: "method_not_allowed", wantAllow: http.MethodGet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := methodRoutingRequest(app, test.method, test.path, "")
			assertJSONError(t, response, test.wantStatus, test.wantCode)
			assertAllowHeader(t, response, test.wantAllow)
		})
	}
}

func TestGeminiDynamicRoutesPreserveMultiSegmentModelNames(t *testing.T) {
	const modelName = "provider/model"
	var upstreamModels []string
	server, secret := newGeminiCodexTestServerForModel(t, modelName, func(request map[string]any) string {
		model, _ := request["model"].(string)
		upstreamModels = append(upstreamModels, model)
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_multisegment", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "multi-segment ok"}},
				}},
				"usage": map[string]any{"input_tokens": 2, "output_tokens": 2, "total_tokens": 4},
			},
		})
	})

	for _, path := range []string{
		"/v1beta/models/provider/model",
		"/v1beta/models/provider%2Fmodel",
		"/v1beta/models/models/provider/model",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			response := geminiMethodRoutingRequest(t, server.Handler(), http.MethodGet, path, nil, secret)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"models/`+modelName+`"`) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			assertAllowHeader(t, response, "")
		})
	}

	payload := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "test multi-segment model"}}}},
	}
	response := geminiMethodRoutingRequest(t, server.Handler(), http.MethodPost, "/v1beta/models/provider%2Fmodel%3AgenerateContent", payload, secret)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"text":"multi-segment ok"`) {
		t.Fatalf("generateContent: status=%d body=%s", response.Code, response.Body.String())
	}
	if len(upstreamModels) != 1 || upstreamModels[0] != modelName {
		t.Fatalf("upstream models = %#v, want [%q]", upstreamModels, modelName)
	}
	assertGeminiRouteHeadersForModel(t, response, modelName)
}

func TestGeminiMethodRoutesRejectEmptyModelActionsBeforeAuthentication(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	spy := &incrementCountingGauge{Gauge: server.metrics.inFlight}
	server.metrics.inFlight = spy
	app := server.Handler()
	requestLogsBefore := len(store.ListRequestLogs())
	auditEventsBefore := len(store.ListAuditEvents())
	imageJobsBefore := len(store.ListImageJobs(1000))

	for _, test := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/v1beta/models/:generateContent"},
		{method: http.MethodPost, path: "/v1beta/models/:generateContent"},
		{method: http.MethodPost, path: "/v1beta/models/%20:countTokens"},
	} {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			before := spy.increments.Load()
			response := methodRoutingRequest(app, test.method, test.path, "")
			assertJSONError(t, response, http.StatusBadRequest, "invalid_model")
			assertAllowHeader(t, response, "")
			if got := spy.increments.Load(); got != before {
				t.Fatalf("invalid path entered in-flight middleware: increments = %d, want %d", got, before)
			}
		})
	}

	httpServer := httptest.NewServer(app)
	t.Cleanup(httpServer.Close)
	request, err := http.NewRequest(http.MethodHead, httpServer.URL+"/v1beta/models/:generateContent", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertGeminiRealHEAD(t, response, http.StatusBadRequest, "")

	if got := len(store.ListRequestLogs()); got != requestLogsBefore {
		t.Fatalf("invalid model actions created request logs: got %d, want %d", got, requestLogsBefore)
	}
	if got := len(store.ListAuditEvents()); got != auditEventsBefore {
		t.Fatalf("invalid model actions created audit events: got %d, want %d", got, auditEventsBefore)
	}
	if got := len(store.ListImageJobs(1000)); got != imageJobsBefore {
		t.Fatalf("invalid model actions created image jobs: got %d, want %d", got, imageJobsBefore)
	}
}

func TestGeminiDynamicRoutesPreserveTrailingSlashSuccess(t *testing.T) {
	server, secret := newGeminiCodexTestServer(t, func(map[string]any) string {
		return geminiCodexTestSSE(map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id": "resp_gemini_method_route", "status": "completed",
				"output": []any{map[string]any{
					"type": "message", "role": "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": "route ok"}},
				}},
				"usage": map[string]any{"input_tokens": 2, "output_tokens": 2, "total_tokens": 4},
			},
		})
	})
	payload := map[string]any{
		"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": "test route"}}}},
	}
	tests := []struct {
		name             string
		method           string
		path             string
		payload          any
		wantString       string
		wantContentType  string
		wantCacheControl string
		wantRouteHeaders bool
	}{
		{name: "model detail", method: http.MethodGet, path: geminiMethodModelPath + "/", wantString: `"name":"models/` + geminiCodexTestModel + `"`, wantContentType: "application/json"},
		{name: "generate content", method: http.MethodPost, path: geminiMethodModelPath + ":generateContent/", payload: payload, wantString: `"text":"route ok"`, wantContentType: "application/json", wantRouteHeaders: true},
		{name: "stream content", method: http.MethodPost, path: geminiMethodModelPath + ":streamGenerateContent/?alt=sse", payload: payload, wantString: `data: {`, wantContentType: "text/event-stream", wantCacheControl: "no-cache", wantRouteHeaders: true},
		{name: "count tokens", method: http.MethodPost, path: geminiMethodModelPath + ":countTokens/", payload: payload, wantString: `"totalTokens":`, wantContentType: "application/json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := geminiMethodRoutingRequest(t, server.Handler(), test.method, test.path, test.payload, secret)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), test.wantString) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if got := response.Header().Get("content-type"); !strings.HasPrefix(got, test.wantContentType) {
				t.Fatalf("content-type = %q, want prefix %q", got, test.wantContentType)
			}
			if got := response.Header().Get("cache-control"); got != test.wantCacheControl {
				t.Fatalf("cache-control = %q, want %q", got, test.wantCacheControl)
			}
			if test.wantContentType == "text/event-stream" && !strings.HasPrefix(response.Body.String(), "data: ") {
				t.Fatalf("stream body does not use SSE framing: %q", response.Body.String())
			}
			if test.wantRouteHeaders {
				assertGeminiRouteHeaders(t, response)
			}
		})
	}
}

func TestGeminiMethodRoutesPreserveMetricsScope(t *testing.T) {
	store := NewMemoryStore()
	if err := SeedDemoData(store); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{AdminToken: "dev_admin_token", MetricsEnabled: true})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	spy := &incrementCountingGauge{Gauge: server.metrics.inFlight}
	server.metrics.inFlight = spy
	app := server.Handler()

	tests := []struct {
		name          string
		method        string
		path          string
		wantIncrement int64
	}{
		{name: "generate wrong method", method: http.MethodGet, path: geminiMethodModelPath + ":generateContent"},
		{name: "stream wrong method", method: http.MethodGet, path: geminiMethodModelPath + ":streamGenerateContent"},
		{name: "generate POST", method: http.MethodPost, path: geminiMethodModelPath + ":generateContent", wantIncrement: 1},
		{name: "stream POST", method: http.MethodPost, path: geminiMethodModelPath + ":streamGenerateContent", wantIncrement: 1},
		{name: "count POST", method: http.MethodPost, path: geminiMethodModelPath + ":countTokens"},
		{name: "unknown action POST", method: http.MethodPost, path: geminiMethodModelPath + ":unknown"},
		{name: "model detail GET", method: http.MethodGet, path: geminiMethodModelPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := spy.increments.Load()
			_ = methodRoutingRequest(app, test.method, test.path, "")
			if got := spy.increments.Load() - before; got != test.wantIncrement {
				t.Fatalf("in-flight increments = %d, want %d", got, test.wantIncrement)
			}
		})
	}
}

func geminiMethodRoutingRequest(t *testing.T, handler http.Handler, method string, path string, payload any, key string) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, body)
	request.Header.Set("x-goog-api-key", key)
	request.Header.Set("user-agent", "GeminiCLI/0.1.9")
	if payload != nil {
		request.Header.Set("content-type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertGeminiRouteHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	assertGeminiRouteHeadersForModel(t, response, geminiCodexTestModel)
}

func assertGeminiRouteHeadersForModel(t *testing.T, response *httptest.ResponseRecorder, modelName string) {
	t.Helper()
	if response.Header().Get("x-request-id") == "" {
		t.Fatal("x-request-id is empty")
	}
	want := map[string]string{
		"x-tokenhub-provider":             "prv_gemini_codex",
		"x-tokenhub-provider-resource-id": "rsrc_gemini_codex",
		"x-tokenhub-model":                modelName,
		"x-tokenhub-route-id":             "route_gemini_codex",
		"x-tokenhub-route-attempts":       "1",
	}
	for name, value := range want {
		if got := response.Header().Get(name); got != value {
			t.Fatalf("%s = %q, want %q", name, got, value)
		}
	}
	if response.Header().Get("x-tokenhub-project-id") == "" {
		t.Fatal("x-tokenhub-project-id is empty")
	}
}

func assertGeminiRealHEAD(t *testing.T, response *http.Response, wantStatus int, wantAllow string) {
	t.Helper()
	if response.StatusCode != wantStatus {
		t.Fatalf("HEAD status = %d, want %d", response.StatusCode, wantStatus)
	}
	allowValues, present := response.Header[http.CanonicalHeaderKey("Allow")]
	if wantAllow == "" {
		if present {
			t.Fatalf("HEAD Allow is present with value %q, want absent", allowValues)
		}
	} else if got := response.Header.Get("Allow"); got != wantAllow {
		t.Fatalf("HEAD Allow = %q, want %q", got, wantAllow)
	}
	if contentType := response.Header.Get("content-type"); !strings.HasPrefix(contentType, "application/json") {
		t.Fatalf("HEAD content type = %q, want application/json", contentType)
	}
	if response.Header.Get("x-request-id") == "" {
		t.Fatal("HEAD x-request-id is empty")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD response body = %q, want empty", body)
	}
}
