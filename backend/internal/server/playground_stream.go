package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

const playgroundClientClosedStatus = 499

type playgroundChatRequest struct {
	ChatCompletionRequest
	ProjectID string
}

func (r *playgroundChatRequest) UnmarshalJSON(data []byte) error {
	var request ChatCompletionRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return err
	}
	var context struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(data, &context); err != nil {
		return err
	}
	for key := range request.raw {
		if strings.EqualFold(key, "project_id") {
			delete(request.raw, key)
		}
	}
	r.ChatCompletionRequest = request
	r.ProjectID = strings.TrimSpace(context.ProjectID)
	return nil
}

type playgroundTiming struct {
	Mode                    string     `json:"mode"`
	StartedAt               time.Time  `json:"started_at"`
	FirstTokenAt            *time.Time `json:"first_token_at,omitempty"`
	LastTokenAt             *time.Time `json:"last_token_at,omitempty"`
	CompletedAt             time.Time  `json:"completed_at"`
	TTFTMS                  *int64     `json:"ttft_ms,omitempty"`
	GenerationMS            *int64     `json:"generation_ms,omitempty"`
	TotalMS                 int64      `json:"total_ms"`
	OutputTokensPerSecond   *float64   `json:"output_tokens_per_second,omitempty"`
	EndToEndTokensPerSecond *float64   `json:"end_to_end_tokens_per_second,omitempty"`
}

type playgroundStreamPayload struct {
	Type      string                   `json:"type"`
	Status    string                   `json:"status"`
	Response  any                      `json:"response,omitempty"`
	Route     PlaygroundRouteSummary   `json:"route,omitzero"`
	Usage     Usage                    `json:"usage,omitzero"`
	Attempts  []PlaygroundRouteAttempt `json:"attempts,omitempty"`
	Timing    playgroundTiming         `json:"timing"`
	RequestID string                   `json:"request_id"`
	Code      string                   `json:"code,omitempty"`
	Error     string                   `json:"error,omitempty"`
}

type playgroundStreamResult struct {
	Response any
	Text     string
	Mode     string
	FirstAt  time.Time
	LastAt   time.Time
}

type playgroundEventStream struct {
	writer  http.ResponseWriter
	flusher http.Flusher
}

func newPlaygroundEventStream(writer http.ResponseWriter, requestID string) *playgroundEventStream {
	writer.Header().Set("content-type", "text/event-stream")
	writer.Header().Set("cache-control", "no-cache")
	writer.Header().Set("connection", "keep-alive")
	writer.Header().Set("x-accel-buffering", "no")
	writer.Header().Set("x-request-id", requestID)
	stream := &playgroundEventStream{writer: writer}
	stream.flusher, _ = writer.(http.Flusher)
	return stream
}

func (s *playgroundEventStream) emit(event string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(s.writer, "event: "+event+"\ndata: "+string(encoded)+"\n\n"); err != nil {
		return err
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

type playgroundDeltaSink struct {
	events    *playgroundEventStream
	requestID string
	mode      string
	buffer    []byte
	dataLines []string
	eventSize int
	text      strings.Builder
	firstAt   time.Time
	lastAt    time.Time
	wrote     bool
}

func newPlaygroundDeltaSink(events *playgroundEventStream, requestID string) *playgroundDeltaSink {
	return &playgroundDeltaSink{events: events, requestID: requestID, mode: "stream"}
}

func (s *playgroundDeltaSink) Write(data []byte) (int, error) {
	originalLength := len(data)
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		if newline < 0 {
			if s.eventSize+len(s.buffer)+len(data) > maxSSEEventBytes {
				return 0, invalidProviderResponseError("provider sent a stream event that exceeds the size limit")
			}
			s.buffer = append(s.buffer, data...)
			break
		}
		lineSize := len(s.buffer) + newline + 1
		if s.eventSize+lineSize > maxSSEEventBytes {
			return 0, invalidProviderResponseError("provider sent a stream event that exceeds the size limit")
		}
		lineBytes := append(s.buffer, data[:newline]...)
		s.buffer = nil
		s.eventSize += lineSize
		line := strings.TrimSuffix(string(lineBytes), "\r")
		if err := s.consumeLine(line); err != nil {
			return 0, err
		}
		data = data[newline+1:]
	}
	return originalLength, nil
}

func (s *playgroundDeltaSink) Flush() {
	if s.events.flusher != nil {
		s.events.flusher.Flush()
	}
}

func (s *playgroundDeltaSink) finish() error {
	if len(s.buffer) > 0 {
		line := strings.TrimSuffix(string(s.buffer), "\r")
		s.buffer = nil
		if err := s.consumeLine(line); err != nil {
			return err
		}
	}
	if len(s.dataLines) > 0 {
		return s.consumeEvent()
	}
	return nil
}

func (s *playgroundDeltaSink) consumeLine(line string) error {
	if line == "" {
		err := s.consumeEvent()
		s.eventSize = 0
		return err
	}
	if strings.HasPrefix(line, "data:") {
		s.dataLines = append(s.dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
	}
	return nil
}

func (s *playgroundDeltaSink) consumeEvent() error {
	if len(s.dataLines) == 0 {
		return nil
	}
	data := strings.Join(s.dataLines, "\n")
	s.dataLines = nil
	if data == "" || data == "[DONE]" {
		return nil
	}
	var frame map[string]any
	if err := json.Unmarshal([]byte(data), &frame); err != nil {
		return invalidProviderResponseError("provider sent a malformed stream event")
	}
	if frame["type"] == "response.output_text.delta" {
		if delta, ok := frame["delta"].(string); ok {
			return s.emitDelta(delta)
		}
	}
	choices, _ := frame["choices"].([]any)
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		if content, ok := delta["content"].(string); ok {
			if err := s.emitDelta(content); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *playgroundDeltaSink) emitDelta(delta string) error {
	if delta == "" {
		return nil
	}
	now := time.Now().UTC()
	if err := s.events.emit("playground.delta", map[string]any{
		"type":        "delta",
		"request_id":  s.requestID,
		"mode":        s.mode,
		"delta":       delta,
		"received_at": now,
	}); err != nil {
		return err
	}
	if s.firstAt.IsZero() {
		s.firstAt = now
	}
	s.lastAt = now
	s.wrote = true
	s.text.WriteString(delta)
	return nil
}

func (s *playgroundDeltaSink) WroteContent() bool { return s != nil && s.wrote }
func (s *playgroundDeltaSink) Text() string {
	if s == nil {
		return ""
	}
	return s.text.String()
}

func (s *Server) handleAdminPlaygroundChatStream(w http.ResponseWriter, r *http.Request) {
	admittedAt := time.Now().UTC()
	user, ok := s.requireAdmin(w, r, "playground", r.Method)
	if !ok {
		return
	}
	var playgroundReq playgroundChatRequest
	if err := s.decodeJSONLimit(w, r, &playgroundReq, s.config.MaxMultimodalRequestBytes); err != nil {
		writeError(w, r, err)
		return
	}
	req := playgroundReq.ChatCompletionRequest
	if err := validatePlaygroundRequest(&req); err != nil {
		writeError(w, r, err)
		return
	}
	guardrailProjectID, err := s.resolvePlaygroundGuardrailProjectID(user, playgroundReq.ProjectID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	call := s.newPlaygroundCallContext(user, req.Model, admittedAt)
	w.Header().Set("x-request-id", call.RequestID)
	decision, guardrailErr := s.evaluateOutboundGuardrails(r.Context(), guardrailProjectID, chatGuardrailTargets(&req))
	requestAuditPayload := guardrailRequestAuditPayload(req.Model, decision, playgroundAuditRequest(req))
	if guardrailErr != nil {
		httpErr := AsHTTPError(guardrailErr)
		s.finishRoutedCall(r, GatewayCallCompletion{
			Kind: CompletionKindPlayground, Call: call, StatusCode: httpErr.Status,
			ErrorCode: httpErr.Code, ErrorMessage: httpErr.Message, RequestPayload: requestAuditPayload,
			ResponsePayload: auditErrorPayload(guardrailErr, call.RequestID),
		})
		s.recordAdminAudit(r, user, "chat_failed", "playground", req.Model, "", map[string]any{
			"model": req.Model, "attempts": []PlaygroundRouteAttempt{}, "error": httpErr.Code,
		})
		writeError(w, r, guardrailErr)
		return
	}

	routed, err := s.preparePlaygroundRoutedCall(r.Context(), req, call)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.finishRoutedCall(r, GatewayCallCompletion{
			Kind:            CompletionKindPlayground,
			Call:            routed.Call,
			StatusCode:      httpErr.Status,
			ErrorCode:       httpErr.Code,
			ErrorMessage:    httpErr.Message,
			RequestPayload:  requestAuditPayload,
			ResponsePayload: auditErrorPayload(err, routed.Call.RequestID),
		})
		s.recordAdminAudit(r, user, "chat_failed", "playground", req.Model, "", map[string]any{
			"model": req.Model, "attempts": []PlaygroundRouteAttempt{}, "error": httpErr.Code,
		})
		writeError(w, r, err)
		return
	}
	events := newPlaygroundEventStream(w, routed.Call.RequestID)
	if err := events.emit("playground.started", map[string]any{
		"type":       "started",
		"request_id": routed.Call.RequestID,
		"model":      req.Model,
		"started_at": routed.Call.StartedAt,
	}); err != nil {
		s.finishPlaygroundClientCancellation(r, routed, requestAuditPayload, nil)
		return
	}

	result, route, usage, attempts, streamErr := s.executeRoutedPlaygroundStream(r, routed, req, events)
	completedAt := time.Now().UTC()
	usage = priceUsage(routed.Call.Model, usage)
	if streamErr != nil {
		s.finishFailedPlaygroundStream(r, user, routed, req, requestAuditPayload, result, route, usage, attempts, completedAt, events, streamErr)
		return
	}
	if result.Response == nil {
		result.Response = responseObject(req.Model, result.Text, usage)
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishRoutedCall(r, GatewayCallCompletion{
		Kind:            CompletionKindPlayground,
		Call:            routed.Call,
		Route:           route,
		Usage:           usage,
		Attempts:        attempts,
		StatusCode:      http.StatusOK,
		RequestPayload:  requestAuditPayload,
		ResponsePayload: result.Response,
	})
	s.recordAdminAudit(r, user, "chat", "playground", req.Model, "", map[string]any{
		"model": req.Model, "route": playgroundRouteSummary(route), "usage": usage, "attempts": len(attempts),
	})
	payload := playgroundStreamPayload{
		Type:      "completed",
		Status:    "completed",
		Response:  result.Response,
		Route:     playgroundRouteForUser(user, route, usage),
		Usage:     playgroundUsageForUser(user, usage),
		Attempts:  playgroundAttemptsForUser(user, attempts),
		Timing:    newPlaygroundTiming(routed.Call.StartedAt, result, completedAt, usage),
		RequestID: routed.Call.RequestID,
	}
	_ = events.emit("playground.completed", payload)
}

func validatePlaygroundRequest(req *ChatCompletionRequest) error {
	req.Model = strings.TrimSpace(req.Model)
	req.Stream = true
	req.StreamOptions = map[string]any{"include_usage": true}
	if req.Model == "" {
		return NewHTTPError(http.StatusBadRequest, "missing_model", "model is required")
	}
	if len(req.Messages) == 0 {
		return NewHTTPError(http.StatusBadRequest, "missing_messages", "messages are required")
	}
	for _, message := range req.Messages {
		if strings.TrimSpace(message.Role) == "" {
			return NewHTTPError(http.StatusBadRequest, "invalid_message", "message role is required")
		}
	}
	return validatePlaygroundImages(req.Messages)
}

func (s *Server) resolvePlaygroundGuardrailProjectID(user AdminUser, requestedID string) (string, error) {
	projectID := strings.TrimSpace(requestedID)
	if projectID == "" {
		return "", NewHTTPError(http.StatusBadRequest, "missing_project", "project_id is required")
	}
	project, ok := s.store.GetProject(projectID)
	if !ok {
		return "", NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found")
	}
	if project.Status != "" && project.Status != StatusActive {
		return "", NewHTTPError(http.StatusConflict, "project_inactive", "Project is not active")
	}
	if !s.canAccessProject(user, project) {
		return "", NewHTTPError(http.StatusForbidden, "project_forbidden", "Project is not available for this user")
	}
	return project.ID, nil
}

func (s *Server) newPlaygroundCallContext(user AdminUser, model string, startedAt time.Time) CallContext {
	return CallContext{
		RequestID:  NewID("pg"),
		Project:    Project{ID: "admin_playground", Name: "Admin Playground", Status: StatusActive},
		Key:        APIKey{ID: user.ID, Name: "Admin Playground"},
		Model:      playgroundModel(s.store, model),
		StartedAt:  startedAt.UTC(),
		measuredAt: startedAt,
	}
}

func (s *Server) preparePlaygroundRoutedCall(ctx context.Context, req ChatCompletionRequest, call CallContext) (RoutedCall, error) {
	routed := RoutedCall{Call: call}
	routes, err := s.store.SelectRouteCandidates(req.Model)
	if err != nil {
		err = s.annotateRoutingPolicyForCandidateError(&routed.Call, err)
		return routed, err
	}
	routes, err = s.filterCodexRoutesByModel(ctx, req.Model, routes)
	if err != nil {
		err = s.annotateRoutingPolicyForCandidateError(&routed.Call, err)
		return routed, err
	}
	var resolution RoutingPolicyResolution
	routes, resolution, err = s.resolveScopedRoutingPolicy(routed.Call, routes)
	applyRoutingPolicyResolution(&routed.Call, resolution)
	if err != nil {
		return routed, err
	}
	routed.Routes = s.planRouteOrder(routed.Call, routes)
	return routed, nil
}

func (s *Server) executeRoutedPlaygroundStream(
	r *http.Request,
	routed RoutedCall,
	req ChatCompletionRequest,
	events *playgroundEventStream,
) (playgroundStreamResult, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(req.ReasoningEffort) != nil
	var lastResult playgroundStreamResult
	result, route, usage, attempts, err := executeRoutedWithStore(
		r.Context(), s.store, routed, allowEffortFallback,
		func(ctx context.Context, candidate RouteSelection, omitReasoningEffort bool, _ int) (playgroundStreamResult, Usage, error) {
			responsesReq, useResponses, conversionErr := playgroundResponsesRequestForRoute(candidate, req)
			if conversionErr != nil {
				return playgroundStreamResult{}, Usage{}, conversionErr
			}
			prepared, prepareErr := s.prepareRouteForUpstream(ctx, candidate)
			if prepareErr != nil {
				return playgroundStreamResult{}, Usage{}, prepareErr
			}
			var attemptResult playgroundStreamResult
			var attemptUsage Usage
			var attemptErr error
			if useResponses {
				upstreamReq := responsesReq
				if omitReasoningEffort {
					upstreamReq = withoutResponsesReasoningEffort(upstreamReq)
				}
				attemptResult, attemptUsage, attemptErr = s.streamPlaygroundResponses(ctx, r, prepared, upstreamReq, events, routed.Call.RequestID)
				if isCodexModelUnsupportedError(attemptErr) {
					s.removeCodexResourceModel(routeResourceID(prepared), prepared.ProviderModel)
				}
			} else {
				upstreamReq := req
				if omitReasoningEffort {
					upstreamReq.ReasoningEffort = nil
				}
				attemptResult, attemptUsage, attemptErr = s.streamPlaygroundChat(ctx, prepared, upstreamReq, events, routed.Call.RequestID)
			}
			lastResult = attemptResult
			return attemptResult, attemptUsage, attemptErr
		},
	)
	if err != nil {
		return lastResult, route, usage, attempts, err
	}
	return result, route, usage, attempts, nil
}

func (s *Server) streamPlaygroundChat(
	ctx context.Context,
	route RouteSelection,
	req ChatCompletionRequest,
	events *playgroundEventStream,
	requestID string,
) (playgroundStreamResult, Usage, error) {
	adapter, err := s.adapterForRoute(route)
	if err != nil {
		return playgroundStreamResult{}, Usage{}, err
	}
	if !s.routeSupportsAdapterCapability(route, AdapterCapabilityChatStream) {
		bufferedReq := req
		bufferedReq.Stream = false
		bufferedReq.StreamOptions = nil
		response, usage, invokeErr := adapter.Chat(ctx, route.Provider, route.ProviderModel, bufferedReq)
		result := playgroundStreamResult{Response: response, Text: playgroundResponseText(response), Mode: "buffered"}
		if invokeErr == nil && result.Text != "" {
			invokeErr = events.emit("playground.delta", map[string]any{
				"type": "delta", "request_id": requestID, "mode": "buffered", "delta": result.Text, "received_at": time.Now().UTC(),
			})
		}
		return result, usage, classifyStreamError(ctx, invokeErr, result.Text != "")
	}
	sink := newPlaygroundDeltaSink(events, requestID)
	usage, streamErr := adapter.ChatStream(ctx, route.Provider, route.ProviderModel, req, sink)
	if finishErr := sink.finish(); streamErr == nil {
		streamErr = finishErr
	}
	result := playgroundStreamResult{Text: sink.Text(), Mode: "stream", FirstAt: sink.firstAt, LastAt: sink.lastAt}
	result.Response = responseObject(req.Model, result.Text, usage)
	return result, usage, classifyStreamError(ctx, streamErr, sink.WroteContent())
}

func (s *Server) streamPlaygroundResponses(
	ctx context.Context,
	r *http.Request,
	route RouteSelection,
	req ResponsesRequest,
	events *playgroundEventStream,
	requestID string,
) (playgroundStreamResult, Usage, error) {
	adapter, err := s.responsesAdapterForRoute(route)
	if err != nil {
		return playgroundStreamResult{}, Usage{}, err
	}
	streamAdapter, canStream := adapter.(ResponsesStreamOpener)
	if !canStream || !s.routeSupportsAdapterCapability(route, AdapterCapabilityResponseStream) {
		response, usage, invokeErr := s.invokeResponsesAdapter(ctx, route, req, r.Header)
		result := playgroundStreamResult{Response: response, Text: playgroundResponseText(response), Mode: "buffered"}
		if invokeErr == nil && result.Text != "" {
			invokeErr = events.emit("playground.delta", map[string]any{
				"type": "delta", "request_id": requestID, "mode": "buffered", "delta": result.Text, "received_at": time.Now().UTC(),
			})
		}
		return result, usage, classifyStreamError(ctx, invokeErr, result.Text != "")
	}
	opened, err := streamAdapter.OpenResponses(ctx, route.Provider, route.ProviderModel, req, r.Header)
	if err != nil {
		return playgroundStreamResult{}, Usage{}, err
	}
	defer opened.Body.Close()
	sink := newPlaygroundDeltaSink(events, requestID)
	response, outputText, usage, streamErr := consumeCodexResponsesStream(opened.Body, sink)
	applyCodexResponseMetadata(&usage, opened.Header)
	if finishErr := sink.finish(); streamErr == nil {
		streamErr = finishErr
	}
	result := playgroundStreamResult{Response: response, Text: sink.Text(), Mode: "stream", FirstAt: sink.firstAt, LastAt: sink.lastAt}
	if result.Text == "" && outputText != "" && streamErr == nil {
		result.Mode = "buffered"
		result.Text = outputText
		streamErr = events.emit("playground.delta", map[string]any{
			"type": "delta", "request_id": requestID, "mode": "buffered", "delta": outputText, "received_at": time.Now().UTC(),
		})
	}
	return result, usage, classifyStreamError(ctx, streamErr, sink.WroteContent() || result.Text != "")
}

func (s *Server) finishFailedPlaygroundStream(
	r *http.Request,
	user AdminUser,
	routed RoutedCall,
	req ChatCompletionRequest,
	requestAuditPayload any,
	result playgroundStreamResult,
	route RouteSelection,
	usage Usage,
	attempts []RouteAttempt,
	completedAt time.Time,
	events *playgroundEventStream,
	err error,
) {
	status, code := statusAndCode(err)
	state := "failed"
	if clientDisconnected(r.Context(), err) {
		status, code, state = playgroundClientClosedStatus, "client_cancelled", "cancelled"
	}
	if route.Route.ID == "" {
		route = lastAttemptRoute(attempts)
	}
	s.finishRoutedCall(r, GatewayCallCompletion{
		Kind: CompletionKindPlayground, Call: routed.Call, Route: route, Usage: usage, Attempts: attempts,
		StatusCode: status, ErrorCode: code, ErrorMessage: errorMessage(err), RequestPayload: requestAuditPayload,
		ResponsePayload: auditErrorPayload(err, routed.Call.RequestID),
	})
	s.recordAdminAudit(r, user, "chat_"+state, "playground", req.Model, "", map[string]any{
		"model": req.Model, "attempts": playgroundRouteAttempts(attempts), "error": code,
	})
	payload := playgroundStreamPayload{
		Type: state, Status: state, Response: result.Response,
		Route: playgroundRouteForUser(user, route, usage), Usage: playgroundUsageForUser(user, usage),
		Attempts:  playgroundAttemptsForUser(user, attempts),
		Timing:    newPlaygroundTiming(routed.Call.StartedAt, result, completedAt, usage),
		RequestID: routed.Call.RequestID, Code: code, Error: AsHTTPError(err).Message,
	}
	_ = events.emit("playground."+state, payload)
}

func (s *Server) finishPlaygroundClientCancellation(r *http.Request, routed RoutedCall, requestAuditPayload any, attempts []RouteAttempt) {
	s.finishRoutedCall(r, GatewayCallCompletion{
		Kind: CompletionKindPlayground, Call: routed.Call, Attempts: attempts,
		StatusCode: playgroundClientClosedStatus, ErrorCode: "client_cancelled", RequestPayload: requestAuditPayload,
	})
}

func newPlaygroundTiming(startedAt time.Time, result playgroundStreamResult, completedAt time.Time, usage Usage) playgroundTiming {
	timing := playgroundTiming{
		Mode: result.Mode, StartedAt: startedAt.UTC(), CompletedAt: completedAt.UTC(),
		TotalMS: maxInt64(0, completedAt.Sub(startedAt).Milliseconds()),
	}
	if timing.Mode == "" {
		timing.Mode = "stream"
	}
	if !result.FirstAt.IsZero() {
		firstAt := result.FirstAt.UTC()
		timing.FirstTokenAt = &firstAt
		ttft := maxInt64(0, firstAt.Sub(startedAt).Milliseconds())
		timing.TTFTMS = &ttft
	}
	if !result.LastAt.IsZero() {
		lastAt := result.LastAt.UTC()
		timing.LastTokenAt = &lastAt
	}
	if timing.FirstTokenAt != nil && timing.LastTokenAt != nil {
		generationMS := maxInt64(0, timing.LastTokenAt.Sub(*timing.FirstTokenAt).Milliseconds())
		timing.GenerationMS = &generationMS
		if generationMS > 0 && usage.CompletionTokens > 0 {
			value := float64(usage.CompletionTokens) / (float64(generationMS) / 1000)
			timing.OutputTokensPerSecond = &value
		}
	}
	if timing.Mode == "buffered" && timing.TotalMS > 0 && usage.CompletionTokens > 0 {
		value := float64(usage.CompletionTokens) / (float64(timing.TotalMS) / 1000)
		timing.EndToEndTokensPerSecond = &value
	}
	return timing
}

func playgroundRouteForUser(user AdminUser, route RouteSelection, usage Usage) PlaygroundRouteSummary {
	if !canAdmin(user.Role, "routing", http.MethodGet) {
		return PlaygroundRouteSummary{}
	}
	summary := playgroundRouteSummary(route)
	summary.UpstreamRequestID = usage.UpstreamRequestID
	summary.ServedModel = usage.ServedModel
	summary.ModelETag = usage.ModelETag
	summary.Transport = usage.Transport
	return summary
}

func playgroundAttemptsForUser(user AdminUser, attempts []RouteAttempt) []PlaygroundRouteAttempt {
	if !canAdmin(user.Role, "routing", http.MethodGet) {
		return nil
	}
	return playgroundRouteAttempts(attempts)
}

func playgroundUsageForUser(user AdminUser, usage Usage) Usage {
	usage.ResponseHeaders = nil
	if !canAdmin(user.Role, "routing", http.MethodGet) {
		usage.UpstreamRequestID = ""
		usage.ServedModel = ""
		usage.ModelETag = ""
		usage.Transport = ""
	}
	return usage
}

func playgroundResponseText(response any) string {
	encoded, err := json.Marshal(response)
	if err != nil {
		return ""
	}
	var body map[string]any
	if json.Unmarshal(encoded, &body) != nil {
		return ""
	}
	if text, ok := body["output_text"].(string); ok {
		return text
	}
	if text, ok := body["content"].(string); ok {
		return text
	}
	choices, _ := body["choices"].([]any)
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		message, _ := choice["message"].(map[string]any)
		if text := contentToText(message["content"]); text != "" {
			return text
		}
		if text, ok := choice["text"].(string); ok {
			return text
		}
	}
	return ""
}
