package server

import (
	"encoding/json"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func mergeAgentTaskSnapshot(record *AgentTask, event a2a.Event) error {
	snapshot := &a2a.Task{
		ID:        a2a.TaskID(record.ID),
		ContextID: record.ContextID,
		Status:    a2a.TaskStatus{State: a2a.TaskState(record.State)},
	}
	if record.SnapshotJSON != "" {
		if err := json.Unmarshal([]byte(record.SnapshotJSON), snapshot); err != nil {
			return err
		}
	}
	snapshot.ID = a2a.TaskID(record.ID)

	switch item := event.(type) {
	case *a2a.Message:
		if item.ContextID != "" {
			snapshot.ContextID = item.ContextID
		}
		snapshot.History = append(snapshot.History, item)
	case *a2a.TaskStatusUpdateEvent:
		if item.ContextID != "" {
			snapshot.ContextID = item.ContextID
		}
		snapshot.Status = item.Status
	case *a2a.TaskArtifactUpdateEvent:
		if item.ContextID != "" {
			snapshot.ContextID = item.ContextID
		}
		mergeAgentTaskArtifact(snapshot, item)
	}

	payload, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	record.ContextID = snapshot.ContextID
	record.State = string(snapshot.Status.State)
	record.SnapshotJSON = string(payload)
	return nil
}

func mergeAgentTaskArtifact(snapshot *a2a.Task, event *a2a.TaskArtifactUpdateEvent) {
	if event.Artifact == nil {
		return
	}
	index := -1
	for candidate, artifact := range snapshot.Artifacts {
		if artifact != nil && artifact.ID == event.Artifact.ID {
			index = candidate
			break
		}
	}
	if index < 0 {
		snapshot.Artifacts = append(snapshot.Artifacts, event.Artifact)
		return
	}
	if !event.Append {
		snapshot.Artifacts[index] = event.Artifact
		return
	}
	existing := snapshot.Artifacts[index]
	existing.Parts = append(existing.Parts, event.Artifact.Parts...)
	if event.Artifact.Name != "" {
		existing.Name = event.Artifact.Name
	}
	if event.Artifact.Description != "" {
		existing.Description = event.Artifact.Description
	}
	if event.Artifact.Extensions != nil {
		existing.Extensions = event.Artifact.Extensions
	}
	if event.Artifact.Metadata != nil {
		existing.Metadata = event.Artifact.Metadata
	}
}

func agentExecutionStatusForTask(state a2a.TaskState) string {
	switch state {
	case a2a.TaskStateCompleted:
		return "completed"
	case a2a.TaskStateCanceled:
		return "canceled"
	case a2a.TaskStateFailed, a2a.TaskStateRejected:
		return "failed"
	default:
		return "running"
	}
}

func (h *agentGatewayHandler) hasNonTerminalInvocationTask(invocation agentInvocation) bool {
	if invocation.ExecutionID == "" {
		return false
	}
	details, found, err := h.server.store.GetAgentExecutionDetails(invocation.ExecutionID)
	if err != nil || !found {
		return false
	}
	for _, task := range details.Tasks {
		if a2a.TaskState(task.State).Terminal() {
			continue
		}
		if invocation.ParentStepID != "" && task.ExecutionStepID != "" {
			if task.ExecutionStepID == invocation.ParentStepID {
				return true
			}
			continue
		}
		if task.AgentID == invocation.Agent.ID {
			return true
		}
	}
	return false
}
