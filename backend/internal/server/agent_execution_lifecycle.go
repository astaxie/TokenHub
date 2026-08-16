package server

import (
	"encoding/json"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func inspectA2AContinuationTaskID(body []byte, method string) string {
	var request struct {
		Params struct {
			ID      string `json:"id"`
			Message struct {
				TaskID string `json:"taskId"`
			} `json:"message"`
		} `json:"params"`
	}
	if json.Unmarshal(body, &request) != nil {
		return ""
	}
	switch method {
	case "GetTask", "CancelTask", "SubscribeToTask":
		return request.Params.ID
	case "SendMessage", "SendStreamingMessage":
		return request.Params.Message.TaskID
	default:
		return ""
	}
}

func (s *Server) resumeAgentTaskInvocation(invocation agentInvocation, task AgentTask) (agentInvocation, bool, error) {
	if task.ExecutionID == "" {
		return invocation, false, nil
	}
	details, found, err := s.store.GetAgentExecutionDetails(task.ExecutionID)
	if err != nil {
		return agentInvocation{}, false, NewHTTPError(500, "agent_execution_unavailable", "Agent execution could not be loaded")
	}
	if !found || details.ProjectID != invocation.Project.ID || details.APIKeyID != invocation.APIKey.ID ||
		details.EndUserID != invocation.EndUserID {
		return agentInvocation{}, false, a2a.NewError(a2a.ErrTaskNotFound, "Task execution was not found")
	}
	if details.Status != "running" {
		return invocation, false, nil
	}
	if details.Deadline != nil && !details.Deadline.After(time.Now().UTC()) {
		_ = s.store.FinishAgentExecution(details.ID, "budget_exceeded")
		return agentInvocation{}, false, NewHTTPError(429, "agent_runtime_budget_exceeded", "Agent execution runtime budget was exceeded")
	}

	edge, ok := agentTaskExecutionEdge(details, task)
	if !ok {
		return agentInvocation{}, false, NewHTTPError(409, "agent_task_execution_step_missing", "Agent task execution step is no longer running")
	}
	invocation.ExecutionID = details.ID
	invocation.TraceID = details.TraceID
	invocation.ParentStepID = edge.ID
	invocation.Depth = edge.Depth
	invocation.Chain = agentExecutionChain(details.Edges, edge)
	invocation.RootExecution = edge.ParentStepID == ""
	if details.Deadline != nil {
		invocation.Deadline = details.Deadline.UTC()
	}
	return invocation, true, nil
}

func agentTaskExecutionEdge(details AgentExecutionDetails, task AgentTask) (AgentExecutionEdge, bool) {
	if task.ExecutionStepID != "" {
		for _, edge := range details.Edges {
			if edge.ID == task.ExecutionStepID && edge.Status == "running" && edge.CalleeID == task.AgentID {
				return edge, true
			}
		}
		return AgentExecutionEdge{}, false
	}
	for index := len(details.Edges) - 1; index >= 0; index-- {
		edge := details.Edges[index]
		if edge.Status == "running" && edge.CalleeID == task.AgentID {
			return edge, true
		}
	}
	return AgentExecutionEdge{}, false
}

func agentExecutionChain(edges []AgentExecutionEdge, leaf AgentExecutionEdge) []string {
	byID := make(map[string]AgentExecutionEdge, len(edges))
	for _, edge := range edges {
		byID[edge.ID] = edge
	}
	chain := make([]string, 0, leaf.Depth+1)
	for current := leaf; current.ID != ""; current = byID[current.ParentStepID] {
		chain = append([]string{current.CalleeID}, chain...)
		if current.ParentStepID == "" {
			break
		}
	}
	return chain
}
