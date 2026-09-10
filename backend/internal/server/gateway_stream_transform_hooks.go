package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type gatewayStreamEventView struct {
	Event string `json:"event,omitempty"`
	Data  string `json:"data,omitempty"`
}

type gatewayStreamEventPatch struct {
	Event *string `json:"event,omitempty"`
	Data  *string `json:"data,omitempty"`
	Drop  bool    `json:"drop,omitempty"`
}

type gatewayStreamTransformWriter struct {
	server   *Server
	ctx      context.Context
	call     CallContext
	route    RouteSelection
	protocol string
	sink     io.Writer
	decoder  *sseStreamWriter
}

func (s *Server) hasGatewayStreamTransformHooksForRoute(route RouteSelection, protocol string) bool {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageStreamTransform, pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		if len(s.gatewayRouteHooksForRoute(stage, route, protocol, true)) > 0 {
			return true
		}
	}
	return false
}

func (s *Server) newGatewayStreamTransformWriter(ctx context.Context, call CallContext, route RouteSelection, protocol string, sink io.Writer) *gatewayStreamTransformWriter {
	writer := &gatewayStreamTransformWriter{server: s, ctx: ctx, call: call, route: route, protocol: protocol, sink: sink}
	writer.decoder = newSSEStreamWriter(writer.handleEvent)
	return writer
}

func (s *Server) streamChatRouteWithGatewayTransforms(ctx context.Context, call CallContext, route RouteSelection, req ChatCompletionRequest, headers http.Header, writer io.Writer) (Usage, error) {
	streamWriter := writer
	var transformer *gatewayStreamTransformWriter
	if s.hasGatewayStreamTransformHooksForRoute(route, providerRouteProtocolChatCompletions) {
		transformer = s.newGatewayStreamTransformWriter(ctx, call, route, providerRouteProtocolChatCompletions, writer)
		streamWriter = transformer
	}
	_, usage, handled, err := s.runGatewayProviderCallHooksOutput(ctx, call, route, req, providerRouteProtocolChatCompletions, streamWriter)
	if err == nil && !handled {
		usage, err = s.streamChatRoute(ctx, route, req, headers, streamWriter)
	}
	if transformer != nil {
		if closeErr := transformer.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}
	return usage, err
}

func (w *gatewayStreamTransformWriter) Write(data []byte) (int, error) {
	if w == nil || w.decoder == nil {
		return 0, io.ErrClosedPipe
	}
	return w.decoder.Write(data)
}

func (w *gatewayStreamTransformWriter) Close() error {
	if w == nil || w.decoder == nil {
		return nil
	}
	return w.decoder.Close()
}

func (w *gatewayStreamTransformWriter) Flush() {
	if flusher, ok := w.sink.(streamFlusher); ok {
		flusher.Flush()
	}
}

func (w *gatewayStreamTransformWriter) handleEvent(event serverSentEvent) error {
	if event.Event == "" && event.Data == "" {
		_, err := w.sink.Write(event.Raw)
		return err
	}
	transformed, emit, err := w.server.runGatewayStreamTransformHooks(w.ctx, w.call, w.route, w.protocol, event)
	if err != nil {
		return err
	}
	if !emit {
		return nil
	}
	_, err = w.sink.Write(renderSSEEvent(transformed))
	return err
}

func (s *Server) runGatewayStreamTransformHooks(ctx context.Context, call CallContext, route RouteSelection, protocol string, event serverSentEvent) (serverSentEvent, bool, error) {
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageStreamTransform, pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost} {
		next, emit, err := s.runGatewayStreamStageHooks(ctx, call, route, protocol, event, stage)
		if err != nil || !emit {
			return event, emit, err
		}
		event = next
	}
	return event, true, nil
}

func (s *Server) runGatewayStreamStageHooks(ctx context.Context, call CallContext, route RouteSelection, protocol string, event serverSentEvent, stage pluginmeta.GatewayHookStage) (serverSentEvent, bool, error) {
	hooks := s.gatewayRouteHooksForRoute(stage, route, protocol, true)
	if len(hooks) == 0 {
		return event, true, nil
	}
	eventData, ok := marshalGatewayHookData(gatewayStreamEventView{Event: event.Event, Data: event.Data})
	if !ok {
		return event, true, NewHTTPError(500, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:       "v1",
			Protocol:      "gateway",
			RouteProtocol: protocol,
			Operation:     string(stage),
			Model:         call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{
			pluginmeta.DataStreamEvents: eventData,
		},
	}

	if stage != pluginmeta.StageStreamTransform && json.Valid([]byte(event.Data)) {
		input.Data[pluginmeta.DataProviderResponse] = json.RawMessage(event.Data)
		var object map[string]json.RawMessage
		if json.Unmarshal([]byte(event.Data), &object) == nil {
			if usage := object["usage"]; len(usage) > 0 {
				input.Data[pluginmeta.DataUsage] = usage
			}
		}
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:         gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata:     call.Project,
		pluginmeta.DataAPIKeyMetadata:      gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataProviderCredentials: gatewayProviderCredentialsView(route),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	if routeData, ok := marshalGatewayHookData(gatewayRouteCandidateView{
		RouteID:          route.Route.ID,
		ProviderID:       route.Provider.ID,
		ProviderType:     route.Provider.Type,
		ProviderModel:    route.ProviderModel,
		ResourceID:       routeResourceID(route),
		ResourceType:     routeResourceType(route),
		RoutePriority:    route.Route.Priority,
		ResourcePriority: routeResourcePriority(route),
		Weight:           routeEffectiveWeight(route),
		Strategy:         routeStrategy(route.Route),
	}); ok {
		input.Envelope.Metadata = map[string]json.RawMessage{"route": routeData}
	}
	report, err := s.runAuditedGatewayHookStageHooks(ctx, call, stage, input, hooks)
	if err != nil {
		return event, true, &ProviderInvocationError{Err: gatewayHookHTTPError(stage, err), Disposition: ProviderErrorPolicy}
	}
	transformed := event
	emit := true
	for _, result := range report.Results {

		if patch, ok := result.Writes[pluginmeta.DataProviderResponse]; ok {
			if !json.Valid(patch.Value) {
				return event, true, NewHTTPError(502, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response")
			}
			transformed.Data = string(patch.Value)
		}
		patch, ok := result.Writes[pluginmeta.DataStreamEvents]
		if !ok {
			continue
		}
		next, nextEmit, err := applyGatewayStreamEventPatch(transformed, patch.Value)
		if err != nil {
			if result.FailurePolicy == pluginmeta.FailurePolicyFailOpen {
				log.Printf("[tokenhub] gateway stream_transform hook %s/%s returned an invalid stream event for request %s: %v", result.PluginID, result.HookID, call.RequestID, err)
				continue
			}
			return event, true, err
		}
		transformed = next
		emit = emit && nextEmit
	}
	return transformed, emit, nil
}

func applyGatewayStreamEventPatch(event serverSentEvent, data json.RawMessage) (serverSentEvent, bool, error) {
	var patch gatewayStreamEventPatch
	if err := decodeGatewayHookPayload(data, &patch, "gateway_hook_stream_event_invalid", "Gateway stream plugin returned an invalid stream event"); err != nil {
		return event, true, err
	}
	if patch.Event != nil {
		if bytes.ContainsAny([]byte(*patch.Event), "\r\n") {
			return event, true, NewHTTPError(502, "gateway_hook_stream_event_invalid", "Gateway stream plugin returned an invalid event name")
		}
		event.Event = *patch.Event
	}
	if patch.Data != nil {
		event.Data = *patch.Data
	}
	return event, !patch.Drop, nil
}

func renderSSEEvent(event serverSentEvent) []byte {
	if event.Event == "" && event.Data == "" && len(event.Raw) > 0 {
		return append([]byte(nil), event.Raw...)
	}
	var output bytes.Buffer
	if event.Event != "" {
		fmt.Fprintf(&output, "event: %s\n", event.Event)
	}
	for _, line := range stringsSplitSSEData(event.Data) {
		fmt.Fprintf(&output, "data: %s\n", line)
	}
	output.WriteByte('\n')
	return output.Bytes()
}

func stringsSplitSSEData(data string) []string {
	if data == "" {
		return []string{""}
	}
	segments := bytes.Split([]byte(data), []byte("\n"))
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		lines = append(lines, string(segment))
	}
	return lines
}
