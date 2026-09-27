package server

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestMediaImageOnlyModelsCanBePublishedAndInvoked(t *testing.T) {
	for _, providerType := range []string{ProviderOpenAI, ProviderOpenAICompatible} {
		for _, endpoint := range []string{"images/edits", "images/variations"} {
			for _, publication := range []string{"initial route", "separate route"} {
				t.Run(providerType+"/"+endpoint+"/"+publication, func(t *testing.T) {
					upstreamCalls := 0
					image := []byte{137, 80, 78, 71, 0, 255}
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						upstreamCalls++
						if r.Method != http.MethodPost || r.URL.Path != "/v1/"+endpoint {
							t.Errorf("unexpected upstream request: %s %s", r.Method, r.URL.Path)
						}
						if err := r.ParseMultipartForm(1 << 20); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						defer func() { _ = r.MultipartForm.RemoveAll() }()
						if r.FormValue("model") != "vendor-image" || r.FormValue("prompt") != "A painted landscape" {
							t.Errorf("unexpected multipart fields: %v", r.MultipartForm.Value)
						}
						file, header, err := r.FormFile("image")
						if err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						defer file.Close()
						data, err := io.ReadAll(file)
						if err != nil || header.Filename != "reference.png" || !bytes.Equal(data, image) {
							t.Errorf("image changed: name=%s data=%v error=%v", header.Filename, data, err)
						}
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"data":[{"b64_json":"aW1hZ2U="}],"usage":{"input_tokens":3,"output_tokens":7,"total_tokens":10}}`)
					}))
					t.Cleanup(upstream.Close)
					store := NewMemoryStore()
					provider := store.AddProvider(Provider{ID: "image-provider", Type: providerType, BaseURL: upstream.URL + "/v1", APIKey: "fixture-upstream-secret", Status: StatusActive, Healthy: true})
					store.AddProviderModel(ProviderModel{ProviderID: provider.ID, UpstreamModel: "vendor-image", Modality: "image", Status: StatusActive})
					server := NewWithConfig(store, Config{AdminToken: "media-admin", SecretKey: "media-secret", ImageStorageDir: t.TempDir()})
					t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
					route := ModelRoute{ModelName: "public-image", ProviderID: provider.ID, ProviderModel: "vendor-image", Status: StatusActive, Weight: 100}
					payload := map[string]any{"name": "public-image", "modality": "image", "status": StatusActive, "metadata": map[string]string{"endpoints": endpoint}}
					if publication == "initial route" {
						payload["routes"] = []ModelRoute{route}
					}
					response := doJSON(t, server.Handler(), http.MethodPost, "/api/admin/models", payload, "media-admin")
					if response.Code != http.StatusCreated {
						t.Fatalf("image-only model publication failed: %d %s", response.Code, response.Body)
					}
					if publication == "separate route" {
						response = doJSON(t, server.Handler(), http.MethodPost, "/api/admin/routing-rules", route, "media-admin")
						if response.Code != http.StatusCreated {
							t.Fatalf("image-only route publication failed: %d %s", response.Code, response.Body)
						}
					}
					project := store.CreateProject(Project{Name: "Image publication", Status: StatusActive})
					_, key, err := store.CreateAPIKey(project.ID, APIKey{Name: "Image key", Allowed: []string{"public-image"}, Status: StatusActive}, "thk_image_publication")
					if err != nil {
						t.Fatal(err)
					}
					hookCalls := 0
					registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "image-scope", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{providerRouteProtocolImageGeneration}}}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
						hookCalls++
						return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
					})
					var body bytes.Buffer
					writer := multipart.NewWriter(&body)
					for _, field := range [][2]string{{"model", "public-image"}, {"prompt", "A painted landscape"}} {
						if err := writer.WriteField(field[0], field[1]); err != nil {
							t.Fatal(err)
						}
					}
					file, err := writer.CreateFormFile("image", "reference.png")
					if err != nil {
						t.Fatal(err)
					}
					if _, err := file.Write(image); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					request := httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, &body)
					request.Header.Set("Authorization", "Bearer "+key)
					request.Header.Set("Content-Type", writer.FormDataContentType())
					recorder := httptest.NewRecorder()
					server.Handler().ServeHTTP(recorder, request)
					if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"b64_json":"aW1hZ2U="`) || upstreamCalls != 1 {
						t.Fatalf("published image request failed: status=%d calls=%d body=%s", recorder.Code, upstreamCalls, recorder.Body)
					}
					if hookCalls != 1 {
						t.Fatalf("existing image hook scope was bypassed: calls=%d", hookCalls)
					}
					if usage := store.ListUsageRecords(); len(usage) != 1 || usage[0].TotalTokens != 10 {
						t.Fatalf("published image usage not recorded: %+v", usage)
					}
				})
			}
		}
	}
}
