package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestAgentStreamEventsUpdateListTaskSnapshot(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-snapshot-secret"})
	initial := &a2a.Task{
		ID: "atask_snapshot", ContextID: "context-snapshot",
		Status: a2a.TaskStatus{State: a2a.TaskStateSubmitted},
	}
	initialJSON, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.CreateAgentTask(AgentTask{
		ID: "atask_snapshot", UpstreamTaskID: "upstream-snapshot", AgentID: "agent-snapshot",
		InstanceID: "instance-snapshot", ProjectID: "project-snapshot", APIKeyID: "key-snapshot",
		ExecutionID: "execution-snapshot", ExecutionStepID: "step-snapshot",
		ContextID: "context-snapshot", State: string(a2a.TaskStateSubmitted), SnapshotJSON: string(initialJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := &agentGatewayHandler{server: NewWithConfig(store, Config{SecretKey: "agent-snapshot-secret"})}
	invocation := agentInvocation{
		Agent:   AgentWithDetails{Agent: Agent{ID: record.AgentID}},
		Project: Project{ID: record.ProjectID}, APIKey: APIKey{ID: record.APIKeyID},
	}
	statusMessage := a2a.NewMessageForTask(a2a.MessageRoleAgent, initial, a2a.NewTextPart("finished"))
	statusMessage.ID = "status-message"
	record, err = handler.rewriteAndPersistEvent(invocation, AgentInstance{}, record, &a2a.TaskStatusUpdateEvent{
		TaskID: "upstream-snapshot", ContextID: "context-snapshot",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: statusMessage},
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err = handler.rewriteAndPersistEvent(invocation, AgentInstance{}, record, &a2a.TaskArtifactUpdateEvent{
		TaskID: "upstream-snapshot", ContextID: "context-snapshot",
		Artifact: &a2a.Artifact{ID: "artifact-snapshot", Parts: a2a.ContentParts{a2a.NewTextPart("first")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler.rewriteAndPersistEvent(invocation, AgentInstance{}, record, &a2a.TaskArtifactUpdateEvent{
		TaskID: "upstream-snapshot", ContextID: "context-snapshot", Append: true, LastChunk: true,
		Artifact: &a2a.Artifact{ID: "artifact-snapshot", Parts: a2a.ContentParts{a2a.NewTextPart(" second")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.WithValue(context.Background(), agentInvocationContextKey{}, invocation)
	listed, err := handler.ListTasks(ctx, &a2a.ListTasksRequest{PageSize: 10, IncludeArtifacts: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tasks) != 1 {
		t.Fatalf("ListTasks returned %d tasks", len(listed.Tasks))
	}
	task := listed.Tasks[0]
	if task.Status.State != a2a.TaskStateCompleted || task.Status.Message == nil ||
		task.Status.Message.TaskID != a2a.TaskID(record.ID) {
		t.Fatalf("streamed status was not merged into the task snapshot: %+v", task.Status)
	}
	if len(task.Artifacts) != 1 || agentPartsText(task.Artifacts[0].Parts) != "first second" {
		t.Fatalf("streamed artifact was not merged into the task snapshot: %+v", task.Artifacts)
	}
}

type asyncAgentReviewUpstream struct {
	mu               sync.Mutex
	delegationTokens []string
	traceIDs         []string
}

func (f *asyncAgentReviewUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			ID string `json:"id"`
		} `json:"params"`
	}
	_ = json.Unmarshal(body, &request)
	f.mu.Lock()
	f.delegationTokens = append(f.delegationTokens, r.Header.Get("X-TokenHub-Delegation-Token"))
	f.traceIDs = append(f.traceIDs, r.Header.Get("X-TokenHub-Trace-ID"))
	f.mu.Unlock()
	switch request.Method {
	case "SendMessage":
		testWriteJSONRPC(w, request.ID, map[string]any{"task": map[string]any{
			"id": "upstream-async-task", "contextId": "async-context",
			"status": map[string]any{"state": "TASK_STATE_SUBMITTED"},
		}}, nil)
	case "GetTask":
		testWriteJSONRPC(w, request.ID, map[string]any{
			"id": request.Params.ID, "contextId": "async-context",
			"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
		}, nil)
	default:
		testWriteJSONRPC(w, request.ID, nil, map[string]any{"code": -32601, "message": "method not found"})
	}
}

func TestNonTerminalAgentTaskKeepsAndResumesExecution(t *testing.T) {
	upstream := &asyncAgentReviewUpstream{}
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)

	const secretKey = "agent-async-secret"
	store := NewMemoryStoreWithConfig(Config{SecretKey: secretKey})
	project := store.CreateProject(Project{Name: "Async Agent", Status: StatusActive})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Async key", Status: StatusActive}, "thk_async_agent")
	if err != nil {
		t.Fatal(err)
	}
	card := &a2a.AgentCard{
		Name: "Async Agent", Version: "1",
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL: upstreamServer.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
		}},
	}
	agent, err := store.SaveAgent(Agent{Slug: "async-agent", Name: card.Name, Version: card.Version}, card, AgentInstance{
		Name: "primary", URL: upstreamServer.URL, Status: StatusActive, Healthy: true, Weight: 1, MaxConcurrency: 4,
	}, nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAgentAccessBinding(AgentAccessBinding{
		AgentID: agent.ID, ScopeType: "api_key", ScopeID: key.ID, Effect: agentBindingAllow, Status: StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{
		SecretKey: secretKey, A2AEnabled: true, A2AAllowPrivateUpstreams: true, A2AMaxRuntimeSeconds: 60,
	})
	app := server.Handler()
	sent := doA2ARequest(t, app, "/a2a/async-agent", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "async-send", "method": "SendMessage",
		"params": map[string]any{"message": map[string]any{
			"messageId": "async-message", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "start"}},
		}},
	})
	var sentPayload struct {
		Result struct {
			Task a2a.Task `json:"task"`
		} `json:"result"`
	}
	if sent.Code != http.StatusOK || json.Unmarshal(sent.Body.Bytes(), &sentPayload) != nil || sentPayload.Result.Task.ID == "" {
		t.Fatalf("async SendMessage failed: %d %s", sent.Code, sent.Body.String())
	}
	task, found, err := store.GetAgentTask(string(sentPayload.Result.Task.ID))
	if err != nil || !found {
		t.Fatalf("async task was not persisted: found=%v err=%v", found, err)
	}
	details, found, err := store.GetAgentExecutionDetails(task.ExecutionID)
	if err != nil || !found || details.Status != "running" || task.ExecutionStepID == "" {
		t.Fatalf("non-terminal task did not keep its execution running: found=%v err=%v task=%+v execution=%+v", found, err, task, details.AgentExecution)
	}

	upstream.mu.Lock()
	delegationToken := upstream.delegationTokens[0]
	firstTraceID := upstream.traceIDs[0]
	upstream.mu.Unlock()
	background := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "ordinary-model", "input": "must not queue", "background": true,
	}, delegationToken)
	if background.Code != http.StatusBadRequest || !strings.Contains(background.Body, `"code":"delegated_background_not_supported"`) {
		t.Fatalf("delegated background request was not rejected: %d %s", background.Code, background.Body)
	}
	details, _, err = store.GetAgentExecutionDetails(task.ExecutionID)
	if err != nil || details.ModelCalls != 0 {
		t.Fatalf("rejected background request consumed a model call: err=%v execution=%+v", err, details.AgentExecution)
	}
	var jobs int64
	if err := store.db.Model(&ResponseJob{}).Count(&jobs).Error; err != nil || jobs != 0 {
		t.Fatalf("delegated background request was queued: count=%d err=%v", jobs, err)
	}

	fetched := doA2ARequest(t, app, "/a2a/async-agent", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "async-get", "method": "GetTask", "params": map[string]any{"id": task.ID},
	})
	if fetched.Code != http.StatusOK || !strings.Contains(fetched.Body.String(), "TASK_STATE_COMPLETED") {
		t.Fatalf("async GetTask failed: %d %s", fetched.Code, fetched.Body.String())
	}
	details, found, err = store.GetAgentExecutionDetails(task.ExecutionID)
	if err != nil || !found || details.Status != "completed" {
		t.Fatalf("terminal task did not complete its original execution: found=%v err=%v execution=%+v", found, err, details.AgentExecution)
	}
	var executions int64
	if err := store.db.Model(&AgentExecution{}).Count(&executions).Error; err != nil || executions != 1 {
		t.Fatalf("GetTask created a second root execution: count=%d err=%v", executions, err)
	}
	upstream.mu.Lock()
	secondTraceID := upstream.traceIDs[1]
	secondDelegationToken := upstream.delegationTokens[1]
	upstream.mu.Unlock()
	secondClaims, err := server.parseAgentDelegation(secondDelegationToken)
	if err != nil || firstTraceID == "" || secondTraceID != firstTraceID || secondClaims.ExecutionID != task.ExecutionID {
		t.Fatalf("GetTask did not resume trace/execution: err=%v first_trace=%q second_trace=%q claims=%+v", err, firstTraceID, secondTraceID, secondClaims)
	}
}

func TestDirectDelegatedAgentInvocationRejectsExpiredAPIKey(t *testing.T) {
	const secretKey = "agent-expired-key-secret"
	store := NewMemoryStoreWithConfig(Config{SecretKey: secretKey})
	project := store.CreateProject(Project{Name: "Expired delegation", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Expiring key", Status: StatusActive}, "thk_expiring_agent")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(time.Minute)
	execution, err := store.CreateAgentExecution(AgentExecution{
		RootAgentID: "root-agent", ProjectID: project.ID, APIKeyID: key.ID, TraceID: "trace-expired", Status: "running",
		MaxAgentHops: 8, MaxModelCalls: 8, MaxMCPCalls: 8, MaxTokens: 1000, MaxCostUSD: 10, MaxConcurrency: 8,
		AgentHops: 1, Deadline: &deadline,
	}, AgentExecutionEdge{ID: "step-expired", CalleeType: "agent", CalleeID: "root-agent", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{SecretKey: secretKey, A2AEnabled: true, A2AMaxAgentHops: 8})
	token := server.mintAgentDelegation(agentInvocation{
		Project: project, APIKey: key, ExecutionID: execution.ID, TraceID: execution.TraceID,
		ParentStepID: "step-expired", Deadline: deadline, Chain: []string{"root-agent"},
	}, "root-agent")
	expiredAt := time.Now().UTC().Add(-time.Minute)
	if err := store.db.Model(&APIKey{}).Where("id = ?", key.ID).Update("expires_at", &expiredAt).Error; err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/a2a/nested-agent", bytes.NewReader(nil))
	request.Header.Set("Authorization", "Bearer "+token)
	_, err = server.authenticateAgentInvocation(request, AgentWithDetails{Agent: Agent{ID: "nested-agent"}})
	if err == nil || AsHTTPError(err).Code != ErrInvalidAPIKey.Code {
		t.Fatalf("expired API key authorized a direct delegated Agent call: %v", err)
	}
}

func TestAgentResponsesAuthorizationIsNonEnumerable(t *testing.T) {
	const secretKey = "agent-responses-enumeration-secret"
	store := NewMemoryStoreWithConfig(Config{SecretKey: secretKey})
	project := store.CreateProject(Project{Name: "Responses enumeration", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Denied key", Status: StatusActive}, "thk_agent_denied")
	if err != nil {
		t.Fatal(err)
	}
	card := &a2a.AgentCard{Name: "Hidden Agent", Version: "1"}
	if _, err := store.SaveAgent(Agent{Slug: "hidden-agent", Name: card.Name, Version: card.Version}, card, AgentInstance{}, nil, "test"); err != nil {
		t.Fatal(err)
	}
	app := NewWithConfig(store, Config{SecretKey: secretKey, A2AEnabled: true}).Handler()
	existing := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "agent/hidden-agent", "input": "probe",
	}, secret)
	missing := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "agent/missing-agent", "input": "probe",
	}, secret)
	var existingPayload, missingPayload struct {
		Error map[string]any `json:"error"`
	}
	existingErr := json.Unmarshal([]byte(existing.Body), &existingPayload)
	missingErr := json.Unmarshal([]byte(missing.Body), &missingPayload)
	existingErrorJSON, _ := json.Marshal(existingPayload.Error)
	missingErrorJSON, _ := json.Marshal(missingPayload.Error)
	if existing.Code != http.StatusNotFound || missing.Code != http.StatusNotFound || existingErr != nil || missingErr != nil ||
		!bytes.Equal(existingErrorJSON, missingErrorJSON) || strings.Contains(existing.Body, "agent_access_denied") {
		t.Fatalf("Responses authorization disclosed Agent existence: existing=%d %s missing=%d %s", existing.Code, existing.Body, missing.Code, missing.Body)
	}
}
