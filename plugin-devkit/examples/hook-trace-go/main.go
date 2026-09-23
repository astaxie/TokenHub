package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/astaxie/TokenHub/plugin-devkit/sdk/go/tokenhubplugin"
)

func main() {
	os.Exit(tokenhubplugin.ServeGatewayHook(context.Background(), os.Stdin, os.Stdout, os.Stderr, handleHook))
}

func handleHook(ctx context.Context, input tokenhubplugin.GatewayHookInput) (tokenhubplugin.GatewayHookResult, error) {
	if input.Stage != tokenhubplugin.StageTraceExport {
		return tokenhubplugin.GatewayHookResult{}, fmt.Errorf("stage %q is not supported", input.Stage)
	}
	_, sawAudit := input.Data["audit"]
	_, sawUsage := input.Data["usage"]
	event, err := json.Marshal(map[string]any{
		"event":      "trace_export_seen",
		"request_id": input.RequestID,
		"saw_audit":  sawAudit,
		"saw_usage":  sawUsage,
	})
	if err != nil {
		return tokenhubplugin.GatewayHookResult{}, err
	}
	return tokenhubplugin.GatewayHookResult{
		Decision:    tokenhubplugin.HookDecisionContinue,
		AuditEvents: []json.RawMessage{event},
	}, nil
}
