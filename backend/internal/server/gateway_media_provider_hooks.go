package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
)

func (s *Server) routeSupportsMedia(call CallContext, route RouteSelection) bool {
	descriptor, found := s.adapterRegistry.Describe(route.Provider.Type)
	_, implemented := resolveTypedAdapter[providerMedia](s.adapterRegistry, route.Provider.Type)
	return found && implemented && adapterSupports(descriptor, AdapterCapabilityMedia) || s.hasGatewayProviderCallHookForRoute(call, route, call.RouteProtocol)
}

func (s *Server) invokeMediaRoute(ctx context.Context, call CallContext, route RouteSelection, endpoint string, request mediaRequest) (mediaResponse, Usage, error) {
	return s.invokeMediaRouteWithResponseLimit(ctx, call, route, endpoint, request, maxMediaResponseBytes)
}

func (s *Server) invokeMediaRouteWithResponseLimit(ctx context.Context, call CallContext, route RouteSelection, endpoint string, request mediaRequest, responseLimit int) (mediaResponse, Usage, error) {
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
				usage, _ = inspectMediaStream(stream.Bytes(), route.Provider)
			}
			usage.MeteringInvalid = true
		}
		return mediaResponse{}, usage, err
	}
	if handled {
		response := mediaResponse{Body: stream.Bytes(), ContentType: "text/event-stream", Status: http.StatusOK}
		if !call.Stream {
			response, err = mediaProviderHookResponse(payload, responseLimit)
			if err != nil {
				// Hook output is already decoded; retain known usage when its
				// media exceeds the bound without parsing the large body again.
				if !usageReported {
					fields, _ := payload.(map[string]any)
					usage = usageFromMap(fields)
				}
				usage.MeteringInvalid = true
				return mediaResponse{}, usage, err
			}
		}
		if mediaResponseIsSSE(response.ContentType) {
			parsed, streamErr := inspectMediaStream(response.Body, route.Provider)
			if !usageReported {
				usage = parsed
			}
			if streamErr != nil {
				return mediaResponse{}, usage, uncertainMediaSubmission(streamErr)
			}
		} else if mediaResponseIsJSON(response.ContentType) {
			parsed, jsonErr := inspectMediaJSON(response.Body)
			if !usageReported {
				usage = parsed
			}
			if jsonErr != nil {
				return mediaResponse{}, usage, invalidMediaProviderHookResponse()
			}
		}
		return response, usage, nil
	}
	adapter, ok := resolveTypedAdapter[providerMedia](s.adapterRegistry, route.Provider.Type)
	if !ok || !s.routeSupportsAdapterCapability(route, AdapterCapabilityMedia) {
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
		if _, _, err := mime.ParseMediaType(contentType); err != nil || base64.StdEncoding.DecodedLen(len(*encoded)) > limit+2 {
			return mediaResponse{}, invalidMediaProviderHookResponse()
		}
		data, err = base64.StdEncoding.DecodeString(*encoded)
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
