package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
)

type gatewayImageProviderResponse struct {
	DataBase64    string `json:"data_base64"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

func (s *Server) invokeImageRouteWithGatewayHooks(ctx context.Context, call CallContext, route RouteSelection, job ImageJob) (imageRunResult, Usage, error) {
	prepared, err := s.prepareRouteForUpstream(ctx, route)
	if err != nil {
		return imageRunResult{}, Usage{}, err
	}
	request, err := s.providerImageGenerationRequest(prepared, job)
	if err != nil {
		return imageRunResult{}, Usage{}, err
	}
	if err := s.runGatewayProviderImageRequestTransformHooks(ctx, call, prepared, &request); err != nil {
		return imageRunResult{}, Usage{}, err
	}
	if response, usage, hit, err := s.runGatewayCacheLookupHooks(ctx, call, request); err != nil || hit {
		if err != nil {
			return imageRunResult{}, Usage{}, err
		}
		result, err := imageRunResultFromGatewayProviderResponse(response)
		if err != nil {
			return imageRunResult{}, Usage{}, err
		}
		result.providerRequest = request
		result.cacheHit = true
		return result, usage, nil
	}
	if response, usage, handled, err := s.runGatewayProviderCallHooks(ctx, call, prepared, request, providerRouteProtocolImageGeneration); err != nil || handled {
		if err != nil {
			return imageRunResult{}, Usage{}, err
		}
		result, err := imageRunResultFromGatewayProviderResponse(response)
		if err != nil {
			return imageRunResult{}, Usage{}, err
		}
		result.providerRequest = request
		return result, usage, nil
	}
	transformedJob := imageJobForProviderRequest(job, request)
	runner := s.imageRunner
	if runner != nil {
		imageBytes, revisedPrompt, usage, err := runner(ctx, prepared, transformedJob)
		if err != nil {
			return imageRunResult{}, usage, err
		}
		return imageRunResult{data: imageBytes, revisedPrompt: revisedPrompt, providerRequest: request}, usage, nil
	}
	if job.Model == codexImageModelName {
		imageBytes, revisedPrompt, usage, err := s.executeCodexSubscriptionImage(ctx, prepared, transformedJob)
		if err != nil {
			return imageRunResult{}, usage, err
		}
		return imageRunResult{data: imageBytes, revisedPrompt: revisedPrompt, providerRequest: request}, usage, nil
	}
	adapter, ok := resolveTypedAdapter[ProviderImageGenerator](s.adapterRegistry, prepared.Provider.Type)
	if !ok {
		return imageRunResult{}, Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support image generation")
	}
	imageBytes, revisedPrompt, usage, err := adapter.GenerateImage(ctx, prepared.Provider, prepared.ProviderModel, request)
	if err != nil {
		return imageRunResult{}, usage, err
	}
	return imageRunResult{data: imageBytes, revisedPrompt: revisedPrompt, providerRequest: request}, usage, nil
}

func (s *Server) finishImageGatewayHooks(ctx context.Context, call CallContext, route RouteSelection, result imageRunResult, usage Usage) (imageRunResult, Usage, error) {
	previousRequest := result.providerRequest
	previousCacheHit := result.cacheHit
	response := gatewayImageProviderResponseFromRunResult(result)
	post, err := s.runGatewayGuardrailPostHooks(ctx, call, route, response, usage)
	if err != nil {
		return imageRunResult{}, usage, err
	}
	post, err = s.runGatewayResponsePostHooks(ctx, call, route, post)
	if err != nil {
		return imageRunResult{}, usage, err
	}
	result, err = imageRunResultFromGatewayProviderResponse(post)
	if err != nil {
		return imageRunResult{}, usage, err
	}
	result.providerRequest = previousRequest
	result.cacheHit = previousCacheHit
	usage, err = s.runGatewayUsageAttributionHooks(ctx, call, route, post, usage)
	if err != nil {
		return imageRunResult{}, usage, err
	}
	if !result.cacheHit && result.providerRequest.Model != "" {
		s.runGatewayCacheWriteHooks(ctx, call, result.providerRequest, post, usage)
	}
	return result, usage, nil
}

func (s *Server) runGatewayProviderImageRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *ProviderImageGenerationRequest) error {
	if req == nil {
		return nil
	}
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, func(data json.RawMessage) error {
		originalAction := req.Action
		originalModel := req.Model
		var patched ProviderImageGenerationRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if strings.TrimSpace(patched.Action) != originalAction {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested image action")
		}
		if strings.TrimSpace(patched.Model) != originalModel {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
		}
		*req = patched
		return nil
	})
}

func imageJobForProviderRequest(job ImageJob, request ProviderImageGenerationRequest) ImageJob {
	job.Action = request.Action
	job.Prompt = request.Prompt
	job.Quality = request.Quality
	job.Size = request.Size
	return job
}

func gatewayImageProviderResponseFromRunResult(result imageRunResult) gatewayImageProviderResponse {
	return gatewayImageProviderResponse{
		DataBase64:    base64.StdEncoding.EncodeToString(result.data),
		RevisedPrompt: result.revisedPrompt,
	}
}

func imageRunResultFromGatewayProviderResponse(response any) (imageRunResult, error) {
	var body gatewayImageProviderResponse
	data, err := json.Marshal(response)
	if err != nil {
		return imageRunResult{}, NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway image plugin returned an invalid response")
	}
	if err := decodeGatewayHookPayload(data, &body, "gateway_hook_response_invalid", "Gateway image plugin returned an invalid response"); err != nil {
		return imageRunResult{}, err
	}
	if strings.TrimSpace(body.DataBase64) == "" {
		return imageRunResult{}, NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway image plugin did not return image data")
	}
	imageBytes, err := decodeGeneratedImage(body.DataBase64)
	if err != nil {
		return imageRunResult{}, NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", err.Error())
	}
	return imageRunResult{data: imageBytes, revisedPrompt: body.RevisedPrompt}, nil
}
