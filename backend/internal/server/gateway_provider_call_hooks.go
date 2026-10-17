package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayProviderCallHooks(ctx context.Context, call CallContext, route RouteSelection, payload any, protocol string) (any, Usage, bool, error) {
	return s.runGatewayProviderCallHooksOutput(ctx, call, route, payload, protocol, nil)
}

func (s *Server) runGatewayProviderCallHooksOutput(ctx context.Context, call CallContext, route RouteSelection, payload any, protocol string, stream io.Writer) (any, Usage, bool, error) {
	return s.runGatewayProviderCallHooksOutputWithUsagePresence(ctx, call, route, payload, protocol, stream, nil)
}

// Usage presence distinguishes an explicit zero override from absent metering.
func (s *Server) runGatewayProviderCallHooksOutputWithUsagePresence(ctx context.Context, call CallContext, route RouteSelection, payload any, protocol string, stream io.Writer, usageReported *bool) (any, Usage, bool, error) {
	if usageReported != nil {
		*usageReported = false
	}
	hooks := s.gatewayProviderCallHooksForRoute(call, route, protocol, stream != nil)
	if len(hooks) == 0 {
		return nil, Usage{}, false, nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return nil, Usage{}, false, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:       "v1",
			Protocol:      "gateway",
			RouteProtocol: protocol,
			Operation:     "provider_call",
			Model:         call.Model.Name,
			RequestBody:   body,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:         gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata:     call.Project,
		pluginmeta.DataProviderCredentials: gatewayProviderCredentialsView(route),
		pluginmeta.DataProviderRequest:     body,
		pluginmeta.DataRouteCandidates:     gatewayRouteCandidateViews([]RouteSelection{route}),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.runAuditedGatewayHookStageHooks(ctx, call, pluginmeta.StageProviderCall, input, hooks)
	if err != nil {
		if pluginmeta.IsGatewayHookRouteSkipped(err) {
			return nil, Usage{}, false, &ProviderInvocationError{
				Err:         NewHTTPError(http.StatusBadGateway, "gateway_hook_route_skipped", "Gateway plugin skipped this route"),
				Disposition: ProviderErrorRouteSkipped,
			}
		}
		// Sensitive endpoint sanitization already removed payload text while
		// retaining the provider's cancellation, retry and outcome classification.
		var invocation *ProviderInvocationError
		if sensitiveGatewayHookCall(call) && (errors.Is(err, context.Canceled) || errors.As(err, &invocation)) {
			return nil, Usage{}, false, err
		}
		return nil, Usage{}, false, &ProviderInvocationError{
			Err:         gatewayHookHTTPError(pluginmeta.StageProviderCall, err),
			Disposition: ProviderErrorPolicy,
		}
	}
	var response any
	var events []gatewayStreamEventView
	var usage Usage
	var responseDecodeErr error
	var usageDecodeErr error
	usagePresent := false
	handled := false
	for _, result := range report.Results {
		if patch, ok := result.Writes[pluginmeta.DataProviderResponse]; ok {
			if err := decodeGatewayHookResponsePayload(patch.Value, &response, protocol); err != nil {
				// Rerank response validation can fail after a complete leading
				// object has supplied independent usage. Keep decoding DataUsage
				// below so the failed attempt remains billable and rate-limited.
				if responseDecodeErr == nil {
					responseDecodeErr = err
				}
			} else if stream == nil {
				handled = true
			}
		}

		if patch, ok := result.Writes[pluginmeta.DataStreamEvents]; ok {
			if stream == nil || decodeGatewayHookPayload(patch.Value, &events, "gateway_hook_stream_event_invalid", "Gateway provider plugin returned invalid stream events") != nil || events == nil {
				return nil, Usage{}, false, &ProviderInvocationError{Err: NewHTTPError(502, "gateway_hook_stream_event_invalid", "Gateway provider plugin must return an array of stream events for a streaming request"), Disposition: ProviderErrorPolicy}
			}
			for _, event := range events {
				if strings.ContainsAny(event.Event, "\r\n") {
					return nil, Usage{}, false, &ProviderInvocationError{Err: NewHTTPError(502, "gateway_hook_stream_event_invalid", "Gateway provider plugin returned an invalid event name"), Disposition: ProviderErrorPolicy}
				}
			}
			handled = true
		}
		if patch, ok := result.Writes[pluginmeta.DataUsage]; ok {
			if err := decodeGatewayHookPayload(patch.Value, &usage, "gateway_hook_usage_invalid", "Gateway plugin returned invalid usage"); err != nil {
				if usageDecodeErr == nil {
					usageDecodeErr = err
				}
				continue
			}
			usagePresent = true
		}
	}
	if responseDecodeErr != nil {
		if protocol != providerRouteProtocolRerank {
			if isAudioMediaProtocol(protocol) {
				if usageDecodeErr != nil {
					return nil, Usage{}, false, &ProviderInvocationError{Err: usageDecodeErr, Disposition: ProviderErrorPolicy}
				}
				// A provider-call hook can report metering independently from its
				// response. Preserve valid audio evidence when response decoding
				// fails, just as rerank preserves validated retrieval evidence.
				usage = sanitizeAudioUsage(usage)
				if err := validateAudioDuration(usage); err != nil {
					return nil, Usage{}, false, &ProviderInvocationError{Err: err, Disposition: ProviderErrorPolicy}
				}
				if usageReported != nil {
					*usageReported = usagePresent
				}
				return nil, usage, false, &ProviderInvocationError{Err: responseDecodeErr, Disposition: ProviderErrorPolicy}
			}
			return nil, Usage{}, false, &ProviderInvocationError{Err: responseDecodeErr, Disposition: ProviderErrorPolicy}
		}
		if usageDecodeErr != nil {
			return nil, Usage{}, false, &ProviderInvocationError{Err: usageDecodeErr, Disposition: ProviderErrorPolicy}
		}
		// Provider-call usage is only trusted after the same retrieval
		// consistency checks used by a successful rerank response. A
		// missing usage object therefore remains unpriced evidence.
		validated, err := pluginRetrievalUsage(nil, usage, providerRerankProtocol(route.Provider) == "cohere")
		if err != nil {
			return nil, Usage{}, false, &ProviderInvocationError{Err: err, Disposition: ProviderErrorPolicy}
		}
		usage = validated
		if usageReported != nil {
			*usageReported = usagePresent
		}
		return nil, usage, false, &ProviderInvocationError{Err: responseDecodeErr, Disposition: ProviderErrorPolicy}
	}
	if usageDecodeErr != nil {
		return nil, Usage{}, false, &ProviderInvocationError{Err: usageDecodeErr, Disposition: ProviderErrorPolicy}
	}
	if usageReported != nil {
		*usageReported = usagePresent
	}
	if report.TerminalDecision == pluginmeta.HookDecisionShortCircuit && !handled {
		return nil, Usage{}, false, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway provider plugin did not return a response"),
			Disposition: ProviderErrorPolicy,
		}
	}

	if handled && stream != nil {
		for _, event := range events {
			if _, err := stream.Write(renderSSEEvent(serverSentEvent{Event: event.Event, Data: event.Data})); err != nil {
				return response, usage, true, err
			}
		}
	}
	return response, usage, handled, nil
}
