package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"tokenhub/backend/internal/guardrails"
)

var agentSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func (s *Server) registerAgentRoutes() {
	s.mux.HandleFunc("/api/admin/agents", s.handleAdminAgents)
	s.mux.HandleFunc("/api/admin/agents/", s.handleAdminAgentItem)
	s.mux.HandleFunc("/api/admin/agent-access-bindings", s.handleAdminAgentAccessBindings)
	s.mux.HandleFunc("/api/admin/agent-access-bindings/", s.handleAdminAgentAccessBindingItem)
	s.mux.HandleFunc("/api/admin/agent-access-groups", s.handleAdminAgentAccessGroups)
	s.mux.HandleFunc("/api/admin/agent-executions", s.handleAdminAgentExecutions)
	s.mux.HandleFunc("/api/admin/agent-executions/", s.handleAdminAgentExecutionItem)
	if !s.config.A2AEnabled {
		return
	}
	s.mux.HandleFunc("/.well-known/agent-card.json", s.handleAgentCard)
	s.mux.HandleFunc("/a2a/", s.handleAgentGateway)
	s.mux.HandleFunc("/api/a2a/executions/mcp", s.handleAgentMCPBudget)
}

func (s *Server) handleAdminAgentExecutions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "routing", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			limit = parsed
		}
	}
	executions, err := s.store.ListAgentExecutions(limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": executions})
}

func (s *Server) handleAdminAgentExecutionItem(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "routing", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/agent-executions/"), "/")
	details, found, err := s.store.GetAgentExecutionDetails(id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "agent_execution_not_found", "Agent execution was not found"))
		return
	}
	writeJSON(w, http.StatusOK, details)
}

type agentMCPBudgetRequest struct {
	Phase   string  `json:"phase"`
	StepID  string  `json:"step_id"`
	TaskID  string  `json:"task_id,omitempty"`
	Tokens  int64   `json:"tokens,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
}

func (s *Server) handleAgentMCPBudget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	token := bearerToken(r)
	if token == "" {
		token = strings.TrimSpace(r.Header.Get("X-TokenHub-Delegation-Token"))
	}
	claims, err := s.parseAgentDelegation(token)
	if err != nil || claims.ExecutionID == "" || claims.CallerAgent == "" {
		writeError(w, r, ErrInvalidAPIKey)
		return
	}
	var request agentMCPBudgetRequest
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	if strings.TrimSpace(request.StepID) == "" || request.Tokens < 0 || request.CostUSD < 0 {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "invalid_mcp_usage", "step_id and non-negative usage are required"))
		return
	}
	switch strings.ToLower(strings.TrimSpace(request.Phase)) {
	case "admit":
		err = s.store.AdmitAgentMCP(AgentUsageRecord{
			ExecutionID: claims.ExecutionID, StepID: request.StepID, TaskID: request.TaskID,
			AgentID: claims.CallerAgent, ProjectID: claims.ProjectID, APIKeyID: claims.APIKeyID,
		})
	case "complete":
		err = s.store.CompleteAgentMCP(AgentUsageRecord{
			ExecutionID: claims.ExecutionID, StepID: request.StepID, TaskID: request.TaskID,
			AgentID: claims.CallerAgent, ProjectID: claims.ProjectID, APIKeyID: claims.APIKeyID,
			Tokens: request.Tokens, CostUSD: request.CostUSD,
		})
	default:
		err = NewHTTPError(http.StatusBadRequest, "invalid_mcp_phase", "phase must be admit or complete")
	}
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "accepted", "execution_id": claims.ExecutionID, "step_id": request.StepID})
}

func (s *Server) handleAgentCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	slug := strings.TrimSpace(r.URL.Query().Get("agent"))
	if slug == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "agent_required", "agent query parameter is required"))
		return
	}
	s.writePublicAgentCard(w, r, slug)
}

func (s *Server) handleAgentGateway(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/a2a/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "agent_not_found", "Agent was not found"))
		return
	}
	slug := parts[0]
	if len(parts) == 2 && parts[1] == ".well-known" {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	if len(parts) == 2 && parts[1] == "agent-card.json" {
		s.writePublicAgentCard(w, r, slug)
		return
	}
	if len(parts) == 3 && parts[1] == ".well-known" && parts[2] == "agent-card.json" {
		s.writePublicAgentCard(w, r, slug)
		return
	}
	if len(parts) != 1 || r.Method != http.MethodPost {
		writeA2AError(w, nil, -32600, "Invalid Request", "INVALID_REQUEST")
		return
	}

	body, err := readAndRestoreBody(r, s.config.MaxJSONRequestBytes)
	if err != nil {
		writeA2AError(w, nil, -32600, "Invalid Request", "INVALID_REQUEST")
		return
	}
	id, method, skillID, validRequest := inspectA2ARequest(body)
	if strings.TrimSpace(r.Header.Get("A2A-Version")) != string(a2a.Version) {
		writeA2AError(w, id, -32009, "This A2A protocol version is not supported", "VERSION_NOT_SUPPORTED")
		return
	}
	if !validRequest {
		writeA2AError(w, id, -32600, "Invalid Request", "INVALID_REQUEST")
		return
	}
	if !supportedA2AGatewayMethod(method) {
		writeA2AError(w, id, -32601, "Method not found", "METHOD_NOT_FOUND")
		return
	}
	agent, found, err := s.store.GetAgentBySlug(slug)
	if err != nil || !found || agent.Status != StatusActive {
		writeA2AAgentNotFound(w, id)
		return
	}
	invocation, err := s.authenticateAgentInvocation(r, agent)
	if err != nil {
		writeA2AAgentNotFound(w, id)
		return
	}
	requireSkill := method == "SendMessage" || method == "SendStreamingMessage"
	if !s.authorizeAgentInvocationForMethod(invocation, skillID, requireSkill) {
		writeA2AAgentNotFound(w, id)
		return
	}
	if requireSkill {
		guardedBody, err := s.applyA2AInputGuardrails(r.Context(), invocation.Project.ID, body)
		if err != nil {
			writeA2AError(w, id, -32008, err.Error(), "CONTENT_POLICY_VIOLATION")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(guardedBody))
		r.ContentLength = int64(len(guardedBody))
	}
	handler := &agentGatewayHandler{server: s}
	resumed := false
	if taskID := inspectA2AContinuationTaskID(body, method); taskID != "" {
		task, _, taskErr := handler.resolveTask(invocation, taskID)
		if taskErr != nil || (invocation.ExecutionID != "" && task.ExecutionID != "" && task.ExecutionID != invocation.ExecutionID) {
			writeA2AAgentNotFound(w, id)
			return
		}
		if invocation.ExecutionID == "" {
			invocation, resumed, err = s.resumeAgentTaskInvocation(invocation, task)
			if err != nil {
				writeA2AError(w, id, -32008, err.Error(), "UNAUTHORIZED")
				return
			}
		}
	}
	if !resumed {
		invocation, err = s.startAgentInvocation(invocation)
		if err != nil {
			writeA2AError(w, id, -32008, err.Error(), "UNAUTHORIZED")
			return
		}
	}

	defer handler.finishInvocation(invocation, "failed")
	ctx := context.WithValue(r.Context(), agentInvocationContextKey{}, invocation)
	s.a2aJSONRPC.ServeHTTP(w, r.WithContext(ctx))
}

func writeA2AAgentNotFound(w http.ResponseWriter, id json.RawMessage) {
	writeA2AError(w, id, -32001, "Agent was not found", "TASK_NOT_FOUND")
}

func (s *Server) applyA2AInputGuardrails(ctx context.Context, projectID string, body []byte) ([]byte, error) {
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		return body, nil
	}
	params, _ := request["params"].(map[string]any)
	message, _ := params["message"].(map[string]any)
	parts, _ := message["parts"].([]any)
	targets := make([]guardrailTextTarget, 0, len(parts))
	for index, value := range parts {
		part, _ := value.(map[string]any)
		text, ok := part["text"].(string)
		if !ok {
			continue
		}
		partIndex := index
		targets = append(targets, guardrailTextTarget{
			fragment: guardrails.Fragment{ID: fmt.Sprintf("params.message.parts.%d.text", index), Text: text, Mutable: true},
			replace: func(replacement string) {
				part["text"] = replacement
				parts[partIndex] = part
			},
		})
	}
	if _, err := s.evaluateOutboundGuardrails(ctx, projectID, targets); err != nil {
		return nil, err
	}
	guarded, err := json.Marshal(request)
	if err != nil {
		return nil, NewHTTPError(http.StatusInternalServerError, "agent_request_encoding_failed", "Agent request could not be encoded")
	}
	return guarded, nil
}

func (s *Server) writePublicAgentCard(w http.ResponseWriter, r *http.Request, slug string) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	agent, found, err := s.store.GetAgentBySlug(slug)
	if err != nil || !found || agent.Status != StatusActive {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "agent_not_found", "Agent was not found"))
		return
	}
	invocation, err := s.authenticateAgentInvocation(r, agent)
	if err != nil || !s.authorizeAgentInvocationForMethod(invocation, "", false) {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "agent_not_found", "Agent was not found"))
		return
	}
	card := cloneAgentCard(agent.Card)
	visibleSkills := make([]a2a.AgentSkill, 0, len(card.Skills))
	for _, skill := range card.Skills {
		if s.authorizeAgentInvocation(invocation, skill.ID) {
			visibleSkills = append(visibleSkills, skill)
		}
	}
	card.Skills = visibleSkills
	baseURL := strings.TrimRight(strings.TrimSpace(s.config.PublicBaseURL), "/")
	if baseURL == "" {
		scheme := "http"
		if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
		baseURL = scheme + "://" + r.Host
	}
	card.SupportedInterfaces = []*a2a.AgentInterface{{
		URL: baseURL + "/a2a/" + agent.Slug, ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version,
	}}
	card.SecuritySchemes = a2a.NamedSecuritySchemes{
		"tokenhubBearer": a2a.HTTPAuthSecurityScheme{Scheme: "Bearer", BearerFormat: "TokenHub API key"},
	}
	card.SecurityRequirements = a2a.SecurityRequirementsOptions{{"tokenhubBearer": {}}}
	writeJSON(w, http.StatusOK, card)
}

func inspectA2ARequest(body []byte) (json.RawMessage, string, string, bool) {
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  struct {
			Metadata map[string]any `json:"metadata"`
			Message  struct {
				Metadata map[string]any `json:"metadata"`
			} `json:"message"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &request) != nil {
		return nil, "", "", false
	}
	request.Method = strings.TrimSpace(request.Method)
	if request.JSONRPC != "2.0" || request.Method == "" {
		return request.ID, request.Method, "", false
	}
	skillID, _ := request.Params.Metadata["skillId"].(string)
	if skillID == "" {
		skillID, _ = request.Params.Message.Metadata["skillId"].(string)
	}
	return request.ID, request.Method, strings.TrimSpace(skillID), true
}

func supportedA2AGatewayMethod(method string) bool {
	switch method {
	case "SendMessage", "SendStreamingMessage", "GetTask", "ListTasks", "CancelTask", "SubscribeToTask":
		return true
	default:
		return false
	}
}

func writeA2AError(w http.ResponseWriter, id json.RawMessage, code int, message string, reason string) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0", "id": id,
		"error": map[string]any{
			"code": code, "message": message,
			"data": []any{map[string]any{"@type": "type.googleapis.com/google.rpc.ErrorInfo", "reason": reason, "domain": "a2a-protocol.org"}},
		},
	})
}

func (s *Server) authenticateAgentInvocation(r *http.Request, agent AgentWithDetails) (agentInvocation, error) {
	token := bearerToken(r)
	if strings.HasPrefix(token, "thd_") {
		claims, err := s.parseAgentDelegation(token)
		if err != nil {
			return agentInvocation{}, ErrInvalidAPIKey
		}
		if claims.Depth > s.config.A2AMaxAgentHops {
			_ = s.store.FinishAgentExecution(claims.ExecutionID, "budget_exceeded")
			return agentInvocation{}, ErrInvalidAPIKey
		}
		if slices.Contains(claims.Chain, agent.ID) {
			_ = s.store.FinishAgentExecution(claims.ExecutionID, "loop_detected")
			return agentInvocation{}, ErrInvalidAPIKey
		}
		project, found := s.store.GetProject(claims.ProjectID)
		if !found || project.Status != StatusActive {
			return agentInvocation{}, ErrInvalidAPIKey
		}
		var key APIKey
		now := time.Now().UTC()
		for _, candidate := range s.store.ListAPIKeys() {
			if candidate.ID == claims.APIKeyID && candidate.ProjectID == project.ID && candidate.Status == StatusActive &&
				(candidate.ExpiresAt == nil || candidate.ExpiresAt.After(now)) {
				key = candidate
				break
			}
		}
		if key.ID == "" {
			return agentInvocation{}, ErrInvalidAPIKey
		}
		invocation := agentInvocation{
			Agent: agent, Project: project, APIKey: key, EndUserID: claims.EndUserID,
			CallerAgent: claims.CallerAgent, ExecutionID: claims.ExecutionID, TraceID: claims.TraceID, ParentStepID: claims.ParentStepID,
			Depth: claims.Depth, Chain: claims.Chain, IncomingHeaders: r.Header.Clone(),
		}
		if claims.Deadline > 0 {
			invocation.Deadline = time.Unix(claims.Deadline, 0).UTC()
		}
		return invocation, nil
	}
	project, key, err := s.authenticate(r)
	if err != nil {
		return agentInvocation{}, err
	}
	endUserID := strings.TrimSpace(r.Header.Get("X-TokenHub-End-User-ID"))
	if endUserID != "" && !strings.EqualFold(key.Metadata["allow_end_user_identity"], "true") {
		return agentInvocation{}, NewHTTPError(http.StatusForbidden, "end_user_identity_not_allowed", "End-user identity is not allowed for this key")
	}
	return agentInvocation{Agent: agent, Project: project, APIKey: key, EndUserID: endUserID, IncomingHeaders: r.Header.Clone()}, nil
}

func (s *Server) authorizeAgentInvocation(invocation agentInvocation, skillID string) bool {
	return s.authorizeAgentInvocationForMethod(invocation, skillID, true)
}

func (s *Server) authorizeAgentInvocationForMethod(invocation agentInvocation, skillID string, requireSkill bool) bool {
	bindings, err := s.store.ListAgentAccessBindings(invocation.Agent.ID)
	if err != nil {
		return false
	}
	scopes := map[string]map[string]bool{
		"global":  {"*": true},
		"project": {invocation.Project.ID: true},
		"api_key": {invocation.APIKey.ID: true},
	}
	if invocation.Project.TeamID != "" {
		scopes["team"] = map[string]bool{invocation.Project.TeamID: true}
	}
	for _, team := range invocation.Project.Teams {
		if scopes["team"] == nil {
			scopes["team"] = map[string]bool{}
		}
		scopes["team"][team.TeamID] = true
	}
	if invocation.EndUserID != "" {
		scopes["end_user"] = map[string]bool{invocation.EndUserID: true}
	}
	if invocation.CallerAgent != "" {
		scopes["agent"] = map[string]bool{invocation.CallerAgent: true}
	}
	groups, err := s.store.ListAgentAccessGroups()
	if err != nil {
		return false
	}
	activeGroups := make(map[string]bool, len(groups))
	for _, group := range groups {
		if group.Status == StatusActive {
			activeGroups[group.ID] = true
		}
	}
	members, err := s.store.ListAgentAccessGroupMembers()
	if err != nil {
		return false
	}
	for _, member := range members {
		if activeGroups[member.GroupID] && scopes[member.SubjectType][member.SubjectID] {
			if scopes["access_group"] == nil {
				scopes["access_group"] = map[string]bool{}
			}
			scopes["access_group"][member.GroupID] = true
		}
	}
	allowed := false
	for _, binding := range bindings {
		if binding.Status != StatusActive || !scopes[binding.ScopeType][binding.ScopeID] || !agentBindingCoversSkill(binding, skillID, requireSkill) {
			continue
		}
		if binding.Effect == agentBindingDeny {
			return false
		}
		if binding.Effect == agentBindingAllow {
			allowed = true
		}
	}
	return allowed
}

func agentBindingCoversSkill(binding AgentAccessBinding, skillID string, requireSkill bool) bool {
	if len(binding.Skills) == 0 {
		return true
	}
	if !requireSkill {
		return true
	}
	return skillID != "" && slices.Contains(binding.Skills, skillID)
}

func (s *Server) startAgentInvocation(invocation agentInvocation) (agentInvocation, error) {
	now := time.Now().UTC()
	deadline := now.Add(time.Duration(s.config.A2AMaxRuntimeSeconds) * time.Second)
	edge := AgentExecutionEdge{
		ID:           NewID("astep"),
		ParentStepID: invocation.ParentStepID, CallerType: "api_key", CallerID: invocation.APIKey.ID,
		CalleeType: "agent", CalleeID: invocation.Agent.ID, Depth: invocation.Depth, Status: "running",
	}
	if invocation.CallerAgent != "" {
		edge.CallerType, edge.CallerID = "agent", invocation.CallerAgent
	}
	if invocation.ExecutionID != "" {
		edge.ExecutionID = invocation.ExecutionID
		if err := s.store.AdmitAgentExecutionEdge(edge); err != nil {
			return agentInvocation{}, err
		}
		invocation.ParentStepID = edge.ID
		return invocation, nil
	}
	execution, err := s.store.CreateAgentExecution(AgentExecution{
		RootAgentID: invocation.Agent.ID, ProjectID: invocation.Project.ID, APIKeyID: invocation.APIKey.ID,
		EndUserID: invocation.EndUserID, TraceID: NewID("atrace"), Status: "running",
		MaxAgentHops: s.config.A2AMaxAgentHops, MaxModelCalls: s.config.A2AMaxModelCalls,
		MaxMCPCalls: s.config.A2AMaxMCPCalls, MaxTokens: s.config.A2AMaxTokens,
		MaxCostUSD: s.config.A2AMaxCostUSD, MaxConcurrency: s.config.A2AMaxConcurrency, AgentHops: 1, Deadline: &deadline,
	}, edge)
	if err != nil {
		return agentInvocation{}, err
	}
	invocation.ExecutionID = execution.ID
	invocation.TraceID = execution.TraceID
	invocation.Deadline = deadline
	invocation.ParentStepID = edge.ID
	invocation.Chain = []string{invocation.Agent.ID}
	invocation.RootExecution = true
	return invocation, nil
}

func (s *Server) handleAdminAgents(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		agents, err := s.store.ListAgents()
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": agents})
	case http.MethodPost:
		var request AgentRegistration
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		agent, err := s.registerAgent(r.Context(), request, agentSourceAdmin, user.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "create", "agent", agent.ID, nil, agent)
		writeJSON(w, http.StatusCreated, agent)
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminAgentItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/agents/"), "/")
	parts := strings.Split(path, "/")
	agent, found, err := s.store.GetAgentByID(parts[0])
	if err != nil || !found {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "agent_not_found", "Agent was not found"))
		return
	}
	if len(parts) == 2 && parts[1] == "revisions" && r.Method == http.MethodGet {
		revisions, err := s.store.ListAgentRevisions(agent.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": revisions})
		return
	}
	if len(parts) == 3 && parts[1] == "revisions" && r.Method == http.MethodPost {
		restored, err := s.store.RestoreAgentRevision(agent.ID, parts[2], user.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "rollback", "agent", agent.ID, agent, restored)
		writeJSON(w, http.StatusOK, restored)
		return
	}
	if len(parts) != 1 {
		writeError(w, r, NewHTTPError(http.StatusNotFound, "not_found", "Not found"))
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, agent)
	case http.MethodPut, http.MethodPatch:
		var request AgentRegistration
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		if request.Slug == "" {
			request.Slug = agent.Slug
		}
		updated, err := s.registerAgent(r.Context(), request, agentSourceAdmin, user.ID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "update", "agent", agent.ID, agent, updated)
		writeJSON(w, http.StatusOK, updated)
	case http.MethodDelete:
		if err := s.store.DeleteAgent(agent.ID); err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "delete", "agent", agent.ID, agent, nil)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminAgentAccessBindings(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		bindings, err := s.store.ListAgentAccessBindings(r.URL.Query().Get("agent_id"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": bindings})
	case http.MethodPost:
		var binding AgentAccessBinding
		if err := s.decodeJSON(w, r, &binding); err != nil {
			writeError(w, r, err)
			return
		}
		if _, found, _ := s.store.GetAgentByID(binding.AgentID); !found {
			writeError(w, r, NewHTTPError(http.StatusBadRequest, "agent_not_found", "Agent was not found"))
			return
		}
		binding, err := s.store.SaveAgentAccessBinding(binding)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "create", "agent_access_binding", binding.ID, nil, binding)
		writeJSON(w, http.StatusCreated, binding)
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) handleAdminAgentAccessBindingItem(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/agent-access-bindings/"), "/")
	if err := s.store.DeleteAgentAccessBinding(id); err != nil {
		writeError(w, r, err)
		return
	}
	s.recordAdminAudit(r, user, "delete", "agent_access_binding", id, nil, nil)
	w.WriteHeader(http.StatusNoContent)
}

type agentAccessGroupRequest struct {
	AgentAccessGroup
	Members []AgentAccessGroupMember `json:"members"`
}

func (s *Server) handleAdminAgentAccessGroups(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "routing", r.Method)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		groups, err := s.store.ListAgentAccessGroups()
		if err != nil {
			writeError(w, r, err)
			return
		}
		members, err := s.store.ListAgentAccessGroupMembers()
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": groups, "members": members})
	case http.MethodPost:
		var request agentAccessGroupRequest
		if err := s.decodeJSON(w, r, &request); err != nil {
			writeError(w, r, err)
			return
		}
		group, err := s.store.SaveAgentAccessGroup(request.AgentAccessGroup, request.Members)
		if err != nil {
			writeError(w, r, err)
			return
		}
		s.recordAdminAudit(r, user, "create", "agent_access_group", group.ID, nil, request)
		writeJSON(w, http.StatusCreated, group)
	default:
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
	}
}

func (s *Server) registerAgent(ctx context.Context, registration AgentRegistration, source string, createdBy string) (AgentWithDetails, error) {
	registration.Slug = strings.ToLower(strings.TrimSpace(registration.Slug))
	if !agentSlugPattern.MatchString(registration.Slug) {
		return AgentWithDetails{}, NewHTTPError(http.StatusBadRequest, "invalid_agent_slug", "slug must contain only lowercase letters, numbers, and hyphens")
	}
	if registration.Weight < 0 || registration.MaxConcurrency < 0 || registration.FixedCostUSD < 0 {
		return AgentWithDetails{}, NewHTTPError(http.StatusBadRequest, "invalid_agent_instance_limits", "weight, max_concurrency, and fixed_cost_usd must be non-negative")
	}
	if err := validateAgentHeaderConfiguration(&registration); err != nil {
		return AgentWithDetails{}, err
	}
	card := registration.Card
	if card == nil && registration.CardURL != "" {
		fetched, err := s.fetchAgentCard(ctx, registration.CardURL, registration.Headers)
		if err != nil {
			return AgentWithDetails{}, err
		}
		card = fetched
	}
	if card == nil {
		return AgentWithDetails{}, NewHTTPError(http.StatusBadRequest, "agent_card_required", "card or card_url is required")
	}
	upstreamURL, err := validateAgentCard(card, registration.UpstreamURL)
	if err != nil {
		return AgentWithDetails{}, err
	}
	if err := validateAgentUpstreamURL(ctx, upstreamURL, s.config.A2AAllowPrivateUpstreams); err != nil {
		return AgentWithDetails{}, NewHTTPError(http.StatusBadRequest, "agent_upstream_not_allowed", err.Error())
	}
	card = cloneAgentCard(card)
	card.SecuritySchemes = nil
	card.SecurityRequirements = nil
	data, _ := json.Marshal(struct {
		Registration AgentRegistration `json:"registration"`
		Card         *a2a.AgentCard    `json:"card"`
		UpstreamURL  string            `json:"upstream_url"`
	}{Registration: registration, Card: card, UpstreamURL: upstreamURL})
	hash := hmac.New(sha256.New, []byte(s.config.SecretKey))
	_, _ = hash.Write(data)
	status := registration.Status
	if status == "" {
		status = StatusActive
	}
	agent := Agent{
		Slug: registration.Slug, Name: card.Name, Description: card.Description, Version: card.Version,
		Status: status, Source: source, SourceHash: hex.EncodeToString(hash.Sum(nil)),
	}
	if source == agentSourceConfig {
		current, found, err := s.store.GetAgentBySlug(agent.Slug)
		if err != nil {
			return AgentWithDetails{}, err
		}
		if found && current.Source == agentSourceConfig && current.SourceHash == agent.SourceHash && current.Status == agent.Status {
			return current, nil
		}
	}
	instance := AgentInstance{
		Name: registration.Slug + "-default", URL: upstreamURL, ProtocolBinding: string(a2a.TransportProtocolJSONRPC),
		ProtocolVersion: string(a2a.Version), Status: StatusActive, Healthy: true,
		Priority: registration.Priority, Weight: registration.Weight, FixedCostUSD: registration.FixedCostUSD,
		MaxConcurrency: registration.MaxConcurrency, Headers: registration.Headers, AllowedForwardHeaders: registration.AllowedForwardHeaders,
	}
	return s.store.SaveAgent(agent, card, instance, agentSkillsFromCard("", card), createdBy)
}

func validateAgentHeaderConfiguration(registration *AgentRegistration) error {
	normalizedHeaders := make(map[string]string, len(registration.Headers))
	for name, value := range registration.Headers {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !validAgentHeaderName(canonical) || strings.ContainsAny(value, "\r\n") || isHopByHopHeader(canonical) ||
			strings.EqualFold(canonical, "Host") || strings.EqualFold(canonical, "Content-Length") ||
			strings.EqualFold(canonical, "A2A-Version") || strings.EqualFold(canonical, "X-TokenHub-Delegation-Token") ||
			strings.EqualFold(canonical, "X-TokenHub-Trace-ID") || strings.EqualFold(canonical, "X-TokenHub-Parent-Step-ID") {
			return NewHTTPError(http.StatusBadRequest, "invalid_agent_static_header", "Static Agent header configuration contains a forbidden header")
		}
		normalizedHeaders[canonical] = value
	}
	registration.Headers = normalizedHeaders
	seen := map[string]bool{}
	allowed := make([]string, 0, len(registration.AllowedForwardHeaders))
	for _, name := range registration.AllowedForwardHeaders {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !validAgentHeaderName(canonical) || isAgentForwardHeaderDenied(canonical) {
			return NewHTTPError(http.StatusBadRequest, "invalid_agent_forward_header", "Agent forward-header allowlist contains a forbidden header")
		}
		if !seen[canonical] {
			seen[canonical] = true
			allowed = append(allowed, canonical)
		}
	}
	registration.AllowedForwardHeaders = allowed
	return nil
}

func validAgentHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", character) {
			continue
		}
		return false
	}
	return true
}

func validateAgentCard(card *a2a.AgentCard, overrideURL string) (string, error) {
	if strings.TrimSpace(card.Name) == "" || strings.TrimSpace(card.Version) == "" {
		return "", NewHTTPError(http.StatusBadRequest, "invalid_agent_card", "Agent Card name and version are required")
	}
	upstreamURL := strings.TrimSpace(overrideURL)
	for _, candidate := range card.SupportedInterfaces {
		if candidate == nil {
			continue
		}
		if candidate.ProtocolVersion != a2a.Version {
			return "", NewHTTPError(http.StatusBadRequest, "unsupported_a2a_version", "Only A2A 1.0 is supported")
		}
		if candidate.ProtocolBinding == a2a.TransportProtocolJSONRPC && upstreamURL == "" {
			upstreamURL = candidate.URL
		}
	}
	if upstreamURL == "" {
		return "", NewHTTPError(http.StatusBadRequest, "jsonrpc_interface_required", "Agent Card must expose an A2A 1.0 JSONRPC interface")
	}
	return upstreamURL, nil
}

func (s *Server) fetchAgentCard(ctx context.Context, rawURL string, headers map[string]string) (*a2a.AgentCard, error) {
	if err := validateAgentUpstreamURL(ctx, rawURL, s.config.A2AAllowPrivateUpstreams); err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "agent_card_url_not_allowed", err.Error())
	}
	client := &http.Client{
		Timeout:       10 * time.Second,
		Transport:     newAgentHTTPTransport(s.config.A2AAllowPrivateUpstreams),
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_agent_card_url", "Agent Card URL is invalid")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "agent_card_fetch_failed", "Agent Card could not be fetched")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, NewHTTPError(http.StatusBadGateway, "agent_card_fetch_failed", "Agent Card endpoint returned a non-success status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return nil, NewHTTPError(http.StatusBadGateway, "agent_card_too_large", "Agent Card exceeds the 1 MiB limit")
	}
	var card a2a.AgentCard
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&card) != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "invalid_agent_card", "Agent Card response is invalid")
	}
	return &card, nil
}

func cloneAgentCard(card *a2a.AgentCard) *a2a.AgentCard {
	if card == nil {
		return &a2a.AgentCard{}
	}
	data, _ := json.Marshal(card)
	var clone a2a.AgentCard
	_ = json.Unmarshal(data, &clone)
	return &clone
}

func initAgentGateway(s *Server) http.Handler {
	return a2asrv.NewJSONRPCHandler(&agentGatewayHandler{server: s})
}
