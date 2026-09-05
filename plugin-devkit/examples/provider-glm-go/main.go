package main

import (
	"context"
	"fmt"
	"os"

	"github.com/astaxie/TokenHub/plugin-devkit/sdk/go/tokenhubplugin"
)

func main() {
	os.Exit(tokenhubplugin.ServeProvider(context.Background(), os.Stdin, os.Stdout, os.Stderr, handleProvider))
}

func handleProvider(ctx context.Context, invocation tokenhubplugin.ProviderInvocation) (tokenhubplugin.ProviderResult, error) {
	if invocation.Provider.Type != "glm_subscription" {
		return tokenhubplugin.ProviderResult{}, fmt.Errorf("provider type %q is not supported", invocation.Provider.Type)
	}
	if invocation.Credentials.APIKey == "" {
		return tokenhubplugin.ProviderResult{}, fmt.Errorf("provider api key is required")
	}
	switch invocation.Operation {
	case tokenhubplugin.OperationChat:
		if err := requireRequestField(invocation, "messages"); err != nil {
			return tokenhubplugin.ProviderResult{}, err
		}
		return tokenhubplugin.ProviderResult{
			Response: map[string]any{
				"id":     "chatcmpl_glm_subscription",
				"object": "chat.completion",
				"choices": []map[string]any{{
					"message": map[string]any{"role": "assistant", "content": "glm subscription chat"},
				}},
			},
			Usage: &tokenhubplugin.Usage{PromptTokens: 2, CompletionTokens: 3, TotalTokens: 5},
		}, nil
	case tokenhubplugin.OperationChatStream:
		if err := requireRequestField(invocation, "stream"); err != nil {
			return tokenhubplugin.ProviderResult{}, err
		}
		return tokenhubplugin.ProviderResult{
			Events: []tokenhubplugin.StreamEvent{
				{Data: map[string]any{"id": "chatcmpl_glm_subscription_stream", "object": "chat.completion.chunk", "choices": []map[string]any{{"delta": map[string]any{"content": "glm subscription stream"}}}}},
				{Data: "[DONE]"},
			},
			Usage: &tokenhubplugin.Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		}, nil
	case tokenhubplugin.OperationResponses:
		if err := requireRequestField(invocation, "input"); err != nil {
			return tokenhubplugin.ProviderResult{}, err
		}
		return tokenhubplugin.ProviderResult{
			Response: map[string]any{"id": "resp_glm_subscription", "object": "response", "status": "completed", "output_text": "glm subscription responses"},
			Usage:    &tokenhubplugin.Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 9},
		}, nil
	case tokenhubplugin.OperationResponsesStream:
		if err := requireRequestField(invocation, "stream"); err != nil {
			return tokenhubplugin.ProviderResult{}, err
		}
		return tokenhubplugin.ProviderResult{
			Events: []tokenhubplugin.StreamEvent{
				{Event: "response.output_text.delta", Data: map[string]any{"type": "response.output_text.delta", "delta": "glm subscription responses stream"}},
				{Event: "response.completed", Data: map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_glm_subscription_stream", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 5, "output_tokens": 6, "total_tokens": 11}}}},
			},
		}, nil
	case tokenhubplugin.OperationEmbeddings:
		if err := requireRequestField(invocation, "input"); err != nil {
			return tokenhubplugin.ProviderResult{}, err
		}
		return tokenhubplugin.ProviderResult{
			Response: map[string]any{"object": "list", "data": []map[string]any{{"index": 0, "embedding": []float64{0.1, 0.2, 0.3}}}, "model": "glm-upstream-embeddings"},
			Usage:    &tokenhubplugin.Usage{PromptTokens: 6, TotalTokens: 6},
		}, nil
	case tokenhubplugin.OperationModels:
		return tokenhubplugin.ProviderResult{
			Status: 200,
			Catalog: map[string]any{
				"id": "glm-subscription", "name": "GLM Subscription", "display_name": "GLM Subscription", "type": "glm_subscription", "models_count": 2, "source": "plugin-live", "etag": "glm-etag",
				"models": []map[string]any{{"id": "glm-subscription-chat", "name": "glm-subscription-chat", "type": "chat"}, {"id": "glm-subscription-embed", "name": "glm-subscription-embed", "type": "embedding"}},
			},
		}, nil
	case tokenhubplugin.OperationProbe:
		resourceID := "rsrc_glm_subscription"
		if invocation.Resource != nil && invocation.Resource.ID != "" {
			resourceID = invocation.Resource.ID
		}
		return tokenhubplugin.ProviderResult{
			Result: map[string]any{"resource_id": resourceID, "model": "glm-upstream-chat", "output_text": "glm subscription provider is reachable", "latency_ms": 12},
		}, nil
	default:
		return tokenhubplugin.ProviderResult{}, fmt.Errorf("provider operation %q is not supported", invocation.Operation)
	}
}

func requireRequestField(invocation tokenhubplugin.ProviderInvocation, field string) error {
	request, err := tokenhubplugin.DecodeRequest[map[string]any](invocation)
	if err != nil {
		return err
	}
	if _, ok := request[field]; !ok {
		return fmt.Errorf("request field %q is required", field)
	}
	return nil
}
