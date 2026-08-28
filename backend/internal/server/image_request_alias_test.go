package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestImageGenerationRequestAliasUsesPluginMetadata(t *testing.T) {
	server := New(NewMemoryStore())
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi-image-alias",
		ActionID:   "kimi.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "kimi_subscription",
		Metadata: map[string]string{
			"public_model":                    "kimi-image",
			"upstream_model":                  "moonshot-image",
			"request_alias.model":             openAIImageModelName,
			"request_alias.header":            "x-kimi-image-turn-id",
			"request_alias.originator_prefix": "kimi",
			"request_alias.response_format":   "b64_json",
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}

	plain := imageGenerationRequest{Model: openAIImageModelName, ResponseFormat: "url"}
	server.applyImageGenerationRequestAliases(httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil), &plain)
	if plain.Model != openAIImageModelName || plain.ResponseFormat != "url" {
		t.Fatalf("plain request was unexpectedly aliased: %+v", plain)
	}

	byHeader := imageGenerationRequest{Model: openAIImageModelName, ResponseFormat: "url"}
	headerRequest := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	headerRequest.Header.Set("x-kimi-image-turn-id", "turn_123")
	server.applyImageGenerationRequestAliases(headerRequest, &byHeader)
	if byHeader.Model != "kimi-image" || byHeader.ResponseFormat != "b64_json" {
		t.Fatalf("header alias did not apply: %+v", byHeader)
	}

	byOriginator := imageGenerationRequest{Model: openAIImageModelName}
	originatorRequest := httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	originatorRequest.Header.Set("Originator", "kimi_cli")
	server.applyImageGenerationRequestAliases(originatorRequest, &byOriginator)
	if byOriginator.Model != "kimi-image" || byOriginator.ResponseFormat != "b64_json" {
		t.Fatalf("originator alias did not apply: %+v", byOriginator)
	}
}

func TestImageGenerationDefaultModelUsesPluginMetadata(t *testing.T) {
	server := &Server{pluginActions: pluginmeta.NewActionBroker()}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi-image-default",
		ActionID:   "kimi.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "kimi_subscription",
		Metadata: map[string]string{
			"public_model":          "kimi-image",
			"upstream_model":        "moonshot-image",
			"request.default_model": "true",
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}

	request := imageGenerationRequest{Prompt: "Draw one square.", N: 1, Quality: "low", Size: "1024x1024"}
	if err := server.normalizeImageGenerationRequest(&request); err != nil {
		t.Fatal(err)
	}
	if request.Model != "kimi-image" {
		t.Fatalf("default image model = %q, want kimi-image", request.Model)
	}
}

func TestImageModelMaskSupportUsesPluginMetadata(t *testing.T) {
	server := &Server{pluginActions: pluginmeta.NewActionBroker()}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi-image-mask",
		ActionID:   "kimi.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "kimi_subscription",
		Metadata: map[string]string{
			"public_model":              "kimi-image",
			"upstream_model":            "moonshot-image",
			"request.supports_mask":     "false",
			"image_request_alias_model": openAIImageModelName,
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}

	if server.imageModelSupportsMask("kimi-image") {
		t.Fatal("plugin image model should use mask support from metadata")
	}
	if !server.imageModelSupportsMask(openAIImageModelName) {
		t.Fatal("non-profile image model should support masks by default")
	}
}

func TestImageRequestAllowedSizesUsePluginMetadata(t *testing.T) {
	server := &Server{pluginActions: pluginmeta.NewActionBroker()}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi-image-size",
		ActionID:   "kimi.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "kimi_subscription",
		Metadata: map[string]string{
			"public_model":          "kimi-image",
			"upstream_model":        "moonshot-image",
			"request.allowed_sizes": "auto,2048x2048",
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}

	accepted := imageGenerationRequest{Model: "kimi-image", Prompt: "Draw one square.", N: 1, Quality: "low", Size: "2048x2048"}
	if err := server.normalizeImageGenerationRequest(&accepted); err != nil {
		t.Fatalf("plugin allowed size was rejected: %v", err)
	}

	rejected := imageGenerationRequest{Model: "kimi-image", Prompt: "Draw one square.", N: 1, Quality: "low", Size: "1024x1024"}
	if err := server.normalizeImageGenerationRequest(&rejected); err == nil || AsHTTPError(err).Code != "invalid_size" {
		t.Fatalf("plugin disallowed size was not rejected: %v", err)
	}
}

func TestImageRequestAllowedQualitiesUsePluginMetadata(t *testing.T) {
	server := &Server{pluginActions: pluginmeta.NewActionBroker()}
	if err := server.pluginActions.Register(pluginmeta.ActionDescriptor{
		PluginID:   "tokenhub.provider.kimi-image-quality",
		ActionID:   "kimi.image_capability.configure",
		Kind:       pluginmeta.ActionKindMutate,
		Capability: "image.capability.configure",
		Subject:    "kimi_subscription",
		Metadata: map[string]string{
			"public_model":              "kimi-image",
			"upstream_model":            "moonshot-image",
			"request.allowed_qualities": "standard",
			"request.allowed_sizes":     "1024x1024",
		},
	}, pluginmeta.ActionHandlerFunc(func(context.Context, pluginmeta.ActionInvocation) (pluginmeta.ActionResult, error) {
		return pluginmeta.ActionResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}

	accepted := imageGenerationRequest{Model: "kimi-image", Prompt: "Draw one square.", N: 1, Quality: "standard", Size: "1024x1024"}
	if err := server.normalizeImageGenerationRequest(&accepted); err != nil {
		t.Fatalf("plugin allowed quality was rejected: %v", err)
	}

	rejected := imageGenerationRequest{Model: "kimi-image", Prompt: "Draw one square.", N: 1, Quality: "low", Size: "1024x1024"}
	if err := server.normalizeImageGenerationRequest(&rejected); err == nil || AsHTTPError(err).Code != "invalid_quality" {
		t.Fatalf("plugin disallowed quality was not rejected: %v", err)
	}
}
