package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func (s *Server) handleAgentResponses(w http.ResponseWriter, r *http.Request, project Project, key APIKey, request ResponsesRequest, slug string) {
	agent, found, err := s.store.GetAgentBySlug(slug)
	if err != nil || !found || agent.Status != StatusActive {
		writeError(w, r, agentResponsesNotFoundError())
		return
	}
	invocation := agentInvocation{Agent: agent, Project: project, APIKey: key, IncomingHeaders: r.Header.Clone()}
	if endUserID := strings.TrimSpace(r.Header.Get("X-TokenHub-End-User-ID")); endUserID != "" {
		if !strings.EqualFold(key.Metadata["allow_end_user_identity"], "true") {
			writeError(w, r, agentResponsesNotFoundError())
			return
		}
		invocation.EndUserID = endUserID
	}
	if !s.authorizeAgentInvocation(invocation, "") {
		writeError(w, r, agentResponsesNotFoundError())
		return
	}
	if _, err := s.evaluateOutboundGuardrails(r.Context(), project.ID, responsesGuardrailTargets(&request)); err != nil {
		writeError(w, r, err)
		return
	}
	invocation, err = s.startAgentInvocation(invocation)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx := context.WithValue(r.Context(), agentInvocationContextKey{}, invocation)
	message := &a2a.Message{
		ID: a2a.NewMessageID(), Role: a2a.MessageRoleUser,
		Parts: []*a2a.Part{a2a.NewTextPart(agentResponsesInput(request))},
	}
	send := &a2a.SendMessageRequest{Message: message, Metadata: map[string]any{"tokenhub.responses": true}}
	handler := &agentGatewayHandler{server: s}
	if request.Stream {
		s.streamAgentResponse(ctx, w, request.Model, handler, send)
		return
	}
	result, err := handler.SendMessage(ctx, send)
	if err != nil {
		writeError(w, r, agentResponsesError(err))
		return
	}
	text := agentEventText(result)
	usage := agentResponseUsage(agentResponsesInput(request), text)
	response := responseObject(request.Model, text, usage)
	response["status"] = "completed"
	response["object"] = "response"
	response["created_at"] = time.Now().UTC().Unix()
	w.Header().Set("x-tokenhub-agent", agent.Slug)
	writeJSON(w, http.StatusOK, response)
}

func agentResponsesNotFoundError() *HTTPError {
	return NewHTTPError(http.StatusNotFound, "agent_not_found", "Agent was not found")
}

func (s *Server) streamAgentResponse(ctx context.Context, w http.ResponseWriter, model string, handler *agentGatewayHandler, request *a2a.SendMessageRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, nil, NewHTTPError(http.StatusInternalServerError, "streaming_unsupported", "Streaming is not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	responseID := NewID("resp")
	created := map[string]any{
		"type":     "response.created",
		"response": map[string]any{"id": responseID, "object": "response", "status": "in_progress", "model": model, "output": []any{}},
	}
	writeResponsesSSE(w, "response.created", created)
	flusher.Flush()
	var output strings.Builder
	for event, err := range handler.SendStreamingMessage(ctx, request) {
		if err != nil {
			streamError := agentResponsesError(err)
			writeResponsesSSE(w, "error", map[string]any{
				"type": "error", "error": map[string]any{"code": streamError.Code, "message": streamError.Message},
			})
			flusher.Flush()
			return
		}
		text := agentEventText(event)
		if text == "" {
			continue
		}
		output.WriteString(text)
		writeResponsesSSE(w, "response.output_text.delta", map[string]any{
			"type": "response.output_text.delta", "response_id": responseID, "delta": text,
		})
		flusher.Flush()
	}
	usage := agentResponseUsage(agentEventText(request.Message), output.String())
	completed := responseObject(model, output.String(), usage)
	completed["id"] = responseID
	completed["status"] = "completed"
	writeResponsesSSE(w, "response.completed", map[string]any{"type": "response.completed", "response": completed})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func agentResponsesError(err error) *HTTPError {
	switch {
	case errors.Is(err, errAgentOutputGuardrailBlocked):
		return NewHTTPError(http.StatusForbidden, "guardrail_blocked", agentOutputGuardrailMessage)
	case errors.Is(err, errAgentOutputGuardrailEvaluation):
		return NewHTTPError(http.StatusInternalServerError, "guardrail_evaluation_failed", "Agent output content security evaluation failed")
	default:
		return NewHTTPError(http.StatusBadGateway, "agent_upstream_error", err.Error())
	}
}

func writeResponsesSSE(w http.ResponseWriter, event string, payload any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}

func agentResponsesInput(request ResponsesRequest) string {
	input := ResponsesInputText(request.Input)
	if strings.TrimSpace(request.Instructions) == "" {
		return input
	}
	if strings.TrimSpace(input) == "" {
		return request.Instructions
	}
	return request.Instructions + "\n\n" + input
}

func agentEventText(event any) string {
	switch item := event.(type) {
	case *a2a.Message:
		return agentPartsText(item.Parts)
	case *a2a.Task:
		if item.Status.Message != nil {
			if text := agentPartsText(item.Status.Message.Parts); text != "" {
				return text
			}
		}
		for index := len(item.Artifacts) - 1; index >= 0; index-- {
			if item.Artifacts[index] != nil {
				if text := agentPartsText(item.Artifacts[index].Parts); text != "" {
					return text
				}
			}
		}
		for index := len(item.History) - 1; index >= 0; index-- {
			if item.History[index] != nil && item.History[index].Role == a2a.MessageRoleAgent {
				if text := agentPartsText(item.History[index].Parts); text != "" {
					return text
				}
			}
		}
	case *a2a.TaskStatusUpdateEvent:
		if item.Status.Message != nil {
			return agentPartsText(item.Status.Message.Parts)
		}
	case *a2a.TaskArtifactUpdateEvent:
		if item.Artifact != nil {
			return agentPartsText(item.Artifact.Parts)
		}
	}
	return ""
}

func agentPartsText(parts a2a.ContentParts) string {
	texts := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != nil && part.Text() != "" {
			texts = append(texts, part.Text())
		}
	}
	return strings.Join(texts, "")
}

func agentResponseUsage(input string, output string) Usage {
	usage := Usage{PromptTokens: EstimateTextTokens(input), CompletionTokens: EstimateTextTokens(output)}
	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	return usage
}
