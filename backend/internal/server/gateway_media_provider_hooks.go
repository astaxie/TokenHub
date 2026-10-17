package server

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
)

func (s *Server) routeSupportsMedia(call CallContext, route RouteSelection, request mediaRequest) bool {
	audio := isAudioMediaProtocol(call.RouteProtocol)
	if audio {
		endpoints, compatible := s.audioRouteModelEndpoints(route.Provider.ID, route.ProviderModel)
		if !compatible || !audioEndpointDeclared(call.RouteProtocol, call.Model.Metadata["endpoints"], endpoints) {
			return false
		}
	}
	descriptor, found := s.adapterRegistry.Describe(route.Provider.Type)
	_, implemented := resolveTypedAdapter[providerMedia](s.adapterRegistry, route.Provider.Type)
	if found && implemented && adapterSupports(descriptor, mediaCapabilityForProtocol(call.RouteProtocol)) && (!audio || adapterDescriptorRouteProtocolSet(descriptor)[call.RouteProtocol]) {
		return true
	}
	return !request.requiresNativeTranscription() && s.hasGatewayProviderCallHookForRoute(call, route, call.RouteProtocol)
}

func (m mediaRequest) requiresNativeTranscription() bool {
	return m.Multipart && mediaCapabilityForProtocol(strings.TrimPrefix(m.Path, "/v1/")) == AdapterCapabilityTranscription
}

func mediaCapabilityForProtocol(protocol string) AdapterCapability {
	switch strings.TrimPrefix(protocol, "/") {
	case "audio/transcriptions", "audio/translations":
		return AdapterCapabilityTranscription
	default:
		return AdapterCapabilityMedia
	}
}

func (s *Server) invokeMediaRoute(ctx context.Context, call CallContext, route RouteSelection, endpoint string, request mediaRequest) (mediaResponse, Usage, error) {
	return s.invokeMediaRouteWithResponseLimit(ctx, call, route, endpoint, request, maxMediaResponseBytes)
}

func (s *Server) invokeMediaRouteWithResponseLimit(ctx context.Context, call CallContext, route RouteSelection, endpoint string, request mediaRequest, responseLimit int) (mediaResponse, Usage, error) {
	if err := request.validateTransformedAudioBounds(route.ProviderModel); err != nil {
		return mediaResponse{}, Usage{}, &ProviderInvocationError{Err: err, Disposition: ProviderErrorPolicy}
	}
	var payload any
	var usage Usage
	var handled, usageReported bool
	var err error
	var stream mediaHookStreamBuffer
	if call.Stream {
		stream.limit = responseLimit
		payload, usage, handled, err = s.runGatewayProviderCallHooksOutputWithUsagePresence(ctx, call, route, request.Fields, call.RouteProtocol, &stream, &usageReported)
	} else {
		payload, usage, handled, err = s.runGatewayProviderCallHooksOutputWithUsagePresence(ctx, call, route, request.Fields, call.RouteProtocol, nil, &usageReported)
	}
	if err != nil {
		if call.Stream && handled {
			// A buffer limit can fail after complete billable events were written.
			// Preserve their usage without replacing the original plugin failure.
			if !usageReported {
				usage, _ = inspectMediaStream(stream.Bytes(), route.Provider, endpoint)
			}
			usage.MeteringInvalid = true
		}
		if mediaCapabilityForProtocol(endpoint) == AdapterCapabilityTranscription {
			err = sanitizeTranscriptionProviderError(err)
		}
		return mediaResponse{}, usage, err
	}
	if handled {
		// Provider hooks receive text fields only. Do not accept a transcription
		// response from a hook that could not inspect the uploaded audio.
		if request.requiresNativeTranscription() {
			return mediaResponse{}, Usage{}, &ProviderInvocationError{
				Err:         NewHTTPError(http.StatusNotImplemented, "multipart_transcription_hook_unsupported", "Multipart transcription requires a native adapter; provider-call hooks can handle JSON requests only"),
				Disposition: ProviderErrorPolicy,
			}
		}
		response := mediaResponse{Body: stream.Bytes(), ContentType: "text/event-stream", Status: http.StatusOK}
		if !call.Stream {
			response, err = mediaProviderHookResponse(payload, responseLimit)
			if err != nil {
				// Hook output is already decoded; retain known usage when its
				// media exceeds the bound without parsing the large body again.
				if !usageReported {
					fields, _ := payload.(map[string]any)
					usage = captureAudioDuration(mediaUsageFromMap(fields), fields, audioTranscriptionEndpoint([]string{endpoint}))
				}
				usage.MeteringInvalid = true
				return mediaResponse{}, usage, err
			}
		}
		if mediaResponseIsSSE(response.ContentType) {
			parsed, streamErr := inspectMediaStream(response.Body, route.Provider, endpoint)
			if !usageReported {
				usage = parsed
			}
			if streamErr != nil {
				if mediaCapabilityForProtocol(endpoint) == AdapterCapabilityTranscription {
					streamErr = sanitizeTranscriptionProviderError(streamErr)
				}
				return mediaResponse{}, usage, uncertainMediaSubmission(streamErr)
			}
		} else if mediaResponseIsJSON(response.ContentType) {
			parsed, jsonErr := inspectMediaJSON(response.Body, endpoint)
			if !usageReported {
				usage = parsed
			}
			if jsonErr != nil {
				return mediaResponse{}, usage, invalidMediaProviderHookResponse()
			}
		}
		if err := validateAudioDuration(usage); err != nil {
			usage.MeteringInvalid = true
			return mediaResponse{}, usage, invalidMediaProviderHookResponse()
		}
		return response, usage, nil
	}
	adapter, ok := resolveTypedAdapter[providerMedia](s.adapterRegistry, route.Provider.Type)
	if !ok || !s.routeSupportsAdapterCapability(route, mediaCapabilityForProtocol(endpoint)) {
		return mediaResponse{}, usage, invalidMediaProviderHookResponse()
	}
	return adapter.Media(ctx, route.Provider, route.ProviderModel, endpoint, request)
}

// Non-JSON media returned by provider_call hooks uses an explicit envelope.
// JSON model responses remain unwrapped, including vendor extension fields.
func mediaProviderHookResponse(payload any, limit int) (mediaResponse, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return mediaResponse{}, invalidMediaProviderHookResponse()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return mediaResponse{}, invalidMediaProviderHookResponse()
	}
	contentType := "application/json"
	if raw, binary := fields["data_base64"]; binary {
		var encoded, declaredType *string
		if json.Unmarshal(raw, &encoded) != nil || encoded == nil || json.Unmarshal(fields["content_type"], &declaredType) != nil || declaredType == nil {
			return mediaResponse{}, invalidMediaProviderHookResponse()
		}
		contentType = *declaredType
		if strings.ContainsAny(contentType, "\r\n") {
			return mediaResponse{}, invalidMediaProviderHookResponse()
		}
		if _, _, err := mime.ParseMediaType(contentType); err != nil {
			return mediaResponse{}, invalidMediaProviderHookResponse()
		}
		data, err = decodeMediaBase64(*encoded, limit)
		if err != nil {
			return mediaResponse{}, invalidMediaProviderHookResponse()
		}
	}
	if len(data) > limit {
		return mediaResponse{}, invalidMediaProviderHookResponse()
	}
	return mediaResponse{Body: data, ContentType: contentType, Status: http.StatusOK}, nil
}

func invalidMediaProviderHookResponse() error {
	return &ProviderInvocationError{Err: NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway provider plugin returned an invalid media response"), Disposition: ProviderErrorPolicy}
}

type mediaHookStreamBuffer struct {
	bytes.Buffer
	limit int
}

func (b *mediaHookStreamBuffer) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		return 0, invalidMediaProviderHookResponse()
	}
	return b.Buffer.Write(data)
}
