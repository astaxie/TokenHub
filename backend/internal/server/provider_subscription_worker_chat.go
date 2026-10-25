package server

import (
	"context"
	"io"
	"net/http"
)

func (a *SubscriptionWorkerAdapter) codec(process *subscriptionWorkerProcess, id string, model subscriptionWorkerModel, stream bool) (ProviderAdapter, Provider, error) {
	transport := &subscriptionWorkerTransport{adapter: a, process: process, resourceID: id, model: model, stream: stream}
	client := &http.Client{Transport: transport}
	provider := Provider{Type: ProviderOpenAICompatible, BaseURL: "https://subscription.tokenhub.invalid", Options: map[string]string{}}
	switch model.Protocol {
	case "chat", "responses":
		return OpenAICompatibleAdapter{Client: client, StreamClient: client, StreamIdleTimeout: a.StreamIdleTimeout}, provider, nil
	case "anthropic":
		provider.Type = ProviderAnthropic
		return AnthropicAdapter{Client: client, StreamClient: client, StreamIdleTimeout: a.StreamIdleTimeout}, provider, nil
	case "gemini":
		provider.Type = ProviderGemini
		return GeminiAdapter{Client: client, StreamClient: client, StreamIdleTimeout: a.StreamIdleTimeout}, provider, nil
	default:
		return nil, provider, NewHTTPError(502, "subscription_protocol_unsupported", "Subscription model uses an unsupported inference protocol")
	}
}

func (a *SubscriptionWorkerAdapter) chat(ctx context.Context, provider Provider, modelID string, request ChatCompletionRequest) (any, Usage, error) {
	process, release, id, err := a.account(ctx, provider, nil)
	if err != nil {
		return nil, Usage{}, err
	}
	defer release()
	if err := a.checkStandardAllowance(ctx, process, id); err != nil {
		return nil, Usage{}, err
	}
	model, err := a.workerModel(ctx, process, id, modelID)
	if err != nil {
		return nil, Usage{}, err
	}
	codec, proxy, err := a.codec(process, id, model, model.Protocol == "responses")
	if err != nil {
		return nil, Usage{}, err
	}
	if model.Protocol != "responses" {
		return codec.Chat(ctx, proxy, model.ID, request)
	}
	converted, err := chatToSubscriptionResponsesRequest(request)
	if err != nil {
		return nil, Usage{}, err
	}
	response, err := codec.(OpenAICompatibleAdapter).OpenResponses(ctx, proxy, model.ID, converted, nil)
	if err != nil {
		return nil, Usage{}, err
	}
	defer response.Body.Close()
	body, _, usage, err := consumeSubscriptionResponsesStream(response.Body, nil)
	if err != nil {
		return nil, usage, err
	}
	result, err := codexResponsesToChat(body, request, usage)
	return result, usage, err
}

func (a *SubscriptionWorkerAdapter) chatStream(ctx context.Context, provider Provider, modelID string, request ChatCompletionRequest, writer io.Writer) (Usage, error) {
	process, release, id, err := a.account(ctx, provider, nil)
	if err != nil {
		return Usage{}, err
	}
	defer release()
	if err := a.checkStandardAllowance(ctx, process, id); err != nil {
		return Usage{}, err
	}
	model, err := a.workerModel(ctx, process, id, modelID)
	if err != nil {
		return Usage{}, err
	}
	codec, proxy, err := a.codec(process, id, model, true)
	if err != nil {
		return Usage{}, err
	}
	if model.Protocol != "responses" {
		return codec.(interface {
			ChatStream(context.Context, Provider, string, ChatCompletionRequest, io.Writer) (Usage, error)
		}).ChatStream(ctx, proxy, model.ID, request, writer)
	}
	converted, err := chatToSubscriptionResponsesRequest(request)
	if err != nil {
		return Usage{}, err
	}
	response, err := codec.(OpenAICompatibleAdapter).OpenResponses(ctx, proxy, model.ID, converted, nil)
	if err != nil {
		return Usage{}, err
	}
	defer response.Body.Close()
	reverseNames, err := chatCodexToolNameReverse(request.Tools)
	if err != nil {
		return Usage{}, err
	}
	sink := &codexChatStreamSink{encoder: newOpenAIChatStreamEncoder(writer, request.Model, streamUsageRequested(request)), reverseNames: reverseNames, toolSlots: map[string]int{}}
	return consumeCodexCompatibilityStream(response.Body, sink)
}
