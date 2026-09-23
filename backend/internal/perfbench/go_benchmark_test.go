package perfbench_test

import (
	"strings"
	"testing"

	"tokenhub/backend/internal/perfbench"
)

func TestParseGoBenchmarksAggregatesMedianMetrics(t *testing.T) {
	t.Parallel()

	input := `goos: darwin
goarch: arm64
cpu: Apple M5 Pro
BenchmarkGatewayChat/SQLite-18  100  1000 ns/op  2.5 MB/s  200 B/op  10 allocs/op
BenchmarkGatewayChat/SQLite-18  100  1200 ns/op  2.1 MB/s  220 B/op  12 allocs/op
BenchmarkGatewayResponses/SQLite-18  100  900 ns/op  180 B/op  9 allocs/op
`
	suite, err := perfbench.ParseGoBenchmarks(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	chat := suite.Benchmarks["BenchmarkGatewayChat/SQLite"]
	if chat.NSPerOp != 1100 || chat.BytesPerOp != 210 || chat.AllocsPerOp != 11 {
		t.Fatalf("unexpected median metrics: %+v", chat)
	}
	if suite.Metadata.OS != "darwin" || suite.Metadata.Arch != "arm64" || suite.Metadata.CPUModel != "Apple M5 Pro" {
		t.Fatalf("unexpected metadata: %+v", suite.Metadata)
	}
}

func TestCheckGoBenchmarkBudgetAppliesEveryMetricAndProfile(t *testing.T) {
	t.Parallel()

	metadata := perfbench.Metadata{GoVersion: "go1.26", OS: "linux", Arch: "amd64", CPUCount: 4, CPUModel: "Test CPU", MemoryBytes: 8 << 30}
	baseline := perfbench.GoBenchmarkSuite{SchemaVersion: perfbench.GoBenchmarkSchemaVersion, Metadata: metadata, Benchmarks: map[string]perfbench.GoBenchmarkMetrics{
		"BenchmarkGatewayChat/SQLite": {NSPerOp: 1000, BytesPerOp: 200, AllocsPerOp: 10},
	}}
	current := baseline
	current.Benchmarks = map[string]perfbench.GoBenchmarkMetrics{
		"BenchmarkGatewayChat/SQLite": {NSPerOp: 1100, BytesPerOp: 220, AllocsPerOp: 11},
	}
	report := perfbench.CheckGoBenchmarkBudget(baseline, current, perfbench.GoBenchmarkBudget{
		MaximumNSRegressionPct: 20, MaximumBytesRegressionPct: 20, MaximumAllocsRegressionPct: 20,
	})
	if !report.Passed || len(report.Checks) != 3 {
		t.Fatalf("expected budget pass: %+v", report)
	}

	current.Benchmarks["BenchmarkGatewayChat/SQLite"] = perfbench.GoBenchmarkMetrics{NSPerOp: 1300, BytesPerOp: 230, AllocsPerOp: 12}
	report = perfbench.CheckGoBenchmarkBudget(baseline, current, perfbench.GoBenchmarkBudget{
		MaximumNSRegressionPct: 20, MaximumBytesRegressionPct: 20, MaximumAllocsRegressionPct: 20,
	})
	if report.Passed || report.Checks[0].Passed {
		t.Fatalf("expected ns/op regression failure: %+v", report)
	}

	current.Metadata.Arch = "arm64"
	report = perfbench.CheckGoBenchmarkBudget(baseline, current, perfbench.GoBenchmarkBudget{})
	if report.Passed || !strings.Contains(report.Error, "architecture") {
		t.Fatalf("expected profile rejection: %+v", report)
	}
}
