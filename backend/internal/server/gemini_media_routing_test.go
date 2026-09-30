package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiMediaModelsPublishDiscoverAndEnforceAllowlists(t *testing.T) {
	for _, publication := range []string{"initial route", "separate route"} {
		t.Run(publication, func(t *testing.T) {
			calls := 0
			server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, geminiMediaFixtureResponse)
			})
			store.AddProviderModel(ProviderModel{ProviderID: "media-provider", UpstreamModel: "native-image", Modality: "image", Status: StatusActive})
			route := ModelRoute{ModelName: "published-gemini", ProviderID: "media-provider", ProviderModel: "native-image", Status: StatusActive, Weight: 100}
			payload := map[string]any{"name": "published-gemini", "modality": "image", "status": StatusActive, "metadata": map[string]string{"endpoints": "gemini"}}
			if publication == "initial route" {
				payload["routes"] = []ModelRoute{route}
			}
			response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/models", payload, "media-admin")
			if response.Code != http.StatusCreated {
				t.Fatalf("native media publication failed: %d %s", response.Code, response.Body)
			}
			if publication == "separate route" {
				response = doJSON(t, server.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "media-admin")
				if response.Code != http.StatusCreated {
					t.Fatalf("native route publication failed: %d %s", response.Code, response.Body)
				}
			}
			project := store.CreateProject(Project{Name: "Native image project", Status: StatusActive})
			_, allowed, err := store.CreateAPIKey(project.ID, APIKey{Name: "Native image key", Allowed: []string{"published-gemini"}, Status: StatusActive}, "thk_published_gemini")
			if err != nil {
				t.Fatal(err)
			}
			response = doGeminiJSON(t, server.Handler(), http.MethodGet, "/v1beta/models", nil, allowed)
			if response.Code != 200 || !strings.Contains(response.Body, "models/published-gemini") || strings.Contains(response.Body, "models/public-media") {
				t.Fatalf("native model discovery failed: %d %s", response.Code, response.Body)
			}
			response = doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/published-gemini:generateContent", json.RawMessage(geminiMediaFixtureRequest), allowed)
			if response.Code != 200 || calls != 1 {
				t.Fatalf("published model invocation failed: %d %s", response.Code, response.Body)
			}
			response = doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/published-gemini:generateContent", json.RawMessage(geminiMediaFixtureRequest), key)
			if response.Code != http.StatusForbidden || calls != 1 {
				t.Fatalf("model allowlist bypassed: %d calls=%d %s", response.Code, calls, response.Body)
			}
		})
	}
}

func TestGeminiMediaAuthenticationFailureAllowsFallback(t *testing.T) {
	calls := 0
	server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if strings.Contains(r.URL.Path, "vendor-media:") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, geminiMediaFixtureResponse)
	})
	provider, _ := store.GetProvider("media-provider")
	provider.ID = "native-fallback-provider"
	store.AddProvider(provider)
	store.AddRoute(ModelRoute{ID: "native-fallback", ModelName: "public-media", ProviderID: provider.ID, ProviderModel: "native-fallback-model", Status: StatusActive, Priority: 2, Weight: 100})
	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:generateContent", json.RawMessage(geminiMediaFixtureRequest), key)
	if response.Code != 200 || calls != 2 || response.Header.Get("x-tokenhub-provider") != provider.ID {
		t.Fatalf("definite rejection did not fail over: %d calls=%d %s", response.Code, calls, response.Body)
	}
}

func TestGeminiMediaRejectsCrossOriginRedirects(t *testing.T) {
	escaped := 0
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { escaped++; w.WriteHeader(200) }))
	defer other.Close()
	calls := 0
	server, _, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, other.URL+"/capture", http.StatusTemporaryRedirect)
	})
	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:generateContent", json.RawMessage(geminiMediaFixtureRequest), key)
	if response.Code == 200 || escaped != 0 || calls != 1 || strings.Contains(response.Body, "upstream-fixture-secret") {
		t.Fatalf("redirect escaped or leaked credentials: %d escaped=%d calls=%d %s", response.Code, escaped, calls, response.Body)
	}
}

func TestGeminiMediaResponseLimitRetainsLeadingUsage(t *testing.T) {
	calls := 0
	padding := strings.Repeat(" ", 1<<20)
	server, store, key := newGeminiMediaFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, geminiMediaFixtureResponse)
		for i := 0; i < maxMediaResponseBytes/len(padding)+1; i++ {
			if _, err := io.WriteString(w, padding); err != nil {
				return
			}
		}
	})
	response := doGeminiJSON(t, server.Handler(), http.MethodPost, "/v1beta/models/public-media:generateContent", json.RawMessage(geminiMediaFixtureRequest), key)
	if response.Code != http.StatusBadGateway || calls != 1 || !strings.Contains(response.Body, "size limit") {
		t.Fatalf("native response limit bypassed: %d calls=%d %s", response.Code, calls, response.Body)
	}
	records := store.ListUsageRecords()
	if len(records) != 1 || records[0].TotalTokens != 15 {
		t.Fatalf("oversize generation usage lost: %+v", records)
	}
}
