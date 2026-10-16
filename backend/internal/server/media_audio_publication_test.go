package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newAudioPublicationFixture(t *testing.T, providerType, endpoint string, upstream http.HandlerFunc) (*Server, *GormStore, ModelRoute, map[string]any) {
	t.Helper()
	remote := httptest.NewServer(upstream)
	t.Cleanup(remote.Close)
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "audio-provider", Type: providerType, BaseURL: remote.URL + "/v1", APIKey: "fixture-upstream-key", Status: StatusActive, Healthy: true})
	metadata := map[string]string{"endpoints": endpoint, "audio_price_usd_per_second": "0"}
	store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "vendor-audio", Modality: "audio", Status: StatusActive, Metadata: metadata})
	server := NewWithConfig(store, Config{AdminToken: "media-admin", SecretKey: "media-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	route := ModelRoute{ModelName: "public-audio", ProviderID: provider.ID, ProviderModel: "vendor-audio", Status: StatusActive, Weight: 100}
	model := map[string]any{"name": route.ModelName, "modality": "audio", "status": StatusActive, "metadata": metadata}
	return server, store, route, model
}

func assertAudioInventorySupport(t *testing.T, server *Server, supported bool) {
	t.Helper()
	response := doJSON(t, server.Handler(), http.MethodGet, "/api/admin/provider-models?provider_id=audio-provider", nil, "media-admin")
	var inventory struct {
		Data []ProviderModel `json:"data"`
	}
	if response.Code != http.StatusOK || json.Unmarshal([]byte(response.Body), &inventory) != nil || len(inventory.Data) != 1 {
		t.Fatalf("audio inventory request failed: status=%d body=%s", response.Code, response.Body)
	}
	if got := inventory.Data[0].CallSupported; got == nil || *got != supported {
		t.Fatalf("audio inventory support = %v, want %v", got, supported)
	}
}

func publishAudioFixture(t *testing.T, server *Server, route ModelRoute, model map[string]any, publication string) responseBody {
	t.Helper()
	if publication == "initial route" {
		model["routes"] = []ModelRoute{route}
	}
	response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/models", model, "media-admin")
	if publication == "separate route" {
		if response.Code != http.StatusCreated {
			t.Fatalf("audio model creation failed: status=%d body=%s", response.Code, response.Body)
		}
		response = doJSON(t, server.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "media-admin")
	}
	return response
}

func TestTranscriptionModelsCanBePublishedAndInvoked(t *testing.T) {
	for _, providerType := range []string{ProviderOpenAI, ProviderOpenAICompatible, "local"} {
		for _, endpoint := range []string{"audio/transcriptions", "audio/translations"} {
			for _, publication := range []string{"initial route", "separate route"} {
				t.Run(providerType+"/"+endpoint+"/"+publication, func(t *testing.T) {
					calls := 0
					server, store, route, model := newAudioPublicationFixture(t, providerType, endpoint, func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.URL.Path != "/v1/"+endpoint || r.Header.Get("Authorization") != "Bearer fixture-upstream-key" {
							t.Error("unexpected transcription upstream endpoint or credentials")
						}
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						defer func() { _ = r.MultipartForm.RemoveAll() }()
						if r.FormValue("model") != "vendor-audio" || len(r.MultipartForm.File["file"]) != 1 {
							t.Error("published transcription route did not preserve model mapping and audio upload")
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"text":"Synthetic transcript","usage":{"type":"duration","seconds":1}}`)
					})
					assertAudioInventorySupport(t, server, true)
					response := publishAudioFixture(t, server, route, model, publication)
					if response.Code != http.StatusCreated {
						t.Fatalf("transcription publication failed: status=%d body=%s", response.Code, response.Body)
					}
					project := store.CreateProject(Project{Name: "Audio publication", Status: StatusActive})
					_, key, err := store.CreateAPIKey(project.ID, APIKey{Name: "Audio key", Allowed: []string{route.ModelName}, Status: StatusActive}, "thk_audio_publication")
					if err != nil {
						t.Fatal(err)
					}
					result := doMediaMultipartReviewRequest(t, server, key, "/v1/"+endpoint, [][2]string{{"model", route.ModelName}})
					if result.Code != http.StatusOK || calls != 1 || !strings.Contains(result.Body.String(), `"text":"Synthetic transcript"`) {
						t.Fatalf("published transcription invocation failed: status=%d calls=%d body=%s", result.Code, calls, result.Body)
					}
				})
			}
		}
	}
}

func TestTranscriptionPublicationRejectsUnsupportedAdapters(t *testing.T) {
	for _, capability := range []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityMedia, AdapterCapabilityTranscription} {
		for _, publication := range []string{"initial route", "separate route"} {
			t.Run(string(capability)+"/"+publication, func(t *testing.T) {
				server, store, route, model := newAudioPublicationFixture(t, ProviderOpenAICompatible, "audio/transcriptions", func(http.ResponseWriter, *http.Request) {
					t.Error("unsupported transcription adapter reached upstream")
				})
				var adapter any = OpenAICompatibleAdapter{}
				if capability == AdapterCapabilityTranscription {
					adapter = MockAdapter{} // A declaration alone cannot implement native uploads.
				}
				server.adapterRegistry.Register(ProviderOpenAICompatible, adapter, capability)
				assertAudioInventorySupport(t, server, false)
				response := publishAudioFixture(t, server, route, model, publication)
				if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, `"code":"model_operation_unsupported"`) || len(store.ListRoutes()) != 0 {
					t.Fatalf("unsupported transcription route was published: status=%d body=%s", response.Code, response.Body)
				}
			})
		}
	}
}

func TestAudioPublicationPreservesMediaOnlySpeechSupport(t *testing.T) {
	server, _, route, model := newAudioPublicationFixture(t, ProviderOpenAICompatible, "audio/speech", func(http.ResponseWriter, *http.Request) {})
	server.adapterRegistry.Register(ProviderOpenAICompatible, OpenAICompatibleAdapter{}, AdapterCapabilityMedia)
	assertAudioInventorySupport(t, server, true)
	response := publishAudioFixture(t, server, route, model, "initial route")
	if response.Code != http.StatusCreated {
		t.Fatalf("media-capable speech publication failed: status=%d body=%s", response.Code, response.Body)
	}
}
