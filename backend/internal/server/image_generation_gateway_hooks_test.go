package server

import (
	"context"
	"encoding/json"
	"errors"
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

func TestImageProviderCallHookServesGatewayImageWithoutBuiltinAdapter(t *testing.T) {
	imageBytes := realPNGFixture(t)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Provider Plugin Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-provider-plugin-key", Allowed: []string{"plugin-image-model"}, Status: StatusActive}, "thk_image_provider_plugin")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_image_provider_plugin", Name: "Image Provider Plugin", Type: "third_party_image_plugin", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_image_provider_plugin", ProviderID: provider.ID, Name: "Image Provider Plugin Account",
		ResourceType: "third_party_image_account", Status: StatusActive, Healthy: true,
		Options: map[string]string{"third_party_image_capability": "available"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "plugin-image-model", Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_provider_plugin", ModelName: "plugin-image-model", ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "upstream-image-model", Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-provider-plugin-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	server.imageRunner = func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
		t.Fatal("image runner should not be called when provider_call hook handles the route")
		return nil, "", Usage{}, nil
	}

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-provider",
		HookID:        "image-provider-call",
		Stage:         pluginmeta.StageProviderCall,
		Priority:      1000,
		Subject:       "third_party_image_plugin",
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest, pluginmeta.DataProviderCredentials, pluginmeta.DataRouteCandidates},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicySkipRoute,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register image provider call hook: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.test-image-provider",
		ActionID:   "third_party_image.capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "third_party_image_plugin",
		Metadata: map[string]string{
			"provider_resource_type":     "third_party_image_account",
			"public_model":               "plugin-image-model",
			"upstream_model":             "upstream-image-model",
			"capability_option":          "third_party_image_capability",
			"capability_supported_value": "available",
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatalf("register image capability action: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var request ProviderImageGenerationRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &request); err != nil {
			t.Fatalf("decode provider image request: %v", err)
		}
		if request.Model != "upstream-image-model" || request.Prompt != "plugin image prompt" {
			t.Fatalf("provider image request = model %q prompt %q, want upstream model and prompt", request.Model, request.Prompt)
		}
		if _, ok := input.Data[pluginmeta.DataProviderCredentials]; !ok {
			t.Fatal("provider credentials were not available to image provider hook")
		}
		return rawProviderCallResult(t, gatewayImageProviderResponse{
			DataBase64:    encodeBase64(imageBytes),
			RevisedPrompt: "served by image plugin",
		}, Usage{PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12}), nil
	})); err != nil {
		t.Fatalf("register image provider call handler: %v", err)
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": "plugin-image-model", "prompt": "plugin image prompt", "response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("image generation failed: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, encodeBase64(imageBytes)) || !strings.Contains(response.Body, "served by image plugin") {
		t.Fatalf("image response was not served by provider plugin: %s", response.Body)
	}
}

func TestImageProviderCallRouteRequiresMatchingHookSubject(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{ID: "prv_image_subject", Name: "Image Subject", Type: "third_party_image_plugin", Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{
		ID: "rsrc_image_subject", ProviderID: provider.ID, Name: "Image Subject Account",
		ResourceType: "third_party_image_account", Status: StatusActive, Healthy: true,
		Options: map[string]string{"third_party_image_capability": "available"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "plugin-image-model", Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_subject", ModelName: "plugin-image-model", ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "upstream-image-model", Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-subject-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-other-provider",
		HookID:        "other-provider-call",
		Stage:         pluginmeta.StageProviderCall,
		Priority:      1000,
		Subject:       "other_provider",
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicySkipRoute,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register unrelated provider call hook: %v", err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.test-image-provider",
		ActionID:   "third_party_image.capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "third_party_image_plugin",
		Metadata: map[string]string{
			"provider_resource_type":     "third_party_image_account",
			"public_model":               "plugin-image-model",
			"upstream_model":             "upstream-image-model",
			"capability_option":          "third_party_image_capability",
			"capability_supported_value": "available",
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatalf("register image capability action: %v", err)
	}

	routes, err := server.imageRouteCandidates("plugin-image-model")
	if err != nil {
		t.Fatalf("image route candidates returned error: %v", err)
	}
	if len(routes) != 0 {
		t.Fatalf("image route candidates = %+v, want no route without matching provider_call hook subject", routes)
	}
}

func TestImageRequestTransformHookRewritesProviderRequestBeforeInvoke(t *testing.T) {
	imageBytes := realPNGFixture(t)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Request Transform Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-transform-plugin-key", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_transform_plugin")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_image_transform", Name: "Image Transform", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_image_transform", ProviderID: provider.ID, Name: "Image Transform Key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_transform", ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: openAIImageModelName, Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-transform-plugin-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-transform",
		HookID:        "provider-request",
		Stage:         pluginmeta.StageRequestTransform,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderRequest},
		FailurePolicy: pluginmeta.FailurePolicySkipRoute,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register image request transform hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var request ProviderImageGenerationRequest
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderRequest], &request); err != nil {
			t.Fatalf("decode provider image request: %v", err)
		}
		request.Prompt = "provider-stage prompt"
		request.Quality = "high"
		return rawProviderRequestPatch(t, request), nil
	})); err != nil {
		t.Fatalf("register image request transform handler: %v", err)
	}
	var selectedJob ImageJob
	server.imageRunner = func(_ context.Context, _ RouteSelection, job ImageJob) ([]byte, string, Usage, error) {
		selectedJob = job
		return imageBytes, "", Usage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4}, nil
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "original provider prompt", "response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("image generation failed: %d %s", response.Code, response.Body)
	}
	if selectedJob.Prompt != "provider-stage prompt" || selectedJob.Quality != "high" {
		t.Fatalf("image job = prompt %q quality %q, want provider-stage patch", selectedJob.Prompt, selectedJob.Quality)
	}
}

func TestImagePostUsageAndCacheWriteHooksRunAfterProviderInvoke(t *testing.T) {
	imageBytes := realPNGFixture(t)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Post Plugin Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-post-plugin-key", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_post_plugin")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_image_post", Name: "Image Post", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_image_post", ProviderID: provider.ID, Name: "Image Post Key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_post", ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: openAIImageModelName, Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-post-plugin-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	server.imageRunner = func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
		return imageBytes, "runner prompt", Usage{PromptTokens: 2, CompletionTokens: 2, TotalTokens: 4}, nil
	}

	responsePost := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-post",
		HookID:        "response",
		Stage:         pluginmeta.StageResponsePost,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	usageAttribution := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-post",
		HookID:        "usage",
		Stage:         pluginmeta.StageUsageAttribution,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataUsage, pluginmeta.DataProviderResponse},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailClosed,
	}
	cacheWrite := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-post",
		HookID:        "cache-write",
		Stage:         pluginmeta.StageCacheWrite,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{responsePost, usageAttribution, cacheWrite} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register image post hook %s: %v", hook.HookID, err)
		}
	}
	if err := server.gatewayHooks.RegisterHandler(responsePost, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		var response gatewayImageProviderResponse
		if err := json.Unmarshal(input.Data[pluginmeta.DataProviderResponse], &response); err != nil {
			t.Fatalf("decode image response: %v", err)
		}
		response.RevisedPrompt = "post-stage revised prompt"
		return rawProviderResponsePatch(t, response), nil
	})); err != nil {
		t.Fatalf("register image response post handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(usageAttribution, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if _, ok := input.Data[pluginmeta.DataProviderResponse]; !ok {
			t.Fatal("provider response was not available to image usage hook")
		}
		return rawUsagePatch(t, Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9}), nil
	})); err != nil {
		t.Fatalf("register image usage attribution handler: %v", err)
	}
	sawCacheWrite := false
	if err := server.gatewayHooks.RegisterHandler(cacheWrite, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		sawCacheWrite = len(input.Data[pluginmeta.DataRequestBody]) > 0 && len(input.Data[pluginmeta.DataProviderResponse]) > 0 && len(input.Data[pluginmeta.DataUsage]) > 0
		return pluginmeta.GatewayHookResult{Decision: pluginmeta.HookDecisionContinue}, nil
	})); err != nil {
		t.Fatalf("register image cache write handler: %v", err)
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "post hook prompt", "response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("image generation failed: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "post-stage revised prompt") || !strings.Contains(response.Body, `"total_tokens":9`) {
		t.Fatalf("image response was not post-processed: %s", response.Body)
	}
	if !sawCacheWrite {
		t.Fatal("image cache_write hook did not receive request, response, and usage data")
	}
}

func TestImageCacheLookupHookShortCircuitsProviderInvoke(t *testing.T) {
	imageBytes := realPNGFixture(t)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Cache Lookup Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-cache-lookup-key", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_cache_lookup")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_image_cache_lookup", Name: "Image Cache Lookup", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_image_cache_lookup", ProviderID: provider.ID, Name: "Image Cache Lookup Key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_cache_lookup", ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: openAIImageModelName, Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-cache-lookup-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	server.imageRunner = func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
		t.Fatal("image runner should not be called on cache hit")
		return nil, "", Usage{}, nil
	}

	cacheLookup := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-cache",
		HookID:        "lookup",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		Writes:        []pluginmeta.GatewayDataClass{pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	cacheWrite := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-cache",
		HookID:        "write",
		Stage:         pluginmeta.StageCacheWrite,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	for _, hook := range []pluginmeta.GatewayHookDescriptor{cacheLookup, cacheWrite} {
		if err := server.gatewayChain.RegisterHook(hook); err != nil {
			t.Fatalf("register image cache hook %s: %v", hook.HookID, err)
		}
	}
	if err := server.gatewayHooks.RegisterHandler(cacheLookup, pluginmeta.GatewayHookHandlerFunc(func(_ context.Context, input pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		if len(input.Data[pluginmeta.DataRequestBody]) == 0 {
			t.Fatal("cache lookup did not receive image request body")
		}
		return rawProviderCallResult(t, gatewayImageProviderResponse{
			DataBase64:    encodeBase64(imageBytes),
			RevisedPrompt: "served from image cache",
		}, Usage{PromptTokens: 6, CompletionTokens: 2, TotalTokens: 8}), nil
	})); err != nil {
		t.Fatalf("register image cache lookup handler: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(cacheWrite, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		t.Fatal("cache write should not run after an image cache hit")
		return pluginmeta.GatewayHookResult{}, nil
	})); err != nil {
		t.Fatalf("register image cache write handler: %v", err)
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "cache image prompt", "response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("image cache hit failed: %d %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, encodeBase64(imageBytes)) || !strings.Contains(response.Body, "served from image cache") || !strings.Contains(response.Body, `"total_tokens":8`) {
		t.Fatalf("unexpected image cache response: %s", response.Body)
	}
}

func TestImageCacheLookupFailOpenContinuesToProviderInvoke(t *testing.T) {
	imageBytes := realPNGFixture(t)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Cache Lookup Failure Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-cache-lookup-failure-key", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_cache_lookup_failure")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_image_cache_lookup_failure", Name: "Image Cache Lookup Failure", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_image_cache_lookup_failure", ProviderID: provider.ID, Name: "Image Cache Lookup Failure Key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_cache_lookup_failure", ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: openAIImageModelName, Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-cache-lookup-failure-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	providerCalls := 0
	server.imageRunner = func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
		providerCalls++
		return imageBytes, "provider image", Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7}, nil
	}

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-cache",
		HookID:        "lookup-fail-open",
		Stage:         pluginmeta.StageCacheLookup,
		Priority:      1000,
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register image cache lookup hook: %v", err)
	}
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, errors.New("cache lookup unavailable")
	})); err != nil {
		t.Fatalf("register image cache lookup handler: %v", err)
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "provider fallback prompt", "response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("image fallback failed: %d %s", response.Code, response.Body)
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls = %d, want 1 after cache lookup fail-open", providerCalls)
	}
	if !strings.Contains(response.Body, "provider image") || !strings.Contains(response.Body, `"total_tokens":7`) {
		t.Fatalf("unexpected provider fallback response: %s", response.Body)
	}
}

func TestImageCacheWriteFailOpenPreservesProviderResponse(t *testing.T) {
	imageBytes := realPNGFixture(t)
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Image Cache Write Failure Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "image-cache-write-failure-key", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_image_cache_write_failure")
	if err != nil {
		t.Fatal(err)
	}
	provider := store.AddProvider(Provider{ID: "prv_image_cache_write_failure", Name: "Image Cache Write Failure", Type: ProviderOpenAI, Status: StatusActive, Healthy: true})
	resource, err := store.AddProviderResource(ProviderResource{ID: "rsrc_image_cache_write_failure", ProviderID: provider.ID, Name: "Image Cache Write Failure Key", ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true, MaxConcurrency: 10})
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	store.AddRoute(ModelRoute{ID: "route_image_cache_write_failure", ModelName: openAIImageModelName, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: openAIImageModelName, Status: StatusActive, Priority: 1, Weight: 100})
	server := NewWithConfig(store, Config{AdminToken: "test-admin-token", SecretKey: "image-cache-write-failure-secret", ImageStorageDir: t.TempDir()})
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	server.imageRunner = func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error) {
		return imageBytes, "provider write fallback", Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9}, nil
	}

	hook := pluginmeta.GatewayHookDescriptor{
		PluginID:      "tokenhub.test-image-cache",
		HookID:        "write-fail-open",
		Stage:         pluginmeta.StageCacheWrite,
		Priority:      1000,
		Reads:         []pluginmeta.GatewayDataClass{pluginmeta.DataRequestBody, pluginmeta.DataProviderResponse, pluginmeta.DataUsage},
		FailurePolicy: pluginmeta.FailurePolicyFailOpen,
	}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatalf("register image cache write hook: %v", err)
	}
	cacheWriteCalls := 0
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		cacheWriteCalls++
		return pluginmeta.GatewayHookResult{}, errors.New("cache write unavailable")
	})); err != nil {
		t.Fatalf("register image cache write handler: %v", err)
	}

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model": openAIImageModelName, "prompt": "cache write fail-open prompt", "response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("image response failed after cache write error: %d %s", response.Code, response.Body)
	}
	if cacheWriteCalls != 1 {
		t.Fatalf("cache write calls = %d, want 1", cacheWriteCalls)
	}
	if !strings.Contains(response.Body, "provider write fallback") || !strings.Contains(response.Body, `"total_tokens":9`) {
		t.Fatalf("unexpected provider response after cache write failure: %s", response.Body)
	}
}
