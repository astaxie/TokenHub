package tokenhubplugin

import (
	"context"
	"encoding/json"
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
		return diagnosticExit(stderr, 2, "gateway hook handler is required")
	}
	var input GatewayHookInput
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return diagnosticExit(stderr, 2, "decode gateway hook input: %v", err)
	}
	if strings.TrimSpace(input.RequestID) == "" || strings.TrimSpace(input.Stage) == "" {
		return diagnosticExit(stderr, 2, "request_id and stage are required")
	}
	result, err := handler(ctx, input)
	if err != nil {
		return diagnosticExit(stderr, 1, "execute gateway hook %s/%s: %v", input.Stage, input.RequestID, err)
	}
	if result.Decision == "" {
		result.Decision = HookDecisionContinue
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		return diagnosticExit(stderr, 1, "encode gateway hook result: %v", err)
	}
	return 0
}
