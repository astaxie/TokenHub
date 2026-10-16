package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Execute the shipped Node clients against the real gateway and a local upstream.
// No credentials, external services or running TokenHub installation are needed.
func TestAudioAndRerankSDKSmoke(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("CI must provide Node.js for the SDK smoke clients")
		}
		t.Skip("Node.js is required for the SDK smoke clients")
	}
	for _, providerType := range []string{ProviderOpenAI, ProviderOpenAICompatible, "local"} {
		t.Run(providerType, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/audio/transcriptions":
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					defer func() { _ = r.MultipartForm.RemoveAll() }()
					if r.FormValue("model") != "sdk-audio-upstream" || len(r.MultipartForm.File["file"]) != 1 {
						t.Error("incorrect upstream transcription fields")
					}
					_, _ = io.WriteString(w, `{"text":"Synthetic transcript","usage":{"type":"duration","seconds":1}}`)
				case "/v1/rerank":
					var payload struct {
						Documents []string `json:"documents"`
						Model     string   `json:"model"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload.Documents) != 2 || payload.Model != "sdk-rerank-upstream" {
						t.Error("upstream must receive only document text and its configured model")
					}
					_, _ = io.WriteString(w, `{"results":[{"index":0,"relevance_score":0.9}],"usage":{"total_tokens":7}}`)
				default:
					t.Errorf("unexpected upstream path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer upstream.Close()
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "SDK fixture", Status: StatusActive})
			_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "SDK key", Allowed: []string{"sdk-audio", "sdk-rerank"}, Status: StatusActive}, "thk_sdk_audio_rerank_fixture")
			if err != nil {
				t.Fatal(err)
			}
			store.AddProvider(Provider{ID: "sdk-provider", Name: "SDK upstream", Type: providerType, BaseURL: upstream.URL + "/v1", APIKey: "synthetic-upstream-key", Status: StatusActive, Healthy: true, Options: map[string]string{"rerank_protocol": "jina"}})
			for _, item := range []struct{ model, upstream, modality string }{{"sdk-audio", "sdk-audio-upstream", "audio"}, {"sdk-rerank", "sdk-rerank-upstream", "rerank"}} {
				metadata := map[string]string{"retrieval_pricing_confirmed": "true", "audio_price_usd_per_second": "0"}
				store.AddModel(Model{Name: item.model, Modality: item.modality, Status: StatusActive, Metadata: metadata})
				store.AddProviderModel(ProviderModel{ProviderID: "sdk-provider", UpstreamModel: item.upstream, Modality: item.modality, Metadata: metadata})
				store.AddRoute(ModelRoute{ID: item.model + "-route", ModelName: item.model, ProviderID: "sdk-provider", ProviderModel: item.upstream, Status: StatusActive, Priority: 1, Weight: 100})
			}
			server := NewWithConfig(store, Config{ImageStorageDir: t.TempDir()})
			defer func() { _ = server.Shutdown(context.Background()) }()
			gateway := httptest.NewServer(server.Handler())
			defer gateway.Close()
			for _, script := range []string{"test-transcriptions.mjs", "test-rerank.mjs"} {
				if providerType == ProviderOpenAI && script == "test-rerank.mjs" {
					continue // OpenAI's native API does not advertise reranking.
				}
				t.Run(script, func(t *testing.T) {
					path, err := filepath.Abs(filepath.Join("..", "..", "..", "sdk", script))
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, node, path)
					command.Env = append(os.Environ(), "TOKENHUB_BASE_URL="+gateway.URL+"/v1", "TOKENHUB_API_KEY="+secret, "TOKENHUB_TRANSCRIPTION_MODEL=sdk-audio", "TOKENHUB_RERANK_MODEL=sdk-rerank")
					if output, err := command.CombinedOutput(); err != nil {
						t.Fatalf("SDK smoke failed: %v\n%s", err, output)
					}
				})
			}
		})
	}
}
