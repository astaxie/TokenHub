package server

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestAgentEventTextPrefersAllTaskArtifacts(t *testing.T) {
	task := &a2a.Task{
		Status: a2a.TaskStatus{
			State: a2a.TaskStateCompleted,
			Message: &a2a.Message{
				Role:  a2a.MessageRoleAgent,
				Parts: a2a.ContentParts{a2a.NewTextPart("completed")},
			},
		},
		Artifacts: []*a2a.Artifact{
			{ID: "report", Parts: a2a.ContentParts{a2a.NewTextPart("full report")}},
			nil,
			{ID: "summary", Parts: a2a.ContentParts{a2a.NewTextPart("executive summary")}},
		},
		History: []*a2a.Message{
			{Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("history fallback")}},
		},
	}

	if got, want := agentEventText(task), "full report\nexecutive summary"; got != want {
		t.Fatalf("agentEventText() = %q, want %q", got, want)
	}
}

func TestAgentEventTextTaskFallbackOrder(t *testing.T) {
	tests := []struct {
		name string
		task *a2a.Task
		want string
	}{
		{
			name: "current status before history",
			task: &a2a.Task{
				Status: a2a.TaskStatus{Message: &a2a.Message{
					Role:  a2a.MessageRoleAgent,
					Parts: a2a.ContentParts{a2a.NewTextPart("input required")},
				}},
				History: []*a2a.Message{
					{Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("older answer")}},
				},
			},
			want: "input required",
		},
		{
			name: "latest agent history when status is absent",
			task: &a2a.Task{History: []*a2a.Message{
				{Role: a2a.MessageRoleUser, Parts: a2a.ContentParts{a2a.NewTextPart("question")}},
				{Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("older answer")}},
				{Role: a2a.MessageRoleAgent, Parts: a2a.ContentParts{a2a.NewTextPart("latest answer")}},
			}},
			want: "latest answer",
		},
		{
			name: "empty task has no response text",
			task: &a2a.Task{},
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := agentEventText(test.task); got != test.want {
				t.Fatalf("agentEventText() = %q, want %q", got, test.want)
			}
		})
	}
}
