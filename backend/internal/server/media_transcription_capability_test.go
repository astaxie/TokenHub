package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionRequiresExplicitAdapterCapability(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capability AdapterCapability
		path       string
		status     int
	}{
		{"generic media does not imply transcription", AdapterCapabilityMedia, "/v1/audio/transcriptions", http.StatusNotImplemented},
		{"generic media does not imply translation", AdapterCapabilityMedia, "/v1/audio/translations", http.StatusNotImplemented},
		{"transcription capability", AdapterCapabilityTranscription, "/v1/audio/transcriptions", http.StatusOK},
		{"translation capability", AdapterCapabilityTranscription, "/v1/audio/translations", http.StatusOK},
		{"generic media remains compatible", AdapterCapabilityMedia, "/v1/audio/speech", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server, _, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"text":"fixture"}`)
			}, "audio")
			adapter, err := server.adapterRegistry.Resolve(ProviderOpenAICompatible)
			if err != nil {
				t.Fatal(err)
			}
			server.adapterRegistry.Register(ProviderOpenAICompatible, adapter, tc.capability)
			response := doJSON(t, server.Handler(), http.MethodPost, tc.path, map[string]any{"model": "public-media"}, key)
			if response.Code != tc.status || calls != map[bool]int{true: 1, false: 0}[tc.status == http.StatusOK] {
				t.Fatalf("capability filter failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestTranscriptionRoutePublicationRequiresMatchingCapability(t *testing.T) {
	server, _, _ := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) {}, "audio")
	model := &Model{Name: "transcription-model", Modality: "audio", Metadata: map[string]string{"endpoints": "audio/transcriptions"}}
	media := AdapterDescriptor{Type: "media-only", Capabilities: []AdapterCapability{AdapterCapabilityMedia}}
	if err := server.validateRouteModelProtocol(model.Name, model, media); err == nil || AsHTTPError(err).Code != "route_protocol_mismatch" {
		t.Fatalf("generic media capability admitted transcription publication: %v", err)
	}
	transcription := AdapterDescriptor{Type: "transcription-only", Capabilities: []AdapterCapability{AdapterCapabilityTranscription}}
	if err := server.validateRouteModelProtocol(model.Name, model, transcription); err != nil {
		t.Fatalf("explicit transcription publication rejected: %v", err)
	}
}

func TestTranscriptionCapabilitySelectsCompatibleFallback(t *testing.T) {
	calls := 0
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		if r.FormValue("model") != "vendor-transcription" {
			t.Errorf("incompatible route was selected: %s", r.FormValue("model"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"fixture"}`)
	}, "audio")
	adapter, err := server.adapterRegistry.Resolve(ProviderOpenAICompatible)
	if err != nil {
		t.Fatal(err)
	}
	server.adapterRegistry.Register("media-without-transcription", adapter, AdapterCapabilityMedia)
	provider, _ := store.GetProvider("media-provider")
	provider.ID, provider.Type = "transcription-provider", ProviderOpenAICompatible
	store.AddProvider(provider)
	if _, err := store.UpdateProvider("media-provider", Provider{Type: "media-without-transcription", Healthy: true}); err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{ID: "transcription-route", ModelName: "public-media", ProviderID: provider.ID, ProviderModel: "vendor-transcription", Priority: 2, Weight: 100, Status: StatusActive})
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
	if response.Code != http.StatusOK || calls != 1 || response.Header().Get("x-tokenhub-provider") == "media-provider" {
		t.Fatalf("compatible fallback was not selected: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}

func TestTranscriptionHookCapabilityMustMatchProtocolAndStreamMode(t *testing.T) {
	for _, tc := range []struct {
		name, protocol string
		output         pluginmeta.GatewayDataClass
		status         int
		multipart      bool
	}{
		{"matching JSON response hook", "audio/transcriptions", pluginmeta.DataProviderResponse, http.StatusOK, false},
		{"speech hook", "audio/speech", pluginmeta.DataProviderResponse, http.StatusNotImplemented, false},
		{"streaming hook", "audio/transcriptions", pluginmeta.DataStreamEvents, http.StatusNotImplemented, false},
		{"multipart requires native adapter", "audio/transcriptions", pluginmeta.DataProviderResponse, http.StatusNotImplemented, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("hook-only route reached upstream") }, "audio")
			if _, err := store.UpdateProvider("media-provider", Provider{Type: "transcription-hook-only", Healthy: true}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "transcription", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{tc.protocol}}, Reads: []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest}, Writes: []pluginmeta.GatewayDataClass{tc.output}}, func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				calls++
				var fields map[string]any
				if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &fields); err != nil || fields["audio_url"] != "https://example.invalid/fixture.wav" {
					t.Fatalf("JSON transcription input was not forwarded: %v", err)
				}
				return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionShortCircuit, Writes: map[pluginmeta.GatewayDataClass]pluginmeta.RawPatch{pluginmeta.DataProviderResponse: {Value: json.RawMessage(`{"text":"hook transcript"}`)}}}, nil
			})
			var response *httptest.ResponseRecorder
			if tc.multipart {
				response = doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
			} else {
				response = doReasoningJSON(t, server.Handler(), "/v1/audio/transcriptions", map[string]any{"model": "public-media", "audio_url": "https://example.invalid/fixture.wav"}, key)
			}
			if response.Code != tc.status || calls != map[bool]int{true: 1, false: 0}[tc.status == http.StatusOK] {
				t.Fatalf("hook capability scope bypassed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestTranscriptionMultipartProviderErrorsRemainStructured(t *testing.T) {
	for _, upstreamStatus := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusRequestTimeout, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(upstreamStatus), func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(upstreamStatus)
				_, _ = io.WriteString(w, `{"error":{"message":"fixture provider rejection","type":"invalid_request_error"}}`)
			}, "audio")
			store.AddRoute(ModelRoute{ID: "fallback-transcription", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "fallback-model", Priority: 2, Weight: 100, Status: StatusActive})
			response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
			wantStatus := upstreamStatus
			if upstreamStatus == http.StatusRequestTimeout {
				wantStatus = http.StatusGatewayTimeout
			}
			wantCalls := 1
			if upstreamStatus == http.StatusTooManyRequests {
				wantCalls = 2
			}
			var envelope struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if response.Code != wantStatus || calls != wantCalls || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Error.Code == "" {
				t.Fatalf("provider error lost its contract: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestTranscriptionMultipartTimeoutDoesNotResubmit(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("transport fixture reached upstream") }, "audio")
	calls := 0
	adapter := OpenAICompatibleAdapter{Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, context.DeadlineExceeded
	})}}
	server.adapterRegistry.Register(ProviderOpenAICompatible, adapter, AdapterCapabilityTranscription)
	store.AddRoute(ModelRoute{ID: "fallback-transcription", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "fallback-model", Priority: 2, Weight: 100, Status: StatusActive})
	request := transcriptionFixtureRequest(t, key, [][2]string{{"model", "public-media"}}, []mediaFile{transcriptionFixtureFile("file", "sample.wav", "audio/wav", transcriptionWAVFixture("fixture"))})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusGatewayTimeout || calls != 1 || !strings.Contains(response.Body.String(), "timeout") {
		t.Fatalf("timeout was changed or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}

func TestTranscriptionTransportFailureDoesNotExposeProviderDetails(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("transport fixture reached upstream") }, "audio")
	adapter := OpenAICompatibleAdapter{Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("fixture transport failed at https://private-provider.invalid/?api_key=private-provider-token")
	})}}
	server.adapterRegistry.Register(ProviderOpenAICompatible, adapter, AdapterCapabilityTranscription)
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "provider_request_failed") {
		t.Fatalf("transport failure response=%d %s", response.Code, response.Body)
	}
	var payloads []RequestPayloadLog
	if err := store.db.Find(&payloads).Error; err != nil {
		t.Fatal(err)
	}
	persisted, err := json.Marshal(payloads)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-provider.invalid", "private-provider-token"} {
		if strings.Contains(response.Body.String()+string(persisted), secret) {
			t.Errorf("raw transport details escaped redaction: %s", secret)
		}
	}
}

func TestTranscriptionCancellationDoesNotRetainProviderURL(t *testing.T) {
	err := uncertainMediaSubmission(&url.Error{Op: "Post", URL: "https://private-provider.invalid/?api_key=private-provider-token", Err: context.Canceled})
	if !errors.Is(err, context.Canceled) || providerErrorDisposition(err) != ProviderErrorClient || strings.Contains(err.Error(), "private-provider") {
		t.Fatalf("cancellation changed classification or retained provider details: %v", err)
	}
}
