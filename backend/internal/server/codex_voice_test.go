package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/websocket"
)

type codexVoiceTestUpstream struct {
	mu               sync.Mutex
	callHeaders      http.Header
	callQuery        url.Values
	callBody         string
	callCount        int
	sidebandHeaders  http.Header
	sidebandPath     string
	sidebandRawQuery string
}

func (u *codexVoiceTestUpstream) handler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/backend-api/codex/realtime/calls":
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.callHeaders = r.Header.Clone()
		u.callQuery = r.URL.Query()
		u.callBody = string(body)
		u.callCount++
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "/v1/realtime/calls/call_voice_test")
		w.Header().Set("X-Future-Voice-Response", "preserved")
		w.Header().Add("Set-Cookie", "upstream=secret")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0\r\na=answer:test\r\n"))
	case r.URL.Path == "/v1/live/call_voice_test" || r.URL.Path == "/v1/realtime":
		websocket.Server{
			Handshake: func(config *websocket.Config, request *http.Request) error {
				u.mu.Lock()
				u.sidebandHeaders = request.Header.Clone()
				u.sidebandPath = request.URL.Path
				u.sidebandRawQuery = request.URL.RawQuery
				u.mu.Unlock()
				config.Header = make(http.Header)
				config.Header.Set("Authorization", "Bearer upstream-must-not-leak")
				config.Header.Set("ChatGPT-Account-ID", "upstream-account-must-not-leak")
				config.Header.Set("OpenAI-Organization", "upstream-org-must-not-leak")
				config.Header.Set("X-TokenHub-Internal", "internal-must-not-leak")
				config.Header.Set("X-Future-Voice-Response", "preserved")
				config.Header.Set("X-Connection-Secret", "connection-must-not-leak")
				config.Header.Add("Set-Cookie", "sideband=must-not-leak")
				return nil
			},
			Handler: func(connection *websocket.Conn) {
				defer func() { _ = connection.Close() }()
				var message []byte
				if err := websocket.Message.Receive(connection, &message); err == nil {
					_ = websocket.Message.Send(connection, append([]byte("echo:"), message...))
				}
			},
		}.ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func newCodexVoiceTestServer(t *testing.T, upstream *httptest.Server) (*Server, *GormStore, string) {
	t.Helper()
	t.Setenv("TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK", "true")
	store := NewMemoryStoreWithConfig(Config{SecretKey: "codex-voice-store-secret"})
	store.AddModel(Model{ID: codexVoiceModelName, Name: codexVoiceModelName, Status: StatusActive})
	project := store.CreateProject(Project{Name: "Codex Voice test project", AllowedCapabilities: []string{AccessCapabilityCodexVoice}, Status: StatusActive})
	secret := "thk_codex_voice_test_secret"
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Codex Voice test key", AllowedCapabilities: []string{AccessCapabilityCodexVoice}, Status: StatusActive}, secret); err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{
		ID:       "prv_codex_voice",
		Name:     "Codex Voice",
		Type:     ProviderOpenAICodex,
		Status:   StatusActive,
		Healthy:  true,
		Priority: 1,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_voice_incomplete",
		ProviderID:   provider.ID,
		Name:         "Incomplete Codex Voice account",
		ResourceType: ProviderResourceOpenAISubscription,
		BaseURL:      upstream.URL + "/backend-api/codex",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
		Options: map[string]string{
			codexResourceSupportedModelsOption: `["gpt-5.4"]`,
		},
		Credentials: &ProviderResourceCredentials{AuthType: "oauth", AccountID: "incomplete-account"},
	}); err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{
		ID:            "route_codex_voice",
		ModelName:     codexVoiceModelName,
		ProviderID:    provider.ID,
		ProviderModel: codexVoiceModelName,
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_voice",
		ProviderID:   provider.ID,
		Name:         "Codex Voice account",
		ResourceType: ProviderResourceOpenAISubscription,
		BaseURL:      upstream.URL + "/backend-api/codex",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     2,
		Weight:       100,
		Options: map[string]string{
			codexResourceSupportedModelsOption: `["gpt-5.4"]`,
		},
		Credentials: &ProviderResourceCredentials{
			AuthType:    "oauth",
			AccessToken: "upstream-access-token",
			AccountID:   "upstream-account-id",
		},
	}); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{
		AdminToken: "dev_admin_token",
		SecretKey:  "codex-voice-server-secret",
	})
	server.codexSubscription.Client = upstream.Client()
	server.codexVoiceSidebandUpstreamBaseURL = upstream.URL + "/v1"
	return server, store, secret
}

func TestCodexVoiceCallCreateForwardsFutureHeadersAndBindsResource(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)

	body := `{"sdp":"v=0\\r\\n","session":{"model":"gpt-live-test"}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/live?client_flag=future", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("ChatGPT-Account-ID", "client-must-not-win")
	request.Header.Set("X-Api-Key", "client-must-not-leak")
	request.Header.Set("Cookie", "session=client-secret")
	request.Header.Set("X-Forwarded-For", "127.0.0.1")
	request.Header.Set("X-Future-Voice-Feature", "future-value")
	request.Header.Set("OpenAI-Alpha", "realtime=v3")
	request.Header.Set("X-Oai-Attestation", "opaque-attestation")
	request.Header.Set("Originator", "codex_desktop_rs")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated || response.Body.String() != "v=0\r\na=answer:test\r\n" {
		t.Fatalf("voice create = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Location") != "/v1/realtime/calls/call_voice_test" || response.Header().Get("X-Future-Voice-Response") != "preserved" {
		t.Fatalf("voice response headers = %#v", response.Header())
	}
	if response.Header().Values("Set-Cookie") != nil {
		t.Fatalf("upstream cookie leaked to client: %#v", response.Header().Values("Set-Cookie"))
	}
	state.mu.Lock()
	headers := state.callHeaders.Clone()
	query := state.callQuery
	forwardedBody := state.callBody
	state.mu.Unlock()
	if headers.Get("Authorization") != "Bearer upstream-access-token" || headers.Get("ChatGPT-Account-ID") != "upstream-account-id" {
		t.Fatalf("managed credentials were not enforced: %#v", headers)
	}
	for _, stripped := range []string{"X-Api-Key", "Cookie", "X-Forwarded-For"} {
		if headers.Get(stripped) != "" {
			t.Fatalf("stripped header %s leaked upstream", stripped)
		}
	}
	if headers.Get("X-Future-Voice-Feature") != "future-value" || headers.Get("OpenAI-Alpha") != "realtime=v3" || headers.Get("X-Oai-Attestation") != "opaque-attestation" {
		t.Fatalf("future Voice headers were not preserved: %#v", headers)
	}
	if headers.Get("Originator") != "codex_desktop_rs" || forwardedBody != body {
		t.Fatalf("client protocol identity/body changed: originator=%q body=%q", headers.Get("Originator"), forwardedBody)
	}
	if query.Get("intent") != "quicksilver" || query.Get("architecture") != "avas" || query.Get("client_flag") != "future" {
		t.Fatalf("voice query = %#v", query)
	}

	_, apiKey, err := store.ValidateAPIKey(secret, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	base := deriveSessionAffinityKey(server.config.SecretKey, apiKey.ID, "codex_voice\x00call_voice_test")
	binding, ok, err := store.GetAdapterSessionBinding(context.Background(), ProviderOpenAICodex, "prv_codex_voice", providerScopedAffinityKeyHash(base, "prv_codex_voice"))
	if err != nil || !ok || binding.ResourceID != "rsrc_codex_voice" || binding.AffinityKind != codexVoiceAffinityKind {
		t.Fatalf("voice binding = %#v ok=%v err=%v", binding, ok, err)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != http.StatusCreated || logs[0].ModelName != codexVoiceModelName || logs[0].ProviderResourceID != "rsrc_codex_voice" {
		t.Fatalf("successful Voice audit = %#v", logs)
	}
	if records := store.ListUsageRecords(); len(records) != 0 {
		t.Fatalf("successful Voice call fabricated token usage: %#v", records)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Where("request_id = ?", logs[0].RequestID).Order("attempt_index asc").Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].ProviderResourceID != "rsrc_codex_voice_incomplete" || attempts[1].ProviderResourceID != "rsrc_codex_voice" {
		t.Fatalf("Voice resource failover attempts = %#v", attempts)
	}
	var payload RequestPayloadLog
	if err := store.db.First(&payload, "request_id = ?", logs[0].RequestID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload.RequestBody, `"sdp"`) || !strings.Contains(payload.RequestBody, `"body_bytes"`) {
		t.Fatalf("Voice audit payload leaked SDP or missed bounded metadata: %s", payload.RequestBody)
	}
}

func TestCodexVoiceV1CallCreateRouteUsesCodexHandler(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, _, secret := newCodexVoiceTestServer(t, upstream)

	request := httptest.NewRequest(http.MethodPost, "/v1/realtime/calls?client_flag=v1", strings.NewReader(`{"sdp":"v=0","session":{}}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("V1 voice create = %d %s", response.Code, response.Body.String())
	}
	state.mu.Lock()
	query := state.callQuery
	state.mu.Unlock()
	if query.Get("intent") != "quicksilver" || query.Get("architecture") != "avas" || query.Get("client_flag") != "v1" {
		t.Fatalf("V1 voice query = %#v", query)
	}
}

func TestCodexVoiceSidebandRelaysV3AndV1OnBoundResource(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)
	gateway := httptest.NewServer(server.Handler())
	defer gateway.Close()

	createRequest, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	createRequest.Header.Set("Authorization", "Bearer "+secret)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse, err := http.DefaultClient.Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", createResponse.StatusCode)
	}

	for _, test := range []struct {
		name       string
		path       string
		upstream   string
		queryValue string
		intent     string
		binary     bool
	}{
		{name: "v3 frameless", path: "/v1/live/call_voice_test", upstream: "/v1/live/call_voice_test"},
		{name: "v1 realtime", path: "/v1/realtime?call_id=call_voice_test", upstream: "/v1/realtime", queryValue: "call_voice_test", intent: "quicksilver", binary: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := websocket.NewConfig("ws"+strings.TrimPrefix(gateway.URL, "http")+test.path, "http://tokenhub.local")
			if err != nil {
				t.Fatal(err)
			}
			config.Header.Set("Authorization", "Bearer "+secret)
			config.Header.Set("ChatGPT-Account-ID", "client-account")
			config.Header.Set("X-Future-Sideband", "future-sideband")
			connection, err := websocket.DialConfig(config)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = connection.Close() }()
			if test.binary {
				if err := websocket.Message.Send(connection, []byte{0x00, 0x01, 0x02}); err != nil {
					t.Fatal(err)
				}
				var reply []byte
				if err := websocket.Message.Receive(connection, &reply); err != nil {
					t.Fatal(err)
				}
				if string(reply) != "echo:\x00\x01\x02" {
					t.Fatalf("binary reply = %q", reply)
				}
			} else {
				if err := websocket.Message.Send(connection, "ping"); err != nil {
					t.Fatal(err)
				}
				var reply string
				if err := websocket.Message.Receive(connection, &reply); err != nil {
					t.Fatal(err)
				}
				if reply != "echo:ping" {
					t.Fatalf("reply = %q", reply)
				}
			}
			state.mu.Lock()
			headers := state.sidebandHeaders.Clone()
			path := state.sidebandPath
			rawQuery := state.sidebandRawQuery
			state.mu.Unlock()
			if path != test.upstream || headers.Get("Authorization") != "Bearer upstream-access-token" || headers.Get("ChatGPT-Account-ID") != "upstream-account-id" {
				t.Fatalf("sideband request path=%q headers=%#v", path, headers)
			}
			if headers.Get("X-Future-Sideband") != "future-sideband" {
				t.Fatalf("future sideband header missing: %#v", headers)
			}
			if test.queryValue != "" {
				parsed, _ := url.ParseQuery(rawQuery)
				if parsed.Get("call_id") != test.queryValue {
					t.Fatalf("sideband query = %q", rawQuery)
				}
				if parsed.Get("intent") != test.intent {
					t.Fatalf("sideband intent = %q", parsed.Get("intent"))
				}
			}
		})
	}
	if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].StatusCode != http.StatusCreated {
		t.Fatalf("sideband connections must not create additional billable calls: %#v", logs)
	}
}

func TestCodexVoiceSidebandStripsSensitiveUpgradeResponseHeaders(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, _, secret := newCodexVoiceTestServer(t, upstream)
	baseTransport := server.codexSubscription.Client.Transport
	if baseTransport == nil {
		baseTransport = http.DefaultTransport
	}
	server.codexSubscription.Client.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		response, err := baseTransport.RoundTrip(request)
		if response != nil && response.StatusCode == http.StatusSwitchingProtocols {
			response.Header.Set("Connection", "Upgrade, X-Connection-Secret")
			response.Header.Set("X-Connection-Secret", "connection-must-not-leak")
			response.Header.Set("Keep-Alive", "timeout=5")
			response.Header.Set("TE", "trailers")
			response.Header.Set("Trailer", "X-Upstream-Trailer")
			response.Header.Set("Transfer-Encoding", "chunked")
		}
		return response, err
	})
	gateway := httptest.NewServer(server.Handler())
	defer gateway.Close()

	createRequest, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	createRequest.Header.Set("Authorization", "Bearer "+secret)
	createResponse, err := http.DefaultClient.Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d", createResponse.StatusCode)
	}

	request, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/live/call_voice_test", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Origin", "http://tokenhub.local")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	request.Header.Set("Sec-WebSocket-Version", "13")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(response.Header.Get("Upgrade"), "websocket") {
		t.Fatalf("sideband upgrade = %d headers=%#v", response.StatusCode, response.Header)
	}
	if response.Header.Get("X-Future-Voice-Response") != "preserved" {
		t.Fatalf("safe future response header missing: %#v", response.Header)
	}
	if !strings.EqualFold(response.Header.Get("Connection"), "Upgrade") {
		t.Fatalf("sideband Connection header was not normalized: %#v", response.Header)
	}
	for _, sensitive := range []string{"Authorization", "ChatGPT-Account-ID", "OpenAI-Organization", "Set-Cookie", "X-TokenHub-Internal", "X-Connection-Secret", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding"} {
		if response.Header.Get(sensitive) != "" {
			t.Fatalf("sensitive sideband response header %s leaked: %#v", sensitive, response.Header)
		}
	}
}

func TestCodexVoiceResponseFilteringTransportStripsUpgradeHeadersFromErrors(t *testing.T) {
	transport := codexVoiceResponseFilteringTransport{next: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header: http.Header{
				"Connection":          []string{"Upgrade, X-Connection-Secret"},
				"Upgrade":             []string{"websocket"},
				"X-Connection-Secret": []string{"must-not-leak"},
				"X-Future-Response":   []string{"preserved"},
			},
			Body:    io.NopCloser(strings.NewReader(`{"error":"forbidden"}`)),
			Request: request,
		}, nil
	})}
	response, err := transport.RoundTrip(httptest.NewRequest(http.MethodGet, "http://upstream.example/v1/live/call", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	for _, stripped := range []string{"Connection", "Upgrade", "X-Connection-Secret"} {
		if response.Header.Get(stripped) != "" {
			t.Fatalf("error response header %s leaked: %#v", stripped, response.Header)
		}
	}
	if response.Header.Get("X-Future-Response") != "preserved" {
		t.Fatalf("safe error response header missing: %#v", response.Header)
	}
}

func TestCodexVoiceRejectsUnsafeOrUnboundRequests(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, _, secret := newCodexVoiceTestServer(t, upstream)

	tests := []struct {
		name   string
		method string
		path   string
		token  string
		body   string
		code   int
	}{
		{name: "authentication required", method: http.MethodPost, path: "/v1/live", body: `{}`, code: http.StatusUnauthorized},
		{name: "body required", method: http.MethodPost, path: "/v1/live", token: secret, code: http.StatusBadRequest},
		{name: "websocket required", method: http.MethodGet, path: "/v1/realtime?call_id=missing", token: secret, code: http.StatusUpgradeRequired},
		{name: "call id required", method: http.MethodGet, path: "/v1/realtime", token: secret, code: http.StatusBadRequest},
		{name: "V1 call body required", method: http.MethodPost, path: "/v1/realtime/calls", token: secret, code: http.StatusBadRequest},
		{name: "V1 call create requires POST", method: http.MethodGet, path: "/v1/realtime/calls", token: secret, code: http.StatusMethodNotAllowed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != test.code {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	for index := 0; index <= codexVoiceHeaderMaxCount; index++ {
		request.Header.Set("X-Future-"+strings.Repeat("A", index%4)+string(rune('a'+index%26))+strings.Repeat("B", index/26), "value")
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized headers status = %d body=%s", response.Code, response.Body.String())
	}

	unbound := httptest.NewRequest(http.MethodGet, "/v1/live/call_missing", nil)
	unbound.Header.Set("Authorization", "Bearer "+secret)
	unbound.Header.Set("Connection", "Upgrade")
	unbound.Header.Set("Upgrade", "websocket")
	unboundResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unboundResponse, unbound)
	if unboundResponse.Code != http.StatusNotFound {
		t.Fatalf("unbound call status = %d body=%s", unboundResponse.Code, unboundResponse.Body.String())
	}

	oversized := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{}`))
	oversized.Header.Set("Authorization", "Bearer "+secret)
	oversized.Header.Set("X-Future-Large", strings.Repeat("x", codexVoiceHeaderValueMaxBytes+1))
	oversizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(oversizedResponse, oversized)
	if oversizedResponse.Code != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("oversized value status = %d body=%s", oversizedResponse.Code, oversizedResponse.Body.String())
	}
}

func TestCodexVoiceRequiresAnActiveSubscriptionResource(t *testing.T) {
	store := NewMemoryStore()
	store.AddModel(Model{ID: codexVoiceModelName, Name: codexVoiceModelName, Status: StatusActive})
	project := store.CreateProject(Project{Name: "No Voice resource", AllowedCapabilities: []string{AccessCapabilityCodexVoice}, Status: StatusActive})
	secret := "thk_codex_voice_no_resource"
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "No Voice resource", AllowedCapabilities: []string{AccessCapabilityCodexVoice}, Status: StatusActive}, secret); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "provider_unavailable") {
		t.Fatalf("missing resource response = %d %s", response.Code, response.Body.String())
	}
}

func TestCodexVoiceRequiresExplicitProjectAndKeyCapability(t *testing.T) {
	for _, test := range []struct {
		name          string
		revokeProject bool
	}{
		{name: "project capability missing", revokeProject: true},
		{name: "key capability missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &codexVoiceTestUpstream{}
			upstream := httptest.NewServer(http.HandlerFunc(state.handler))
			defer upstream.Close()
			server, store, secret := newCodexVoiceTestServer(t, upstream)
			project, key, err := store.ValidateAPIKey(secret, "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if test.revokeProject {
				if _, err := store.UpdateProject(project.ID, Project{AllowedCapabilities: []string{}}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := store.UpdateAPIKey(key.ID, APIKey{AllowedCapabilities: []string{}}); err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
			request.Header.Set("Authorization", "Bearer "+secret)
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"model_not_allowed"`) {
				t.Fatalf("capability denial = %d %s", response.Code, response.Body.String())
			}
			state.mu.Lock()
			callCount := state.callCount
			state.mu.Unlock()
			if callCount != 0 {
				t.Fatalf("denied Voice request reached upstream %d times", callCount)
			}
			logs := store.ListRequestLogs()
			if len(logs) != 1 || logs[0].ModelName != codexVoiceModelName || logs[0].ErrorCode != "model_not_allowed" {
				t.Fatalf("denied Voice audit = %#v", logs)
			}
			if records := store.ListUsageRecords(); len(records) != 0 {
				t.Fatalf("denied Voice request created usage: %#v", records)
			}
		})
	}
}

func TestCodexVoiceRequiresInternalModelPermission(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)
	_, key, err := store.ValidateAPIKey(secret, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{ID: "gpt-5.4", Name: "gpt-5.4", Status: StatusActive})
	if _, err := store.UpdateAPIKey(key.ID, APIKey{
		ModelAccessMode: ModelAccessModeRestricted,
		Allowed:         []string{"gpt-5.4"},
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), `"code":"model_not_allowed"`) {
		t.Fatalf("Voice model denial = %d %s", response.Code, response.Body.String())
	}
	state.mu.Lock()
	callCount := state.callCount
	state.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("model-denied Voice request reached upstream %d times", callCount)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].ModelName != codexVoiceModelName || logs[0].ErrorCode != "model_not_allowed" {
		t.Fatalf("model-denied Voice audit = %#v", logs)
	}
}

func TestCodexVoiceModelDiscoveryRequiresExplicitCapability(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)
	project, key, err := store.ValidateAPIKey(secret, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"codex-voice"`) {
		t.Fatalf("authorized Voice model discovery = %d %s", response.Code, response.Body.String())
	}

	if _, err := store.UpdateProject(project.ID, Project{AllowedCapabilities: []string{}}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"id":"codex-voice"`) {
		t.Fatalf("unauthorized Voice model discovery = %d %s", response.Code, response.Body.String())
	}

	if _, err := store.UpdateProject(project.ID, Project{AllowedCapabilities: []string{AccessCapabilityCodexVoice}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAPIKey(key.ID, APIKey{AllowedCapabilities: []string{}}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), `"id":"codex-voice"`) {
		t.Fatalf("key-denied Voice model discovery = %d %s", response.Code, response.Body.String())
	}
}

func TestCodexVoiceCallCreateUsesRequestQuotaAndZeroTokenAccounting(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)
	_, key, err := store.ValidateAPIKey(secret, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAPIKey(key.ID, APIKey{Limits: QuotaLimits{DailyRequests: 1}, LimitsSet: true}); err != nil {
		t.Fatal(err)
	}

	for index, expectedStatus := range []int{http.StatusCreated, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
		request.Header.Set("Authorization", "Bearer "+secret)
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		if response.Code != expectedStatus {
			t.Fatalf("Voice request %d = %d %s", index+1, response.Code, response.Body.String())
		}
	}
	state.mu.Lock()
	callCount := state.callCount
	state.mu.Unlock()
	if callCount != 1 {
		t.Fatalf("quota-rejected request reached upstream; call count = %d", callCount)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 2 {
		t.Fatalf("Voice quota audits = %#v", logs)
	}
	var created, rejected bool
	for _, log := range logs {
		if log.ModelName != codexVoiceModelName {
			t.Fatalf("Voice quota audit used model %q", log.ModelName)
		}
		created = created || log.StatusCode == http.StatusCreated
		rejected = rejected || log.ErrorCode == "quota_exceeded"
	}
	if !created || !rejected {
		t.Fatalf("Voice quota audits = %#v", logs)
	}
	if records := store.ListUsageRecords(); len(records) != 0 {
		t.Fatalf("Voice call create must not fabricate token usage: %#v", records)
	}
}

func TestCodexVoiceHonorsProjectScopedRoutesBeforeUpstream(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)
	if _, err := store.UpdateRoute("route_codex_voice", ModelRoute{
		ProjectScope: RouteProjectScopeInclude,
		ProjectIDs:   []string{"prj_other"},
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"codex_voice_resource_unavailable"`) {
		t.Fatalf("project-scoped Voice route = %d %s", response.Code, response.Body.String())
	}
	state.mu.Lock()
	callCount := state.callCount
	state.mu.Unlock()
	if callCount != 0 {
		t.Fatalf("out-of-scope Voice route reached upstream %d times", callCount)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].ModelName != codexVoiceModelName || logs[0].ErrorCode != "codex_voice_resource_unavailable" {
		t.Fatalf("project-scoped Voice audit = %#v", logs)
	}
}

func TestCodexVoiceSidebandRechecksCapabilityWithoutChargingAgain(t *testing.T) {
	state := &codexVoiceTestUpstream{}
	upstream := httptest.NewServer(http.HandlerFunc(state.handler))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)
	create := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
	create.Header.Set("Authorization", "Bearer "+secret)
	createResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("Voice create = %d %s", createResponse.Code, createResponse.Body.String())
	}
	_, key, err := store.ValidateAPIKey(secret, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateAPIKey(key.ID, APIKey{AllowedCapabilities: []string{}}); err != nil {
		t.Fatal(err)
	}
	sideband := httptest.NewRequest(http.MethodGet, "/v1/live/call_voice_test", nil)
	sideband.Header.Set("Authorization", "Bearer "+secret)
	sideband.Header.Set("Connection", "Upgrade")
	sideband.Header.Set("Upgrade", "websocket")
	sidebandResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(sidebandResponse, sideband)
	if sidebandResponse.Code != http.StatusForbidden || !strings.Contains(sidebandResponse.Body.String(), `"code":"model_not_allowed"`) {
		t.Fatalf("revoked Voice sideband = %d %s", sidebandResponse.Code, sidebandResponse.Body.String())
	}
	if logs := store.ListRequestLogs(); len(logs) != 1 || logs[0].StatusCode != http.StatusCreated {
		t.Fatalf("sideband authorization created another billable call: %#v", logs)
	}
}

func TestCodexVoiceUpstreamErrorDoesNotCreateBinding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"voice_session_access_denied","message":"Voice session access denied"}}`))
	}))
	defer upstream.Close()
	server, store, secret := newCodexVoiceTestServer(t, upstream)
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{"sdp":"v=0","session":{}}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "Voice session access denied") {
		t.Fatalf("upstream Voice error = %d %s", response.Code, response.Body.String())
	}
	var bindings int64
	if err := store.db.Model(&AdapterSessionBinding{}).Where("affinity_kind = ?", codexVoiceAffinityKind).Count(&bindings).Error; err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("failed Voice call created %d bindings", bindings)
	}
	logs := store.ListRequestLogs()
	if len(logs) != 1 || logs[0].StatusCode != http.StatusForbidden || logs[0].ModelName != codexVoiceModelName || logs[0].ErrorCode == "" {
		t.Fatalf("failed Voice audit = %#v", logs)
	}
	if records := store.ListUsageRecords(); len(records) != 0 {
		t.Fatalf("failed Voice call fabricated token usage: %#v", records)
	}
}

func TestCodexVoiceAdapterCapabilityIsDeclared(t *testing.T) {
	server := New(NewMemoryStore())
	descriptor, ok := server.adapterRegistry.Describe(ProviderOpenAICodex)
	if !ok || !adapterSupports(descriptor, AdapterCapabilityCodexVoice) {
		encoded, _ := json.Marshal(descriptor)
		t.Fatalf("Codex Voice capability is not declared: %s", encoded)
	}
}

func TestCodexVoiceRouteValidationRequiresCodexProviderAndInternalModel(t *testing.T) {
	store := NewMemoryStore()
	codex := store.AddProvider(Provider{ID: "prv_voice_route_codex", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	mock := store.AddProvider(Provider{ID: "prv_voice_route_mock", Type: ProviderMock, Status: StatusActive, Healthy: true})
	server := New(store)
	valid := ModelRoute{ModelName: codexVoiceModelName, ProviderID: codex.ID, ProviderModel: codexVoiceModelName}
	if err := server.validateImportedProviderModel(valid); err != nil {
		t.Fatalf("valid Codex Voice route rejected: %v", err)
	}
	invalidProvider := valid
	invalidProvider.ProviderID = mock.ID
	if code := AsHTTPError(server.validateImportedProviderModel(invalidProvider)).Code; code != "codex_voice_provider_required" {
		t.Fatalf("invalid Voice Provider error = %q", code)
	}
	invalidModel := valid
	invalidModel.ProviderModel = "gpt-realtime"
	if code := AsHTTPError(server.validateImportedProviderModel(invalidModel)).Code; code != "codex_voice_upstream_model_invalid" {
		t.Fatalf("invalid Voice internal model error = %q", code)
	}
}
