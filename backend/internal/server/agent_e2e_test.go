package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"tokenhub/backend/internal/guardrails"
)

type fakeA2AUpstream struct {
	mu                sync.Mutex
	requests          []string
	authorizations    []string
	delegationTokens  []string
	traceIDs          []string
	lastRequestedTask string
}

func (f *fakeA2AUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &request)
	f.mu.Lock()
	f.requests = append(f.requests, request.Method)
	f.authorizations = append(f.authorizations, r.Header.Get("Authorization"))
	f.delegationTokens = append(f.delegationTokens, r.Header.Get("X-TokenHub-Delegation-Token"))
	f.traceIDs = append(f.traceIDs, r.Header.Get("X-TokenHub-Trace-ID"))
	f.mu.Unlock()

	if r.Header.Get("A2A-Version") != "1.0" {
		testWriteJSONRPC(w, request.ID, nil, map[string]any{"code": -32009, "message": "version"})
		return
	}
	switch request.Method {
	case "SendMessage":
		testWriteJSONRPC(w, request.ID, map[string]any{"task": fakeUpstreamTask("upstream-task-1", "agent says hello")}, nil)
	case "GetTask":
		var params struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(request.Params, &params)
		f.mu.Lock()
		f.lastRequestedTask = params.ID
		f.mu.Unlock()
		testWriteJSONRPC(w, request.ID, fakeUpstreamTask(params.ID, "agent says hello"), nil)
	case "CancelTask":
		var params struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(request.Params, &params)
		task := fakeUpstreamTask(params.ID, "canceled")
		task["status"] = map[string]any{"state": "TASK_STATE_CANCELED"}
		testWriteJSONRPC(w, request.ID, task, nil)
	case "SendStreamingMessage", "SubscribeToTask":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		result := map[string]any{"task": fakeUpstreamTask("upstream-stream-task", "")}
		testWriteSSEJSONRPC(w, request.ID, result)
		status := map[string]any{"statusUpdate": map[string]any{
			"taskId": "upstream-stream-task", "contextId": "context-stream",
			"status": map[string]any{
				"state": "TASK_STATE_COMPLETED",
				"message": map[string]any{
					"messageId": "message-stream", "role": "ROLE_AGENT",
					"taskId": "upstream-stream-task", "parts": []any{map[string]any{"text": "streamed reply"}},
				},
			},
		}}
		testWriteSSEJSONRPC(w, request.ID, status)
		artifact := map[string]any{"artifactUpdate": map[string]any{
			"taskId": "upstream-stream-task", "contextId": "context-stream", "append": true, "lastChunk": true,
			"artifact": map[string]any{"artifactId": "stream-artifact", "parts": []any{map[string]any{"text": "streamed artifact"}}},
		}}
		testWriteSSEJSONRPC(w, request.ID, artifact)
	default:
		testWriteJSONRPC(w, request.ID, nil, map[string]any{"code": -32601, "message": "method not found"})
	}
}

func fakeUpstreamTask(id string, text string) map[string]any {
	task := map[string]any{
		"id": id, "contextId": "context-1",
		"status": map[string]any{"state": "TASK_STATE_COMPLETED"},
	}
	if text != "" {
		task["artifacts"] = []any{map[string]any{
			"artifactId": "artifact-1", "parts": []any{map[string]any{"text": text}},
		}}
	}
	return task
}

func testWriteJSONRPC(w http.ResponseWriter, id json.RawMessage, result any, rpcError any) {
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcError != nil {
		payload["error"] = rpcError
	} else {
		payload["result"] = result
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func testWriteSSEJSONRPC(w io.Writer, id json.RawMessage, result any) {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
}

func TestA2A10GatewayEndToEnd(t *testing.T) {
	upstream := &fakeA2AUpstream{}
	upstreamServer := httptest.NewServer(upstream)
	t.Cleanup(upstreamServer.Close)

	store := NewMemoryStoreWithConfig(Config{SecretKey: "test-agent-storage-secret"})
	project := store.CreateProject(Project{Name: "A2A E2E", Status: StatusActive})
	key, secret, err := store.CreateAPIKey(project.ID, APIKey{
		Name: "A2A key", Status: StatusActive, Metadata: map[string]string{"allow_end_user_identity": "true"},
	}, "thk_a2a_e2e")
	if err != nil {
		t.Fatal(err)
	}
	config := Config{
		AdminToken: "dev_admin_token", SecretKey: "test-agent-storage-secret",
		A2AEnabled: true, A2AAllowPrivateUpstreams: true, PublicBaseURL: "https://gateway.example",
	}
	app := NewWithConfig(store, config).Handler()
	card := &a2a.AgentCard{
		Name: "Research Agent", Description: "Researches a topic", Version: "2026.8",
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL: upstreamServer.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
		}},
		Capabilities:       a2a.AgentCapabilities{Streaming: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills:             []a2a.AgentSkill{{ID: "research", Name: "Research", Description: "Research a topic"}},
	}
	registration := doJSON(t, app, http.MethodPost, "/api/admin/agents", AgentRegistration{
		Slug: "research", Card: card, Headers: map[string]string{"Authorization": "Bearer upstream-static"},
	}, "dev_admin_token")
	if registration.Code != http.StatusCreated {
		t.Fatalf("register Agent: %d %s", registration.Code, registration.Body)
	}
	var registered AgentWithDetails
	if err := json.Unmarshal([]byte(registration.Body), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.Source != agentSourceAdmin || len(registered.Instances) != 1 {
		t.Fatalf("unexpected registered Agent: %+v", registered)
	}

	requestBody := map[string]any{
		"jsonrpc": "2.0", "id": "send-1", "method": "SendMessage",
		"params": map[string]any{
			"message": map[string]any{
				"messageId": "message-1", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "hello"}},
			},
		},
	}
	denied := doA2ARequest(t, app, "/a2a/research", secret, "1.0", requestBody)
	missingDenied := doA2ARequest(t, app, "/a2a/missing", secret, "1.0", requestBody)
	if !strings.Contains(denied.Body.String(), "TASK_NOT_FOUND") || denied.Body.String() != missingDenied.Body.String() {
		t.Fatalf("default-deny access was not enforced: %s", denied.Body.String())
	}
	unauthenticated := doA2ARequest(t, app, "/a2a/research", "", "1.0", requestBody)
	missingUnauthenticated := doA2ARequest(t, app, "/a2a/missing", "", "1.0", requestBody)
	if unauthenticated.Body.String() != missingUnauthenticated.Body.String() {
		t.Fatalf("Agent existence was disclosed before authentication: existing=%s missing=%s", unauthenticated.Body.String(), missingUnauthenticated.Body.String())
	}

	binding := doJSON(t, app, http.MethodPost, "/api/admin/agent-access-bindings", AgentAccessBinding{
		AgentID: registered.ID, ScopeType: "api_key", ScopeID: key.ID, Effect: agentBindingAllow, Status: StatusActive,
	}, "dev_admin_token")
	if binding.Code != http.StatusCreated {
		t.Fatalf("create binding: %d %s", binding.Code, binding.Body)
	}
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "A2A input protection",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Blocked marker", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionBlock,
			Config: map[string]any{"keywords": []string{"blocked A2A input"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	upstream.mu.Lock()
	beforeBlocked := len(upstream.requests)
	upstream.mu.Unlock()
	blockedByGuardrail := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "guardrail-1", "method": "SendMessage",
		"params": map[string]any{"message": map[string]any{
			"messageId": "blocked-message", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "blocked A2A input"}},
		}},
	})
	if !strings.Contains(blockedByGuardrail.Body.String(), "CONTENT_POLICY_VIOLATION") {
		t.Fatalf("A2A input guardrail was not enforced: %s", blockedByGuardrail.Body.String())
	}
	upstream.mu.Lock()
	afterBlocked := len(upstream.requests)
	upstream.mu.Unlock()
	if afterBlocked != beforeBlocked {
		t.Fatal("guardrail-blocked A2A request reached the upstream Agent")
	}

	wrongVersion := doA2ARequest(t, app, "/a2a/research", secret, "0.3", requestBody)
	if !strings.Contains(wrongVersion.Body.String(), "VERSION_NOT_SUPPORTED") {
		t.Fatalf("wrong A2A version was not rejected: %s", wrongVersion.Body.String())
	}
	missingVersion := doA2ARequest(t, app, "/a2a/research", secret, "", requestBody)
	if !strings.Contains(missingVersion.Body.String(), "VERSION_NOT_SUPPORTED") {
		t.Fatalf("missing A2A version was not rejected: %s", missingVersion.Body.String())
	}
	unsupportedMethod := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "unsupported-1", "method": "CreateTaskPushConfig", "params": map[string]any{},
	})
	if !strings.Contains(unsupportedMethod.Body.String(), "METHOD_NOT_FOUND") {
		t.Fatalf("unsupported A2A method was not rejected before admission: %s", unsupportedMethod.Body.String())
	}
	malformedSupportedMethod := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": false, "method": "GetTask", "params": map[string]any{"id": "missing"},
	})
	if malformedSupportedMethod.Code != http.StatusOK {
		t.Fatalf("malformed supported A2A method returned an unexpected HTTP status: %d", malformedSupportedMethod.Code)
	}

	sent := doA2ARequest(t, app, "/a2a/research", secret, "1.0", requestBody)
	if sent.Code != http.StatusOK {
		t.Fatalf("SendMessage: %d %s", sent.Code, sent.Body.String())
	}
	var sentPayload struct {
		Result struct {
			Task a2a.Task `json:"task"`
		} `json:"result"`
	}
	if err := json.Unmarshal(sent.Body.Bytes(), &sentPayload); err != nil {
		t.Fatal(err)
	}
	gatewayTaskID := string(sentPayload.Result.Task.ID)
	if !strings.HasPrefix(gatewayTaskID, "atask_") || gatewayTaskID == "upstream-task-1" {
		t.Fatalf("upstream task identity leaked: %s", sent.Body.String())
	}
	aliceRequest := map[string]any{
		"jsonrpc": "2.0", "id": "send-alice", "method": "SendMessage",
		"params": map[string]any{"message": map[string]any{
			"messageId": "message-alice", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "alice"}},
		}},
	}
	aliceSent := doA2ARequestForEndUser(t, app, "/a2a/research", secret, "alice", aliceRequest)
	var alicePayload struct {
		Result struct {
			Task a2a.Task `json:"task"`
		} `json:"result"`
	}
	if err := json.Unmarshal(aliceSent.Body.Bytes(), &alicePayload); err != nil || alicePayload.Result.Task.ID == "" {
		t.Fatalf("end-user task was not created: err=%v body=%s", err, aliceSent.Body.String())
	}
	aliceTaskID := string(alicePayload.Result.Task.ID)
	bobGet := doA2ARequestForEndUser(t, app, "/a2a/research", secret, "bob", map[string]any{
		"jsonrpc": "2.0", "id": "get-bob", "method": "GetTask", "params": map[string]any{"id": aliceTaskID},
	})
	if !strings.Contains(bobGet.Body.String(), "TASK_NOT_FOUND") {
		t.Fatalf("task identity crossed end users: %s", bobGet.Body.String())
	}
	bobList := doA2ARequestForEndUser(t, app, "/a2a/research", secret, "bob", map[string]any{
		"jsonrpc": "2.0", "id": "list-bob", "method": "ListTasks", "params": map[string]any{"pageSize": 10},
	})
	if strings.Contains(bobList.Body.String(), aliceTaskID) {
		t.Fatalf("task listing crossed end users: %s", bobList.Body.String())
	}
	anonymousList := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "list-anonymous", "method": "ListTasks", "params": map[string]any{"pageSize": 10},
	})
	if strings.Contains(anonymousList.Body.String(), aliceTaskID) {
		t.Fatalf("end-user task leaked to an invocation without end-user identity: %s", anonymousList.Body.String())
	}

	getTask := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "get-1", "method": "GetTask", "params": map[string]any{"id": gatewayTaskID},
	})
	if getTask.Code != http.StatusOK || !strings.Contains(getTask.Body.String(), gatewayTaskID) {
		t.Fatalf("GetTask: %d %s", getTask.Code, getTask.Body.String())
	}
	upstream.mu.Lock()
	lastUpstreamTask := upstream.lastRequestedTask
	authHeader := upstream.authorizations[len(upstream.authorizations)-1]
	delegationToken := upstream.delegationTokens[len(upstream.delegationTokens)-1]
	traceID := upstream.traceIDs[len(upstream.traceIDs)-1]
	upstream.mu.Unlock()
	if lastUpstreamTask != "upstream-task-1" {
		t.Fatalf("gateway task did not preserve upstream mapping: %q", lastUpstreamTask)
	}
	if authHeader != "Bearer upstream-static" {
		t.Fatalf("static upstream credential did not override client auth: %q", authHeader)
	}
	if !strings.HasPrefix(delegationToken, "thd_") {
		t.Fatalf("short-lived delegation identity missing: %q", delegationToken)
	}
	if !strings.HasPrefix(traceID, "atrace_") {
		t.Fatalf("Agent trace identity missing: %q", traceID)
	}
	otherKey, otherSecret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Other A2A key", Status: StatusActive}, "thk_a2a_other")
	if err != nil {
		t.Fatal(err)
	}
	otherBinding := doJSON(t, app, http.MethodPost, "/api/admin/agent-access-bindings", AgentAccessBinding{
		AgentID: registered.ID, ScopeType: "api_key", ScopeID: otherKey.ID, Effect: agentBindingAllow, Status: StatusActive,
	}, "dev_admin_token")
	if otherBinding.Code != http.StatusCreated {
		t.Fatalf("create other key binding: %d %s", otherBinding.Code, otherBinding.Body)
	}
	isolatedTask := doA2ARequest(t, app, "/a2a/research", otherSecret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "isolated-get", "method": "GetTask", "params": map[string]any{"id": gatewayTaskID},
	})
	if !strings.Contains(isolatedTask.Body.String(), "TASK_NOT_FOUND") {
		t.Fatalf("task identity crossed API Keys: %s", isolatedTask.Body.String())
	}

	listed := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "list-1", "method": "ListTasks", "params": map[string]any{"pageSize": 10},
	})
	if !strings.Contains(listed.Body.String(), gatewayTaskID) || strings.Contains(listed.Body.String(), "upstream-task-1") {
		t.Fatalf("ListTasks did not use local sanitized task state: %s", listed.Body.String())
	}

	subscribed := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "subscribe-1", "method": "SubscribeToTask", "params": map[string]any{"id": gatewayTaskID},
	})
	if !strings.Contains(subscribed.Header().Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(subscribed.Body.String(), "streamed reply") || strings.Contains(subscribed.Body.String(), "upstream-stream-task") {
		t.Fatalf("SubscribeToTask was not proxied and sanitized: %s", subscribed.Body.String())
	}

	canceled := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "cancel-1", "method": "CancelTask", "params": map[string]any{"id": gatewayTaskID},
	})
	if !strings.Contains(canceled.Body.String(), "TASK_STATE_CANCELED") || strings.Contains(canceled.Body.String(), "upstream-task-1") {
		t.Fatalf("CancelTask was not proxied and sanitized: %s", canceled.Body.String())
	}

	streamed := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "stream-1", "method": "SendStreamingMessage",
		"params": map[string]any{"message": map[string]any{
			"messageId": "message-stream", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "stream"}},
		}},
	})
	if !strings.Contains(streamed.Header().Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(streamed.Body.String(), "streamed reply") || !strings.Contains(streamed.Body.String(), "streamed artifact") ||
		strings.Contains(streamed.Body.String(), "upstream-stream-task") {
		t.Fatalf("stream was not proxied and sanitized: %s", streamed.Body.String())
	}

	responses := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "agent/research", "input": "bridge request",
	}, secret)
	if responses.Code != http.StatusOK || !strings.Contains(responses.Body, "agent says hello") {
		t.Fatalf("Responses bridge failed: %d %s", responses.Code, responses.Body)
	}
	backgroundResponses := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "agent/research", "input": "background bridge request", "background": true,
	}, secret)
	if backgroundResponses.Code != http.StatusBadRequest || !strings.Contains(backgroundResponses.Body, `"code":"agent_background_not_supported"`) {
		t.Fatalf("Agent background Responses were not rejected explicitly: %d %s", backgroundResponses.Code, backgroundResponses.Body)
	}

	cardResponse := doJSON(t, app, http.MethodGet, "/a2a/research/.well-known/agent-card.json", nil, secret)
	if cardResponse.Code != http.StatusOK || !strings.Contains(cardResponse.Body, "https://gateway.example/a2a/research") ||
		strings.Contains(cardResponse.Body, upstreamServer.URL) {
		t.Fatalf("public card was not sanitized: %d %s", cardResponse.Code, cardResponse.Body)
	}
	unauthorizedCard := doJSON(t, app, http.MethodGet, "/a2a/research/.well-known/agent-card.json", nil, "")
	if unauthorizedCard.Code != http.StatusNotFound || strings.Contains(unauthorizedCard.Body, "Research Agent") {
		t.Fatalf("unauthorized Agent discovery leaked registry data: %d %s", unauthorizedCard.Code, unauthorizedCard.Body)
	}

	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "A2A output protection",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Blocked Agent output", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionBlock,
			Config: map[string]any{"keywords": []string{"agent says hello", "streamed artifact"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	var tasksBeforeBlockedOutput int64
	if err := store.db.Model(&AgentTask{}).Count(&tasksBeforeBlockedOutput).Error; err != nil {
		t.Fatal(err)
	}
	blockedOutput := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "blocked-output", "method": "SendMessage",
		"params": map[string]any{"message": map[string]any{
			"messageId": "blocked-output-message", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "safe request"}},
		}},
	})
	if strings.Contains(blockedOutput.Body.String(), "agent says hello") || !strings.Contains(blockedOutput.Body.String(), "CONTENT_POLICY_VIOLATION") {
		t.Fatalf("non-streaming Agent output guardrail was not enforced: %s", blockedOutput.Body.String())
	}
	var tasksAfterBlockedOutput int64
	if err := store.db.Model(&AgentTask{}).Count(&tasksAfterBlockedOutput).Error; err != nil {
		t.Fatal(err)
	}
	if tasksAfterBlockedOutput != tasksBeforeBlockedOutput {
		t.Fatal("guardrail-blocked non-streaming Agent output was persisted")
	}

	var artifactEventsBefore int64
	if err := store.db.Model(&AgentTaskEvent{}).Where("payload_json LIKE ?", "%streamed artifact%").Count(&artifactEventsBefore).Error; err != nil {
		t.Fatal(err)
	}
	blockedArtifact := doA2ARequest(t, app, "/a2a/research", secret, "1.0", map[string]any{
		"jsonrpc": "2.0", "id": "blocked-artifact", "method": "SendStreamingMessage",
		"params": map[string]any{"message": map[string]any{
			"messageId": "blocked-artifact-message", "role": "ROLE_USER", "parts": []any{map[string]any{"text": "safe stream request"}},
		}},
	})
	if strings.Contains(blockedArtifact.Body.String(), "streamed artifact") || !strings.Contains(blockedArtifact.Body.String(), "CONTENT_POLICY_VIOLATION") {
		t.Fatalf("streaming Agent artifact guardrail was not enforced: %s", blockedArtifact.Body.String())
	}
	var artifactEventsAfter int64
	if err := store.db.Model(&AgentTaskEvent{}).Where("payload_json LIKE ?", "%streamed artifact%").Count(&artifactEventsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if artifactEventsAfter != artifactEventsBefore {
		t.Fatal("guardrail-blocked streaming Agent artifact was persisted")
	}

	blockedResponses := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "agent/research", "input": "safe bridged request",
	}, secret)
	if blockedResponses.Code != http.StatusForbidden || !strings.Contains(blockedResponses.Body, `"code":"guardrail_blocked"`) ||
		strings.Contains(blockedResponses.Body, "agent says hello") {
		t.Fatalf("Responses bridge misclassified blocked Agent output: %d %s", blockedResponses.Code, blockedResponses.Body)
	}
	blockedResponsesStream := doJSON(t, app, http.MethodPost, "/v1/responses", map[string]any{
		"model": "agent/research", "input": "safe streamed bridged request", "stream": true,
	}, secret)
	if blockedResponsesStream.Code != http.StatusOK || !strings.Contains(blockedResponsesStream.Body, `"code":"guardrail_blocked"`) ||
		strings.Contains(blockedResponsesStream.Body, "streamed artifact") || strings.Contains(blockedResponsesStream.Body, "agent_upstream_error") {
		t.Fatalf("streaming Responses bridge misclassified blocked Agent output: %d %s", blockedResponsesStream.Code, blockedResponsesStream.Body)
	}

	var persisted AgentInstance
	if err := store.db.First(&persisted, "agent_id = ?", registered.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted.HeadersCiphertext, "upstream-static") || persisted.HeadersCiphertext == "" {
		t.Fatalf("upstream credential was not encrypted at rest: %q", persisted.HeadersCiphertext)
	}
	if persisted.ActiveRequests != 0 {
		t.Fatalf("completed requests leaked %d instance reservations", persisted.ActiveRequests)
	}
	var runningExecutions int64
	if err := store.db.Model(&AgentExecution{}).Where("status = ?", "running").Count(&runningExecutions).Error; err != nil {
		t.Fatal(err)
	}
	if runningExecutions != 0 {
		t.Fatalf("completed requests left %d Agent executions running", runningExecutions)
	}
	executionList := doJSON(t, app, http.MethodGet, "/api/admin/agent-executions?limit=100", nil, "dev_admin_token")
	if executionList.Code != http.StatusOK || !strings.Contains(executionList.Body, registered.ID) {
		t.Fatalf("Agent execution observability list failed: %d %s", executionList.Code, executionList.Body)
	}
	var latestExecution AgentExecution
	if err := store.db.Order("created_at DESC").First(&latestExecution).Error; err != nil {
		t.Fatal(err)
	}
	executionDetails := doJSON(t, app, http.MethodGet, "/api/admin/agent-executions/"+latestExecution.ID, nil, "dev_admin_token")
	if executionDetails.Code != http.StatusOK || !strings.Contains(executionDetails.Body, `"edges"`) || strings.Contains(executionDetails.Body, "upstream-static") {
		t.Fatalf("Agent execution detail failed or leaked credentials: %d %s", executionDetails.Code, executionDetails.Body)
	}
}

func TestAgentMCPBudgetIsIdempotent(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-mcp-secret"})
	deadline := time.Now().UTC().Add(time.Minute)
	execution, err := store.CreateAgentExecution(AgentExecution{
		RootAgentID: "agent-1", ProjectID: "project-1", APIKeyID: "key-1", Status: "running",
		MaxAgentHops: 4, MaxModelCalls: 4, MaxMCPCalls: 1, MaxTokens: 100, MaxCostUSD: 10,
		MaxConcurrency: 4, AgentHops: 1, Deadline: &deadline,
	}, AgentExecutionEdge{ID: "step-root", CalleeType: "agent", CalleeID: "agent-1", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	record := AgentUsageRecord{
		ExecutionID: execution.ID, StepID: "mcp-step-1", AgentID: "agent-1", ProjectID: "project-1", APIKeyID: "key-1",
	}
	if err := store.AdmitAgentMCP(record); err != nil {
		t.Fatal(err)
	}
	if err := store.AdmitAgentMCP(record); err != nil {
		t.Fatalf("duplicate admission was not idempotent: %v", err)
	}
	record.TaskID, record.Tokens, record.CostUSD = "task-1", 12, 0.25
	if err := store.CompleteAgentMCP(record); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentMCP(record); err != nil {
		t.Fatalf("duplicate completion was not idempotent: %v", err)
	}
	var persisted AgentExecution
	if err := store.db.First(&persisted, "id = ?", execution.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.MCPCalls != 1 || persisted.Tokens != 12 || persisted.CostUSD != 0.25 {
		t.Fatalf("MCP usage was counted more than once: %+v", persisted)
	}
	var usageCount int64
	if err := store.db.Model(&AgentUsageRecord{}).Where("step_id = ?", record.StepID).Count(&usageCount).Error; err != nil {
		t.Fatal(err)
	}
	if usageCount != 1 {
		t.Fatalf("expected one MCP usage record, got %d", usageCount)
	}
}

func TestAgentBudgetExceededStatusCannotBeOverwritten(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-budget-secret"})
	deadline := time.Now().UTC().Add(time.Minute)
	execution, err := store.CreateAgentExecution(AgentExecution{
		RootAgentID: "agent-1", ProjectID: "project-1", APIKeyID: "key-1", Status: "running",
		MaxAgentHops: 2, MaxModelCalls: 2, MaxMCPCalls: 2, MaxTokens: 1, MaxCostUSD: 1,
		MaxConcurrency: 2, AgentHops: 1, Deadline: &deadline,
	}, AgentExecutionEdge{ID: "budget-root", CalleeType: "agent", CalleeID: "agent-1", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	err = store.ConsumeAgentExecutionBudget(execution.ID, "usage", 2, 0.5)
	if err == nil || AsHTTPError(err).Code != "agent_token_budget_exceeded" {
		t.Fatalf("token overage was not rejected: %v", err)
	}
	if err := store.FinishAgentExecution(execution.ID, "completed"); err != nil {
		t.Fatal(err)
	}
	var persisted AgentExecution
	if err := store.db.First(&persisted, "id = ?", execution.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "budget_exceeded" || persisted.Tokens != 2 || persisted.CostUSD != 0.5 || persisted.CompletedAt == nil {
		t.Fatalf("budget overage state was lost: %+v", persisted)
	}
}

func TestAgentModelUsageIsIdempotent(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-model-usage-secret"})
	deadline := time.Now().UTC().Add(time.Minute)
	execution, err := store.CreateAgentExecution(AgentExecution{
		RootAgentID: "agent-1", ProjectID: "project-1", APIKeyID: "key-1", Status: "running",
		MaxAgentHops: 2, MaxModelCalls: 2, MaxMCPCalls: 2, MaxTokens: 100, MaxCostUSD: 10,
		MaxConcurrency: 2, AgentHops: 1, ModelCalls: 1, Deadline: &deadline,
	}, AgentExecutionEdge{ID: "model-root", CalleeType: "agent", CalleeID: "agent-1", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	record := AgentUsageRecord{
		ExecutionID: execution.ID, StepID: "model-step-1", AgentID: "agent-1",
		ProjectID: "project-1", APIKeyID: "key-1", Tokens: 20, CostUSD: 0.5,
	}
	if err := store.CompleteAgentModel(record); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteAgentModel(record); err != nil {
		t.Fatalf("duplicate model completion was not idempotent: %v", err)
	}
	var persisted AgentExecution
	if err := store.db.First(&persisted, "id = ?", execution.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Tokens != 20 || persisted.CostUSD != 0.5 {
		t.Fatalf("model usage was counted more than once: %+v", persisted)
	}
}

func TestAgentRevisionRestoresInstanceConfiguration(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-revision-secret"})
	cardV1 := &a2a.AgentCard{
		Name: "Revision Agent", Description: "version one", Version: "1",
		SupportedInterfaces: []*a2a.AgentInterface{{
			URL: "https://agent.example/v1", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
		}},
	}
	created, err := store.SaveAgent(Agent{Slug: "revision-agent", Name: cardV1.Name, Version: cardV1.Version}, cardV1, AgentInstance{
		Name: "primary", URL: "https://agent.example/v1", Status: StatusActive, Healthy: true,
		Priority: 1, Weight: 2, MaxConcurrency: 3, Headers: map[string]string{"Authorization": "Bearer revision-one"},
	}, nil, "admin")
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := store.ListAgentRevisions(created.ID)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("first revision: %v %+v", err, revisions)
	}
	firstRevisionID := revisions[0].ID

	cardV2 := cloneAgentCard(cardV1)
	cardV2.Version = "2"
	cardV2.Description = "version two"
	cardV2.SupportedInterfaces[0].URL = "https://agent.example/v2"
	created.Version, created.Description = cardV2.Version, cardV2.Description
	if _, err := store.SaveAgent(created.Agent, cardV2, AgentInstance{
		Name: "canary", URL: "https://agent.example/v2", Status: StatusActive, Healthy: true,
		Priority: 9, Weight: 1, Headers: map[string]string{"Authorization": "Bearer revision-two"},
	}, nil, "admin"); err != nil {
		t.Fatal(err)
	}
	restored, err := store.RestoreAgentRevision(created.ID, firstRevisionID, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != "1" || restored.Description != "version one" {
		t.Fatalf("Agent Card was not restored: %+v", restored.Agent)
	}
	var active []AgentInstance
	if err := store.db.Where("agent_id = ? AND status = ?", created.ID, StatusActive).Find(&active).Error; err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].URL != "https://agent.example/v1" || active[0].Priority != 1 || active[0].MaxConcurrency != 3 {
		t.Fatalf("instance configuration was not restored: %+v", active)
	}
	hydrated, found, err := store.GetAgentInstance(active[0].ID)
	if err != nil || !found || hydrated.Headers["Authorization"] != "Bearer revision-one" {
		t.Fatalf("encrypted instance credentials were not restored: found=%v err=%v headers=%v", found, err, hydrated.Headers)
	}
}

func TestAgentInstanceSelectionFallsBackWhenPriorityTierIsFull(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-selection-secret"})
	card := &a2a.AgentCard{Name: "Tiered Agent", Version: "1"}
	agent, err := store.SaveAgent(Agent{Slug: "tiered-agent", Name: card.Name, Version: card.Version}, card, AgentInstance{
		Name: "primary", URL: "https://primary.agent.example/a2a", Status: StatusActive, Healthy: true,
		Priority: 10, Weight: 1, MaxConcurrency: 1,
	}, nil, "admin")
	if err != nil {
		t.Fatal(err)
	}
	agent, err = store.SaveAgent(agent.Agent, card, AgentInstance{
		Name: "fallback", URL: "https://fallback.agent.example/a2a", Status: StatusActive, Healthy: true,
		Priority: 1, Weight: 1, MaxConcurrency: 1,
	}, nil, "admin")
	if err != nil {
		t.Fatal(err)
	}
	var primary AgentInstance
	if err := store.db.Where("agent_id = ? AND priority = ?", agent.ID, 10).First(&primary).Error; err != nil {
		t.Fatal(err)
	}
	_, primaryLease, err := store.ReserveAgentInstanceByID(primary.ID, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.ReleaseAgentInstance(primaryLease) }()
	selected, fallbackLease, err := store.ReserveAgentInstance(agent.ID, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.ReleaseAgentInstance(fallbackLease) }()
	if selected.URL != "https://fallback.agent.example/a2a" {
		t.Fatalf("expected lower-priority fallback instance, got %+v", selected)
	}
}

func TestAgentInstanceReservationsAreAtomicAndRecoverExpiredLeases(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-reservation-secret"})
	card := &a2a.AgentCard{Name: "Reserved Agent", Version: "1"}
	agent, err := store.SaveAgent(Agent{Slug: "reserved-agent", Name: card.Name, Version: card.Version}, card, AgentInstance{
		Name: "only", URL: "https://reserved.agent.example/a2a", Status: StatusActive, Healthy: true,
		Priority: 1, Weight: 1, MaxConcurrency: 1,
	}, nil, "admin")
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	type reservationResult struct {
		leaseID string
		err     error
	}
	results := make(chan reservationResult, 2)
	for range 2 {
		go func() {
			<-start
			_, leaseID, reserveErr := store.ReserveAgentInstance(agent.ID, time.Now().Add(time.Minute))
			results <- reservationResult{leaseID: leaseID, err: reserveErr}
		}()
	}
	close(start)
	var leaseID string
	var successes, exhausted int
	for range 2 {
		result := <-results
		if result.err == nil {
			successes++
			leaseID = result.leaseID
			continue
		}
		if AsHTTPError(result.err).Code == "agent_concurrency_exhausted" {
			exhausted++
			continue
		}
		t.Fatalf("unexpected concurrent reservation error: %v", result.err)
	}
	if successes != 1 || exhausted != 1 {
		t.Fatalf("max_concurrency=1 admitted successes=%d exhausted=%d", successes, exhausted)
	}
	if err := store.ReleaseAgentInstance(leaseID); err != nil {
		t.Fatal(err)
	}

	_, expiredLease, err := store.ReserveAgentInstance(agent.ID, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&AgentInstanceLease{}).Where("id = ?", expiredLease).Update("expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	_, recoveredLease, err := store.ReserveAgentInstance(agent.ID, time.Now().Add(time.Minute))
	if err != nil {
		var leases []AgentInstanceLease
		var current AgentInstance
		_ = store.db.First(&current, "agent_id = ?", agent.ID).Error
		_ = store.db.Where("instance_id = ?", current.ID).Find(&leases).Error
		t.Fatalf("expired reservation was not recovered: %v instance=%+v leases=%+v", err, current, leases)
	}
	if err := store.ReleaseAgentInstance(recoveredLease); err != nil {
		t.Fatal(err)
	}
	var instance AgentInstance
	if err := store.db.First(&instance, "agent_id = ?", agent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if instance.ActiveRequests != 0 {
		t.Fatalf("reservation counter was not reconciled: %+v", instance)
	}
}

func TestDisabledAgentAccessGroupDoesNotAuthorize(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-access-group-secret"})
	project := store.CreateProject(Project{Name: "Agent access group", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "group key", Status: StatusActive}, "thk_group_test")
	if err != nil {
		t.Fatal(err)
	}
	card := &a2a.AgentCard{Name: "Group Agent", Version: "1"}
	agent, err := store.SaveAgent(Agent{Slug: "group-agent", Name: card.Name, Version: card.Version}, card, AgentInstance{}, nil, "admin")
	if err != nil {
		t.Fatal(err)
	}
	group, err := store.SaveAgentAccessGroup(AgentAccessGroup{Name: "disabled", Status: StatusDisabled}, []AgentAccessGroupMember{{
		SubjectType: "api_key", SubjectID: key.ID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAgentAccessBinding(AgentAccessBinding{
		AgentID: agent.ID, ScopeType: "access_group", ScopeID: group.ID, Effect: agentBindingAllow, Status: StatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	server := NewWithConfig(store, Config{SecretKey: "agent-access-group-secret", A2AEnabled: true})
	invocation := agentInvocation{Agent: agent, Project: project, APIKey: key}
	if server.authorizeAgentInvocationForMethod(invocation, "", false) {
		t.Fatal("disabled Agent access group granted access")
	}
	group.Status = StatusActive
	if _, err := store.SaveAgentAccessGroup(group, []AgentAccessGroupMember{{SubjectType: "api_key", SubjectID: key.ID}}); err != nil {
		t.Fatal(err)
	}
	if !server.authorizeAgentInvocationForMethod(invocation, "", false) {
		t.Fatal("active Agent access group did not grant access")
	}
}

func TestAgentRedirectsDoNotForwardCredentials(t *testing.T) {
	var redirectedRequests int
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests++
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(redirectTarget.Close)
	redirectSource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	t.Cleanup(redirectSource.Close)

	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-redirect-secret"})
	server := NewWithConfig(store, Config{
		SecretKey: "agent-redirect-secret", A2AEnabled: true, A2AAllowPrivateUpstreams: true, A2AMaxRuntimeSeconds: 60,
	})
	if _, err := server.fetchAgentCard(context.Background(), redirectSource.URL, map[string]string{"Authorization": "Bearer card-secret"}); err == nil {
		t.Fatal("Agent Card redirect was followed")
	}
	if redirectedRequests != 0 {
		t.Fatal("Agent Card redirect reached a different origin")
	}

	invocation := agentInvocation{
		Agent: AgentWithDetails{Agent: Agent{ID: "agent-redirect"}}, Project: Project{ID: "project-redirect"},
		APIKey: APIKey{ID: "key-redirect"}, ExecutionID: "execution-redirect", TraceID: "trace-redirect",
		ParentStepID: "step-redirect", Deadline: time.Now().Add(time.Minute),
	}
	handler := &agentGatewayHandler{server: server}
	client, err := handler.client(context.Background(), invocation, AgentInstance{
		AgentID: invocation.Agent.ID, URL: redirectSource.URL, Headers: map[string]string{"Authorization": "Bearer runtime-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	message := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("redirect"))
	message.ID = "redirect-message"
	_, err = client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: message})
	if err == nil {
		t.Fatal("runtime Agent redirect unexpectedly succeeded")
	}
	if redirectedRequests != 0 {
		t.Fatal("runtime Agent redirect leaked a request to a different origin")
	}
}

func TestA2ARegistrationBlocksPrivateUpstreamByDefault(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(upstream.Close)
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-ssrf-secret"})
	app := NewWithConfig(store, Config{AdminToken: "dev_admin_token", SecretKey: "agent-ssrf-secret", A2AEnabled: true}).Handler()
	response := doJSON(t, app, http.MethodPost, "/api/admin/agents", AgentRegistration{
		Slug: "blocked", Card: &a2a.AgentCard{
			Name: "Blocked", Version: "1", SupportedInterfaces: []*a2a.AgentInterface{{
				URL: upstream.URL, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
			}},
		},
	}, "dev_admin_token")
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body, "agent_upstream_not_allowed") {
		t.Fatalf("private upstream SSRF protection failed: %d %s", response.Code, response.Body)
	}
}

func TestAgentUpstreamSpecialUseAddressesAreBlockedBeforeCredentialedDial(t *testing.T) {
	tests := []struct {
		address    string
		disallowed bool
	}{
		{address: "0.0.0.0", disallowed: true},
		{address: "100.100.100.200", disallowed: true},
		{address: "192.0.2.10", disallowed: true},
		{address: "198.18.0.1", disallowed: true},
		{address: "224.0.0.1", disallowed: true},
		{address: "2001:db8::1", disallowed: true},
		{address: "64:ff9b:1::1", disallowed: true},
		{address: "1.1.1.1", disallowed: false},
		{address: "2606:4700:4700::1111", disallowed: false},
	}
	for _, test := range tests {
		t.Run(test.address, func(t *testing.T) {
			if got := isDisallowedAgentUpstreamIP(net.ParseIP(test.address)); got != test.disallowed {
				t.Fatalf("isDisallowedAgentUpstreamIP(%s) = %v, want %v", test.address, got, test.disallowed)
			}
		})
	}

	if err := validateAgentUpstreamURL(context.Background(), "https://100.100.100.200/a2a", false); err == nil || !strings.Contains(err.Error(), "special-use") {
		t.Fatalf("CGNAT upstream passed registration validation: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://100.100.100.200/a2a", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	transport := &agentCredentialTransport{
		base: newAgentHTTPTransport(false),
		headers: map[string]string{
			"Authorization": "Bearer must-not-leak",
		},
		delegationToken: "thd_must-not-leak",
	}
	if _, err := transport.RoundTrip(request); err == nil || !strings.Contains(err.Error(), "special-use") {
		t.Fatalf("credential-bearing CGNAT dial was not rejected: %v", err)
	}
}

func TestAgentOutputGuardrailsCoverMessagesStatusesAndArtifacts(t *testing.T) {
	store := NewMemoryStoreWithConfig(Config{SecretKey: "agent-output-guardrail-secret"})
	project := store.CreateProject(Project{Name: "Agent output guardrails", Status: StatusActive})
	if _, err := store.CreateGuardrailPolicy(guardrails.Policy{
		Name: "Block Agent output marker",
		DetectionItems: []guardrails.DetectionItem{{
			Name: "Blocked output", DetectorType: guardrails.DetectorPattern, Action: guardrails.ActionBlock,
			Config: map[string]any{"keywords": []string{"blocked output marker"}},
		}},
		Bindings: []guardrails.Binding{{ScopeType: guardrails.ScopeAllProjects}},
	}); err != nil {
		t.Fatal(err)
	}
	handler := &agentGatewayHandler{server: NewWithConfig(store, Config{SecretKey: "agent-output-guardrail-secret"})}
	invocation := agentInvocation{Project: project}
	message := func() *a2a.Message {
		result := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("blocked output marker"))
		result.ID = "output-message"
		return result
	}
	tests := []struct {
		name  string
		event a2a.Event
	}{
		{name: "message", event: message()},
		{name: "task status", event: &a2a.Task{
			ID: "task-output", ContextID: "context-output",
			Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: message()},
		}},
		{name: "streaming status", event: &a2a.TaskStatusUpdateEvent{
			TaskID: "task-output", ContextID: "context-output",
			Status: a2a.TaskStatus{State: a2a.TaskStateCompleted, Message: message()},
		}},
		{name: "task artifact", event: &a2a.Task{
			ID: "task-artifact", ContextID: "context-output", Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
			Artifacts: []*a2a.Artifact{{ID: "artifact-output", Parts: a2a.ContentParts{a2a.NewTextPart("blocked output marker")}}},
		}},
		{name: "streaming artifact", event: &a2a.TaskArtifactUpdateEvent{
			TaskID: "task-output", ContextID: "context-output",
			Artifact: &a2a.Artifact{ID: "artifact-output", Parts: a2a.ContentParts{a2a.NewTextPart("blocked output marker")}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := handler.applyAgentOutputGuardrails(context.Background(), invocation, test.event)
			if !errors.Is(err, errAgentOutputGuardrailBlocked) {
				t.Fatalf("output guardrail returned %v", err)
			}
		})
	}

	safe := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("safe output"))
	safe.ID = "safe-output"
	if err := handler.applyAgentOutputGuardrails(context.Background(), invocation, safe); err != nil {
		t.Fatalf("safe Agent output was blocked: %v", err)
	}
}

func TestAgentHeaderConfigurationRejectsCredentialForwarding(t *testing.T) {
	registration := AgentRegistration{
		Headers:               map[string]string{"authorization": "Bearer upstream"},
		AllowedForwardHeaders: []string{"traceparent", "Authorization"},
	}
	if err := validateAgentHeaderConfiguration(&registration); err == nil || AsHTTPError(err).Code != "invalid_agent_forward_header" {
		t.Fatalf("client Authorization forwarding was not rejected: %v", err)
	}
	registration = AgentRegistration{Headers: map[string]string{"A2A-Version": "0.3"}}
	if err := validateAgentHeaderConfiguration(&registration); err == nil || AsHTTPError(err).Code != "invalid_agent_static_header" {
		t.Fatalf("static protocol-version override was not rejected: %v", err)
	}
}

func doA2ARequest(t *testing.T, handler http.Handler, path string, token string, version string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	if version != "" {
		request.Header.Set("A2A-Version", version)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func doA2ARequestForEndUser(t *testing.T, handler http.Handler, path string, token string, endUserID string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("A2A-Version", "1.0")
	request.Header.Set("X-TokenHub-End-User-ID", endUserID)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
