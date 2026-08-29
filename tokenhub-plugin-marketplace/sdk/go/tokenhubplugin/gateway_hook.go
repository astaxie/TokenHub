package tokenhubplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	StageTraceExport = "trace_export"

	HookDecisionContinue     = "continue"
	HookDecisionDeny         = "deny"
	HookDecisionShortCircuit = "short_circuit"
)

type GatewayEnvelope struct {
	Version        string                     `json:"version"`
	Protocol       string                     `json:"protocol"`
	Operation      string                     `json:"operation"`
	Model          string                     `json:"model"`
	RequestBody    json.RawMessage            `json:"request_body,omitempty"`
	NormalizedText []TextSegment              `json:"normalized_text,omitempty"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
}

type TextSegment struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type GatewayHookInput struct {
	RequestID string                     `json:"request_id"`
	Stage     string                     `json:"stage"`
	Envelope  GatewayEnvelope            `json:"envelope"`
	Data      map[string]json.RawMessage `json:"data,omitempty"`
}

type GatewayHookResult struct {
	Decision    string              `json:"decision"`
	Writes      map[string]RawPatch `json:"writes,omitempty"`
	AuditEvents []json.RawMessage   `json:"audit_events,omitempty"`
}

type RawPatch struct {
	Value json.RawMessage `json:"value"`
}

type GatewayHookHandler func(context.Context, GatewayHookInput) (GatewayHookResult, error)

func ServeGatewayHook(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer, handler GatewayHookHandler) int {
	if handler == nil {
		fmt.Fprintln(stderr, "gateway hook handler is required")
		return 2
	}
	var input GatewayHookInput
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fmt.Fprintf(stderr, "decode gateway hook input: %v\n", err)
		return 2
	}
	if strings.TrimSpace(input.RequestID) == "" || strings.TrimSpace(input.Stage) == "" {
		fmt.Fprintln(stderr, "request_id and stage are required")
		return 2
	}
	result, err := handler(ctx, input)
	if err != nil {
		fmt.Fprintf(stderr, "execute gateway hook %s/%s: %v\n", input.Stage, input.RequestID, err)
		return 1
	}
	if result.Decision == "" {
		result.Decision = HookDecisionContinue
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode gateway hook result: %v\n", err)
		return 1
	}
	return 0
}
