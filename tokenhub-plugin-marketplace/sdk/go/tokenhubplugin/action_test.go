package tokenhubplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServeActionDispatchesTypedInvocation(t *testing.T) {
	input := `{"plugin_id":"tokenhub.action.echo","action_id":"echo.run","actor":{"id":"admin_1","role":"owner"},"payload":{"message":"hello"}}`
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ServeAction(context.Background(), strings.NewReader(input), &stdout, &stderr, func(ctx context.Context, invocation ActionInvocation) (ActionResult, error) {
		if invocation.PluginID != "tokenhub.action.echo" || invocation.ActionID != "echo.run" || invocation.Actor.ID != "admin_1" {
			t.Fatalf("invocation = %+v", invocation)
		}
		payload, err := DecodeActionPayload[map[string]string](invocation)
		if err != nil {
			t.Fatal(err)
		}
		return ActionResult{Data: map[string]string{"message": payload["message"]}}, nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	var result ActionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["message"] != "hello" {
		t.Fatalf("result = %+v", result)
	}
}

func TestServeActionRejectsUnknownInvocationFields(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ServeAction(context.Background(), strings.NewReader(`{"plugin_id":"p","action_id":"a","unexpected":true}`), &stdout, &stderr, func(context.Context, ActionInvocation) (ActionResult, error) {
		t.Fatal("handler should not run")
		return ActionResult{}, nil
	})
	if code == 0 {
		t.Fatal("unknown fields were accepted")
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
