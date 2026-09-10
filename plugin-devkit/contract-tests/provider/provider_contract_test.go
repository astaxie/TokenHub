package providercontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderOperationsFixtureIsStable(t *testing.T) {
	path := filepath.Join("..", "protocol", "stdio-json-v1", "provider_operations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provider operations fixture: %v", err)
	}
	var fixture struct {
		SchemaVersion int `json:"schema_version"`
		Cases         []struct {
			Name      string         `json:"name"`
			Operation string         `json:"operation"`
			Request   map[string]any `json:"request"`
			Expect    map[string]any `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode provider operations fixture: %v", err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", fixture.SchemaVersion)
	}
	seen := map[string]bool{}
	for _, testCase := range fixture.Cases {
		if testCase.Name == "" || testCase.Operation == "" {
			t.Fatalf("case has empty name or operation: %+v", testCase)
		}
		if seen[testCase.Name] {
			t.Fatalf("duplicate case %q", testCase.Name)
		}
		seen[testCase.Name] = true
		if testCase.Request == nil || testCase.Expect == nil {
			t.Fatalf("case %q must include request and expect objects", testCase.Name)
		}
	}
	for _, required := range []string{"chat", "chat_stream", "responses", "responses_stream", "embeddings", "models", "probe"} {
		if !seen[required] {
			t.Fatalf("provider fixture missing %q", required)
		}
	}
}
