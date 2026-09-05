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
		fmt.Fprintln(stderr, "background job handler is required")
		return 2
	}
	var invocation BackgroundJobInvocation
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&invocation); err != nil {
		fmt.Fprintf(stderr, "decode background job invocation: %v\n", err)
		return 2
	}
	if strings.TrimSpace(invocation.PluginID) == "" || strings.TrimSpace(invocation.JobID) == "" {
		fmt.Fprintln(stderr, "plugin_id and job_id are required")
		return 2
	}
	result, err := handler(ctx, invocation)
	if err != nil {
		fmt.Fprintf(stderr, "execute background job %s/%s: %v\n", invocation.PluginID, invocation.JobID, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode background job result: %v\n", err)
		return 1
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
