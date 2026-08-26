package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestImageGatewayPreflightHooksRewritePromptBeforeJobRuns(t *testing.T) {
	stages := []pluginmeta.GatewayHookStage{
		pluginmeta.StagePrivacyPre,
		pluginmeta.StageContextOptimize,
		pluginmeta.StageGuardrailPre,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			imageBytes := realPNGFixture(t)
			store := NewMemoryStore()
			project := store.CreateProject(Project{Name: "Image Preflight Plugin Project", Status: StatusActive})
			_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-preflight-plugin-key", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_preflight_plugin")
			if err != nil {
				t.Fatal(err)
			}
			provider := store.AddProvider(Provider{ID: "prv_image_preflight", Name: "Image Preflight", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
			resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_image_preflight", ProviderID: provider.ID, Name: "Image Preflight Key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, MaxConcurrency: 10})
			if err != nil {
				t.Fatal(err)
			}
			store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
			store.AddRoute(ModelRoute{ID: "route_image_preflight", ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: openAIImageModelName, Status: StatusActive, Priority: 1, Weight: 100})
			server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-preflight-plugin-secret", ImageStorageDir: t.TempDir()})
			t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
			hook := pluginmeta.GatewayHookDescriptor{
				PluginID:      "tokenhub.test-image-preflight",
				HookID:        "rewrite-image-prompt-" + string(stage),
				Stage:         stage,
				Priority:      1000,
				Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataNormalizedText},
				Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
				FailurePolicy: pluginmeta.FailurePolicyFailClosed,
			}
			if err := server.gatewayChain.RegisterHook(hook); err != nil {
				t.Fatalf("register image preflight hook: %v", err)
			}
			patchedPrompt := "plugin-rewritten image prompt for " + string(stage)
			if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				var request imageGenerationRequest
				if err := json.Unmarshal(input.Data[pluginmeta.DataRequestBody], &request); err != nil {
					t.Fatalf("decode image hook request body: %v", err)
				}
				if stage == pluginmeta.StageGuardrailPre && !strings.Contains(string(input.Data[pluginmeta.DataNormalizedText]), "original image prompt") {
					t.Fatalf("guardrail hook normalized text = %s, want original prompt segment", input.Data[pluginmeta.DataNormalizedText])
				}
				request.Prompt = patchedPrompt
				request.Quality = "low"
				request.Size = "1024x1024"
				return rawRequestBodyPatch(t, request), nil
			})); err != nil {
				t.Fatalf("register image preflight handler: %v", err)
			}
			var selectedJob ImageJob
			server.imageRunner = func(_ context.Context, _ RouteSelection, job ImageJob) ([]byte, string, Usage, error) {
				selectedJob = job
				return imageBytes, "", Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4}, nil
			}

			response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
				"model": openAIImageModelName, "prompt": "original image prompt", "response_format": "b64_json",
			}, secret, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("image generation failed: %d %s", response.Code, response.Body)
			}
			if selectedJob.Prompt != patchedPrompt || selectedJob.Quality != "low" || selectedJob.Size != "1024x1024" {
				t.Fatalf("image job = prompt %q quality %q size %q, want plugin patch", selectedJob.Prompt, selectedJob.Quality, selectedJob.Size)
			}
		})
	}
}
