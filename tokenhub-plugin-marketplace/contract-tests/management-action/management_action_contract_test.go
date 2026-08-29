package managementactioncontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestActionInvocationsFixtureIsStable(t *testing.T) {
	path := filepath.Join("..", "protocol", "stdio-json-v1", "action_invocations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read action invocation fixture: %v", err)
	}
	var fixture struct {
		SchemaVersion int `json:"schema_version"`
		Cases         []struct {
			Name    string         `json:"name"`
			Actor   map[string]any `json:"actor"`
			Payload map[string]any `json:"payload"`
			Expect  map[string]any `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode action invocation fixture: %v", err)
	}
	if fixture.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", fixture.SchemaVersion)
	}
	if len(fixture.Cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(fixture.Cases))
	}
	for _, testCase := range fixture.Cases {
		if testCase.Name == "" || testCase.Actor["id"] == "" {
			t.Fatalf("case has empty name or actor id: %+v", testCase)
		}
		if testCase.Payload == nil || testCase.Expect == nil {
			t.Fatalf("case %q must include payload and expect objects", testCase.Name)
		}
	}
}
