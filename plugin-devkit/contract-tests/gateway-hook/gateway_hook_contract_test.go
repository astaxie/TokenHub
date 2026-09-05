package gatewayhookcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGatewayHookTraceFixtureIsStable(t *testing.T) {
	path := filepath.Join("..", "protocol", "stdio-json-v1", "gateway_hook_trace.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read gateway hook fixture: %v", err)
	}
	var fixture struct {
		SchemaVersion int      `json:"schema_version"`
		Kind          string   `json:"kind"`
		Stage         string   `json:"stage"`
		FailurePolicy string   `json:"failure_policy"`
		Reads         []string `json:"reads"`
		Writes        []string `json:"writes"`
		Cases         []struct {
			Name string `json:"name"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode gateway hook fixture: %v", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Kind != "gateway_hook" || fixture.Stage != "trace_export" {
		t.Fatalf("fixture identity = %+v", fixture)
	}
	if fixture.FailurePolicy != "observe_only" || len(fixture.Writes) != 0 {
		t.Fatalf("fixture policy = %s writes=%v, want observe_only with no writes", fixture.FailurePolicy, fixture.Writes)
	}
	if len(fixture.Reads) != 2 || fixture.Reads[0] != "audit" || fixture.Reads[1] != "usage" {
		t.Fatalf("reads = %v, want audit and usage", fixture.Reads)
	}
	if len(fixture.Cases) != 1 || fixture.Cases[0].Name == "" {
		t.Fatalf("cases = %+v, want one named case", fixture.Cases)
	}
}
