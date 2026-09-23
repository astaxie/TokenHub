package backgroundjobcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBackgroundHeartbeatFixtureIsStable(t *testing.T) {
	path := filepath.Join("..", "protocol", "stdio-json-v1", "background_heartbeat.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read background heartbeat fixture: %v", err)
	}
	var fixture struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
		PluginID      string `json:"plugin_id"`
		JobID         string `json:"job_id"`
		Schedule      string `json:"schedule"`
		Cases         []struct {
			Name    string         `json:"name"`
			Trigger string         `json:"trigger"`
			Actor   map[string]any `json:"actor"`
			Payload map[string]any `json:"payload"`
			Expect  map[string]any `json:"expect"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode background heartbeat fixture: %v", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Kind != "background_job" || fixture.PluginID == "" || fixture.JobID == "" {
		t.Fatalf("fixture identity = %+v", fixture)
	}
	if fixture.Schedule != "@startup" {
		t.Fatalf("schedule = %q, want @startup", fixture.Schedule)
	}
	if len(fixture.Cases) != 2 {
		t.Fatalf("cases = %d, want 2", len(fixture.Cases))
	}
	for _, testCase := range fixture.Cases {
		if testCase.Name == "" || testCase.Trigger == "" || testCase.Actor["id"] == "" {
			t.Fatalf("case has empty identity fields: %+v", testCase)
		}
		if testCase.Payload == nil || testCase.Expect == nil {
			t.Fatalf("case %q must include payload and expect objects", testCase.Name)
		}
	}
}
