package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"tokenhub/backend/internal/guardrails"
)

type agentInvocationContextKey struct{}

type agentInvocation struct {
	Agent           AgentWithDetails
	Project         Project
	APIKey          APIKey
	EndUserID       string
	CallerAgent     string
	ExecutionID     string
	TraceID         string
	ParentStepID    string
	Depth           int64
	Chain           []string
	IncomingHeaders http.Header
	RootExecution   bool
	Deadline        time.Time
}

type agentGatewayHandler struct {
	server *Server
}

var _ a2asrv.RequestHandler = (*agentGatewayHandler)(nil)

const agentOutputGuardrailMessage = "Agent output was blocked by a content security policy"

var (
	errAgentOutputGuardrailBlocked    = fmt.Errorf("agent output guardrail blocked: %w", a2a.ErrUnauthorized)
	errAgentOutputGuardrailEvaluation = fmt.Errorf("agent output guardrail evaluation failed: %w", a2a.ErrInternalError)
)

func (h *agentGatewayHandler) GetTask(ctx context.Context, request *a2a.GetTaskRequest) (*a2a.Task, error) {
	invocation, err := invocationFromContext(ctx)
	if err != nil {
		return nil, err
	}
	status := "failed"
	defer func() { h.finishInvocation(invocation, status) }()
	task, instance, err := h.resolveTask(invocation, string(request.ID))
	if err != nil {
		return nil, err
	}
	instance, leaseID, err := h.server.store.ReserveAgentInstanceByID(instance.ID, invocation.Deadline)
	if err != nil {
		return nil, normalizeAgentUpstreamError(err)
	}
	defer func() { _ = h.server.store.ReleaseAgentInstance(leaseID) }()
	if err := h.chargeInvocationCost(invocation, instance); err != nil {
		status = "budget_exceeded"
		return nil, err
	}
	client, err := h.client(ctx, invocation, instance)
	if err != nil {
		return nil, err
	}
	upstream := *request
	upstream.ID = a2a.TaskID(task.UpstreamTaskID)
	result, err := client.GetTask(ctx, &upstream)
	if err != nil {
		h.recordInstanceFailure(instance, err)
		return nil, normalizeAgentUpstreamError(err)
	}
	h.recordInstanceSuccess(instance)
	if err := h.applyAgentOutputGuardrails(ctx, invocation, result); err != nil {
		return nil, err
	}
	if err := h.rewriteAndPersistTask(&task, result, "task"); err != nil {
		return nil, a2a.NewError(a2a.ErrInternalError, "Task state could not be persisted")
	}
	status = agentExecutionStatusForTask(result.Status.State)
	return result, nil
}

func (h *agentGatewayHandler) ListTasks(ctx context.Context, request *a2a.ListTasksRequest) (*a2a.ListTasksResponse, error) {
	invocation, err := invocationFromContext(ctx)
	if err != nil {
		return nil, err
	}
	status := "failed"
	defer func() { h.finishInvocation(invocation, status) }()
	pageSize := request.PageSize
	if pageSize == 0 {
		pageSize = 50
	}
	if pageSize < 1 || pageSize > 100 {
		return nil, a2a.NewError(a2a.ErrInvalidParams, "pageSize must be between 1 and 100")
	}
	offset := 0
	if request.PageToken != "" {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(request.PageToken)
		if decodeErr != nil {
			return nil, a2a.NewError(a2a.ErrInvalidParams, "pageToken is invalid")
		}
		offset, err = strconv.Atoi(string(decoded))
		if err != nil || offset < 0 {
			return nil, a2a.NewError(a2a.ErrInvalidParams, "pageToken is invalid")
		}
	}
	records, total, err := h.server.store.ListAgentTasks(
		invocation.Agent.ID, invocation.Project.ID, invocation.APIKey.ID, invocation.EndUserID,
		request.ContextID, string(request.Status), offset, pageSize,
	)
	if err != nil {
		return nil, a2a.NewError(a2a.ErrInternalError, "Tasks could not be listed")
	}
	tasks := make([]*a2a.Task, 0, len(records))
	for _, record := range records {
		var task a2a.Task
		if json.Unmarshal([]byte(record.SnapshotJSON), &task) == nil {
			if !request.IncludeArtifacts {
				task.Artifacts = nil
			}
			if request.HistoryLength != nil && *request.HistoryLength >= 0 && len(task.History) > *request.HistoryLength {
				task.History = task.History[len(task.History)-*request.HistoryLength:]
			}
			if err := h.applyAgentOutputGuardrails(ctx, invocation, &task); err != nil {
				return nil, err
			}
			tasks = append(tasks, &task)
		}
	}
	next := ""
	if int64(offset+len(records)) < total {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + len(records))))
	}
	status = "completed"
	return &a2a.ListTasksResponse{Tasks: tasks, TotalSize: int(total), PageSize: pageSize, NextPageToken: next}, nil
}

func (h *agentGatewayHandler) CancelTask(ctx context.Context, request *a2a.CancelTaskRequest) (*a2a.Task, error) {
	invocation, err := invocationFromContext(ctx)
	if err != nil {
		return nil, err
	}
	status := "failed"
	defer func() { h.finishInvocation(invocation, status) }()
	task, instance, err := h.resolveTask(invocation, string(request.ID))
	if err != nil {
		return nil, err
	}
	instance, leaseID, err := h.server.store.ReserveAgentInstanceByID(instance.ID, invocation.Deadline)
	if err != nil {
		return nil, normalizeAgentUpstreamError(err)
	}
	defer func() { _ = h.server.store.ReleaseAgentInstance(leaseID) }()
	if err := h.chargeInvocationCost(invocation, instance); err != nil {
		status = "budget_exceeded"
		return nil, err
	}
	client, err := h.client(ctx, invocation, instance)
	if err != nil {
		return nil, err
	}
	upstream := *request
	upstream.ID = a2a.TaskID(task.UpstreamTaskID)
	result, err := client.CancelTask(ctx, &upstream)
	if err != nil {
		h.recordInstanceFailure(instance, err)
		return nil, normalizeAgentUpstreamError(err)
	}
	h.recordInstanceSuccess(instance)
	if err := h.applyAgentOutputGuardrails(ctx, invocation, result); err != nil {
		return nil, err
	}
	if err := h.rewriteAndPersistTask(&task, result, "task"); err != nil {
		return nil, a2a.NewError(a2a.ErrInternalError, "Task state could not be persisted")
	}
	status = agentExecutionStatusForTask(result.Status.State)
	return result, nil
}

func (h *agentGatewayHandler) SendMessage(ctx context.Context, request *a2a.SendMessageRequest) (a2a.SendMessageResult, error) {
	invocation, err := invocationFromContext(ctx)
	if err != nil {
		return nil, err
	}
	status := "failed"
	defer func() { h.finishInvocation(invocation, status) }()
	if request == nil || request.Message == nil {
		return nil, a2a.NewError(a2a.ErrInvalidParams, "message is required")
	}
	upstream, task, instance, leaseID, err := h.prepareSend(invocation, request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = h.server.store.ReleaseAgentInstance(leaseID) }()
	client, err := h.client(ctx, invocation, instance)
	if err != nil {
		return nil, err
	}
	if err := h.chargeInvocationCost(invocation, instance); err != nil {
		status = "budget_exceeded"
		return nil, err
	}
	result, err := client.SendMessage(ctx, upstream)
	if err != nil {
		h.recordInstanceFailure(instance, err)
		return nil, normalizeAgentUpstreamError(err)
	}
	h.recordInstanceSuccess(instance)
	if err := h.applyAgentOutputGuardrails(ctx, invocation, result); err != nil {
		return nil, err
	}
	switch event := result.(type) {
	case *a2a.Task:
		task, err = h.ensureTask(invocation, instance, task, string(event.ID), event.ContextID)
		if err == nil {
			err = h.rewriteAndPersistTask(&task, event, "task")
		}
		status = agentExecutionStatusForTask(event.Status.State)
	case *a2a.Message:
		if task.ID != "" && event.TaskID != "" {
			event.TaskID = a2a.TaskID(task.ID)
		}
		status = "completed"
	}
	if err != nil {
		return nil, a2a.NewError(a2a.ErrInternalError, "Task state could not be persisted")
	}
	return result, nil
}

func (h *agentGatewayHandler) SendStreamingMessage(ctx context.Context, request *a2a.SendMessageRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		invocation, err := invocationFromContext(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		status := "failed"
		defer func() { h.finishInvocation(invocation, status) }()
		if request == nil || request.Message == nil {
			yield(nil, a2a.NewError(a2a.ErrInvalidParams, "message is required"))
			return
		}
		upstream, task, instance, leaseID, err := h.prepareSend(invocation, request)
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = h.server.store.ReleaseAgentInstance(leaseID) }()
		if err := h.chargeInvocationCost(invocation, instance); err != nil {
			status = "budget_exceeded"
			yield(nil, err)
			return
		}
		client, err := h.client(ctx, invocation, instance)
		if err != nil {
			yield(nil, err)
			return
		}
		failed := false
		for event, streamErr := range client.SendStreamingMessage(ctx, upstream) {
			if streamErr != nil {
				failed = true
				h.recordInstanceFailure(instance, streamErr)
				yield(nil, normalizeAgentUpstreamError(streamErr))
				break
			}
			if err = h.applyAgentOutputGuardrails(ctx, invocation, event); err != nil {
				failed = true
				yield(nil, err)
				break
			}
			task, err = h.rewriteAndPersistEvent(invocation, instance, task, event)
			if err != nil {
				failed = true
				yield(nil, a2a.NewError(a2a.ErrInternalError, "Task event could not be persisted"))
				break
			}
			if task.ID != "" {
				status = agentExecutionStatusForTask(a2a.TaskState(task.State))
			}
			if !yield(event, nil) {
				status = "canceled"
				return
			}
		}
		if failed {
			return
		}
		h.recordInstanceSuccess(instance)
		if task.ID == "" {
			status = "completed"
		}
	}
}

func (h *agentGatewayHandler) SubscribeToTask(ctx context.Context, request *a2a.SubscribeToTaskRequest) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		invocation, err := invocationFromContext(ctx)
		if err != nil {
			yield(nil, err)
			return
		}
		status := "failed"
		defer func() { h.finishInvocation(invocation, status) }()
		task, instance, err := h.resolveTask(invocation, string(request.ID))
		if err != nil {
			yield(nil, err)
			return
		}
		status = agentExecutionStatusForTask(a2a.TaskState(task.State))
		instance, leaseID, err := h.server.store.ReserveAgentInstanceByID(instance.ID, invocation.Deadline)
		if err != nil {
			yield(nil, normalizeAgentUpstreamError(err))
			return
		}
		defer func() { _ = h.server.store.ReleaseAgentInstance(leaseID) }()
		if err := h.chargeInvocationCost(invocation, instance); err != nil {
			status = "budget_exceeded"
			yield(nil, err)
			return
		}
		client, err := h.client(ctx, invocation, instance)
		if err != nil {
			yield(nil, err)
			return
		}
		upstream := *request
		upstream.ID = a2a.TaskID(task.UpstreamTaskID)
		for event, streamErr := range client.SubscribeToTask(ctx, &upstream) {
			if streamErr != nil {
				h.recordInstanceFailure(instance, streamErr)
				yield(nil, normalizeAgentUpstreamError(streamErr))
				return
			}
			if err = h.applyAgentOutputGuardrails(ctx, invocation, event); err != nil {
				yield(nil, err)
				return
			}
			task, err = h.rewriteAndPersistEvent(invocation, instance, task, event)
			if err != nil {
				yield(nil, a2a.NewError(a2a.ErrInternalError, "Task event could not be persisted"))
				return
			}
			status = agentExecutionStatusForTask(a2a.TaskState(task.State))
			if !yield(event, nil) {
				status = "canceled"
				return
			}
		}
		h.recordInstanceSuccess(instance)
	}
}

func (h *agentGatewayHandler) GetTaskPushConfig(context.Context, *a2a.GetTaskPushConfigRequest) (*a2a.PushConfig, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}

func (h *agentGatewayHandler) ListTaskPushConfigs(context.Context, *a2a.ListTaskPushConfigRequest) (*a2a.ListTaskPushConfigResponse, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}

func (h *agentGatewayHandler) CreateTaskPushConfig(context.Context, *a2a.PushConfig) (*a2a.PushConfig, error) {
	return nil, a2a.ErrPushNotificationNotSupported
}

func (h *agentGatewayHandler) DeleteTaskPushConfig(context.Context, *a2a.DeleteTaskPushConfigRequest) error {
	return a2a.ErrPushNotificationNotSupported
}

func (h *agentGatewayHandler) GetExtendedAgentCard(ctx context.Context, _ *a2a.GetExtendedAgentCardRequest) (*a2a.AgentCard, error) {
	invocation, err := invocationFromContext(ctx)
	if err != nil {
		return nil, err
	}
	defer h.finishInvocation(invocation, "completed")
	return invocation.Agent.Card, nil
}

func (h *agentGatewayHandler) prepareSend(invocation agentInvocation, request *a2a.SendMessageRequest) (*a2a.SendMessageRequest, AgentTask, AgentInstance, string, error) {
	upstream := cloneSendMessageRequest(request)
	var task AgentTask
	var instance AgentInstance
	var err error
	if upstream.Message.TaskID != "" {
		task, instance, err = h.resolveTask(invocation, string(upstream.Message.TaskID))
		if err != nil {
			return nil, AgentTask{}, AgentInstance{}, "", err
		}
		upstream.Message.TaskID = a2a.TaskID(task.UpstreamTaskID)
	}
	for index, reference := range upstream.Message.ReferenceTasks {
		referenced, _, resolveErr := h.resolveTask(invocation, string(reference))
		if resolveErr != nil {
			return nil, AgentTask{}, AgentInstance{}, "", resolveErr
		}
		upstream.Message.ReferenceTasks[index] = a2a.TaskID(referenced.UpstreamTaskID)
	}
	var leaseID string
	if instance.ID != "" {
		instance, leaseID, err = h.server.store.ReserveAgentInstanceByID(instance.ID, invocation.Deadline)
	} else {
		instance, leaseID, err = h.server.store.ReserveAgentInstance(invocation.Agent.ID, invocation.Deadline)
	}
	if err != nil {
		return nil, AgentTask{}, AgentInstance{}, "", normalizeAgentUpstreamError(err)
	}
	return upstream, task, instance, leaseID, nil
}

func (h *agentGatewayHandler) ensureTask(invocation agentInvocation, instance AgentInstance, task AgentTask, upstreamID string, contextID string) (AgentTask, error) {
	if task.ID != "" {
		return task, nil
	}
	return h.server.store.CreateAgentTask(AgentTask{
		UpstreamTaskID: upstreamID, AgentID: invocation.Agent.ID, InstanceID: instance.ID,
		ProjectID: invocation.Project.ID, APIKeyID: invocation.APIKey.ID, EndUserID: invocation.EndUserID,
		ExecutionID: invocation.ExecutionID, ExecutionStepID: invocation.ParentStepID,
		ContextID: contextID, State: string(a2a.TaskStateSubmitted),
	})
}

func (h *agentGatewayHandler) resolveTask(invocation agentInvocation, id string) (AgentTask, AgentInstance, error) {
	task, found, err := h.server.store.GetAgentTask(id)
	if err != nil || !found || task.AgentID != invocation.Agent.ID || task.ProjectID != invocation.Project.ID ||
		task.APIKeyID != invocation.APIKey.ID || task.EndUserID != invocation.EndUserID {
		return AgentTask{}, AgentInstance{}, a2a.NewError(a2a.ErrTaskNotFound, "Task was not found")
	}
	instance, found, err := h.server.store.GetAgentInstance(task.InstanceID)
	if err != nil || !found || instance.AgentID != invocation.Agent.ID {
		return AgentTask{}, AgentInstance{}, a2a.NewError(a2a.ErrTaskNotFound, "Task instance was not found")
	}
	return task, instance, nil
}

func (h *agentGatewayHandler) applyAgentOutputGuardrails(ctx context.Context, invocation agentInvocation, event a2a.Event) error {
	if _, err := h.server.evaluateOutboundGuardrails(ctx, invocation.Project.ID, agentOutputGuardrailTargets(event)); err != nil {
		if AsHTTPError(err).Status == http.StatusForbidden {
			return a2a.NewError(errAgentOutputGuardrailBlocked, agentOutputGuardrailMessage).WithDetails(map[string]any{
				"code": "guardrail_blocked", "reason": "CONTENT_POLICY_VIOLATION",
			})
		}
		return a2a.NewError(errAgentOutputGuardrailEvaluation, "Agent output content security evaluation failed")
	}
	return nil
}

func agentOutputGuardrailTargets(event a2a.Event) []guardrailTextTarget {
	targets := make([]guardrailTextTarget, 0)
	switch item := event.(type) {
	case *a2a.Task:
		appendAgentMessageGuardrailTargets(&targets, item.Status.Message, "output.task.status.message")
		for index, artifact := range item.Artifacts {
			appendAgentArtifactGuardrailTargets(&targets, artifact, fmt.Sprintf("output.task.artifacts.%d", index))
		}
		for index, message := range item.History {
			appendAgentMessageGuardrailTargets(&targets, message, fmt.Sprintf("output.task.history.%d", index))
		}
	case *a2a.Message:
		appendAgentMessageGuardrailTargets(&targets, item, "output.message")
	case *a2a.TaskStatusUpdateEvent:
		appendAgentMessageGuardrailTargets(&targets, item.Status.Message, "output.status.message")
	case *a2a.TaskArtifactUpdateEvent:
		appendAgentArtifactGuardrailTargets(&targets, item.Artifact, "output.artifact")
	}
	return targets
}

func appendAgentMessageGuardrailTargets(targets *[]guardrailTextTarget, message *a2a.Message, prefix string) {
	if message == nil {
		return
	}
	appendAgentPartGuardrailTargets(targets, message.Parts, prefix+".parts")
}

func appendAgentArtifactGuardrailTargets(targets *[]guardrailTextTarget, artifact *a2a.Artifact, prefix string) {
	if artifact == nil {
		return
	}
	if artifact.Name != "" {
		*targets = append(*targets, guardrailTextTarget{
			fragment: guardrails.Fragment{ID: prefix + ".name", Text: artifact.Name, Mutable: true},
			replace:  func(replacement string) { artifact.Name = replacement },
		})
	}
	if artifact.Description != "" {
		*targets = append(*targets, guardrailTextTarget{
			fragment: guardrails.Fragment{ID: prefix + ".description", Text: artifact.Description, Mutable: true},
			replace:  func(replacement string) { artifact.Description = replacement },
		})
	}
	appendAgentPartGuardrailTargets(targets, artifact.Parts, prefix+".parts")
}

func appendAgentPartGuardrailTargets(targets *[]guardrailTextTarget, parts a2a.ContentParts, prefix string) {
	for index, part := range parts {
		if part == nil || part.Text() == "" {
			continue
		}
		current := part
		*targets = append(*targets, guardrailTextTarget{
			fragment: guardrails.Fragment{ID: fmt.Sprintf("%s.%d.text", prefix, index), Text: current.Text(), Mutable: true},
			replace:  func(replacement string) { current.Content = a2a.Text(replacement) },
		})
	}
}

func (h *agentGatewayHandler) rewriteAndPersistTask(record *AgentTask, task *a2a.Task, eventType string) error {
	if task == nil {
		return nil
	}
	task.ID = a2a.TaskID(record.ID)
	for _, message := range task.History {
		if message != nil && message.TaskID != "" {
			message.TaskID = a2a.TaskID(record.ID)
		}
	}
	if task.Status.Message != nil && task.Status.Message.TaskID != "" {
		task.Status.Message.TaskID = a2a.TaskID(record.ID)
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	record.ContextID = task.ContextID
	record.State = string(task.Status.State)
	record.SnapshotJSON = string(payload)
	*record, err = h.server.store.UpdateAgentTask(*record, eventType, task)
	return err
}

func (h *agentGatewayHandler) rewriteAndPersistEvent(invocation agentInvocation, instance AgentInstance, task AgentTask, event a2a.Event) (AgentTask, error) {
	var upstreamTaskID string
	var contextID string
	switch item := event.(type) {
	case *a2a.Task:
		upstreamTaskID, contextID = string(item.ID), item.ContextID
	case *a2a.Message:
		upstreamTaskID, contextID = string(item.TaskID), item.ContextID
	case *a2a.TaskStatusUpdateEvent:
		upstreamTaskID, contextID = string(item.TaskID), item.ContextID
	case *a2a.TaskArtifactUpdateEvent:
		upstreamTaskID, contextID = string(item.TaskID), item.ContextID
	}
	var err error
	if upstreamTaskID != "" {
		task, err = h.ensureTask(invocation, instance, task, upstreamTaskID, contextID)
		if err != nil {
			return AgentTask{}, err
		}
	}
	if task.ID == "" {
		return task, nil
	}
	switch item := event.(type) {
	case *a2a.Task:
		err = h.rewriteAndPersistTask(&task, item, "task")
		return task, err
	case *a2a.Message:
		item.TaskID = a2a.TaskID(task.ID)
	case *a2a.TaskStatusUpdateEvent:
		item.TaskID = a2a.TaskID(task.ID)
		task.State = string(item.Status.State)
		if item.Status.Message != nil {
			item.Status.Message.TaskID = a2a.TaskID(task.ID)
		}
	case *a2a.TaskArtifactUpdateEvent:
		item.TaskID = a2a.TaskID(task.ID)
	}
	if err := mergeAgentTaskSnapshot(&task, event); err != nil {
		return AgentTask{}, err
	}
	task, err = h.server.store.UpdateAgentTask(task, fmt.Sprintf("%T", event), event)
	return task, err
}

func (h *agentGatewayHandler) client(ctx context.Context, invocation agentInvocation, instance AgentInstance) (*a2aclient.Client, error) {
	if err := validateAgentUpstreamURL(ctx, instance.URL, h.server.config.A2AAllowPrivateUpstreams); err != nil {
		return nil, a2a.NewError(a2a.ErrInternalError, "Agent upstream URL is not allowed")
	}
	transport := &agentCredentialTransport{
		base: newAgentHTTPTransport(h.server.config.A2AAllowPrivateUpstreams), headers: instance.Headers,
		incoming: invocation.IncomingHeaders, allowedForwardHeaders: instance.AllowedForwardHeaders,
		delegationToken: h.server.mintAgentDelegation(invocation, instance.AgentID), traceID: invocation.TraceID,
		parentStepID: invocation.ParentStepID,
	}
	timeout := time.Duration(h.server.config.A2AMaxRuntimeSeconds) * time.Second
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	if !invocation.Deadline.IsZero() {
		remaining := time.Until(invocation.Deadline)
		if remaining <= 0 {
			return nil, a2a.NewError(a2a.ErrServerError, "Agent execution runtime budget was exceeded")
		}
		if remaining < timeout {
			timeout = remaining
		}
	}
	client, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{{
		URL: instance.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
	}}, a2aclient.WithJSONRPCTransport(&http.Client{
		Transport: transport, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}))
	if err != nil {
		return nil, a2a.NewError(a2a.ErrInternalError, "Agent upstream client could not be created")
	}
	return client, nil
}

func (h *agentGatewayHandler) recordInstanceFailure(instance AgentInstance, err error) {
	if errors.Is(err, a2a.ErrTaskNotFound) || errors.Is(err, a2a.ErrTaskNotCancelable) || errors.Is(err, a2a.ErrUnauthorized) {
		return
	}
	cooldown := time.Now().UTC().Add(time.Duration(max(1, h.server.config.ResourceCooldownSeconds)) * time.Second)
	_ = h.server.store.SetAgentInstanceHealth(instance.ID, false, &cooldown)
}

func (h *agentGatewayHandler) recordInstanceSuccess(instance AgentInstance) {
	_ = h.server.store.SetAgentInstanceHealth(instance.ID, true, nil)
}

func (h *agentGatewayHandler) chargeInvocationCost(invocation agentInvocation, instance AgentInstance) error {
	if instance.FixedCostUSD <= 0 || invocation.ExecutionID == "" {
		return nil
	}
	if err := h.server.store.ConsumeAgentExecutionBudget(invocation.ExecutionID, "reserve_cost", 0, instance.FixedCostUSD); err != nil {
		return a2a.NewError(a2a.ErrServerError, err.Error())
	}
	if err := h.server.store.RecordAgentUsage(AgentUsageRecord{
		ExecutionID: invocation.ExecutionID, StepID: invocation.ParentStepID,
		AgentID: invocation.Agent.ID, ProjectID: invocation.Project.ID, APIKeyID: invocation.APIKey.ID,
		SourceType: "agent", CostUSD: instance.FixedCostUSD,
	}); err != nil {
		return a2a.NewError(a2a.ErrInternalError, "Agent usage could not be persisted")
	}
	return nil
}

func (h *agentGatewayHandler) finishInvocation(invocation agentInvocation, status string) {
	if status == "running" || (status == "failed" && h.hasNonTerminalInvocationTask(invocation)) {
		return
	}
	if invocation.ParentStepID != "" {
		_ = h.server.store.FinishAgentExecutionEdge(invocation.ParentStepID, status)
	}
	if invocation.RootExecution && invocation.ExecutionID != "" {
		_ = h.server.store.FinishAgentExecution(invocation.ExecutionID, status)
	}
}

func invocationFromContext(ctx context.Context) (agentInvocation, error) {
	invocation, ok := ctx.Value(agentInvocationContextKey{}).(agentInvocation)
	if !ok {
		return agentInvocation{}, a2a.NewError(a2a.ErrUnauthenticated, "Authentication context is missing")
	}
	return invocation, nil
}

func cloneSendMessageRequest(request *a2a.SendMessageRequest) *a2a.SendMessageRequest {
	data, _ := json.Marshal(request)
	var cloned a2a.SendMessageRequest
	_ = json.Unmarshal(data, &cloned)
	return &cloned
}

func normalizeAgentUpstreamError(err error) error {
	for _, sentinel := range []error{
		a2a.ErrTaskNotFound, a2a.ErrTaskNotCancelable, a2a.ErrUnauthorized, a2a.ErrUnauthenticated,
		a2a.ErrUnsupportedContentType, a2a.ErrInvalidParams, a2a.ErrUnsupportedOperation,
	} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return a2a.NewError(a2a.ErrServerError, "Agent upstream request failed")
}

type agentCredentialTransport struct {
	base                  http.RoundTripper
	headers               map[string]string
	incoming              http.Header
	allowedForwardHeaders []string
	delegationToken       string
	traceID               string
	parentStepID          string
}

func (t *agentCredentialTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	for _, name := range t.allowedForwardHeaders {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if canonical == "" || isAgentForwardHeaderDenied(canonical) {
			continue
		}
		if values := t.incoming.Values(canonical); len(values) > 0 {
			cloned.Header.Del(canonical)
			for _, value := range values {
				cloned.Header.Add(canonical, value)
			}
		}
	}
	for name, value := range t.headers {
		cloned.Header.Set(name, value)
	}
	if t.delegationToken != "" {
		cloned.Header.Set("X-TokenHub-Delegation-Token", t.delegationToken)
	}
	if t.traceID != "" {
		cloned.Header.Set("X-TokenHub-Trace-ID", t.traceID)
	}
	if t.parentStepID != "" {
		cloned.Header.Set("X-TokenHub-Parent-Step-ID", t.parentStepID)
	}
	response, err := t.base.RoundTrip(cloned)
	if err != nil {
		return nil, err
	}
	response.Body = http.MaxBytesReader(nil, response.Body, 16<<20)
	return response, nil
}

func isAgentForwardHeaderDenied(name string) bool {
	if isHopByHopHeader(name) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "host", "content-length", "a2a-version",
		"x-api-key", "x-goog-api-key", "x-tokenhub-delegation-token", "x-tokenhub-trace-id", "x-tokenhub-parent-step-id":
		return true
	default:
		return false
	}
}

func isHopByHopHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade", "host":
		return true
	}
	return false
}

type agentDelegationClaims struct {
	ProjectID    string   `json:"project_id"`
	APIKeyID     string   `json:"api_key_id"`
	EndUserID    string   `json:"end_user_id,omitempty"`
	CallerAgent  string   `json:"caller_agent"`
	ExecutionID  string   `json:"execution_id"`
	TraceID      string   `json:"trace_id"`
	ParentStepID string   `json:"parent_step_id"`
	Depth        int64    `json:"depth"`
	Chain        []string `json:"chain"`
	ExpiresAt    int64    `json:"exp"`
	Deadline     int64    `json:"deadline"`
}

func (s *Server) mintAgentDelegation(invocation agentInvocation, callerAgent string) string {
	claims := agentDelegationClaims{
		ProjectID: invocation.Project.ID, APIKeyID: invocation.APIKey.ID, EndUserID: invocation.EndUserID,
		CallerAgent: callerAgent, ExecutionID: invocation.ExecutionID, ParentStepID: invocation.ParentStepID,
		TraceID: invocation.TraceID,
		Depth:   invocation.Depth + 1, Chain: append(append([]string{}, invocation.Chain...), callerAgent),
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute).Unix(),
	}
	if !invocation.Deadline.IsZero() {
		claims.Deadline = invocation.Deadline.Unix()
	}
	payload, _ := json.Marshal(claims)
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(s.config.SecretKey))
	_, _ = mac.Write([]byte(encoded))
	return "thd_" + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) parseAgentDelegation(token string) (agentDelegationClaims, error) {
	if !strings.HasPrefix(token, "thd_") {
		return agentDelegationClaims{}, ErrInvalidAPIKey
	}
	parts := strings.Split(strings.TrimPrefix(token, "thd_"), ".")
	if len(parts) != 2 {
		return agentDelegationClaims{}, ErrInvalidAPIKey
	}
	mac := hmac.New(sha256.New, []byte(s.config.SecretKey))
	_, _ = mac.Write([]byte(parts[0]))
	want, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(want, mac.Sum(nil)) {
		return agentDelegationClaims{}, ErrInvalidAPIKey
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return agentDelegationClaims{}, ErrInvalidAPIKey
	}
	var claims agentDelegationClaims
	if json.Unmarshal(payload, &claims) != nil || claims.ExpiresAt < time.Now().UTC().Unix() {
		return agentDelegationClaims{}, ErrInvalidAPIKey
	}
	return claims, nil
}

func validateAgentUpstreamURL(ctx context.Context, rawURL string, allowPrivate bool) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return errors.New("invalid upstream URL")
	}
	if parsed.Scheme != "https" && !(allowPrivate && parsed.Scheme == "http") {
		return errors.New("upstream URL must use HTTPS")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if !allowPrivate && isDisallowedAgentUpstreamIP(address.IP) {
			return errors.New("special-use upstream address is not allowed")
		}
	}
	return nil
}

var disallowedAgentUpstreamPrefixes = []netip.Prefix{
	// IPv4 special-use networks, including private, shared, documentation,
	// benchmarking, multicast, and reserved address space.
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	// IPv6 special-use networks. IPv4-mapped addresses are unmapped before
	// matching so they are covered by the IPv4 prefixes above.
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("100:0:0:1::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"),
	netip.MustParsePrefix("3fff::/20"),
	netip.MustParsePrefix("5f00::/16"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

var allocatedAgentUpstreamIPv6Prefix = netip.MustParsePrefix("2000::/3")

func isDisallowedAgentUpstreamIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	address = address.Unmap()
	// IANA currently allocates globally routable IPv6 unicast addresses from
	// 2000::/3. Treat the rest as reserved unless private upstreams are enabled.
	if address.Is6() && !allocatedAgentUpstreamIPv6Prefix.Contains(address) {
		return true
	}
	for _, prefix := range disallowedAgentUpstreamPrefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func newAgentHTTPTransport(allowPrivate bool) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 30 * time.Second
	transport.TLSHandshakeTimeout = 10 * time.Second
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network string, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve Agent upstream: %w", err)
		}
		if len(addresses) == 0 {
			return nil, errors.New("Agent upstream did not resolve to an address")
		}
		for _, candidate := range addresses {
			if !allowPrivate && isDisallowedAgentUpstreamIP(candidate.IP) {
				return nil, errors.New("special-use upstream address is not allowed")
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
	}
	return transport
}

func readAndRestoreBody(r *http.Request, limit int64) ([]byte, error) {
	reader := io.LimitReader(r.Body, limit+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("request body too large")
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}
