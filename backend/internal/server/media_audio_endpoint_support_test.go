package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAudioPublicationRequiresSharedInventoryEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name, provider, modality, public, upstream string
		accepted                                   bool
	}{
		{"unsupported inventory", ProviderOpenAICompatible, "audio", "audio/transcriptions", "realtime", false},
		{"speech inventory", ProviderOpenAICompatible, "audio", "audio/transcriptions", "audio/speech", false},
		{"transcription inventory", ProviderOpenAICompatible, "audio", "audio/speech", "audio/transcriptions", false},
		{"different operation", ProviderOpenAICompatible, "embedding", "audio/transcriptions", "", false},
		{"disjoint declarations", ProviderOpenAICompatible, "audio", "audio/speech,audio/translations", "audio/transcriptions", false},
		{"shared endpoint unsupported by adapter", "local", "audio", "audio/speech,audio/transcriptions", "audio/speech,audio/translations", false},
		{"shared transcription", ProviderOpenAICompatible, "audio", "audio/speech,audio/transcriptions", "audio/transcriptions,audio/translations", true},
		{"shared speech", ProviderOpenAICompatible, "audio", "audio/speech,audio/transcriptions", "audio/speech", true},
		{"unspecified inventory endpoints", ProviderOpenAICompatible, "audio", "audio/transcriptions", "", true},
		{"unspecified public endpoints", ProviderOpenAICompatible, "audio", "", "audio/transcriptions", true},
	} {
		for _, publication := range []string{"initial route", "separate route"} {
			t.Run(tc.name+"/"+publication, func(t *testing.T) {
				server, store, route, model := newAudioPublicationFixture(t, tc.provider, tc.upstream, func(http.ResponseWriter, *http.Request) {
					t.Error("publication invoked upstream")
				})
				upstream := store.ListProviderModels()[0]
				if _, err := store.UpdateProviderModel(upstream.ID, ProviderModel{Modality: tc.modality}); err != nil {
					t.Fatal(err)
				}
				model["metadata"] = map[string]string{"endpoints": tc.public}
				response := publishAudioFixture(t, server, route, model, publication)
				wantStatus, wantRoutes := http.StatusBadRequest, 0
				if tc.accepted {
					wantStatus, wantRoutes = http.StatusCreated, 1
				}
				if response.Code != wantStatus || len(store.ListRoutes()) != wantRoutes || (!tc.accepted && !strings.Contains(response.Body, `"code":"model_operation_mismatch"`)) {
					t.Fatalf("audio publication status=%d routes=%d body=%s", response.Code, len(store.ListRoutes()), response.Body)
				}
			})
		}
	}
}

func audioEndpointFixtureKey(t *testing.T, store *GormStore, model string) string {
	t.Helper()
	project := store.CreateProject(Project{Name: "Audio endpoint fixture", Status: StatusActive})
	_, key, err := store.CreateAPIKey(project.ID, APIKey{Name: "Audio endpoint key", Allowed: []string{model}, Status: StatusActive}, "thk_audio_endpoint_fixture")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func requestAudioEndpoint(t *testing.T, server *Server, key, model, endpoint string) responseBody {
	t.Helper()
	if endpoint == "audio/speech" {
		return doJSON(t, server.Handler(), http.MethodPost, "/v1/"+endpoint, map[string]any{"model": model, "input": "Synthetic speech"}, key)
	}
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/"+endpoint, [][2]string{{"model", model}})
	return responseBody{Code: response.Code, Body: response.Body.String(), Header: response.Header()}
}

func TestAudioRuntimeRechecksPublishedAndInventoryOperations(t *testing.T) {
	for _, endpoint := range []string{"audio/speech", "audio/transcriptions", "audio/translations"} {
		for _, change := range []string{"inventory endpoints", "inventory operation", "public endpoints", "adapter endpoints"} {
			t.Run(endpoint+"/"+change, func(t *testing.T) {
				calls := 0
				declarations := "audio/speech,audio/transcriptions,audio/translations"
				server, store, route, model := newAudioPublicationFixture(t, ProviderOpenAICompatible, declarations, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"text":"Synthetic transcript"}`)
				})
				if response := publishAudioFixture(t, server, route, model, "initial route"); response.Code != http.StatusCreated {
					t.Fatalf("publication failed: status=%d body=%s", response.Code, response.Body)
				}
				key := audioEndpointFixtureKey(t, store, route.ModelName)
				if response := requestAudioEndpoint(t, server, key, route.ModelName, endpoint); response.Code != http.StatusOK || calls != 1 {
					t.Fatalf("compatible route failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				other := "audio/speech"
				if endpoint == other {
					other = "audio/transcriptions"
				}
				var err error
				switch change {
				case "inventory endpoints":
					_, err = store.UpdateProviderModel(store.ListProviderModels()[0].ID, ProviderModel{Metadata: map[string]string{"endpoints": other}})
				case "inventory operation":
					_, err = store.UpdateProviderModel(store.ListProviderModels()[0].ID, ProviderModel{Modality: "embedding"})
				case "public endpoints":
					_, err = store.UpdateModel(route.ModelName, Model{Metadata: map[string]string{"endpoints": other}})
				case "adapter endpoints":
					adapter, resolveErr := server.adapterRegistry.Resolve(ProviderOpenAICompatible)
					if resolveErr != nil {
						t.Fatal(resolveErr)
					}
					descriptor := providerPluginDescriptorWithRouteProtocol("test.audio-endpoint-policy", "Audio endpoint policy", ProviderOpenAICompatible, AdapterCapabilityTranscription, other)
					err = server.adapterRegistry.RegisterPlugin(descriptor, AdapterRegistration{Type: ProviderOpenAICompatible, Adapter: adapter, Capabilities: []AdapterCapability{AdapterCapabilityMedia, AdapterCapabilityTranscription}})
				}
				if err != nil {
					t.Fatal(err)
				}
				response := requestAudioEndpoint(t, server, key, route.ModelName, endpoint)
				if response.Code != http.StatusNotImplemented || calls != 1 || !strings.Contains(response.Body, "provider_capability_not_supported") {
					t.Fatalf("incompatible route invoked: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
			})
		}
	}
}

func TestAudioRuntimeSkipsIncompatibleInventoryAndKeepsLegacyRoutes(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "matching inventory fallback"
		if legacy {
			name = "legacy route without inventory"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = r.MultipartForm.RemoveAll() }()
				wantModel := "vendor-transcription"
				if legacy {
					wantModel = "vendor-media"
				}
				if r.FormValue("model") != wantModel {
					t.Errorf("unexpected selected model: %s", r.FormValue("model"))
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"text":"Synthetic transcript"}`)
			}, "audio")
			if legacy {
				for _, upstream := range store.ListProviderModels() {
					if err := store.DeleteProviderModel(upstream.ID); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-media", Modality: "audio", Metadata: map[string]string{"endpoints": "audio/speech"}})
				store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "vendor-transcription", Modality: "audio", Metadata: map[string]string{"endpoints": "audio/transcriptions"}})
				store.AddRoute(ModelRoute{ID: "transcription-fallback", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "vendor-transcription", Status: StatusActive, Priority: 2, Weight: 100})
			}
			response := requestAudioEndpoint(t, server, key, "public-media", "audio/transcriptions")
			if response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("compatible route failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestAudioRuntimePreservesLegacyChatModelExtensions(t *testing.T) {
	for _, endpoint := range []string{"audio/speech", "audio/transcriptions", "audio/translations"} {
		t.Run(endpoint, func(t *testing.T) {
			calls := 0
			server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"text":"Synthetic transcript"}`)
			}, "chat")
			inventory := store.ListProviderModels()
			if len(inventory) != 1 || inventory[0].Source != "existing-route" || inventory[0].Modality != "chat" {
				t.Fatalf("legacy fixture did not backfill inferred chat inventory: %+v", inventory)
			}
			response := requestAudioEndpoint(t, server, key, "public-media", endpoint)
			if response.Code != http.StatusOK || calls != 1 {
				t.Fatalf("legacy audio extension failed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
			if _, err := store.UpdateProviderModel(inventory[0].ID, ProviderModel{Metadata: map[string]string{"endpoints": "chat/completions"}}); err != nil {
				t.Fatal(err)
			}
			response = requestAudioEndpoint(t, server, key, "public-media", endpoint)
			if response.Code != http.StatusNotImplemented || calls != 1 {
				t.Fatalf("explicit legacy endpoint restriction ignored: status=%d calls=%d body=%s", response.Code, calls, response.Body)
			}
		})
	}
}
