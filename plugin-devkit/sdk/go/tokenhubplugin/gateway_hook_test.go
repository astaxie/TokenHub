package tokenhubplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServeGatewayHookDispatchesTypedInput(t *testing.T) {
	input := `{"request_id":"req_1","stage":"trace_export","envelope":{"version":"v1","protocol":"gateway","operation":"chat","model":"gpt"},"data":{"audit":{"request_id":"req_1"},"usage":{"total_tokens":7}}}`
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ServeGatewayHook(context.Background(), strings.NewReader(input), &stdout, &stderr, func(ctx context.Context, input GatewayHookInput) (GatewayHookResult, error) {
		if input.RequestID != "req_1" || input.Stage != StageTraceExport || input.Envelope.Model != "gpt" {
			t.Fatalf("input = %+v", input)
		}
		return GatewayHookResult{
			AuditEvents: []json.RawMessage{json.RawMessage(`{"event":"trace_seen"}`)},
		}, nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	var result GatewayHookResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout: %v", err)
	}
	if result.Decision != HookDecisionContinue || len(result.AuditEvents) != 1 {
		t.Fatalf("result = %+v", result)
	}
}

func TestServeGatewayHookRejectsUnknownInputFields(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ServeGatewayHook(context.Background(), strings.NewReader(`{"request_id":"req_1","stage":"trace_export","unexpected":true}`), &stdout, &stderr, func(context.Context, GatewayHookInput) (GatewayHookResult, error) {
		t.Fatal("handler should not run")
		return GatewayHookResult{}, nil
	})
	if code == 0 {
		t.Fatal("unknown fields were accepted")
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
