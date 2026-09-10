package tokenhubplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	BackgroundTriggerManual   = "manual"
	BackgroundTriggerSchedule = "schedule"
	BackgroundTriggerStartup  = "startup"
)

type BackgroundJobInvocation struct {
	PluginID string          `json:"plugin_id"`
	JobID    string          `json:"job_id"`
	Trigger  string          `json:"trigger,omitempty"`
	Actor    ActionActor     `json:"actor,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type BackgroundJobResult struct {
	Data     any               `json:"data,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type BackgroundJobHandler func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error)

func ServeBackgroundJob(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, handler BackgroundJobHandler) int {
	if handler == nil {
		return diagnosticExit(stderr, 2, "background job handler is required")
	}
	var invocation BackgroundJobInvocation
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&invocation); err != nil {
		return diagnosticExit(stderr, 2, "decode background job invocation: %v", err)
	}
	if strings.TrimSpace(invocation.PluginID) == "" || strings.TrimSpace(invocation.JobID) == "" {
		return diagnosticExit(stderr, 2, "plugin_id and job_id are required")
	}
	result, err := handler(ctx, invocation)
	if err != nil {
		return diagnosticExit(stderr, 1, "execute background job %s/%s: %v", invocation.PluginID, invocation.JobID, err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return diagnosticExit(stderr, 1, "encode background job result: %v", err)
	}
	return 0
}

func DecodeBackgroundPayload[T any](invocation BackgroundJobInvocation) (T, error) {
	var value T
	if len(invocation.Payload) == 0 {
		return value, nil
	}
	if err := json.Unmarshal(invocation.Payload, &value); err != nil {
		return value, fmt.Errorf("decode background job payload: %w", err)
	}
	return value, nil
}
