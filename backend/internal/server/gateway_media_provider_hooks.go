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
	var payload any
	var usage Usage
	var handled bool
	var err error
	var stream mediaHookStreamBuffer
	if call.Stream {
		stream.limit = maxMediaResponseBytes
		payload, usage, handled, err = s.runGatewayProviderCallHooksOutput(ctx, call, route, request.Fields, call.RouteProtocol, &stream)
	} else {
		payload, usage, handled, err = s.runGatewayProviderCallHooks(ctx, call, route, request.Fields, call.RouteProtocol)
	}
	if err != nil {
		return mediaResponse{}, usage, err
	}
	if handled {
		if call.Stream {
			return mediaResponse{Body: stream.Bytes(), ContentType: "text/event-stream", Status: http.StatusOK}, usage, nil
		}
		response, err := mediaProviderHookResponse(payload)
		return response, usage, err
	}
	adapter, ok := resolveTypedAdapter[providerMedia](s.adapterRegistry, route.Provider.Type)
	if !ok || !s.routeSupportsAdapterCapability(route, AdapterCapabilityMedia) {
		return mediaResponse{}, usage, invalidMediaProviderHookResponse()
	}
	return adapter.Media(ctx, route.Provider, route.ProviderModel, endpoint, request)
}

// Non-JSON media returned by provider_call hooks uses an explicit envelope.
// JSON model responses remain unwrapped, including vendor extension fields.
func mediaProviderHookResponse(payload any) (mediaResponse, error) {
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
		if _, _, err := mime.ParseMediaType(contentType); err != nil || base64.StdEncoding.DecodedLen(len(*encoded)) > maxMediaResponseBytes+2 {
			return mediaResponse{}, invalidMediaProviderHookResponse()
		}
		data, err = base64.StdEncoding.DecodeString(*encoded)
		if err != nil {
			return mediaResponse{}, invalidMediaProviderHookResponse()
		}
	}
	if len(data) > maxMediaResponseBytes {
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
