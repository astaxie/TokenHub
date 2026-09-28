package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

// Buffered media can carry a large base64 image in a single event. Its existing
// total response limit still applies without imposing the text-stream ceiling.
func newMediaSSEDecoder(body []byte) *sseDecoder {
	decoder := newSSEDecoder(bytes.NewReader(body))
	decoder.assembler.limit = maxMediaResponseBytes
	return decoder
}

func mediaResponseIsSSE(contentType string) bool {
	return mediaResponseMIMEType(contentType) == "text/event-stream"
}

// Inspect the original event bytes before plugins can transform or drop them.
// Usage remains billable even when a later error or policy blocks delivery.
func inspectMediaStream(body []byte, provider Provider) (Usage, error) {
	decoder := newMediaSSEDecoder(body)
	var usage Usage
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			return usage, nil
		}
		if err != nil {
			return usage, err
		}
		var probe providerStreamEventProbe
		data := strings.TrimSpace(event.Data)
		invalid := data != "" && data != "[DONE]" && (decodeResponsesJSON([]byte(data), &probe) != nil || probe == nil)
		if parsed, ok := probe.usage(); ok {
			usage = parsed
		} else {
			var response providerStreamEventProbe
			if json.Unmarshal(probe["response"], &response) == nil {
				if parsed, ok := response.usage(); ok {
					usage = parsed
				}
			}
		}
		if invalid {
			usage.MeteringInvalid = true
			return usage, NewHTTPError(http.StatusBadGateway, "invalid_media_response", "Media provider returned an invalid JSON stream event")
		}
		if sseEventNameIsError(event.Event) || probe.isError() {
			if failure, ok := openAIErrorFrame(event, provider); ok {
				return usage, failure
			}
			return usage, NewHTTPError(http.StatusBadGateway, "provider_stream_error", "Media provider reported a failed stream")
		}
	}
}

func (s *Server) finishMediaStreamHooks(ctx context.Context, call CallContext, route RouteSelection, result mediaResponse, usage Usage) (mediaResponse, Usage, error) {
	if s.hasGatewayStreamTransformHooksForRoute(route, call.RouteProtocol) {
		decoder := newMediaSSEDecoder(result.Body)
		output := mediaHookStreamBuffer{limit: maxMediaResponseBytes}
		for {
			event, err := decoder.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return result, usage, err
			}
			data := event.Raw
			if event.Event != "" || event.Data != "" {
				transformed, emit, err := s.runGatewayStreamTransformHooks(ctx, call, route, call.RouteProtocol, event)
				if err != nil {
					return result, usage, err
				}
				if !emit {
					continue
				}
				if transformed.Event != event.Event || transformed.Data != event.Data {
					data = renderSSEEvent(transformed)
				}
			}
			if _, err := output.Write(data); err != nil {
				return result, usage, err
			}
		}
		result.Body = output.Bytes()
		if _, err := inspectMediaStream(result.Body, route.Provider); err != nil {
			return result, usage, invalidMediaProviderHookResponse()
		}
	}
	if len(s.gatewayRouteHooksForRoute(pluginmeta.StageUsageAttribution, route, call.RouteProtocol, true)) == 0 {
		return result, usage, nil
	}
	payload := map[string]any{"data_base64": base64.StdEncoding.EncodeToString(result.Body)}
	attributed, err := s.runGatewayUsageAttributionHooks(ctx, call, route, payload, usage, call.RouteProtocol)
	if err != nil {
		return result, usage, err
	}
	return result, attributed, nil
}
