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
