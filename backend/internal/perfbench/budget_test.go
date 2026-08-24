package perfbench_test

import (
	"strings"
	"testing"

	"tokenhub/backend/internal/perfbench"
)

func TestCheckBudgetAppliesDocumentedRegressionTolerances(t *testing.T) {
	t.Parallel()

	baseline := perfbench.Result{Summary: perfbench.Summary{
		SuccessRatePercent: 100,
		AchievedRPS:        1000,
		LatencyMS:          perfbench.Distribution{Mean: 10, P99: 20},
	}}
	budget := perfbench.Budget{
		MinimumSuccessRatePercent: 99.9,
		MinimumThroughputRatio:    0.90,
		MaximumMeanRegressionPct:  15,
		MaximumP99RegressionPct:   20,
	}

	t.Run("within tolerance", func(t *testing.T) {
		current := perfbench.Result{Summary: perfbench.Summary{
			SuccessRatePercent: 99.95,
			AchievedRPS:        910,
			LatencyMS:          perfbench.Distribution{Mean: 11.4, P99: 24},
		}}
		report := perfbench.CheckBudget(baseline, current, budget)
		if !report.Passed {
			t.Fatalf("expected budget to pass: %+v", report)
		}
		if len(report.Checks) != 4 {
			t.Fatalf("checks = %d, want 4", len(report.Checks))
		}
	})

	t.Run("outside tolerance", func(t *testing.T) {
		current := perfbench.Result{Summary: perfbench.Summary{
			SuccessRatePercent: 99,
			AchievedRPS:        800,
			LatencyMS:          perfbench.Distribution{Mean: 13, P99: 30},
		}}
		report := perfbench.CheckBudget(baseline, current, budget)
		if report.Passed {
			t.Fatalf("expected budget to fail: %+v", report)
		}
		for _, name := range []string{"success_rate", "throughput", "mean_latency", "p99_latency"} {
			if !hasFailedCheck(report, name) {
				t.Fatalf("expected failed %s check: %+v", name, report)
			}
		}
	})
}

func TestCheckBudgetRejectsDifferentRuntimeProfiles(t *testing.T) {
	t.Parallel()

	baseline := perfbench.Result{SchemaVersion: perfbench.SchemaVersion, Metadata: perfbench.Metadata{
		GoVersion: "go1.26.5", OS: "darwin", Arch: "arm64", CPUCount: 18, CPUModel: "Apple M5 Pro", MemoryBytes: 48 << 30,
	}}
	current := baseline
	current.Metadata.OS = "linux"
	report := perfbench.CheckBudget(baseline, current, perfbench.Budget{})
	if report.Passed || !strings.Contains(report.Error, "operating system") {
		t.Fatalf("expected runtime incompatibility: %+v", report)
	}
}

func hasFailedCheck(report perfbench.BudgetReport, name string) bool {
	for _, check := range report.Checks {
		if check.Name == name && !check.Passed {
			return true
		}
	}
	return false
}
