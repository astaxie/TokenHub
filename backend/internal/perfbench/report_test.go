package perfbench_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/perfbench"
)

func TestResultJSONUsesReadableDurationsAndRoundTrips(t *testing.T) {
	t.Parallel()

	want := perfbench.Result{
		SchemaVersion: perfbench.SchemaVersion,
		Config: perfbench.Config{
			Label:                   "chat-c8",
			BaseURL:                 "http://127.0.0.1:8080",
			Model:                   "benchmark-model",
			Protocol:                perfbench.ProtocolChat,
			Mode:                    perfbench.ModeConcurrency,
			Concurrency:             8,
			Duration:                10 * time.Second,
			Warmup:                  2 * time.Second,
			Timeout:                 30 * time.Second,
			ExpectedUpstreamLatency: 5 * time.Millisecond,
		},
	}
	var output bytes.Buffer
	if err := perfbench.WriteJSON(&output, want); err != nil {
		t.Fatal(err)
	}
	jsonText := output.String()
	if strings.Contains(jsonText, "base_url") {
		t.Fatalf("JSON retained the private benchmark target:\n%s", jsonText)
	}
	for _, field := range []string{`"duration": "10s"`, `"warmup": "2s"`, `"expected_upstream_latency": "5ms"`} {
		if !strings.Contains(jsonText, field) {
			t.Fatalf("JSON missing %s:\n%s", field, jsonText)
		}
	}
	got, err := perfbench.ReadResult(strings.NewReader(jsonText))
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Duration != want.Config.Duration || got.Config.ExpectedUpstreamLatency != want.Config.ExpectedUpstreamLatency {
		t.Fatalf("duration round trip = %+v, want %+v", got.Config, want.Config)
	}
}

func TestMarkdownReportNamesEstimatedOverheadAndConfiguration(t *testing.T) {
	t.Parallel()

	result := perfbench.Result{
		SchemaVersion: perfbench.SchemaVersion,
		Config:        perfbench.Config{Label: "tokenhub", Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeRate, Rate: 100, Duration: time.Second},
		Metadata:      perfbench.Metadata{GitCommit: "abc123", GoVersion: "go1.test", OS: "linux", Arch: "amd64", CPUCount: 4},
		Summary:       perfbench.Summary{Requests: 100, SuccessRatePercent: 100, AchievedRPS: 99.5, LatencyMS: perfbench.Distribution{P50: 2, P95: 3, P99: 4}, EstimatedGatewayOverheadMS: perfbench.Distribution{P50: 1, P95: 2, P99: 3}},
	}
	markdown := perfbench.Markdown(result)
	for _, text := range []string{"tokenhub", "Estimated gateway overhead", "not an internal timer", "99.50", "abc123", "rate=100 rps"} {
		if !strings.Contains(markdown, text) {
			t.Fatalf("Markdown missing %q:\n%s", text, markdown)
		}
	}
}

func TestCheckBudgetRejectsIncompatibleScenarios(t *testing.T) {
	t.Parallel()

	baseline := perfbench.Result{SchemaVersion: perfbench.SchemaVersion, Config: perfbench.Config{Protocol: perfbench.ProtocolChat, Mode: perfbench.ModeConcurrency, Concurrency: 8}}
	current := perfbench.Result{SchemaVersion: perfbench.SchemaVersion, Config: perfbench.Config{Protocol: perfbench.ProtocolResponses, Mode: perfbench.ModeConcurrency, Concurrency: 8}}
	report := perfbench.CheckBudget(baseline, current, perfbench.Budget{})
	if report.Passed || !strings.Contains(report.Error, "protocol") {
		t.Fatalf("expected incompatible protocol failure: %+v", report)
	}
}
