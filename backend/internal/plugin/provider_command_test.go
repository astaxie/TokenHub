package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestProviderCommandRunnerExecutesStdioJSONProviderCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	cases := []struct {
		name          string
		operation     string
		providerModel string
		responseID    string
	}{
		{name: "chat", operation: "chat", providerModel: "upstream-chat", responseID: "chatcmpl_provider"},
		{name: "responses", operation: "responses", providerModel: "upstream-responses", responseID: "resp_provider"},
		{name: "embeddings", operation: "embeddings", providerModel: "upstream-embeddings", responseID: "embedding_provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "provider.sh")
			output := fmt.Sprintf(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"%s"'*'"provider":{"id":"prv_1"'*'"type":"custom_stdio"'*'"provider_model":"%s"'*'"api_key":"provider-secret"'*)
    printf '{"response":{"id":"%s"},"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}'
    ;;
  *)
    printf 'unexpected provider payload: %%s' "$payload" >&2
    exit 2
    ;;
esac
`, tc.operation, tc.providerModel, tc.responseID)
			if err := os.WriteFile(script, []byte(output), 0o755); err != nil {
				t.Fatal(err)
			}

			runner := NewProviderCommandRunner(dir, "provider.sh")
			var result struct {
				Response map[string]any `json:"response"`
				Usage    map[string]any `json:"usage,omitempty"`
			}
			err := runner.ExecuteProviderCommand(t.Context(), ProviderCommandRequest{
				Operation:     tc.operation,
				Provider:      ProviderCommandProvider{ID: "prv_1", Type: "custom_stdio"},
				ProviderModel: tc.providerModel,
				Request: struct {
					Model string `json:"model"`
					Input string `json:"input"`
				}{
					Model: "gateway-model",
					Input: "hello",
				},
				Credentials: ProviderCommandCredentials{APIKey: "provider-secret"},
			}, &result)
			if err != nil {
				t.Fatalf("execute provider command: %v", err)
			}
			if result.Response["id"] != tc.responseID {
				t.Fatalf("response = %#v, want id %q", result.Response, tc.responseID)
			}
			if result.Usage["total_tokens"] != float64(7) {
				t.Fatalf("usage = %+v, want total 7", result.Usage)
			}
		})
	}
}

func TestProviderCommandRunnerRejectsEscapingCommandPath(t *testing.T) {
	runner := NewProviderCommandRunner(t.TempDir(), "../provider.sh")
	var result struct {
		Response map[string]any `json:"response"`
		Usage    map[string]any `json:"usage,omitempty"`
	}
	err := runner.ExecuteProviderCommand(t.Context(), ProviderCommandRequest{
		Operation: "chat",
	}, &result)
	if err == nil {
		t.Fatal("escaping command path was accepted")
	}
}
