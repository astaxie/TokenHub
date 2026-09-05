package tokenhubplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServeBackgroundJobDispatchesTypedInvocation(t *testing.T) {
	input := `{"plugin_id":"tokenhub.background.heartbeat","job_id":"heartbeat.ping","trigger":"manual","actor":{"id":"admin_1","role":"owner"},"payload":{"resource_id":"rsrc_1"}}`
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ServeBackgroundJob(context.Background(), strings.NewReader(input), &stdout, &stderr, func(ctx context.Context, invocation BackgroundJobInvocation) (BackgroundJobResult, error) {
		if invocation.PluginID != "tokenhub.background.heartbeat" || invocation.JobID != "heartbeat.ping" || invocation.Actor.ID != "admin_1" {
			t.Fatalf("invocation = %+v", invocation)
		}
		payload, err := DecodeBackgroundPayload[map[string]string](invocation)
		if err != nil {
			t.Fatal(err)
		}
		return BackgroundJobResult{
			Data:     map[string]string{"resource_id": payload["resource_id"]},
			Metadata: map[string]string{"status": "ok"},
		}, nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	var result BackgroundJobResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout: %v", err)
	}
	data := result.Data.(map[string]any)
	if data["resource_id"] != "rsrc_1" || result.Metadata["status"] != "ok" {
		t.Fatalf("result = %+v", result)
	}
}

func TestServeBackgroundJobRejectsUnknownInvocationFields(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ServeBackgroundJob(context.Background(), strings.NewReader(`{"plugin_id":"p","job_id":"j","unexpected":true}`), &stdout, &stderr, func(context.Context, BackgroundJobInvocation) (BackgroundJobResult, error) {
		t.Fatal("handler should not run")
		return BackgroundJobResult{}, nil
	})
	if code == 0 {
		t.Fatal("unknown fields were accepted")
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
