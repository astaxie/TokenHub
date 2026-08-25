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
		u.mu.Unlock()
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "/v1/realtime/calls/call_voice_test")
		w.Header().Set("X-Future-Voice-Response", "preserved")
		w.Header().Add("Set-Cookie", "upstream=secret")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0\r\na=answer:test\r\n"))
	case r.URL.Path == "/v1/live/call_voice_test" || r.URL.Path == "/v1/realtime":
		websocket.Server{
			Handshake: func(_ *websocket.Config, request *http.Request) error {
				u.mu.Lock()
				u.sidebandHeaders = request.Header.Clone()
				u.sidebandPath = request.URL.Path
				u.sidebandRawQuery = request.URL.RawQuery
				u.mu.Unlock()
				return nil
			},
			Handler: func(connection *websocket.Conn) {
				defer connection.Close()
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
	project := store.CreateProject(Project{Name: "Codex Voice test project", Status: StatusActive})
	secret := "thk_codex_voice_test_secret"
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Codex Voice test key", Status: StatusActive}, secret); err != nil {
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
		Priority:     0,
		Weight:       100,
		Credentials:  &ProviderResourceCredentials{AuthType: "oauth", AccountID: "incomplete-account"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddProviderResource(ProviderResource{
		ID:           "rsrc_codex_voice",
		ProviderID:   provider.ID,
		Name:         "Codex Voice account",
		ResourceType: ProviderResourceOpenAISubscription,
		BaseURL:      upstream.URL + "/backend-api/codex",
		Status:       StatusActive,
		Healthy:      true,
		Priority:     1,
		Weight:       100,
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
	server, _, secret := newCodexVoiceTestServer(t, upstream)
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
			defer connection.Close()
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
	project := store.CreateProject(Project{Name: "No Voice resource", Status: StatusActive})
	secret := "thk_codex_voice_no_resource"
	if _, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "No Voice resource", Status: StatusActive}, secret); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/live", strings.NewReader(`{}`))
	request.Header.Set("Authorization", "Bearer "+secret)
	response := httptest.NewRecorder()
	New(store).Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "codex_voice_resource_unavailable") {
		t.Fatalf("missing resource response = %d %s", response.Code, response.Body.String())
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
}

func TestCodexVoiceAdapterCapabilityIsDeclared(t *testing.T) {
	server := New(NewMemoryStore())
	descriptor, ok := server.adapterRegistry.Describe(ProviderOpenAICodex)
	if !ok || !adapterSupports(descriptor, AdapterCapabilityCodexVoice) {
		encoded, _ := json.Marshal(descriptor)
		t.Fatalf("Codex Voice capability is not declared: %s", encoded)
	}
}
