package tokenhubplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestServeProviderDispatchesTypedInvocation(t *testing.T) {
	input := `{"operation":"chat","provider":{"id":"prv_1","type":"external_mock"},"provider_model":"upstream","request":{"model":"gateway","messages":[{"role":"user","content":"hello"}]},"credentials":{"api_key":"secret"}}`
	var stderr bytes.Buffer
	var stdout bytes.Buffer
	code := ServeProvider(context.Background(), strings.NewReader(input), &stdout, &stderr, func(ctx context.Context, invocation ProviderInvocation) (ProviderResult, error) {
		if invocation.Operation != OperationChat || invocation.Provider.Type != "external_mock" || invocation.Credentials.APIKey != "secret" {
			t.Fatalf("invocation = %+v", invocation)
		}
		request, err := DecodeRequest[map[string]any](invocation)
		if err != nil {
			t.Fatal(err)
		}
		if request["model"] != "gateway" {
			t.Fatalf("request = %+v", request)
		}
		return ProviderResult{
			Response: map[string]any{"id": "chatcmpl_sdk"},
			Usage:    &Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		}, nil
	})
	if code != 0 {
		t.Fatalf("exit code = %d stderr=%s", code, stderr.String())
	}
	var result ProviderResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode stdout: %v", err)
	}
	if result.Usage == nil || result.Usage.TotalTokens != 3 {
		t.Fatalf("result = %+v", result)
	}
}

func TestServeProviderRejectsUnknownInvocationFields(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := ServeProvider(context.Background(), strings.NewReader(`{"operation":"chat","unexpected":true}`), &stdout, &stderr, func(context.Context, ProviderInvocation) (ProviderResult, error) {
		t.Fatal("handler should not run")
		return ProviderResult{}, nil
	})
	if code == 0 {
		t.Fatal("unknown fields were accepted")
	}
	if !strings.Contains(stderr.String(), "unknown field") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
